package main

// commit — read the implementing agent's outcome off the disk and, where
// there is one, make the commit that agent can no longer make itself.
//
// The implementing stage used to hold `Bash(git add:*)` and `Bash(git
// commit:*)`, and its prompt carried a paragraph of permission-matcher tax
// ("a single simple command ... no heredoc, no $(...), no &&"). It now holds
// no Bash at all: it edits files, writes its commit message to
// .ci-handoff/commit-msg.txt, and stops. This verb does the rest.
//
// That is worth more than two fewer tool grants. "Did the agent commit?" used
// to be a claim to check — claude-code-action reports `conclusion: success`
// for a run that did nothing. It is now a question about the tree, and the
// tree does not have opinions.
//
// The guards are internal/commit, which carries the incident record above
// each; the secret scan is internal/scan. This file is the sequence they run
// in, the subprocesses between them — git and tofu — the files, and the exit
// code. It changes directory to the repository root and stays there, as the
// script did: every path git reports is relative to that root, and every
// path built from those reports only resolves correctly from there.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/zetlen/falconet/internal/commit"
	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/handoff"
	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/scan"
)

const commitUsageText = `commit — read the implementing agent's outcome off the disk and, where
there is one, make the commit that agent can no longer make itself.

Modes:
  falconet commit [--out-dir DIR] [--config FILE]

Prints exactly one word on stdout — the outcome — and nothing else:

  needs-info  DIR/needs-info.md is non-empty. The requester gets asked.
  success     the tree is dirty AND DIR/commit-msg.txt is non-empty. The
              touched .tf files have been formatted, everything is
              committed, and the subject and body are filed for the
              pull-request stage.
  failure     anything else. DIR/failure-reason.txt says what, in prose a
              requester can read.

needs-info wins over an ordinary success: an agent that both committed work
and asked a question keeps its commit, because the push step runs before
the park, which is the ordering run 32093607680 taught this pipeline. A
path or content violation is decided BEFORE needs-info is even consulted,
though, and beats it regardless of whether questions were also written: a
refused run commits nothing, so there is no committed work to protect, and
a run that both tried to escalate and asked a question should fail loudly
rather than park quietly.

Outputs, written into DIR (default: handoff_dir from config, .falconet/ at
the root of the repository):
  commit-subject.txt   the message's first line — the pull-request TITLE
  commit-body.md       the rest of the message — the pull-request BODY
  failure-reason.txt   written only on failure

Exit codes: 0 = an outcome was determined and printed
            1 = git, tofu or the secret scan refused; nothing is printed,
                stderr says why
            2 = usage error (including --help: it isn't one of the three
                outcomes, so it does not get an outcome's exit code)

$TOFU overrides the formatter and $GITLEAKS the secret scanner, for the
tests.
`

func commitUsage() int {
	fmt.Fprint(os.Stderr, commitUsageText)
	return 2
}

func runCommit(args []string) int {
	var outDir, explicit string
	for len(args) > 0 {
		flag := args[0]
		value := func(what string) (string, bool) {
			if len(args) < 2 {
				fmt.Fprintf(os.Stderr, "%s needs %s\n", flag, what)
				return "", false
			}
			return args[1], true
		}
		var v string
		var ok bool
		switch flag {
		case "--out-dir":
			v, ok = value("a directory")
			outDir = v
		case "--config":
			v, ok = value("a file")
			explicit = v
		case "-h", "--help":
			return commitUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return commitUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
		return 1
	}
	// Resolve --out-dir against the caller's CWD before changing directories:
	// git status below reports paths relative to the repository root, and
	// every path built from those reports (the file checks, tofu fmt, git
	// add) only resolves correctly if this process is standing in the root
	// when it uses them.
	if outDir != "" && !filepath.IsAbs(outDir) {
		outDir = filepath.Join(cwd, outDir)
	}
	root, err := repo.Root(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot enter %s: %v\n", root, err)
		return 1
	}

	// Config is read from the repository root, so this follows the cd. An
	// explicit --out-dir still wins over handoff_dir; that is what
	// handoff.Init is given.
	cfg, err := config.Load(explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}
	out, err := handoff.Init(outDir, cfg, root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}

	// --- the policy, out of config ----------------------------------------
	//
	// Read once, here, rather than at each use: a guard that re-reads its own
	// rule mid-run is a guard whose behavior depends on when you look.
	policy, err := commit.NewPolicy(cfg.Schema.Paths.Allow, cfg.Schema.Paths.DenyContent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}

	questions := filepath.Join(out, "needs-info.md")
	message := filepath.Join(out, "commit-msg.txt")
	reasonFile := filepath.Join(out, "failure-reason.txt")
	if err := os.Remove(reasonFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "falconet: cannot remove %s: %v\n", reasonFile, err)
		return 1
	}

	// A failure is an outcome, not an error: print the word, exit 0, let the
	// workflow route it.
	giveUp := func(text string) int {
		if err := os.WriteFile(reasonFile, []byte(text), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", reasonFile, err)
			return 1
		}
		fmt.Println("failure")
		return 0
	}
	parked := func() bool { return nonEmptyFile(questions) }

	// --- the publish-boundary secret scan ---------------------------------
	//
	// See internal/commit's header. The scanner's report is a list of the
	// channels that matched, and it comes back as values rather than through
	// this process's stdout: this verb's only contract with the workflow is
	// that its own stdout is exactly one word, and the lesson of `tofu fmt`
	// below is that a subprocess with something to say will say it into that
	// contract if you let it.
	//
	// A broken scanner is a mechanical failure (exit 1, no outcome word), not
	// a pass. "gitleaks is not installed" and "gitleaks found nothing" must
	// never produce the same run.
	scanner := &scan.Scanner{Gitleaks: envOr("GITLEAKS", "gitleaks"), Root: root, Stderr: os.Stderr}
	refuseOnSecret := func(files []string, staged bool) (rc int, done bool) {
		var found []string
		hit, err := scanner.Scan(files, staged, func(label string) { found = append(found, label) })
		if err != nil {
			fmt.Fprintf(os.Stderr, "scan: %v\n", err)
			fmt.Fprintln(os.Stderr, "the secret scan could not be run; refusing to")
			fmt.Fprintln(os.Stderr, "continue, because a scan that did not happen is not a pass")
			return 1, true
		}
		if hit {
			return giveUp(commit.ReasonSecret(found)), true
		}
		return 0, false
	}

	// --- what did the agent leave behind? ---------------------------------
	//
	// See commit.ParseStatus for the -z and the rename arm. The command's own
	// exit status is checked: running outside a git repository must be a
	// mechanical failure, not a false "the tree is untouched".
	status := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "-z")
	status.Stderr = os.Stderr
	listing, err := status.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "git status failed")
		return 1
	}
	changed, renamed := commit.ParseStatus(listing)
	if renamed != nil {
		return giveUp(commit.ReasonRename(renamed.Code, renamed.Path))
	}

	// --- the allowlist ------------------------------------------------------
	//
	// A path is allowed if ANY paths.allow glob matches it (commit.AllowPattern
	// says what a glob means). An allowed path that no longer exists on disk
	// is neither scanned nor refused: a deleted file cannot carry new
	// executable content.
	var denied, existing []string
	for _, path := range changed {
		if !policy.PathAllowed(path) {
			denied = append(denied, path)
			continue
		}
		if isRegularFile(path) {
			existing = append(existing, path)
		}
	}

	// --- the content denylist -----------------------------------------------
	//
	// See "The content denylist" in internal/commit. Only files that still
	// exist on disk are readable (existing already filters that); a deleted
	// .tf file cannot carry new executable content. A file that exists and
	// cannot be read is a mechanical failure: a guard that could not look is
	// not a guard that found nothing.
	var contentDenied []string
	for _, path := range existing {
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", path, err)
			return 1
		}
		if construct, hit := policy.DenylistHit(content); hit {
			contentDenied = append(contentDenied, path+": "+construct)
		}
	}

	// Both refusals below run before either needs-info exit — Ruling B: a
	// denied run commits nothing, so there is no committed work for
	// needs-info's ordering to protect, and an issue that both tried to
	// escalate and asked a question should fail loudly rather than park
	// quietly.
	if len(denied) > 0 {
		return giveUp(commit.ReasonDeniedPaths(policy.Allow, denied))
	}
	if len(contentDenied) > 0 {
		return giveUp(commit.ReasonDeniedContent(contentDenied))
	}

	// Above both needs-info exits, for the same reason the two refusals above
	// are: a run that leaked something must not park quietly with the leak in
	// the very comment that parks it. needs-info.md is scanned here precisely
	// because it is the file the park verb posts unfenced.
	if rc, done := refuseOnSecret([]string{message, questions}, false); done {
		return rc
	}

	if len(changed) == 0 {
		if parked() {
			fmt.Println("needs-info")
			return 0
		}
		return giveUp(commit.ReasonUnchanged())
	}

	if !nonEmptyFile(message) {
		if parked() {
			fmt.Println("needs-info")
			return 0
		}
		return giveUp(commit.ReasonNoMessage(changed))
	}

	// --- format, then commit ------------------------------------------------
	//
	// One target per invocation: `tofu fmt` takes a single file or directory,
	// not a list. Never -recursive, and never a path the agent did not touch —
	// main is fmt-clean today, and the point of the narrow scope is that a
	// future regression somewhere else cannot ride into an unrelated change.
	// The `--` guards a file whose name looks like a flag (an agent might
	// create `-check.tf`); the stdout redirect matters more: `tofu fmt` prints
	// the reformatted file's own name to STDOUT on success, and this verb's
	// only contract with its caller is that stdout is exactly one of three
	// words.
	tofu := envOr("TOFU", "tofu")
	for _, path := range existing {
		format := exec.Command(tofu, "fmt", "--", path)
		format.Stdout = os.Stderr
		format.Stderr = os.Stderr
		if err := format.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "tofu fmt failed on %s\n", path)
			return 1
		}
	}

	// Stage exactly the vetted paths — not `git add -A` — so the security
	// boundary enforced above is a pathspec you can read, not an invariant
	// about what `-A` happens to pick up given everything checked so far. To
	// stderr: same reasoning as `tofu fmt` above, though `git add` is
	// ordinarily silent.
	add := exec.Command("git", append([]string{"add", "--"}, changed...)...)
	add.Stdout = os.Stderr
	add.Stderr = os.Stderr
	if err := add.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "git add failed")
		return 1
	}

	// `tofu fmt` can turn an agent's edit into a no-op — whitespace it just
	// undid, say — leaving nothing staged. `git commit` would fail on that,
	// but with "nothing to commit, working tree clean" on STDOUT even under
	// -q, which would otherwise leak into this verb's own stdout contract.
	// Caught here instead: an empty change is `failure`, not a mechanical
	// error, so it gets failure's exit code (0) rather than a git failure's
	// (1). `--quiet` exits 0 for no difference and 1 for one; anything else
	// is git refusing, and is a mechanical failure.
	quiet := exec.Command("git", "diff", "--cached", "--quiet")
	quiet.Stderr = os.Stderr
	var exit *exec.ExitError
	switch err := quiet.Run(); {
	case err == nil:
		return giveUp(commit.ReasonEmptyAfterFmt(changed))
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		// Something is staged. Carry on.
	default:
		fmt.Fprintln(os.Stderr, "git diff --cached failed")
		return 1
	}

	// The diff, now that there is one to read, and still before the commit:
	// the branch is pushed immediately after this verb returns, so a
	// credential that reaches a commit reaches the remote. .tf content is
	// also what `tofu plan` echoes into plan.txt and from there into the
	// pull-request body, so this arm guards that channel too.
	if rc, done := refuseOnSecret(nil, true); done {
		return rc
	}

	gitCommit := exec.Command("git", "commit", "-q", "-F", message)
	gitCommit.Stdout = os.Stderr
	gitCommit.Stderr = os.Stderr
	if err := gitCommit.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "git commit failed")
		return 1
	}

	text, err := os.ReadFile(message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", message, err)
		return 1
	}
	subjectFile := filepath.Join(out, "commit-subject.txt")
	bodyFile := filepath.Join(out, "commit-body.md")
	if err := os.WriteFile(subjectFile, commit.Subject(text), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", subjectFile, err)
		return 1
	}
	if err := os.WriteFile(bodyFile, commit.Body(text), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", bodyFile, err)
		return 1
	}

	if parked() {
		fmt.Println("needs-info")
	} else {
		fmt.Println("success")
	}
	return 0
}

// nonEmptyFile is `[[ -s path ]]`: it exists and has something in it.
func nonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// envOr is an environment variable, or a default when it is unset or empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
