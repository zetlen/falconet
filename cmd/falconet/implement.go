package main

// implement — run the agent, once, on the tree as it stands, and stop.
//
// This is step 2. The verb is the seam between falconet and the harness:
// everything the agent reads was laid out by prepare in the handoff
// directory, everything it leaves behind is read by check and commit from
// the same place, and this verb is what stands between — one subprocess,
// started with the rendered prompt on its stdin, from the repository root,
// with this process's environment. Which agent is the config's to say
// (`harness.command`, an argv); the shipped default is the Claude Code CLI
// with file tools only, no shell and a turn cap, and the README's implement
// contract is what any replacement has to meet.
//
// The verb runs the harness and nothing else. It does not loop (the caller
// does, on the check verb's word), does not commit (the commit verb does,
// through the guards), and does not decide what the agent produced: a
// commit message, a question or nothing is the commit verb's reading, made
// once, after the last check. What this verb decides is only that the
// harness ran to completion, and it says so in one word.
//
// The prompt is written to the handoff directory as well as piped: a
// harness that takes its instructions as a file names `{handoff}/prompt.md`
// in its argv, and one that reads stdin gets the same bytes there.
//
// The command comes from the config, and the config is read from the
// working tree after the agent has had its turn at it — on the second pass
// of a run, the agent of the first pass could have rewritten
// `harness.command` to anything. So, as in the check verb: a tree that
// changed the config file is a mechanical failure here, before anything
// runs, whatever that file now names; the commit verb refuses it again with
// a reason for the requester.
//
// Nothing this verb holds is the boundary. The job it runs in is: a job
// with no token and no secret but the model key is what keeps the agent
// from publishing, whatever tools the harness grants (principle 2), and
// this verb runs the same way on a workstation as in that job.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zetlen/falconet/internal/commit"
	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/gitsafe"
	"github.com/zetlen/falconet/internal/handoff"
	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/runlog"
)

// PromptFile is where the rendered prompt is left in the handoff directory,
// for a harness that takes a file rather than stdin.
const PromptFile = "prompt.md"

// harnessWaitDelay is how long the harness's output is still read after the
// harness has exited. See "the agent" below.
const harnessWaitDelay = 5 * time.Second

const implementUsageText = `implement — run the agent, once, on the tree as it stands, and stop.

Modes:
  falconet implement [--out-dir DIR] [--config FILE]

Runs harness.command from .github/falconet.json — an argv, no shell — from
the repository root, with the implement prompt (the config's override, or
the shipped one) rendered and written to DIR/prompt.md and piped to the
command's stdin. The harness's own output goes to stderr, a line at a time
as it arrives, shown in the format harness.output names: text as it is, or
claude-stream-json as one readable line per thing the agent did. No line
of it can be read as a workflow command. Prints exactly one word on stdout,
and nothing else:

  done      the harness exited 0. What it left in the tree and in DIR
            is for the check and commit verbs to read; this verb does
            not look.

This verb does not loop, does not commit, and does not decide what the
agent produced: the caller runs the check on the word, and the commit
verb reads the handoff once, after the last check.

Exit codes: 0 = the harness ran to completion
            1 = refused mechanically — harness.command is empty or could
                not be started, or exited non-zero; the config file was
                changed by the agent; the tree is not a repository.
                Nothing is printed, stderr says why
            2 = usage error (including --help)
`

func implementUsage() int {
	fmt.Fprint(os.Stderr, implementUsageText)
	return 2
}

func runImplement(args []string) int {
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
			return implementUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return implementUsage()
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

	// --- the guard's own configuration ------------------------------------
	//
	// See the header. git's own exit status is checked: outside a repository
	// this must be a mechanical failure, not a clean tree.
	// gitsafe: the status runs in the agent's tree; a core.fsmonitor set in
	// its .git/config would run a program here. The command is hardened so
	// it does not; the commit verb refuses such a tree outright.
	status := gitsafe.Command(root, "status", "--porcelain", "--untracked-files=all", "-z")
	status.Stderr = os.Stderr
	listing, err := status.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "git status failed")
		return 1
	}
	changed, _ := commit.ParseStatus(listing)
	if path, hit := commit.ConfigChanged(cfg.File, root, changed); hit {
		fmt.Fprintf(os.Stderr, "implement: %s was changed in this tree, and it is where harness.command is read from; refusing to run a command the agent chose\n", path)
		return 1
	}

	// --- no agent -----------------------------------------------------------
	argv := cfg.Schema.Harness.Command
	if len(argv) == 0 || argv[0] == "" {
		fmt.Fprintf(os.Stderr, "implement: no harness configured (harness.command in %s is empty); nothing ran\n",
			orDefaultFile(cfg.File))
		return 1
	}

	// --- the prompt -----------------------------------------------------------
	prompt, rc := resolvePrompt("implement", cfg, root, out)
	if rc != 0 {
		return rc
	}
	prompt += "\n"
	promptFile := filepath.Join(out, PromptFile)
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", promptFile, err)
		return 1
	}

	// --- the agent ------------------------------------------------------------
	//
	// Both of the harness's streams go to stderr, line by line as they arrive,
	// shown in the format harness.output names (internal/runlog): the run log
	// follows the agent while it works, and stdout here is exactly one word.
	// Every line is printed so that it cannot be read as a workflow command,
	// because the harness's output is steered by the issue's text.
	//
	// The streams reach the log through pipes this process reads, and a
	// harness can exit leaving a process of its own holding them open. Wait
	// would then wait for that process too, for as long as it lives. So once
	// the harness has exited, its output is read for harnessWaitDelay more and
	// no longer; the harness's own exit status still decides the word.
	format := cfg.Schema.Harness.Output
	shown := runlog.NewHarness(os.Stderr, format)
	_, _ = fmt.Fprintf(shown.Stderr, "implement: running %v in %s, its output shown as %s (harness.output)\n", argv, root, format)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = shown.Stdout
	cmd.Stderr = shown.Stderr
	cmd.WaitDelay = harnessWaitDelay
	runErr := cmd.Run()
	shown.Close()
	var exit *exec.ExitError
	switch {
	case runErr == nil:
		fmt.Fprintln(os.Stderr, "implement: the harness finished")
		fmt.Println("done")
		return 0
	case errors.Is(runErr, exec.ErrWaitDelay):
		fmt.Fprintf(os.Stderr, "implement: the harness finished; something it started still held its output after %s, and is no longer shown\n", harnessWaitDelay)
		fmt.Println("done")
		return 0
	case errors.As(runErr, &exit):
		// The harness ran and did not finish cleanly — an API error, a
		// crash, a signal. Nothing it left is trusted as a completed pass;
		// the run stops here and the workflow's contain job says so.
		fmt.Fprintf(os.Stderr, "implement: the harness failed (%s)\n", exit)
		return 1
	default:
		fmt.Fprintf(os.Stderr, "implement: could not run %v: %v\n", argv, runErr)
		return 1
	}
}
