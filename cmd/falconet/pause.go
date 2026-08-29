package main

// pause — put an infra-request issue into a terminal state and say so, in
// plain language, where the requester will see it. The comment and the two
// rules it is held to are internal/pause, which carries the record; this file
// is the flags, the body file, the three GitHub calls, and the exit code.
//
// It was `park` until #5's rename was taken, the commit after #15 landed the
// port. No alias: there are no users yet, and two words for one verb is the
// drift #5 was filed to prevent.
//
// GitHub calls go through the `Client` adapter in internal/github, backed by
// `gh api`, against the repository GITHUB_REPOSITORY names. That variable is
// the only source — it is set in every Actions run, and a repository guessed
// from a git remote is how a comment lands on the wrong one. A local run
// exports it.

import (
	"fmt"
	"os"
	"strconv"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/github"
	"github.com/zetlen/falconet/internal/pause"
)

const pauseUsageText = `pause — put an infra-request issue into a terminal state and say so, in
plain language, where the requester will see it.

Modes:
  falconet pause --issue N --label needs-info|ready-for-human
                 --preamble TEXT
                 [--body FILE] [--body-title TEXT]
                 [--run-url URL] [--unassign LOGIN] [--branch NAME]
                 [--config FILE]

    --preamble    the plain-language sentence the requester reads first
    --body        extra detail appended after the preamble. A file that is
                  missing or empty is no detail, not an error: a run paused
                  before it planned has no plan.
    --body-title  if given, --body is folded into a collapsed <details>
                  block and fenced as code. Use it for machine output
                  (validation logs, plan errors); omit it when --body is
                  already prose written for a human.
    --unassign    release the claim (see the workflow's claim step)
    --branch      the pushed working branch carrying the commits this run
                  made, named and linked immediately under the preamble.
                  Pass it wherever a commit exists; pass nothing (or an
                  empty string) where none does.
    --label       one of the two pause labels from config
                  (labels.needs_info, labels.human); anything else is a
                  usage error

The comment is capped at 60000 characters; if --body is longer it is cut on
a line boundary with an explicit note pointing at --run-url. As everywhere
else in this pipeline, content is dropped loudly or not at all.

Requires GH_TOKEN or GITHUB_TOKEN, and GITHUB_REPOSITORY (owner/name), in
the environment. GITHUB_API_URL overrides the API endpoint.

Prints exactly one word on stdout, once the flags have been read:

  success   the comment is posted and the label is on the issue. Releasing
            the claim is best-effort: a failed un-assign is a warning.
  failure   anything else — a GitHub call refused, no token, no repository,
            a --body that cannot be read. The caller must treat the issue
            as still un-paused.

Exit codes: 0 = success, 1 = failure, 2 = usage error (nothing on stdout).

failure is exit 1 here, where commit's is 0, because nothing downstream
routes on this verb's word: a pause that did not fully happen must fail the
step that asked for it, so that the containment job runs and tries again.
A step that passed with "failure" in an output nobody reads is the silent
disappearance this verb exists to prevent.
`

func pauseUsage() int {
	fmt.Fprint(os.Stderr, pauseUsageText)
	return 2
}

func runPause(args []string) int {
	var issue, label, preamble, bodyPath, bodyTitle, runURL, unassign, branch, explicit string
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
			issue = v
		case "--label":
			v, ok = value("a label")
			label = v
		case "--preamble":
			v, ok = value("text")
			preamble = v
		case "--body":
			v, ok = value("a file")
			bodyPath = v
		case "--body-title":
			v, ok = value("text")
			bodyTitle = v
		case "--run-url":
			v, ok = value("a URL")
			runURL = v
		case "--unassign":
			v, ok = value("a login")
			unassign = v
		case "--branch":
			// An empty --branch is legal and means "no branch": the caller is
			// a workflow step that may or may not have one, and forcing every
			// one of them to build its argument list conditionally is how a
			// hand-over path gets missed.
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "--branch needs a value (the empty string is fine)")
				return 2
			}
			branch, ok = args[1], true
		case "--config":
			v, ok = value("a file")
			explicit = v
		case "-h", "--help":
			return pauseUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return pauseUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}

	if issue == "" || label == "" || preamble == "" {
		return pauseUsage()
	}
	if !digits.MatchString(issue) {
		fmt.Fprintln(os.Stderr, "--issue must be a number")
		return 2
	}
	number, err := strconv.Atoi(issue)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--issue is too large: %s\n", issue)
		return 2
	}

	// From here on every refusal is the word `failure` and exit 1: the issue
	// is not paused, and the caller hears that from the word and the exit
	// code both. See the usage text for why failure is not exit 0 here.
	failure := func(format string, a ...any) int {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
		fmt.Println("failure")
		return 1
	}

	// Config is read where this verb stands, as it always was: pause never
	// needed the repository root, because it operates on an issue and not on
	// a tree.
	cfg, err := config.Load(explicit)
	if err != nil {
		return failure("falconet: %v", err)
	}
	if err := pause.Label(label, cfg.Schema.Labels.NeedsInfo, cfg.Schema.Labels.Human); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	// Both of these are checked before anything is built or sent: a pause
	// with nowhere to post is a failure before any call, and the caller must
	// know the issue is still un-paused.
	token := github.TokenFromEnv()
	if token == "" {
		return failure("pause needs a token in GH_TOKEN or GITHUB_TOKEN to comment on #%d", number)
	}
	owner, name, err := github.SplitRepository(os.Getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return failure("pause needs GITHUB_REPOSITORY (owner/name) to know which repository #%d is in: %v", number, err)
	}

	// `[[ -s FILE ]]`: a missing or empty body is no body — the workflow
	// passes the handoff file a step MAY have written, and a run paused
	// before it planned has no plan. A directory, or a file that exists and
	// cannot be read, is a mechanical failure: the bash posted a comment with
	// nothing where the detail should have been.
	var body []byte
	if bodyPath != "" {
		info, err := os.Stat(bodyPath)
		switch {
		case err != nil:
			// No file: no detail.
		case info.IsDir():
			return failure("--body names a directory: %s", bodyPath)
		case info.Size() > 0:
			body, err = os.ReadFile(bodyPath)
			if err != nil {
				return failure("falconet: cannot read %s: %v", bodyPath, err)
			}
		}
	}

	comment := pause.Comment(pause.Input{
		Preamble:   preamble,
		Branch:     branch,
		ServerURL:  os.Getenv("GITHUB_SERVER_URL"),
		Repository: os.Getenv("GITHUB_REPOSITORY"),
		Body:       body,
		BodyTitle:  bodyTitle,
		RunURL:     runURL,
	})

	// The three things "stopped" always means, each attempted regardless of
	// the one before: an issue that got its label and not its comment is
	// still better paused than not, and the word and the exit code say it
	// was partial.
	client := github.NewGH(github.APIURLFromEnv(), token)
	status := 0
	if err := client.CreateIssueComment(owner, name, number, string(comment)); err != nil {
		fmt.Fprintf(os.Stderr, "could not comment on #%d: %v\n", number, err)
		status = 1
	}
	if err := client.AddIssueLabels(owner, name, number, []string{label}); err != nil {
		fmt.Fprintf(os.Stderr, "could not add label %s to #%d: %v\n", label, number, err)
		status = 1
	}
	if unassign != "" {
		// Releasing the claim is best-effort: the claim itself is (see the
		// workflow) and an issue that keeps a stale assignee is still paused.
		if err := client.RemoveIssueAssignees(owner, name, number, []string{unassign}); err != nil {
			fmt.Fprintf(os.Stderr, "::warning::could not un-assign %s from #%d: %v\n", unassign, number, err)
		}
	}

	if status == 0 {
		fmt.Println("success")
		return 0
	}
	return failure("failed to fully pause issue #%d", number)
}
