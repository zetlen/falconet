package main

// init — install falconet into the repository this is run in: README steps
// 2–8, each idempotent, each reported one line in doctor's format, then one
// commit and never a push. What each step decides and writes is
// internal/setup, which carries the record; doctor's checks run through
// internal/doctor so the two verbs are the same code. This file is the
// flags, the repository, the prompts, git, and the exit code.
//
// The second setup verb (ADR-0006 D4, D5): a person invokes it, on a laptop,
// in a clone of the repository they are installing falconet into. This is
// the part that needs no token (#10): the .gitignore line, the config, the
// workflow file, and one commit. The labels and the secrets (#11) are
// skipped and listed under "Left for you:" — the run degrades to the
// README, never to nothing.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/doctor"
	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/setup"
	"github.com/zetlen/falconet/prompts"
)

const initUsageText = `init — install falconet into this repository: README steps 2, 7 and 8,
each idempotent and reported one line, then one commit, never a push.

Modes:
  falconet init [--config FILE]
                [--plan a,b] [--validate-only c,d]
                [--no-commit]

    --config           the config file, instead of .github/falconet.json
    --plan             the stacks a human will apply from the pull request
    --validate-only    every other directory with .tf in it. Both are
                       comma-separated, and every discovered stack must be
                       named in one of them — unless stdin is a terminal,
                       where init asks per stack
    --no-commit        leave what was written staged, and say so

Runs from the root of the repository it is standing in, which must have a
clean working tree: the commit this makes carries only what it wrote.

Steps 3–6 — the App's two secrets, the API key, the planning environment
and the four labels — need FALCONET_SETUP_TOKEN and land with #11; until
then they are skipped and listed under "Left for you:" in the README's
words.

Prints one line per step on stdout, in doctor's format — a fixed-width
status word, the step number and the step:

  ok           already done; nothing written
  done         written by this run
  skipped      not attempted, and listed under "Left for you:"
  MISSING      a check doctor makes that init cannot fix
  note         not a step: something the README says in a sentence

then a summary line, then "Left for you:" — what remains, in order, ending
with the canary and the check (falconet doctor).

Exit codes: 0 = everything attempted succeeded (skipped steps are not
                failures; neither is a MISSING init cannot fix)
            1 = a dirty tree, a refused write, a repository that does not
                qualify
            2 = usage error (including --help)
`

func initUsage() int {
	fmt.Fprint(os.Stderr, initUsageText)
	return 2
}

// initFlags is the command line, read.
type initFlags struct {
	explicit, plan, validateOnly       string
	planGiven, validateGiven, noCommit bool
}

func runInit(args []string) int {
	var f initFlags
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
		ok := true
		takes := true
		switch flag {
		case "--config":
			v, ok = value("a file")
			f.explicit = v
		case "--plan":
			v, ok = value("stack names, comma-separated")
			f.plan, f.planGiven = v, true
		case "--validate-only":
			v, ok = value("stack names, comma-separated")
			f.validateOnly, f.validateGiven = v, true
		case "--no-commit":
			f.noCommit, takes = true, false
		case "-h", "--help":
			return initUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return initUsage()
		}
		if !ok {
			return 2
		}
		if takes {
			args = args[2:]
		} else {
			args = args[1:]
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
		return 1
	}
	// Every file the caller names resolves against where the caller stands,
	// before the cd to the root; config resolves at the root after it.
	if f.explicit != "" && !filepath.IsAbs(f.explicit) {
		f.explicit = filepath.Join(cwd, f.explicit)
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

	// The report is printed as it happens — a line lands when its step does,
	// so a person at a prompt sees what came before — and kept for the
	// summary.
	var report doctor.Report
	say := func(l doctor.Line) {
		report = append(report, l)
		fmt.Println(l.String())
	}
	// What is left for a person, collected as the run goes and printed in
	// the README's order at the end: the push first, then the steps init
	// skipped (3–7), then what doctor found wrong and init cannot fix, then
	// the canary and the check.
	var left leftovers

	// --- preflight: the tree ----------------------------------------------
	//
	// The tree must be clean before anything else happens, and refused before
	// any call and before any file: the commit this verb makes must carry only
	// what it wrote, which is the same reason prepare refuses a dirty tree
	// before the agent runs — a commit that carries what the tree happened to
	// hold is a reading of the tree that is a lie.
	if err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "init: %s is not a git repository\n", root)
		return 1
	}
	status := exec.Command("git", "status", "--porcelain")
	status.Stderr = os.Stderr
	dirt, err := status.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "init: could not read the working tree")
		return 1
	}
	if len(strings.TrimSpace(string(dirt))) > 0 {
		fmt.Fprintln(os.Stderr, "init: the working tree is dirty, and the commit init makes must carry only what it writes; commit or stash these first:")
		fmt.Fprint(os.Stderr, string(dirt))
		return 1
	}
	say(doctor.Line{Status: doctor.OK, Step: 1, Text: "the working tree is clean"})

	// --- preflight: the config ----------------------------------------------
	//
	// A config that exists and does not parse is a refusal, not a MISSING:
	// init never rewrites a consumer's config, and every later step — the
	// label names, the handoff directory, the stacks — reads it.
	cfg, err := config.Load(f.explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		fmt.Fprintln(os.Stderr, "init never rewrites a config: fix it, or move it aside to have one written")
		return 1
	}

	// --- preflight: the stacks -----------------------------------------------
	//
	// Decided now, before any write, because step 7 needs the lists and step
	// 5's note needs to know whether any stack is planned. A config that exists is
	// the answer and is kept; otherwise the stacks are discovered and the
	// flags sort them, or the prompt does.
	terminal := isTerminal()
	stdin := bufio.NewReader(os.Stdin)
	writeConfig := cfg.File == ""
	var stacks setup.Stacks
	if writeConfig {
		discovered, err := setup.DiscoverStacks(os.DirFS(root))
		if err != nil {
			fmt.Fprintf(os.Stderr, "init: looking for stacks: %v\n", err)
			return 1
		}
		if len(discovered) == 0 {
			fmt.Fprintln(os.Stderr, "init: this repository does not qualify: no directory under it holds a .tf file (README step 1:")
			fmt.Fprintln(os.Stderr, "OpenTofu, with each stack in its own subdirectory — a .tf at the root is not a stack)")
			return 1
		}
		stacks, err = setup.Sort(discovered, setup.SplitList(f.plan), setup.SplitList(f.validateOnly))
		var unknown *setup.UnknownStackError
		var unsorted *setup.UnsortedError
		switch {
		case errors.As(err, &unknown):
			fmt.Fprintf(os.Stderr, "init: %v\n", err)
			return 2
		case errors.As(err, &unsorted) && terminal:
			stacks, err = askStacks(stdin, unsorted.Names, setup.SplitList(f.plan), setup.SplitList(f.validateOnly), discovered)
			if err != nil {
				fmt.Fprintf(os.Stderr, "init: %v\n", err)
				return 1
			}
		case err != nil:
			fmt.Fprintf(os.Stderr, "init: %v\n", err)
			fmt.Fprintf(os.Stderr, "name each of these in --plan (a human will apply it from the pull request) or --validate-only (every other stack); found: %s\n",
				strings.Join(discovered, ", "))
			return 1
		}
	} else {
		stacks = setup.Stacks{Plan: cfg.Schema.Stacks.Plan, ValidateOnly: cfg.Schema.Stacks.ValidateOnly}
	}
	planned := len(stacks.Plan)

	// --- steps 3–6: the remote steps, which are #11 ----------------------------
	//
	// The labels, the API key, the planning environment and the App's two
	// secrets need FALCONET_SETUP_TOKEN and the sealed box, and land with
	// #11. Until then every one is skipped and listed under "Left for you:"
	// in the README's words, in the order #11 will do them: the run degrades
	// to the README, never to nothing (ADR-0006 D4).
	say(doctor.Line{Status: doctor.Skipped, Step: 6, Text: "the four labels (#11)"})
	left.add(leftLabels, setup.LeftLabels)
	say(doctor.Line{Status: doctor.Skipped, Step: 4, Text: "secret ANTHROPIC_API_KEY (#11)"})
	left.add(leftAnthropic, setup.LeftAnthropic)
	if planned == 0 {
		say(doctor.SecretLine(doctor.Secret{Name: "FALCONET_PLAN_ENV", Step: 5}, nil, planned, true))
	} else {
		say(doctor.Line{Status: doctor.Skipped, Step: 5, Text: "secret FALCONET_PLAN_ENV (#11)"})
		left.add(leftPlanEnv, setup.LeftPlanEnv)
	}
	say(doctor.Line{Status: doctor.Skipped, Step: 3, Text: "secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (#11)"})
	left.add(leftApp, setup.LeftApp)

	// --- the local files: steps 2, 7 and 8 -----------------------------------
	//
	// Each idempotent: a file that exists is kept and reported, never
	// rewritten. What is written is remembered by path, for `git add` of
	// exactly those paths and for the commit body.
	var written []setup.Written
	write := func(path string, data []byte, perm os.FileMode, what string) int {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot create %s: %v\n", filepath.Dir(path), err)
			return 1
		}
		if err := os.WriteFile(path, data, perm); err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot write %s: %v\n", path, err)
			return 1
		}
		written = append(written, setup.Written{Path: path, What: what})
		return 0
	}

	// Step 2: the handoff directory is ignored. `git check-ignore` is the
	// check, as doctor makes it, so an entry in .git/info/exclude or a
	// broader pattern counts; the line is appended only when it does not.
	entry := setup.IgnoreEntry(cfg.Schema.HandoffDir)
	switch rc, _ := checkIgnore(cfg.Schema.HandoffDir); rc {
	case 0:
		say(doctor.HandoffIgnored(cfg.Schema.HandoffDir, true))
	case 1:
		current, err := os.ReadFile(".gitignore")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "init: cannot read .gitignore: %v\n", err)
			return 1
		}
		out, changed := setup.AppendIgnore(current, entry)
		if changed {
			if rc := write(".gitignore", out, 0o644, "ignores "+entry+", the handoff directory (README step 2)"); rc != 0 {
				return rc
			}
			say(doctor.Line{Status: doctor.Done, Step: 2, Text: entry + " added to .gitignore"})
		} else {
			// The line is there and git still says not ignored: something
			// later in the file un-ignores it. Not init's to untangle.
			say(doctor.HandoffIgnored(cfg.Schema.HandoffDir, false))
			left.add(leftFix, setup.LeftFix(doctor.HandoffIgnored(cfg.Schema.HandoffDir, false)))
		}
	default:
		fmt.Fprintln(os.Stderr, "init: git check-ignore failed")
		return 1
	}

	// Step 7: the config and the prompt. A config that exists is kept — and
	// checked as doctor checks it; what it names wrong is left for a person.
	// One written names the stacks and the prompt copy.
	if !writeConfig {
		say(doctor.ConfigLine(cfg.File, nil))
		var onDisk []doctor.Stack
		for _, key := range []struct {
			name  string
			names []string
		}{{"plan", cfg.Schema.Stacks.Plan}, {"validate_only", cfg.Schema.Stacks.ValidateOnly}} {
			for _, s := range key.names {
				onDisk = append(onDisk, stackOnDisk(key.name, s))
			}
		}
		for _, l := range doctor.Stacks(onDisk, cfg.File) {
			say(l)
			if l.Status == doctor.Missing {
				left.add(leftFix, setup.LeftFix(l))
			}
		}
		// The prompt overrides the file sets: one that names a missing file
		// under the root gets the shipped prompt copied there, which is the
		// hint doctor gives; the rest are reported as doctor reports them.
		namesImplement := false
		for _, p := range promptsOnDisk(cfg) {
			if p.Key == "implement" {
				namesImplement = true
			}
			line := doctor.PromptLines([]doctor.Prompt{p})[0]
			if line.Status == doctor.Missing && p.Inside && !p.Exists {
				if rc := copyPrompt(p.Key, filepath.Clean(p.Path), write); rc != 0 {
					return rc
				}
				say(doctor.Line{Status: doctor.Done, Step: 7, Text: fmt.Sprintf("prompts.%s names %s, copied from the shipped prompt", p.Key, p.Path)})
				if p.Key == "implement" {
					left.add(leftPrompt, strings.Replace(setup.LeftPrompt, setup.PromptPath, p.Path, 1))
				}
				continue
			}
			say(line)
			if line.Status == doctor.Missing {
				left.add(leftFix, setup.LeftFix(line))
			}
		}
		if !namesImplement {
			say(doctor.Line{Status: doctor.Note, Step: 7, Text: "prompts.implement is not set, so the shipped prompt is used, and its standing facts are the origin's"})
			left.add(leftPrompt, setup.LeftPromptUnset)
		}
	} else {
		if rc := write(".github/falconet.json", setup.ConfigJSON(stacks), 0o644,
			fmt.Sprintf("the stacks — plan: %s; validate_only: %s — and the prompt override (README step 7)", orNone(stacks.Plan), orNone(stacks.ValidateOnly))); rc != 0 {
			return rc
		}
		say(doctor.Line{Status: doctor.Done, Step: 7,
			Text: fmt.Sprintf(".github/falconet.json written (plan: %s; validate_only: %s)", orNone(stacks.Plan), orNone(stacks.ValidateOnly))})
		if isRegularFile(setup.PromptPath) {
			say(doctor.Line{Status: doctor.OK, Step: 7, Text: "prompts.implement names " + setup.PromptPath + ", which exists"})
		} else {
			if rc := copyPrompt("implement", setup.PromptPath, write); rc != 0 {
				return rc
			}
			say(doctor.Line{Status: doctor.Done, Step: 7, Text: "prompts.implement names " + setup.PromptPath + ", copied from the shipped prompt"})
			left.add(leftPrompt, setup.LeftPrompt)
		}
	}

	// Step 8: the caller workflow, pinned to this binary's own version. One
	// that exists is kept and checked as doctor checks it.
	ref := setup.WorkflowRef(resolvedVersion())
	if text, err := os.ReadFile(doctor.WorkflowPath); err == nil {
		for _, l := range doctor.WorkflowLines(text, true) {
			say(l)
			if l.Status == doctor.Missing {
				left.add(leftFix, setup.LeftFix(l))
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if rc := write(doctor.WorkflowPath, setup.Workflow(ref), 0o644, "the caller workflow, "+setup.Uses(ref)+" (README step 8)"); rc != 0 {
			return rc
		}
		say(doctor.Line{Status: doctor.Done, Step: 8, Text: doctor.WorkflowPath + " written (uses " + setup.Uses(ref) + ")"})
	} else {
		fmt.Fprintf(os.Stderr, "init: cannot read %s: %v\n", doctor.WorkflowPath, err)
		return 1
	}

	// --- the commit, never the push ------------------------------------------
	//
	// Exactly the paths written, never -A: the tree was clean, so nothing
	// else could be staged, but the pathspec is the statement. The message
	// goes through a file, as every commit in this pipeline does, so no
	// shell is asked to quote it.
	branch := currentBranch()
	switch {
	case len(written) == 0:
		say(doctor.Line{Status: doctor.OK, Text: "nothing to commit: every file exists"})
	default:
		paths := make([]string, 0, len(written))
		for _, w := range written {
			paths = append(paths, w.Path)
		}
		add := exec.Command("git", append([]string{"add", "--"}, paths...)...)
		add.Stdout, add.Stderr = os.Stderr, os.Stderr
		if err := add.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "init: git add failed")
			return 1
		}
		if f.noCommit {
			say(doctor.Line{Status: doctor.Skipped, Text: fmt.Sprintf("the commit (--no-commit): %d %s staged", len(paths), files(len(paths)))})
			left.add(leftPush, setup.LeftCommit(branch))
			break
		}
		msg, err := os.CreateTemp("", "falconet-init-*.txt")
		if err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot write the commit message: %v\n", err)
			return 1
		}
		defer func() { _ = os.Remove(msg.Name()) }()
		if _, err := msg.Write(setup.CommitMessage(written)); err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot write the commit message: %v\n", err)
			return 1
		}
		if err := msg.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot write the commit message: %v\n", err)
			return 1
		}
		commit := exec.Command("git", "commit", "-q", "-F", msg.Name())
		commit.Stdout, commit.Stderr = os.Stderr, os.Stderr
		if err := commit.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "init: git commit failed; the files are written and staged")
			return 1
		}
		say(doctor.Line{Status: doctor.Done, Text: fmt.Sprintf("committed %q (%d %s)", setup.CommitSubject, len(paths), files(len(paths)))})
		left.add(leftPush, setup.LeftPush(branch))
	}

	fmt.Println(setup.Summary(report))
	left.add(leftCanary, setup.LeftCanary)
	left.add(leftDoctor, setup.LeftDoctor)
	fmt.Print(setup.LeftForYou(left.inOrder()))
	return 0
}

// leftovers is the "Left for you:" list, each item with its place in the
// README's order; items are added as the run meets them and sorted once.
type leftovers []struct {
	order int
	text  string
}

const (
	leftPush = iota
	leftApp
	leftAnthropic
	leftPlanEnv
	leftLabels
	leftPrompt
	leftFix
	leftCanary
	leftDoctor
)

func (l *leftovers) add(order int, text string) {
	*l = append(*l, struct {
		order int
		text  string
	}{order, text})
}

func (l leftovers) inOrder() []string {
	sort.SliceStable(l, func(i, j int) bool { return l[i].order < l[j].order })
	out := make([]string, 0, len(l))
	for _, item := range l {
		out = append(out, item.text)
	}
	return out
}

// isTerminal is whether stdin is a terminal a person is typing at. `stty`
// answers it exactly — it refuses anything that is not a terminal, /dev/null
// included; without stty, a character device that is not /dev/null is the
// best the standard library can say.
func isTerminal() bool {
	if stty, err := exec.LookPath("stty"); err == nil {
		probe := exec.Command(stty, "-g")
		probe.Stdin = os.Stdin
		return probe.Run() == nil
	}
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}

// askStacks is the terminal's way of sorting the stacks the flags did not:
// one question per stack. Answers are folded into the flags' lists and
// sorted again, so the result is what the flags would have produced.
func askStacks(stdin *bufio.Reader, unsorted, plan, validateOnly, discovered []string) (setup.Stacks, error) {
	fmt.Fprintln(os.Stderr, "README step 7: plan is every stack a human will apply from the pull request; validate-only is every other directory with .tf in it.")
	for _, s := range unsorted {
		for {
			fmt.Fprintf(os.Stderr, "%s — plan or validate-only? [p/v] ", s)
			line, err := stdin.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return setup.Stacks{}, err
			}
			answer := strings.ToLower(strings.TrimSpace(line))
			if answer == "p" || answer == "plan" {
				plan = append(plan, s)
				break
			}
			if answer == "v" || answer == "validate-only" || answer == "validate" {
				validateOnly = append(validateOnly, s)
				break
			}
			if errors.Is(err, io.EOF) {
				return setup.Stacks{}, fmt.Errorf("no answer for %s; name it in --plan or --validate-only", s)
			}
		}
	}
	return setup.Sort(discovered, plan, validateOnly)
}

// copyPrompt writes the shipped prompt called key to path, through write.
func copyPrompt(key, path string, write func(string, []byte, os.FileMode, string) int) int {
	text, ok := prompts.Read(strings.ReplaceAll(key, "_", "-"))
	if !ok {
		fmt.Fprintf(os.Stderr, "init: no shipped prompt named %s\n", key)
		return 1
	}
	return write(path, text, 0o644, "the shipped prompt, copied; its standing-facts block needs editing (README step 7)")
}

// currentBranch is `git branch --show-current`: empty on a detached HEAD.
func currentBranch() string {
	out, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func files(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}
