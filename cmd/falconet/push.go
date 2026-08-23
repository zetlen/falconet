package main

// push — get the working branch onto the remote the moment there is a commit
// on it, so no terminal path in the pipeline can throw the work away.
//
// This verb is a subprocess sequence and nothing else — two or three git
// commands and one line into $GITHUB_ENV — so the record lives here rather
// than in an internal package: the comment above each step is the step.
//
// ---------------------------------------------------------------------------
// The incident
// ---------------------------------------------------------------------------
// Until run 32093607680 (issue #36), the only `git push` in
// .github/workflows/infra-issues.yml lived inside the `Open the pull request`
// stage, behind `REVIEW == 'approved'`. Every other way out of the pipeline —
// validation failed twice, the post-review amend broke validation, the review
// did not approve — left the implementing agent's commit on the runner's disk
// and nowhere else, and the runner is destroyed minutes later. That run parked
// issue #36 with:
//
//	I prepared this change, but the automated review stage did not return a
//	usable verdict, so I have not opened a pull request. This one needs a
//	person.
//
// `git ls-remote --heads origin 'issue-36*'` returned nothing. The comment
// handed a human a pointer to work that no longer existed anywhere. A promise
// of a prepared change with no prepared change behind it is worse than
// silence, because a person acts on it.
//
// So this verb runs the moment there is a commit to push — directly after
// the commit verb, before validation, and before any of the branches that
// decide what to do with the change. There is no second push and nothing to
// amend: the repair loops are gone, and each run makes exactly one commit and
// pushes it once. Pushing is unconditional on the verdict: the remote is
// where work lives, and a branch with no pull request costs nothing (the
// in-flight check in stage 1 and the terminal-state check at the bottom of
// the workflow both key on OPEN PULL REQUESTS, not on branches, so an
// abandoned branch never suppresses a later run).
//
// ---------------------------------------------------------------------------
// Why --force-with-lease
// ---------------------------------------------------------------------------
// Not for an amend. Nothing in this pipeline rewrites history any more: no
// agent holds git at all, and the commit verb appends one commit and stops.
// The flag is here for the one thing this push cannot see — a branch of this
// name that was already on the remote before the run started, and was never
// fetched.
//
// --force-with-lease says yes to exactly the pushes that are ours: creating
// the branch, fast-forwarding it, and replacing a tip we ourselves put there
// (git remembers it as the remote-tracking ref). It says no to a tip that
// arrived from anywhere else, and it says no to a branch we hold no lease on
// at all — that one is refused as "stale info" rather than clobbered. The
// claim stage already renames the branch when `git ls-remote` finds a
// collision, so this should be unreachable; this is what happens when it
// becomes reachable anyway, and a refused push naming a lease we do not hold
// is the right answer there — better than `--force`, which would silently
// overwrite whatever that other run left, and better than a plain push, whose
// refusal depends on the geometry of two histories nobody in this job has
// looked at.
//
// ---------------------------------------------------------------------------
// The credential, and where it must not end up
// ---------------------------------------------------------------------------
// The remote URL is rewritten first, and rewritten TOKENLESS.
//
// It has to be rewritten at all because claude-code-action unsets the
// credential actions/checkout left in .git/config and points `origin` at its
// own GitHub App token, which it revokes when its step ends; every push in
// this pipeline now happens directly after an agent step, so every one of them
// meets a dead token unless it fixes the remote first. It has to be rewritten
// WITHOUT a credential in it because a credential embedded in a remote URL
// takes precedence over any credential helper: leave the revoked one in the
// URL and the helper below is never consulted, and the push meets that dead
// token anyway.
//
// The token reaches git through a one-shot credential helper passed with `-c`
// on the command line, and so lands in neither of the two places the old
// `https://x-access-token:$GH_TOKEN@...` URL put it (issue #41):
//
//   - NOT in .git/config, where it used to sit for the rest of the job. That
//     is the part that mattered: Validate runs `tofu plan` over .tf files an
//     agent just wrote, and Review then hands a second agent Read over the
//     same workspace. A `file("${path.module}/.git/config")` in agent-authored
//     HCL, or a plain Read by the reviewer, found the token sitting there.
//   - NOT in argv, because the helper string names `$GH_TOKEN` and never its
//     value: it is expanded by the shell git runs for the helper, not by this
//     process, so the value never appears in this process's command line —
//     or in git's — where /proc/<pid>/cmdline would show it to anything
//     running on the runner.
//
// The empty `-c credential.helper=` in front clears any helper the environment
// already configured, so ours is the only one asked.
//
// Be exact about what that leaves open, because "the token is unreachable" is
// a stronger claim than the truth. GH_TOKEN is still in the job environment —
// the scripted steps genuinely need it. Both agent steps blank it in their own
// `env:` blocks, which is a best-effort tightening rather than a closed door
// (see the comment there), and /proc/self/environ remains an untested read
// path for an agent whose Read tool may accept absolute paths outside the
// workspace.
//
// Requires GH_TOKEN, GITHUB_SERVER_URL and GITHUB_REPOSITORY for all of that;
// with GH_TOKEN unset the remote is left exactly as it is and the push is made
// with no helper at all, which is what a local run wants.
//
// On a successful push, PUSHED_BRANCH=<name> is appended to $GITHUB_ENV when
// that variable is set. The hand-over steps in the workflow name their branch
// from THAT and never from their own $BRANCH, so a comment can only ever link
// a branch a push actually landed.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/zetlen/falconet/internal/handoff"
	"github.com/zetlen/falconet/internal/repo"
)

const pushUsageText = `push — get the working branch onto the remote the moment there is a commit
on it, so no terminal path in the pipeline can throw the work away.

Modes:
  falconet push --branch NAME [--base-sha SHA]

    --branch    the branch to push; it must be the checked-out one
    --base-sha  the commit the run started from. If HEAD still equals it,
                nothing was committed and there is nothing to push — the
                needs-info path, where the agent asked a question instead of
                making a change, and which must not fail because of it.

The push is --force-with-lease: it creates the branch, fast-forwards it, or
replaces a tip this checkout itself put there, and refuses a tip that
arrived from anywhere else rather than clobbering it.

With GH_TOKEN and GITHUB_REPOSITORY set, the origin URL is first rewritten
TOKENLESS to $GITHUB_SERVER_URL/$GITHUB_REPOSITORY.git and the token is
handed to git through a one-shot credential helper, so it lands in neither
.git/config nor argv. With GH_TOKEN unset the remote is left exactly as it
is and the push is made with no helper at all, which is what a local run
wants.

On a successful push, PUSHED_BRANCH=<name> is appended to $GITHUB_ENV when
that variable is set. The hand-over steps name their branch from THAT and
never from their own --branch, so a comment can only ever link a branch a
push actually landed.

Prints nothing on stdout, ever: push decides nothing, and stdout belongs to
the verbs that do.

Exit codes: 0 = the branch is on the remote (or there was nothing to push),
            1 = the push failed, 2 = usage error.
`

func pushUsage() int {
	fmt.Fprint(os.Stderr, pushUsageText)
	return 2
}

func runPush(args []string) int {
	var branch, baseSHA string
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
		case "--branch":
			v, ok = value("a name")
			branch = v
		case "--base-sha":
			v, ok = value("a commit sha")
			baseSHA = v
		case "-h", "--help":
			return pushUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return pushUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}
	if branch == "" {
		return pushUsage()
	}

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

	// git's own chatter goes to stderr with everything else here. `push -u`
	// prints "branch 'x' set up to track 'origin/x'." on STDOUT, and this
	// verb's stdout is captured by the action wrapper and written to
	// $GITHUB_OUTPUT: anything on it that is not a decision is a step failing
	// after its work is done. push decides nothing, so it says nothing there.
	git := func(argv ...string) *exec.Cmd {
		cmd := exec.Command("git", append([]string{"-C", root}, argv...)...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd
	}

	if baseSHA != "" {
		head := exec.Command("git", "-C", root, "rev-parse", "HEAD")
		head.Stderr = os.Stderr
		out, err := head.Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "git rev-parse HEAD failed")
			return 1
		}
		if strings.TrimSpace(string(out)) == baseSHA {
			fmt.Fprintf(os.Stderr, "no commit on %s yet (HEAD is still %s) — nothing to push\n",
				branch, prefix(baseSHA, 7))
			return 0
		}
	}

	// Restore a working credential without writing one down. See "The
	// credential, and where it must not end up" above: the URL is set
	// TOKENLESS so that the helper is consulted at all, and the helper names
	// $GH_TOKEN so that git — not this process — expands it.
	var auth []string
	if os.Getenv("GH_TOKEN") != "" && os.Getenv("GITHUB_REPOSITORY") != "" {
		server := envOr("GITHUB_SERVER_URL", "https://github.com")
		if err := git("remote", "set-url", "origin", server+"/"+os.Getenv("GITHUB_REPOSITORY")+".git").Run(); err != nil {
			fmt.Fprintln(os.Stderr, "could not rewrite the origin URL")
			return 1
		}
		// $GH_TOKEN must NOT be expanded here. It is expanded by the shell
		// git runs for the helper, which is the whole point: expanded now,
		// the token would be in git's argv.
		auth = []string{
			"-c", "credential.helper=",
			"-c", `credential.helper=!f(){ echo username=x-access-token; echo "password=$GH_TOKEN"; };f`,
		}
	} else {
		fmt.Fprintln(os.Stderr, "GH_TOKEN or GITHUB_REPOSITORY unset — pushing with the remote as configured")
	}

	push := git(append(auth, "push", "--force-with-lease", "--set-upstream", "origin", branch)...)
	if err := push.Run(); err == nil {
		short := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD")
		short.Stderr = os.Stderr
		sha := ""
		if out, err := short.Output(); err == nil {
			sha = strings.TrimSpace(string(out))
		}
		fmt.Fprintf(os.Stderr, "pushed %s (%s)\n", branch, sha)
		// The hand-over comments read this, and they read it INSTEAD of
		// --branch: written here and only here, it is a statement that the
		// branch is on the remote right now, not that the workflow intended
		// to put it there. Every `--branch` argument to pause in the workflow
		// is this variable.
		if err := handoff.GitHubEnvAppend("PUSHED_BRANCH=" + branch); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		return 0
	}

	// Loud, and fatal to the step that called it. A run that cannot put its
	// work on the remote has nothing to hand anybody, and the workflow's
	// terminal-state check will park the issue with this log attached.
	fmt.Fprintf(os.Stderr, "::error::could not push %s — the work for this run exists only on this runner\n", branch)
	return 1
}

// prefix is the first n characters of s, or all of it.
func prefix(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
