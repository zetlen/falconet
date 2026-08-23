package main

// park — put an infra-request issue into a terminal state and say so, in
// plain language, where the requester will see it. The comment and the two
// rules it is held to are internal/park, which carries the record; this file
// is the flags, the body file, the three GitHub calls, and the exit code.
//
// The first verb to talk to GitHub without `gh` (ADR-0006 D2): three calls
// against GITHUB_API_URL with the token from GH_TOKEN or GITHUB_TOKEN, on the
// repository GITHUB_REPOSITORY names. That variable is the only source — it
// is set in every Actions run, and a repository guessed from a git remote is
// how a comment lands on the wrong one. A local run exports it.

import (
	"fmt"
	"os"
	"strconv"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/github"
	"github.com/zetlen/falconet/internal/park"
)

const parkUsageText = `park — put an infra-request issue into a terminal state and say so, in
plain language, where the requester will see it.

Modes:
  falconet park --issue N --label needs-info|ready-for-human
                --preamble TEXT
                [--body FILE] [--body-title TEXT]
                [--run-url URL] [--unassign LOGIN] [--branch NAME]
                [--config FILE]

    --preamble    the plain-language sentence the requester reads first
    --body        extra detail appended after the preamble. A file that is
                  missing or empty is no detail, not an error: a run parked
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
    --label       one of the two parking labels from config
                  (labels.needs_info, labels.human); anything else is a
                  usage error

The comment is capped at 60000 characters; if --body is longer it is cut on
a line boundary with an explicit note pointing at --run-url. As everywhere
else in this pipeline, content is dropped loudly or not at all.

Requires GH_TOKEN or GITHUB_TOKEN, and GITHUB_REPOSITORY (owner/name), in
the environment. GITHUB_API_URL overrides the API endpoint.

Exit codes: 0 = parked, 1 = a GitHub call failed (the caller must treat the
            issue as still un-parked), 2 = usage error.
`

func parkUsage() int {
	fmt.Fprint(os.Stderr, parkUsageText)
	return 2
}

func runPark(args []string) int {
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
			return parkUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return parkUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}

	if issue == "" || label == "" || preamble == "" {
		return parkUsage()
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

	// Config is read where this verb stands, as it always was: park never
	// needed the repository root, because it operates on an issue and not on
	// a tree.
	cfg, err := config.Load(explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}
	if err := park.Label(label, cfg.Schema.Labels.NeedsInfo, cfg.Schema.Labels.Human); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	// Both of these are checked before anything is built or sent: a park
	// with nowhere to post is a mechanical failure, and the caller must know
	// the issue is still un-parked.
	token := github.TokenFromEnv()
	if token == "" {
		fmt.Fprintf(os.Stderr, "park needs a token in GH_TOKEN or GITHUB_TOKEN to comment on #%d\n", number)
		return 1
	}
	owner, name, err := github.SplitRepository(os.Getenv("GITHUB_REPOSITORY"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "park needs GITHUB_REPOSITORY (owner/name) to know which repository #%d is in: %v\n", number, err)
		return 1
	}

	// `[[ -s FILE ]]`: a missing or empty body is no body — the workflow
	// passes the handoff file a step MAY have written, and a run parked
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
			fmt.Fprintf(os.Stderr, "--body names a directory: %s\n", bodyPath)
			return 1
		case info.Size() > 0:
			body, err = os.ReadFile(bodyPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", bodyPath, err)
				return 1
			}
		}
	}

	comment := park.Comment(park.Input{
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
	// still better parked than not, and the exit code says it was partial.
	client := github.New(github.APIURLFromEnv(), token)
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
		// workflow) and an issue that keeps a stale assignee is still parked.
		if err := client.RemoveIssueAssignees(owner, name, number, []string{unassign}); err != nil {
			fmt.Fprintf(os.Stderr, "::warning::could not un-assign %s from #%d: %v\n", unassign, number, err)
		}
	}

	if status == 0 {
		fmt.Printf("issue #%d parked %s\n", number, label)
		return 0
	}
	fmt.Fprintf(os.Stderr, "failed to fully park issue #%d\n", number)
	return 1
}
