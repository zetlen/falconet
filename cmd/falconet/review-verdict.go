package main

// review-verdict — turn the review agent's final message into the files a
// workflow reads.
//
// UNWIRED, ON PURPOSE. `falconet review-verdict` is a door, not a verb: it is
// unlisted in usage (main.go's `unlisted`; review-verdict.test.sh holds that),
// and it has no caller anywhere in this repository — the reusable workflow
// names it zero times, and contract.test.sh fails if that changes. It ships
// as the reference implementation of the verdict protocol and nothing else.
//
// ADR-0002 dropped the independent review agent on measurements: the watchdog
// cost ~44% of the worker on a small task, and two cold contexts cost more
// than one warm one. ADR-0001 risk 9 stands, including its bar for any
// future replacement — which this file is the record of, not an invitation.
// Anything that wires it up must first clear that bar: an independent,
// uncontaminated read of the diff, the commit message and the plan, before a
// human is asked to look. A review that reads the implementing agent's
// reasoning, or that shares its context, is not that.
//
// The invariant is asserted, not merely written down: the contract test
// requires this file be referenced zero times by the reusable workflow.
//
// The reviewing agent is granted exactly Read, Grep and Glob. No Bash, no
// Edit, no Write: it cannot run a command, cannot touch the working tree, and
// cannot reach GitHub — not by design accident but on purpose, because its
// only job is to look at the artifacts with fresh eyes and say yes or no.
// That also means it cannot put its own verdict on disk. So it ends its run
// with a sentinel line and this verb does the filing.
//
// The sentinel rule and the record of run 32093607680 are internal/verdict;
// this file is the flags, the execution log, the two files, and the exit
// code. It reads no config: the one directory it needs is the handoff
// directory at its default, and the out-dir flag names any other.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/verdict"
)

const reviewVerdictUsageText = `review-verdict — turn the review agent's final message into the files a
workflow reads. Unwired: the reference implementation of the verdict
protocol, with no caller (ADR-0002; ADR-0001 risk 9).

Modes:
  falconet review-verdict [--execution-file FILE] [--out-dir DIR]

Read the JSON message log claude-code-action writes (its execution_file
step output; default $RUNNER_TEMP/claude-execution-output.json, /tmp when
$RUNNER_TEMP is unset), take the text of the final "result" message, and
route it by the first SENTINEL LINE anywhere in it:

    APPROVED           -> the rest is written to DIR/pr-body.md
    CHANGES REQUESTED  -> the rest is written to DIR/rejection.md

On the APPROVED path "the rest" is normally EMPTY, and that is the intended
shape: the prompt tells the reviewer to say nothing after the sentinel,
because the pull-request description is the implementing agent's commit
message (DIR/commit-body.md), not anything the reviewer writes. pr-body.md
is kept as a record for whoever is debugging a run; no stage reads it.
rejection.md, by contrast, is read and posted to the requester's issue.

DIR defaults to .falconet/ at the root of the repository this is run in
(the working directory when there is no repository) — the handoff
directory at its default. This verb reads no config: handoff_dir in
.github/falconet.json is not consulted, and --out-dir is the only way to
name another directory. The execution file is the one thing that does NOT
live there: claude-code-action chooses where to write its own log, and it
writes it under $RUNNER_TEMP. This verb only ever reads it.

Surrounding markdown emphasis (#, *, ` + "`" + `, _) and trailing punctuation are
stripped before matching, and the match is case-insensitive: be liberal
about the formatting, strict about the words. The sentinel must stand on a
line of its own; the whole message is scanned for the first such line.

Prints exactly one word on stdout — approved | rejected | missing.
"missing" means no usable verdict was found (no execution file, no result
message, or no recognizable sentinel). The caller MUST treat that as "not
approved" and park the issue; guessing on a reviewer's behalf is the one
thing this whole stage exists to prevent.

Exit codes: 0 = a verdict word was printed (including "missing"),
            1 = DIR could not be prepared,
            2 = usage error.
`

func reviewVerdictUsage() int {
	fmt.Fprint(os.Stderr, reviewVerdictUsageText)
	return 2
}

func runReviewVerdict(args []string) int {
	var execFile, outDir string
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
		case "--execution-file":
			v, ok = value("a path")
			execFile = v
		case "--out-dir":
			v, ok = value("a directory")
			outDir = v
		case "-h", "--help":
			return reviewVerdictUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return reviewVerdictUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}

	if outDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
			return 1
		}
		root, err := repo.Root(cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		outDir = filepath.Join(root, ".falconet")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot create %s: %v\n", outDir, err)
		return 1
	}
	approval := filepath.Join(outDir, "pr-body.md")
	rejection := filepath.Join(outDir, "rejection.md")
	// A verdict from a previous review round must never be mistaken for this one.
	for _, stale := range []string{approval, rejection} {
		if err := os.Remove(stale); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "falconet: cannot remove %s: %v\n", stale, err)
			return 1
		}
	}

	// Not the out directory: the action's log is the action's business, and
	// it puts it in $RUNNER_TEMP. The caller normally passes the step's
	// execution_file output and this default never fires.
	if execFile == "" {
		execFile = filepath.Join(envOr("RUNNER_TEMP", "/tmp"), "claude-execution-output.json")
	}

	// Every way out from here is a word and exit 0: missing is an answer,
	// and the caller routes on it.
	missing := func(why string) int {
		fmt.Fprintln(os.Stderr, why)
		fmt.Println("missing")
		return 0
	}

	log, err := os.ReadFile(execFile)
	if err != nil || len(log) == 0 {
		return missing(fmt.Sprintf("no execution log at %s — the reviewer produced nothing", execFile))
	}
	final := verdict.Final(log)
	if verdict.IsBlank(final) {
		return missing(fmt.Sprintf("no final result message in %s", execFile))
	}

	result, ok := verdict.Parse(final)
	if !ok {
		// Quote the opening line: when a reviewer buries or mangles its
		// verdict, that line is what a human debugging the run needs to see.
		return missing(fmt.Sprintf("no verdict sentinel on a line of its own: %s", verdict.Opener(final, 120)))
	}

	// Exactly one newline at the end, as `printf '%s\n'` wrote it.
	write := func(path, body string) bool {
		if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", path, err)
			return false
		}
		return true
	}

	// No catch-all: Parse already refused everything that is not one of the
	// sentinels, so the two arms below are exhaustive by construction and the
	// default arm exists only to make a future edit to the sentinel table —
	// or a third Verdict — fail loudly here instead of silently filing a new
	// verdict word as a rejection.
	switch result.Verdict {
	case verdict.Approved:
		// An empty body is the EXPECTED shape of an approval now, not a
		// defect: the prompt tells the reviewer to say nothing after APPROVED,
		// and the pull-request description is the implementing agent's commit
		// message, assembled from the handoff directory's commit-body.md.
		// Nothing reads this file any more. There was a stand-in paragraph
		// and a `::warning::` here; both would have fired on every successful
		// run from now on, which is warning fatigue on the happy path. The
		// file is still written — an approval that did carry prose is worth
		// keeping in the handoff directory for whoever is debugging a run.
		if !write(approval, result.Body) {
			return 1
		}
		fmt.Println("approved")
	case verdict.Rejected:
		body := result.Body
		if verdict.IsBlank(body) {
			body = "The reviewing agent rejected this change but gave no reasons."
		}
		if !write(rejection, body) {
			return 1
		}
		fmt.Println("rejected")
	default:
		fmt.Fprintf(os.Stderr, "sentinel %q matched the scan but no arm of the switch — verdict.Sentinels and the switch here have drifted apart\n", result.Sentinel)
		fmt.Println("missing")
	}
	return 0
}
