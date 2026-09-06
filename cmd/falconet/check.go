package main

// check — run the repository's own check on the tree the agent left, and say
// whether it passed.
//
// This is step 3's second half. The guards in the commit verb decide whether
// what the agent wrote may ship at all; the repository's own check — its
// tests, its linter, its build, whatever the operator names at
// `check.command` — decides whether it is right. The two are kept apart on
// purpose (docs/decisions.md, "A check verb and a caller-owned loop"): a
// guard refusal is terminal and never comes back here, because a guard the
// agent can iterate against is an oracle; a failing check may send the run
// back to the agent, a bounded number of times, and this file is what it
// sends.
//
// The verb does not loop, does not run the agent, and does not decide what
// happens next. It runs one command, once, from the repository root, with
// the tree as it stands, and leaves one of two states in the handoff
// directory: check-failure.txt, holding the command, how it ended and the
// end of its output; or no such file. The caller owns the iteration — in
// CI, the attempts unrolled in .github/workflows/falconet.yml, each
// conditioned on the word this verb printed the time before; on a
// workstation, a shell loop.
//
// The command is an argv, not a command line: os/exec, no shell, no quoting
// (docs/decisions.md, "The language is Go"). It runs with this process's
// environment and its stdout and stderr both go to THIS verb's stderr — the
// run log — and to the tail that becomes the file; stdout here is exactly
// one word, and a test runner with something to say would say it into that
// contract if you let it.
//
// The config the command comes from is read from the working tree, after
// the agent has had its turn at it — the same exposure the commit verb's
// policy has, and the same refusal: a change to the file the command was
// read from is a mechanical failure here, before anything runs, whatever
// that file now names. The commit verb will refuse it again with a reason
// for the requester; this verb's job is not to run the agent's command in
// the meantime.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/zetlen/falconet/internal/check"
	"github.com/zetlen/falconet/internal/commit"
	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/handoff"
	"github.com/zetlen/falconet/internal/repo"
)

const checkUsageText = `check — run the repository's own check on the tree the agent left, and say
whether it passed.

Modes:
  falconet check [--out-dir DIR] [--config FILE]

Runs check.command from .github/falconet.json — an argv, no shell — from the
repository root, with the tree exactly as it stands. Its output goes to
stderr, whole. Prints exactly one word on stdout, and nothing else:

  pass      the command exited 0. DIR/check-failure.txt, if a previous
            check left one, is removed.
  fail      the command ran and did not exit 0. DIR/check-failure.txt
            holds the command, how it ended, and the last 64 KiB of its
            output, cut on a line boundary with a note saying so. That
            file is what the next agent pass reads; nothing else feeds
            back.
  skipped   check.command is empty: this repository has no check
            configured, and nothing ran.

This verb does not loop, does not run the agent, and does not decide what
happens next: the caller does, on the word.

Exit codes: 0 = a word was printed
            1 = refused mechanically — the command could not be started,
                the config file was changed by the agent, the tree is not
                a repository; nothing is printed, stderr says why
            2 = usage error (including --help)
`

func checkUsage() int {
	fmt.Fprint(os.Stderr, checkUsageText)
	return 2
}

func runCheck(args []string) int {
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
			return checkUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return checkUsage()
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
	failureFile := filepath.Join(out, check.FailureFile)

	// --- the guard's own configuration ------------------------------------
	//
	// See the header, and "The guard's own configuration" in internal/commit.
	// git's own exit status is checked: outside a repository this must be a
	// mechanical failure, not a clean tree.
	status := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "-z")
	status.Stderr = os.Stderr
	listing, err := status.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "git status failed")
		return 1
	}
	changed, _ := commit.ParseStatus(listing)
	if path, hit := commit.ConfigChanged(cfg.File, root, changed); hit {
		fmt.Fprintf(os.Stderr, "check: %s was changed in this tree, and it is where check.command is read from; refusing to run a command the agent chose\n", path)
		return 1
	}

	// --- nothing to run ---------------------------------------------------
	//
	// Said on stderr every time, so a key misspelled in the config is a line
	// in every run log rather than a check that quietly never happens.
	argv := cfg.Schema.Check.Command
	if len(argv) == 0 || argv[0] == "" {
		fmt.Fprintf(os.Stderr, "check: no check configured (check.command in %s is empty); nothing ran\n",
			orDefaultFile(cfg.File))
		if err := removeIfPresent(failureFile); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		fmt.Println("skipped")
		return 0
	}

	// --- the check ----------------------------------------------------------
	//
	// Both streams to stderr and to the tail. The command's own stdout must
	// not reach this verb's: one word is the contract.
	fmt.Fprintf(os.Stderr, "check: running %v in %s\n", argv, root)
	tail := &check.Tail{}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Stdout = io.MultiWriter(os.Stderr, tail)
	cmd.Stderr = cmd.Stdout
	runErr := cmd.Run()
	var exit *exec.ExitError
	switch {
	case runErr == nil:
		if err := removeIfPresent(failureFile); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "check: passed")
		fmt.Println("pass")
		return 0
	case errors.As(runErr, &exit):
		// The command ran and said no — a signal counts: a check that was
		// killed did not pass. What it said is the file.
		report := check.Report(argv, exit.String(), tail)
		if err := os.WriteFile(failureFile, report, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", failureFile, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "check: failed (%s); %s written\n", exit, failureFile)
		fmt.Println("fail")
		return 0
	default:
		// Not found, not executable, could not fork: the check did not
		// happen, and a check that did not happen is not a pass and not a
		// failure the agent can do anything about.
		fmt.Fprintf(os.Stderr, "check: could not run %v: %v\n", argv, runErr)
		return 1
	}
}

// removeIfPresent deletes path, and a path that is not there is not an
// error: a pass leaves no failure file, whether or not there was one.
func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %s: %v", path, err)
	}
	return nil
}

// orDefaultFile names the config file that was read, or the defaults.
func orDefaultFile(file string) string {
	if file == "" {
		return "the built-in defaults (no .github/falconet.json)"
	}
	return file
}
