package main

// prepare — decide whether an issue is this pipeline's to work, and if it
// is, assign it and lay out everything the implementing agent will need.
//
// The gate — the rules, their order, the two modes, and the record of why —
// is internal/prepare, and so are the request's markdown and the branch
// name. This file is the flags, the event file, the GitHub calls, the
// clean-tree assertion, the handoff files, git and tofu, in the script's
// order, and the exit code. It changes directory to the repository root and
// stays there, as every verb that works on a tree does.
//
// Prints exactly one word on stdout — the outcome — and nothing else:
//
//	ready        the issue is ours, the branch exists, the handoff is written
//	in-flight    an open pull request is already carrying this issue
//	ineligible   a blocking label, an opt-out, a closed issue, or not queued
//
// in-flight and ineligible write NOTHING and change NOTHING. They make no
// GitHub call that mutates, create no branch, leave no file, and pause
// nothing: duplicate and ineligible events are silent no-ops. The reason goes
// to stderr, because "ineligible" on its own is not a diagnostic.
//
// Outputs on the ready path, written into the handoff directory:
//
//	issue.json          the one snapshot every later step reads
//	ack.md              the comment posted to the requester (entry only)
//	request.md          the request in markdown — both agents read this first
//	plan-baseline.txt   what main already plans, before anyone touches it
//	base-sha.txt        the commit this run started from
//	branch.txt          the working branch
//
// and, when $GITHUB_ENV is writable, BRANCH and BASE_SHA.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/github"
	"github.com/zetlen/falconet/internal/handoff"
	"github.com/zetlen/falconet/internal/prepare"
	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/stacks"
)

const prepareUsageText = `prepare — decide whether an issue is this pipeline's to work, and if it is,
assign it and lay out everything the implementing agent will need.

Modes:
  falconet prepare --issue N [--config FILE] [--out-dir DIR] [--event FILE]
                   [--assignee LOGIN] [--re-entry] [--no-ack]

    --event      a GitHub webhook payload (issues / issue_comment). Also
                 read from $FALCONET_EVENT_PATH. Optional: without one the
                 gate reads the issue itself.
    --assignee   who the issue is assigned to; defaults to
                 $GITHUB_TRIGGERING_ACTOR, then to the token's own login
    --re-entry   treat this as a requester's reply to a needs-info question
                 rather than a first entry. Inferred from the event when
                 there is one; this is how a workstation says it.
    --no-ack     skip the acknowledgment comment

Prints exactly one word on stdout — the outcome — and nothing else:

  ready        the issue is ours, the branch exists, the handoff is written
  in-flight    an open pull request is already carrying this issue
  ineligible   a blocking label, an opt-out, a closed issue, or not queued

in-flight and ineligible write NOTHING and change NOTHING. They make no
GitHub call that mutates, create no branch, leave no file, and pause
nothing: duplicate and ineligible events are silent no-ops. The reason goes
to stderr, because "ineligible" on its own is not a diagnostic.

Outputs on the ready path, written into the handoff directory:
  issue.json          the one snapshot every later step reads
  ack.md              the comment posted to the requester (entry only)
  request.md          the request in markdown — both agents read this first
  plan-baseline.txt   what main already plans, before anyone touches it
  base-sha.txt        the commit this run started from
  branch.txt          the working branch

and, when $GITHUB_ENV is writable, BRANCH and BASE_SHA.

GitHub is reached with GH_TOKEN or GITHUB_TOKEN, on the repository
GITHUB_REPOSITORY names, or else the one the origin remote points at; both
are resolved at the first call that needs them, so an ineligible event
costs no network and no credential. GITHUB_API_URL overrides the endpoint.

Exit codes: 0 = an outcome was determined and printed
            1 = refused mechanically — a dirty tree, an issue that cannot
                be read, a label that cannot be cleared, a stack that cannot
                be planned; nothing is printed, stderr says why
            2 = usage error (including --help)

$TOFU overrides the planner, for the tests.
`

func prepareUsage() int {
	fmt.Fprint(os.Stderr, prepareUsageText)
	return 2
}

// eventPayload is the part of a webhook payload the gate reads, every field
// optional, as jq read them: `.issue.labels[]?.name`, `.issue.body // ""`,
// `.issue.state // "open"`, `.action // ""`, whether `.issue.pull_request` is
// set, and `.comment.user.type`.
type eventPayload struct {
	Action string `json:"action"`
	Issue  struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Body        *string         `json:"body"`
		State       *string         `json:"state"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
	Comment struct {
		User struct {
			Type string `json:"type"`
		} `json:"user"`
	} `json:"comment"`
}

// readEvent reads the gate's inputs from the event file. The file must
// exist and parse; a payload whose top-level value is null or false is "not
// valid JSON", as `jq -e .` reported it, and one that is not an object at all
// is refused by name rather than read as an issue with nothing on it.
func readEvent(path string) (prepare.Event, prepare.Snapshot, error) {
	var ev prepare.Event
	var snap prepare.Snapshot
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ev, snap, fmt.Errorf("--event names no file: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ev, snap, fmt.Errorf("cannot read %s: %v", path, err)
	}
	var top any
	if err := json.Unmarshal(raw, &top); err != nil || top == nil || top == false {
		return ev, snap, fmt.Errorf("%s is not valid JSON", path)
	}
	if _, ok := top.(map[string]any); !ok {
		return ev, snap, fmt.Errorf("%s is not a GitHub event: the top-level value is not an object", path)
	}
	var p eventPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return ev, snap, fmt.Errorf("%s is not a GitHub event: %v", path, err)
	}
	for _, l := range p.Issue.Labels {
		if l.Name != "" {
			snap.Labels = append(snap.Labels, l.Name)
		}
	}
	if p.Issue.Body != nil {
		snap.Body = *p.Issue.Body
	}
	snap.State = "open"
	if p.Issue.State != nil {
		snap.State = *p.Issue.State
	}
	ev.Action = p.Action
	// jq's `if .issue.pull_request then`: anything but null and false.
	pr := bytes.TrimSpace(p.Issue.PullRequest)
	ev.PullRequest = len(pr) > 0 && string(pr) != "null" && string(pr) != "false"
	ev.Bot = p.Comment.User.Type == "Bot"
	return ev, snap, nil
}

// issueSnapshot is the one fetch: the issue and its comment thread, as
// GitHub sent them and decoded.
type issueSnapshot struct {
	rawIssue    json.RawMessage
	rawComments json.RawMessage
	issue       github.Issue
	comments    []github.IssueComment
}

// snapshotJSON is issue.json: the issue object as GitHub sent it, with the
// comment thread merged in under "comments". Nothing else in the repository
// reads it; it is the record of what the run decided on.
func snapshotJSON(s *issueSnapshot) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(s.rawIssue))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("the issue is not a JSON object: %v", err)
	}
	thread := []any{}
	if len(s.rawComments) > 0 {
		dec = json.NewDecoder(bytes.NewReader(s.rawComments))
		dec.UseNumber()
		if err := dec.Decode(&thread); err != nil {
			return nil, fmt.Errorf("the comment thread is not a JSON array: %v", err)
		}
		if thread == nil {
			thread = []any{}
		}
	}
	obj["comments"] = thread
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func runPrepare(args []string) int {
	var issueArg, explicit, outDir, assignee string
	var reEntry, noAck bool
	eventPath := os.Getenv("FALCONET_EVENT_PATH")
	for len(args) > 0 {
		flag := args[0]
		value := func(what string) (string, bool) {
			if len(args) < 2 || args[1] == "" {
				fmt.Fprintf(os.Stderr, "%s needs %s\n", flag, what)
				return "", false
			}
			return args[1], true
		}
		var v string
		var ok bool
		switch flag {
		case "--issue":
			v, ok = value("a number")
			issueArg = v
		case "--config":
			v, ok = value("a file")
			explicit = v
		case "--out-dir":
			v, ok = value("a directory")
			outDir = v
		case "--event":
			v, ok = value("a file")
			eventPath = v
		case "--assignee":
			v, ok = value("a login")
			assignee = v
		case "--re-entry":
			reEntry = true
			args = args[1:]
			continue
		case "--no-ack":
			noAck = true
			args = args[1:]
			continue
		case "-h", "--help":
			return prepareUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return prepareUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}
	if issueArg == "" {
		return prepareUsage()
	}
	// The event schema used to guarantee this was an integer. A CLI caller
	// guarantees nothing, and the number goes into a regex and a branch name.
	if !digits.MatchString(issueArg) {
		fmt.Fprintln(os.Stderr, "--issue must be a number")
		return 2
	}
	number, err := strconv.Atoi(issueArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--issue is too large: %s\n", issueArg)
		return 2
	}

	say := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	die := func(format string, a ...any) int {
		say(format, a...)
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		return die("falconet: cannot determine the working directory: %v", err)
	}
	// Resolve caller-relative paths before the cd.
	if eventPath != "" && !filepath.IsAbs(eventPath) {
		eventPath = filepath.Join(cwd, eventPath)
	}
	if outDir != "" && !filepath.IsAbs(outDir) {
		outDir = filepath.Join(cwd, outDir)
	}
	root, err := repo.Root(cwd)
	if err != nil {
		return die("falconet: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		return die("falconet: cannot enter %s: %v", root, err)
	}
	// Config is read from the repository root, so this follows the cd.
	cfg, err := config.Load(explicit)
	if err != nil {
		return die("falconet: %v", err)
	}
	rules := prepare.Rules{
		QueueLabel:       cfg.Schema.Issue.QueueLabel,
		OptOutText:       cfg.Schema.Issue.OptOutText,
		NeedsInfo:        cfg.Schema.Labels.NeedsInfo,
		BranchPrefix:     cfg.Schema.Issue.BranchPrefix,
		InFlightPrefixes: cfg.Schema.Issue.InFlightPrefixes,
		BlockingLabels:   cfg.Schema.Issue.BlockingLabels,
	}

	// --- the gate's inputs ----------------------------------------------------
	//
	// One fetch at most. With an event payload the gate reads it and an
	// ineligible issue costs no network at all; without one the issue is
	// fetched once, gated, and the same snapshot is reused by the ready path.
	// The issue is not going to change during the next twenty lines.
	//
	// The token and the repository are resolved at the first call that needs
	// them, never at startup: "no network at all" has to mean no credential
	// either, and a workstation run that stops at the gate should not have
	// to explain which repository it would have asked.
	var client *github.Client
	var owner, name string
	connect := func() error {
		if client != nil {
			return nil
		}
		token := github.TokenFromEnv()
		if token == "" {
			return errors.New("no token in GH_TOKEN or GITHUB_TOKEN")
		}
		o, n, err := repo.Repository(root)
		if err != nil {
			return err
		}
		owner, name = o, n
		client = github.New(github.APIURLFromEnv(), token)
		return nil
	}
	var snap *issueSnapshot
	fetchIssue := func() error {
		if snap != nil {
			return nil
		}
		if err := connect(); err != nil {
			return err
		}
		s := &issueSnapshot{}
		raw, err := client.GetIssueRaw(owner, name, number)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &s.issue); err != nil {
			return fmt.Errorf("the issue does not decode: %v", err)
		}
		s.rawIssue = raw
		raw, err = client.ListIssueCommentsRaw(owner, name, number)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &s.comments); err != nil {
			return fmt.Errorf("the comment thread does not decode: %v", err)
		}
		s.rawComments = raw
		snap = s
		return nil
	}

	mode := prepare.Entry
	var gate prepare.Snapshot
	if eventPath != "" {
		ev, s, err := readEvent(eventPath)
		if err != nil {
			return die("prepare: %v", err)
		}
		// The re-entry shape, exactly: a human comment on an issue that is
		// parked needs-info and still queued. `.issue.pull_request` is what
		// distinguishes a PR comment from an issue comment.
		mode = prepare.InferMode(&ev, s.Labels, rules)
		// A bot comment, or a comment on a pull request, is not a way in.
		if prepare.NotAWayIn(&ev) {
			say("issue #%d: comment event is from a bot or on a pull request", number)
			fmt.Println("ineligible")
			return 0
		}
		gate = s
	} else {
		if err := fetchIssue(); err != nil {
			return die("prepare: could not read issue #%d: %v", number, err)
		}
		for _, l := range snap.issue.Labels {
			if l.Name != "" {
				gate.Labels = append(gate.Labels, l.Name)
			}
		}
		gate.State = snap.issue.State
		gate.Body = snap.issue.Body
	}
	if reEntry {
		mode = prepare.ReEntry
	}

	// --- rules 0 to 3 -----------------------------------------------------------
	//
	// See internal/prepare: open, no blocking label, the opt-out box
	// unticked, the queue label present, in that order.
	if reason := prepare.Gate(number, gate, mode, rules); reason != "" {
		say("%s", reason)
		fmt.Println("ineligible")
		return 0
	}

	// --- rule 4: no open pull request is already carrying it --------------------
	//
	// See internal/prepare for the record. The list is fetched whole, then
	// inspected: the first call that needs GitHub on the event path.
	if err := connect(); err != nil {
		return die("prepare: could not list open pull requests for #%d: %v", number, err)
	}
	open, err := client.ListOpenPulls(owner, name)
	if err != nil {
		return die("prepare: could not list open pull requests for #%d: %v", number, err)
	}
	pulls := make([]prepare.Pull, 0, len(open))
	for _, p := range open {
		pulls = append(pulls, prepare.Pull{Number: p.Number, Head: p.Head.Ref})
	}
	if hits := prepare.InFlight(number, pulls, rules); len(hits) > 0 {
		say("%s", prepare.InFlightReason(number, hits))
		fmt.Println("in-flight")
		return 0
	}

	// ===========================================================================
	// ready
	// ===========================================================================

	// The tree must be clean before anything else happens.
	//
	// The agent's outcome is read from the state of the tree, so the tree has
	// to be clean before it starts or the reading is a lie. The origin
	// asserted this AFTER the assignment, the acknowledgment and the branch —
	// so a dirty tree thanked the requester, assigned the issue, cut a branch
	// and then died. The human-facing skill put it in preflight. Preflight is
	// right, and this is as early as it can go while still being free: after
	// the gate, which touches nothing, and before the first mutating call.
	if err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return die("prepare: %s is not a git repository", root)
	}
	status := exec.Command("git", "status", "--porcelain")
	status.Stderr = os.Stderr
	dirt, err := status.Output()
	if err != nil {
		return die("prepare: could not read the working tree")
	}
	if len(bytes.TrimSpace(dirt)) > 0 {
		say("prepare: working tree is dirty before the agent ran:")
		_, _ = os.Stderr.Write(dirt)
		return 1
	}

	out, err := handoff.Init(outDir, cfg, root)
	if err != nil {
		return die("falconet: %v", err)
	}

	if err := fetchIssue(); err != nil {
		return die("prepare: could not read issue #%d: %v", number, err)
	}
	snapshot, err := snapshotJSON(snap)
	if err != nil {
		return die("prepare: could not write the issue snapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(out, "issue.json"), snapshot, 0o644); err != nil {
		return die("falconet: cannot write %s: %v", filepath.Join(out, "issue.json"), err)
	}

	// The requester replied, so clear the parking label rather than spending an
	// agent turn on it. Hard-fails, deliberately, while the assignment and
	// the acknowledgment below are best-effort: an issue left parked while a
	// run proceeds against it is a contradiction a human has to untangle
	// later.
	if mode == prepare.ReEntry {
		if err := client.RemoveIssueLabel(owner, name, number, rules.NeedsInfo); err != nil {
			return die("prepare: could not clear '%s' from #%d: %v", rules.NeedsInfo, number, err)
		}
		say("cleared '%s': this run is a requester reply", rules.NeedsInfo)
	}

	// The assignment. Best effort — it buys one thing, which is dropping this
	// issue out of the unassigned queue a human's own tooling reads, and that
	// is not worth failing a run over. A bot cannot be an assignee, so in CI
	// this records the human who triggered the run. With neither --assignee
	// nor $GITHUB_TRIGGERING_ACTOR the token's own login is asked for — gh's
	// `@me` — and an App token, which has no login, cannot answer, which is
	// a warning like any other failure here.
	who := assignee
	if who == "" {
		who = os.Getenv("GITHUB_TRIGGERING_ACTOR")
	}
	assignErr := error(nil)
	if who == "" {
		who = "the token's own login"
		user, err := client.GetAuthenticatedUser()
		if err != nil {
			assignErr = err
		} else if user.Login == "" {
			assignErr = errors.New("GET /user answered with no login")
		} else {
			who = user.Login
		}
	}
	if assignErr == nil {
		assignErr = client.AddIssueAssignees(owner, name, number, []string{who})
	}
	if assignErr != nil {
		say("warning: could not assign #%d to %s: %v", number, who, assignErr)
	}

	// The acknowledgment, on entry only. Someone who has just answered a
	// question is already mid-conversation with this system and does not need
	// to be greeted again.
	//
	// It exists because the next thing this pipeline says can be twenty
	// minutes away, and silence after filing a request reads as nothing
	// happened. It is scripted so it costs no tokens and cannot be rephrased
	// into something that overpromises: a machine is doing the work, and a
	// person still decides.
	//
	// Written to a file rather than passed inline, which is also how pause
	// does it: the file is the artifact a test can read to assert what a
	// requester saw.
	if mode != prepare.ReEntry && !noAck {
		ackPath := filepath.Join(out, "ack.md")
		if err := os.WriteFile(ackPath, []byte(prepare.Ack), 0o644); err != nil {
			return die("falconet: cannot write %s: %v", ackPath, err)
		}
		if err := client.CreateIssueComment(owner, name, number, prepare.Ack); err != nil {
			say("warning: could not acknowledge #%d to its requester: %v", number, err)
		}
	}

	// The request, in markdown, on disk. Both agents read this file; neither
	// has gh. Built from the snapshot taken before the acknowledgment was
	// posted, which is why the acknowledgment is not in it: the agents should
	// read the requester's words, not this pipeline's.
	thread := make([]prepare.Comment, 0, len(snap.comments))
	for _, c := range snap.comments {
		thread = append(thread, prepare.Comment{Login: c.User.Login, CreatedAt: c.CreatedAt, Body: c.Body})
	}
	request := prepare.Request(snap.issue.Number, snap.issue.Title, snap.issue.Body, thread)
	requestPath := filepath.Join(out, "request.md")
	if err := os.WriteFile(requestPath, []byte(request), 0o644); err != nil {
		return die("falconet: cannot write %s: %v", requestPath, err)
	}
	say("wrote request.md (%d lines)", bytes.Count([]byte(request), []byte{'\n'}))

	// The branch name is mechanics, not judgment (internal/prepare.Slug
	// carries the record): the prefix, the number, the slug.
	branch := prepare.BranchName(rules.BranchPrefix, number, prepare.Slug(snap.issue.Title))

	// A previous run can leave this branch on the remote — its PR closed, or
	// never opened, so the in-flight check let this run start. Pushing onto
	// it would be refused: --force-with-lease says no to a ref it has never
	// seen, which is the right answer, because the alternative is silently
	// overwriting the last run's work. Disambiguate now instead. The prefix
	// survives, so the in-flight check and the containment check still
	// recognize it.
	//
	// --exit-code with both streams discarded means this is never a failure,
	// only an answer: no remote, or no credentials, reads as "no such branch".
	//
	// $GITHUB_RUN_ID is CI-only, and the suffix's only job is to
	// disambiguate: the run id when there is one, the clock otherwise.
	if exec.Command("git", "ls-remote", "--exit-code", "--heads", "origin", branch).Run() == nil {
		branch += "-" + envOr("GITHUB_RUN_ID", strconv.FormatInt(time.Now().Unix(), 10))
		say("a branch by the obvious name already exists on the remote; using %s", branch)
	}

	head := exec.Command("git", "rev-parse", "HEAD")
	head.Stderr = os.Stderr
	headOut, err := head.Output()
	if err != nil {
		return die("prepare: could not read HEAD")
	}
	baseSHA := strings.TrimSpace(string(headOut))
	sw := exec.Command("git", "switch", "-qc", branch)
	sw.Stdout = os.Stderr
	sw.Stderr = os.Stderr
	if err := sw.Run(); err != nil {
		return die("prepare: could not create branch %s", branch)
	}

	// Nothing on a fresh runner has an identity, and the commit is made by a
	// script rather than by an agent's tooling, so without this the commit
	// verb dies on "Please tell me who you are". Set only when unset: on a
	// workstation this is a real repository with a real author, and
	// overwriting that would be a surprise the origin never had to consider.
	if exec.Command("git", "config", "user.email").Run() != nil {
		for _, kv := range [][2]string{
			{"user.name", "github-actions[bot]"},
			{"user.email", "41898282+github-actions[bot]@users.noreply.github.com"},
		} {
			set := exec.Command("git", "config", kv[0], kv[1])
			set.Stdout = os.Stderr
			set.Stderr = os.Stderr
			if err := set.Run(); err != nil {
				return die("prepare: could not set %s", kv[0])
			}
		}
	}

	for _, f := range []struct{ name, value string }{
		{"branch.txt", branch},
		{"base-sha.txt", baseSHA},
	} {
		if err := os.WriteFile(filepath.Join(out, f.name), []byte(f.value+"\n"), 0o644); err != nil {
			return die("falconet: cannot write %s: %v", filepath.Join(out, f.name), err)
		}
	}
	if err := handoff.GitHubEnvAppend("BRANCH="+branch, "BASE_SHA="+baseSHA); err != nil {
		return die("falconet: %v", err)
	}

	// What main already plans, before anyone touches anything. Handing this
	// to the implementing agent is what stops it trying to fix pre-existing
	// drift, and it is what lets a reviewer tell this change's plan lines
	// from main's.
	//
	// Hard-fails on purpose: if main itself cannot plan, no amount of agent
	// time will fix it, and failing here costs nothing because no agent has
	// run yet.
	//
	// The stacks, the init that comes first when tofu is the planner, and the
	// plan's argv are internal/stacks, shared with validate so that the two
	// verbs hand tofu the same thing; the baseline lands in one file, each
	// stack's output under a `## <stack>` heading when more than one is
	// planned, and in exactly the bytes tofu wrote when one is.
	planPath := filepath.Join(out, "plan-baseline.txt")
	baseline, err := os.Create(planPath)
	if err != nil {
		return die("falconet: cannot write %s: %v", planPath, err)
	}
	defer func() { _ = baseline.Close() }()
	planStacks := nonEmpty(cfg.Schema.Stacks.Plan)
	multi := len(planStacks) > 1
	initFirst := stacks.InitFirst(cfg.Schema.Plan.Command)
	runner := stacks.Runner{Tofu: envOr("TOFU", "tofu"), RepoRoot: root}

	scratchFile, err := os.CreateTemp("", "falconet-prepare-*")
	if err != nil {
		return die("falconet: cannot create a scratch file: %v", err)
	}
	scratch := scratchFile.Name()
	_ = scratchFile.Close()
	stackPlan := scratch + ".plan"
	defer func() {
		_ = os.Remove(scratch)
		_ = os.Remove(stackPlan)
	}()

	for _, s := range planStacks {
		if !isDir(runner.Dir(s)) {
			return die("prepare: %s", cfg.StackMissing("plan", s, root))
		}
		if initFirst {
			ok, err := runInit(runner, s, scratch)
			if err != nil {
				return die("falconet: %v", err)
			}
			if !ok {
				say("prepare: tofu init failed in %s/ — the stack cannot be planned:", s)
				if err := copyToStderr(scratch); err != nil {
					return die("falconet: %v", err)
				}
				return 1
			}
		}
		argv, err := runner.PlanCommand(cfg.Schema.Plan.Command, s)
		if err != nil {
			return die("falconet: %v (set plan.command in %s)", err, orDefault(cfg.File, ".github/falconet.json"))
		}
		// stdout to its own file, stderr to the scratch. See stacks.Run for
		// why the plan is never a pipe into this process.
		ok, err := runPlan(argv, stackPlan, scratch)
		if err != nil {
			return die("falconet: %v", err)
		}
		if !ok {
			say("prepare: the baseline plan failed on %s/ — main does not plan cleanly:", s)
			if err := copyToStderr(scratch); err != nil {
				return die("falconet: %v", err)
			}
			return 1
		}
		planBytes, err := os.ReadFile(stackPlan)
		if err != nil {
			return die("falconet: cannot read %s: %v", stackPlan, err)
		}
		if multi {
			if _, err := baseline.WriteString("## " + s + "\n\n"); err != nil {
				return die("falconet: cannot write %s: %v", planPath, err)
			}
		}
		if _, err := baseline.Write(planBytes); err != nil {
			return die("falconet: cannot write %s: %v", planPath, err)
		}
	}
	if err := baseline.Close(); err != nil {
		return die("falconet: cannot write %s: %v", planPath, err)
	}
	written, err := os.ReadFile(planPath)
	if err != nil {
		return die("falconet: cannot read %s: %v", planPath, err)
	}
	say("baseline plan: %d lines", bytes.Count(written, []byte{'\n'}))
	say("working branch %s from %s", branch, prefix(baseSHA, 7))

	fmt.Println("ready")
	return 0
}

// runInit runs `tofu init` in the stack with both streams into the scratch
// file — truncated first — and returns whether it succeeded. A tofu that
// could not be started at all has that written into the file as well, so the
// failure names its cause; the error return is for the scratch file itself.
func runInit(r stacks.Runner, stack, scratch string) (bool, error) {
	f, err := os.OpenFile(scratch, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o600)
	if err != nil {
		return false, fmt.Errorf("cannot write %s: %v", scratch, err)
	}
	ok := toFile(f, stacks.Run(r.Init(stack, true), f, f))
	if cerr := f.Close(); cerr != nil {
		return false, fmt.Errorf("cannot write %s: %v", scratch, cerr)
	}
	return ok, nil
}

// copyToStderr is `cat FILE >&2`.
func copyToStderr(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %v", path, err)
	}
	_, _ = os.Stderr.Write(b)
	return nil
}
