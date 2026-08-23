package main

// validate — the deterministic gate between the implementing agent and
// whatever looks at the change next.
//
// Everything an agent used to prove by hand — did I commit? does it parse?
// what does the plan say? — happens here, once, in one process, and lands in
// files. Agents run no tofu in CI.
//
// The guards and the report's words are internal/validate; the tofu argv and
// the one subprocess call that carries the two facts about tofu are
// internal/stacks. This file is the sequence, in the order the script ran
// it: git, the stacks, the files, and the exit code. It changes directory to
// the repository root and stays there, as every verb that works on a tree
// does.
//
// Validate HEAD against the commit the run started from:
//
//  1. assert at least one commit exists on top of --base
//  2. assert the commit does not touch the handoff dir — CI's own scratch
//     has no business inside a change (see internal/validate)
//  3. snapshot the commits to DIR/diff.patch and the changed paths to
//     DIR/changed-files.txt — the review agent is granted no Bash, so
//     its evidence has to be on disk before it starts
//  4. tofu validate, once per configured stack (dns/, workspace/, site/ by
//     default — #16 split the single root module into three)
//  5. the plan command — `tofu -chdir=dns plan -no-color -refresh=false
//     -lock=false` by default — into DIR/plan.txt — stacks.plan only; see
//     step 5 for why the other two stacks are not planned here.
//
// Nothing checks a record registry between 4 and 5 any more. That step
// cross-checked the dns/records-*.tf locals lists against
// scripts/record-manifest.txt, a hand-copied mirror of the record list that
// existed in order to be checked; #17 deleted both. The live-DNS verification
// the same script did is now the check blocks in dns/checks-live-dns.tf, which
// are inert unless a run names a zone to verify — so this verb never
// triggers them and CI never queries public DNS.
//
// diff.patch is `git log -p`, not `git diff`: the reviewing agent gets each
// commit MESSAGE with the change it describes. The message is the implementing
// agent's claim about what it did, and checking a change against its own claim
// — and both against the request — is a real part of reviewing it. It is also
// the one thing that agent says which outlives the run, and it is a public
// artifact of exactly the kind humans review everywhere. Its reasoning
// transcript is not, and the reviewer never sees that.
//
// -refresh=false -lock=false is mandatory in CI: the job holds a
// bucket-scoped READ-ONLY state credential, so taking a lock is refused, and
// it must never call the Namecheap API.
//
// There is deliberately NO `tofu fmt -check` here, but no longer for the
// reason issue #20 gave — that `tofu fmt` already failed on main. #20 is
// closed and the tree is clean. .github/workflows/ci.yml now runs
// `tofu fmt -check -recursive` as its first step on every pull request, so
// repeating it here would report the same thing twice to the same reader.
//
// ONE PLAN, NOT THREE (#16). Of the three stacks, only dns/ is ever applied
// by this pipeline: deploy.yml runs an untargeted `tofu -chdir=dns apply`
// and nothing else applies anything — workspace/ is applied by hand against
// the real Google Workspace tenant, and site/ is plan-only forever, against
// a GCP project that does not exist. A human approves THIS verb's plan and
// that approval becomes exactly one apply. Planning workspace/ or site/
// here would show a reviewer a diff their approval cannot act on, which is
// the dishonest option; showing them only the plan their label will trigger
// is the truthful one. (workspace/ and site/ still get `tofu validate` in
// step 4 below, so a broken stack is still caught — just not planned.)
//
// Outputs, written into DIR (default: handoff_dir at the root of the
// repository, which is where the CI pipeline hands files between its stages
// and is listed in .gitignore):
//
//	diff.patch              base..HEAD as `git log -p`, oldest commit first
//	changed-files.txt       one changed path per line
//	plan.txt                full plan output — written only when a plan ran
//	                        and succeeded; deleted if one is half-written
//	validation-failure.txt  human-readable summary — written only on failure
//
// The two snapshots are written by every run that gets past the two guards
// below, INCLUDING failing ones — a reviewer needs the evidence most when
// something went wrong. They are not written by a run that stops in a guard,
// because a guard stopping means the evidence would be a lie. Do not read the
// first sentence as "always"; the guards are the exception and they are the
// whole point.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/handoff"
	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/stacks"
	"github.com/zetlen/falconet/internal/validate"
)

const validateUsageText = `validate — the deterministic gate between the implementing agent and
whatever looks at the change next.

Modes:
  falconet validate --base SHA [--out-dir DIR] [--config FILE]

Validate HEAD against the commit the run started from:
  1. assert at least one commit exists on top of --base
  2. assert the commit does not touch the handoff dir — CI's own scratch
     has no business inside a change
  3. snapshot the commits to DIR/diff.patch and the changed paths to
     DIR/changed-files.txt — the review agent is granted no Bash, so
     its evidence has to be on disk before it starts
  4. tofu validate, once per configured stack (stacks.plan, then
     stacks.validate_only), collecting every failure
  5. plan.command, once per stacks.plan stack, into DIR/plan.txt — only
     when every planned stack validated

--base is resolved to a commit before anything compares against it; a
ref, a short sha or HEAD are fine, and a --base naming no commit is a
usage error rather than an empty diff.

Outputs, written into DIR (default: handoff_dir from config, .falconet/ at
the root of the repository):
  diff.patch              base..HEAD as ` + "`git log -p`" + `, oldest commit first
  changed-files.txt       one changed path per line
  plan.txt                full plan output — written only when a plan ran
                          and succeeded; deleted if one is half-written
  validation-failure.txt  human-readable summary — written only on failure

The snapshots are written by every run that gets past the two guards,
including failing ones. A run that stops in a guard writes neither,
because the evidence would be a lie.

This verb's stdout is not one word: it prints the whole plan into the run
log on purpose, because that is the untruncated copy a pull-request body's
truncation note points a reviewer at. Its verdict is its exit code.

On success, VALIDATED=true is appended to $GITHUB_ENV when that is set.

Exit codes: 0 = every check passed, 1 = a check failed and
            validation-failure.txt says which, 2 = usage error (including
            --help: this verb's exit code IS the verdict, and 0 would mean
            validation passed).

$TOFU overrides the planner, for the tests.
`

func validateUsage() int {
	fmt.Fprint(os.Stderr, validateUsageText)
	return 2
}

// The artifacts this verb owns in the handoff directory.
const (
	failuresFile     = "validation-failure.txt"
	planFile         = "plan.txt"
	diffFile         = "diff.patch"
	changedFilesFile = "changed-files.txt"
)

func runValidate(args []string) int {
	var base, outDir, explicit string
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
		case "--base":
			v, ok = value("a commit sha")
			base = v
		case "--out-dir":
			v, ok = value("a directory")
			outDir = v
		case "--config":
			v, ok = value("a file")
			explicit = v
		case "-h", "--help":
			// 2, not 0. This verb's exit code IS the verdict, so a --help
			// that exits 0 is a run reporting that validation passed.
			return validateUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return validateUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}
	if base == "" {
		return validateUsage()
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
		return 1
	}
	// Resolve --out-dir against the caller's CWD before changing directories:
	// every path below is built against the repository root, and this process
	// stands there from here on.
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
	// The name, for the message and the prefix check below. handoff.Init
	// resolved the directory; this is what to call it.
	handoffName := filepath.Base(out)

	// Resolve --base to a full commit sha before anything compares against it.
	//
	// This was a string comparison against the raw argument, and every guard
	// below inherited the assumption that the caller passed a 40-character
	// sha. It usually did, because prepare writes one. But `--base main`, or
	// a short sha, or `HEAD`, made the "no commit exists" check below
	// silently false — and then `git log "$BASE"..HEAD` produced an empty
	// diff.patch, `git diff` an empty changed-files.txt, and the run could
	// reach `exit 0` having snapshotted nothing at all. The reviewing agent
	// is granted no Bash; it would have read an empty diff and seen no
	// change. Resolve, or refuse.
	resolved, err := gitOutput("rev-parse", "--verify", "--quiet", base+"^{commit}")
	if err != nil || resolved == "" {
		fmt.Fprintln(os.Stderr, "validate: --base does not name a commit in this repository")
		return 2
	}
	base = resolved

	failures := filepath.Join(out, failuresFile)
	// Clear artifacts from any earlier pass: a stale plan.txt or diff.patch
	// read as this attempt's evidence is exactly the class of bug this
	// pipeline exists to kill. Every one of these is rewritten below on the
	// paths that still reach them, so an absent file always means "this
	// attempt never got that far".
	for _, name := range []string{failuresFile, planFile, diffFile, changedFilesFile} {
		if err := remove(filepath.Join(out, name)); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
	}

	// A guard stopping: the report is the whole report, and it goes to
	// stderr too, where a person debugging the run reads it.
	stopWith := func(report string) int {
		if err := os.WriteFile(failures, []byte(report), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", failures, err)
			return 1
		}
		fmt.Fprint(os.Stderr, report)
		return 1
	}

	// --- 1. a commit must exist -------------------------------------------
	//
	// See internal/validate. Unreachable as the pipeline now stands, and kept
	// anyway: this guard is what catches that stopping being true.
	head, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		fmt.Fprintln(os.Stderr, "git rev-parse HEAD failed")
		return 1
	}
	if head == base {
		return stopWith(validate.ReportNoCommit(base))
	}
	short, err := gitOutput("rev-parse", "--short", "HEAD")
	if err != nil {
		fmt.Fprintln(os.Stderr, "git rev-parse --short HEAD failed")
		return 1
	}
	fmt.Println(validate.CommitLine(short, base))

	// --- 2. the commit must not carry CI's own handoff files ----------------
	//
	// See internal/validate for why the guard exists and why it is a literal
	// prefix. Unlike everything below it, this does not join a collected
	// report and carry on: it invalidates the very artifacts the remaining
	// steps produce — step 3 would snapshot a half-megabyte plan file as an
	// added file and hand that to the reviewer — so it stops here, and stops
	// first.
	changedFiles := filepath.Join(out, changedFilesFile)
	if err := gitToFile(changedFiles, "diff", "--name-only", base, "HEAD"); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}
	changed, err := os.ReadFile(changedFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", changedFiles, err)
		return 1
	}
	if smuggled := validate.Smuggled(validate.SplitLines(changed), handoffName); len(smuggled) > 0 {
		rc := stopWith(validate.ReportSmuggled(handoffName, smuggled))
		// The snapshot is not evidence of anything now; it is not left to
		// be read as if it were.
		if err := remove(changedFiles); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		return rc
	}

	// --- 3. snapshot the evidence for the reviewer --------------------------
	// `git log -p`, not `git diff`: the reviewer gets each commit message with
	// the change it claims to describe (see the header). --reverse so it
	// reads oldest-first, the order the work happened in, rather than git's
	// default newest-first. --no-color for the same reason tofu gets
	// -no-color: a color.ui=always anywhere in the config would otherwise
	// write escape codes into a file the next reader has to strip, which is
	// two of the nineteen shell calls the #33 run wasted.
	if err := gitToFile(filepath.Join(out, diffFile), "log", "--reverse", "--no-decorate", "--no-color", "-p", base+"..HEAD"); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}
	fmt.Println("changed files:")
	fmt.Print(validate.Indent(string(changed)))

	status := 0
	// Appends one section to the report. Every failure below is collected
	// this way, and none of them stops the run (see internal/validate).
	report := func(section string) bool {
		f, err := os.OpenFile(failures, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", failures, err)
			return false
		}
		_, werr := f.WriteString(section)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", failures, werr)
			return false
		}
		return true
	}

	// One scratch file for tofu's streams, reused per stack and removed on
	// the way out, with a second one beside it for a plan's stdout.
	scratchFile, err := os.CreateTemp("", "falconet-validate-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot create a scratch file: %v\n", err)
		return 1
	}
	scratch := scratchFile.Name()
	_ = scratchFile.Close()
	stackPlan := scratch + ".plan"
	defer func() {
		_ = os.Remove(scratch)
		_ = os.Remove(stackPlan)
	}()

	runner := stacks.Runner{Tofu: envOr("TOFU", "tofu"), RepoRoot: root}

	// --- 4. tofu validate, once per stack -----------------------------------
	//
	// The stacks come from config now. stacks.plan get a real init because
	// they are the ones step 5 plans and one init serves both verbs;
	// stacks.validate_only get -backend=false, which is enough for `validate`
	// to see provider schemas without touching state or credentials they do
	// not need for that (stacks.Runner.Init carries the record).
	//
	// planStackFailed tracks the PLANNED stacks alone, because only their own
	// validate result decides whether step 5 attempts a plan; a broken
	// validate-only stack must not silently cancel the one plan a reviewer
	// acts on.
	//
	// It was called dns_validate_ok and it was inverted with respect to its
	// name: 0 meant OK and 1 meant failed, and it read correctly only because
	// its one use said `-ne 0`. Renamed rather than left as a trap for
	// whoever writes `-eq 1` by intuition and plans a stack whose validate
	// just failed.
	planStacks := nonEmpty(cfg.Schema.Stacks.Plan)
	checkStacks := nonEmpty(cfg.Schema.Stacks.ValidateOnly)
	planStackFailed := false
	for _, s := range append(append([]string{}, planStacks...), checkStacks...) {
		planned := contains(planStacks, s)
		// A configured stack that is not there is a configuration error
		// rather than a validation failure. Reported rather than fatal, so
		// the other stacks are still checked and the report says which key
		// named a directory that is not in the repository.
		if !isDir(runner.Dir(s)) {
			status = 1
			key := "validate_only"
			if planned {
				planStackFailed = true
				key = "plan"
			}
			if !report(validate.SectionStackMissing(s, cfg.StackMissing(key, s, root))) {
				return 1
			}
			continue
		}

		// init, then validate, both streams into the one scratch file, the
		// second carrying on where the first stopped.
		ok, output, err := initAndValidate(runner, s, planned, scratch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		if ok {
			fmt.Println(validate.ValidateOK(s))
			continue
		}
		status = 1
		if planned {
			planStackFailed = true
		}
		if !report(validate.SectionValidateFailed(s, output)) {
			return 1
		}
	}

	// --- 5. plan (the planned stacks only — see the header on why) ----------
	//
	// The command is plan.command from config (stacks.Runner.PlanCommand
	// carries the record of how it is read). All planned stacks land in one
	// plan.txt, because the handoff protocol names one file and assemble
	// attaches one file. With more than one they are separated by a
	// `## <stack>` heading; with the default single stack the file is exactly
	// what it always was.
	planPath := filepath.Join(out, planFile)
	if planStackFailed {
		if !report(validate.SectionPlanNotAttempted()) {
			return 1
		}
	} else {
		multi := len(planStacks) > 1
		// Created empty first, as `: > plan.txt` did: a run with nothing to
		// plan still leaves the file the next stage looks for.
		plan, err := os.Create(planPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", planPath, err)
			return 1
		}
		for _, s := range planStacks {
			argv, err := runner.PlanCommand(cfg.Schema.Plan.Command, s)
			if err != nil {
				// A configuration error, and a mechanical one: no plan ran,
				// so no plan.txt is left to be read as one.
				_ = plan.Close()
				_ = os.Remove(planPath)
				fmt.Fprintf(os.Stderr, "falconet: %v (set plan.command in %s)\n", err, orDefault(cfg.File, ".github/falconet.json"))
				return 1
			}
			// stdout to its own file, stderr to the scratch. See stacks.Run
			// for why the plan is never a pipe into this process.
			ok, err := runPlan(argv, stackPlan, scratch)
			if err != nil {
				_ = plan.Close()
				_ = os.Remove(planPath)
				fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
				return 1
			}
			planBytes, err := os.ReadFile(stackPlan)
			if err != nil {
				_ = plan.Close()
				_ = os.Remove(planPath)
				fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", stackPlan, err)
				return 1
			}
			if ok {
				if multi {
					if _, err := plan.WriteString(validate.PlanHeading(s)); err != nil {
						_ = plan.Close()
						fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", planPath, err)
						return 1
					}
				}
				if _, err := plan.Write(planBytes); err != nil {
					_ = plan.Close()
					fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", planPath, err)
					return 1
				}
				fmt.Println(validate.PlanOK(s, planBytes))
				// Echo the whole plan into the run log. When a PR body has to
				// truncate the plan to fit GitHub's 65536-character limit,
				// this is the untruncated copy the truncation note points a
				// reviewer at.
				fmt.Println(validate.PlanBegin(s))
				if _, err := os.Stdout.Write(planBytes); err != nil {
					_ = plan.Close()
					fmt.Fprintf(os.Stderr, "falconet: cannot write to stdout: %v\n", err)
					return 1
				}
				fmt.Println(validate.PlanEnd(s))
				continue
			}
			status = 1
			planErr, err := os.ReadFile(scratch)
			if err != nil {
				_ = plan.Close()
				fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", scratch, err)
				return 1
			}
			if !report(validate.SectionPlanFailed(s, planErr, planBytes)) {
				_ = plan.Close()
				return 1
			}
			// On stderr, never in the report: see validate.GuardNote.
			fmt.Fprint(os.Stderr, validate.GuardNote)
			// A half-written plan must never reach the PR-body assembler.
			_ = plan.Close()
			if err := remove(planPath); err != nil {
				fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
				return 1
			}
			break
		}
		if err := plan.Close(); err != nil && status == 0 {
			fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", planPath, err)
			return 1
		}
	}

	if status == 0 {
		fmt.Println("validation OK")
		if err := handoff.GitHubEnvAppend("VALIDATED=true"); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "validation FAILED — see %s\n", failures)
	text, err := os.ReadFile(failures)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", failures, err)
		return 1
	}
	_, _ = os.Stderr.Write(text)
	return status
}

// initAndValidate runs `init` then `validate` in the stack, both streams of
// both into the scratch file — truncated first, then carried on — and
// returns whether both succeeded and what they wrote. A tofu that could not
// be started at all is written into the output as well, so the report says
// why there was no validate rather than showing an empty section; the
// error return is for the scratch file itself.
func initAndValidate(r stacks.Runner, stack string, planned bool, scratch string) (ok bool, output []byte, err error) {
	f, err := os.OpenFile(scratch, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o600)
	if err != nil {
		return false, nil, fmt.Errorf("cannot write %s: %v", scratch, err)
	}
	ok = toFile(f, stacks.Run(r.Init(stack, planned), f, f)) &&
		toFile(f, stacks.Run(r.Validate(stack), f, f))
	if cerr := f.Close(); cerr != nil {
		return false, nil, fmt.Errorf("cannot write %s: %v", scratch, cerr)
	}
	output, err = os.ReadFile(scratch)
	if err != nil {
		return false, nil, fmt.Errorf("cannot read %s: %v", scratch, err)
	}
	return ok, output, nil
}

// runPlan runs the plan command with stdout to one file and stderr to
// another, both truncated, and returns whether it succeeded. As with
// initAndValidate, a command that could not start has that written to its
// stderr file.
func runPlan(argv []string, stdout, stderr string) (bool, error) {
	outF, err := os.OpenFile(stdout, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o600)
	if err != nil {
		return false, fmt.Errorf("cannot write %s: %v", stdout, err)
	}
	errF, err := os.OpenFile(stderr, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o600)
	if err != nil {
		_ = outF.Close()
		return false, fmt.Errorf("cannot write %s: %v", stderr, err)
	}
	ok := toFile(errF, stacks.Run(exec.Command(argv[0], argv[1:]...), outF, errF))
	if cerr := outF.Close(); cerr != nil {
		_ = errF.Close()
		return false, fmt.Errorf("cannot write %s: %v", stdout, cerr)
	}
	if cerr := errF.Close(); cerr != nil {
		return false, fmt.Errorf("cannot write %s: %v", stderr, cerr)
	}
	return ok, nil
}

// toFile turns a subprocess's result into success or failure. A non-zero
// exit is a failure the process explained itself; a process that could not
// be started is a failure this explains, into the same file, so the report
// never carries an empty section for a tofu that was not there.
func toFile(f *os.File, err error) bool {
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		// Best effort into a scratch file; the failure itself is the answer.
		_, _ = fmt.Fprintf(f, "falconet: %v\n", err)
	}
	return false
}

// gitOutput is one git command's stdout, trimmed, with its stderr passed
// through. The command runs in the repository root, where this process
// stands.
func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitToFile runs one git command with its stdout into a file — created or
// truncated — so the snapshot is the bytes git wrote and nothing else.
func gitToFile(path string, args ...string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cannot write %s: %v", path, err)
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("cannot write %s: %v", path, cerr)
	}
	if runErr != nil {
		return fmt.Errorf("git %s failed", strings.Join(args[:1], " "))
	}
	return nil
}

// remove is `rm -f`: a file that is not there is not an error.
func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %s: %v", path, err)
	}
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// nonEmpty drops empty entries, as the `[ -n "$_s" ]` in the read loops did.
func nonEmpty(list []string) []string {
	var out []string
	for _, s := range list {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
