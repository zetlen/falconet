// Package stacks is the OpenTofu a configured stack is put through — `init`,
// `validate`, and the plan command from config — shared by the two verbs
// that run it: validate, which checks and plans the change, and prepare,
// which plans the baseline the change is measured against. One place, so
// the argv each verb hands tofu is the same argv, and the two facts about
// tofu that survive the port (ADR-0006: "never end a plan early" and "always
// -no-color") are written once, above the one subprocess call.
//
// The tests stub the planner, as they stub the formatter and the scanner:
// $TOFU names a script that records its argv and fails where the case says
// to, so the argv shapes here are part of the contract the suite holds.
//
// # The plan command
//
// The command is plan.command from config, with {stack} replaced by the
// stack's directory. It is split on whitespace, so an argument containing a
// space cannot be expressed — the default has none, and a consumer who needs
// one should say so and get a better mechanism rather than a quoting puzzle.
// If the first word is `tofu` it is replaced by $TOFU, which is how the tests
// reach it.
//
// # Initialising before planning
//
// A runner is a fresh checkout with no .terraform/ in it, and `tofu plan`
// there is "missing required providers", so the stack is initialised first --
// the same init validate runs, for the same reason.
//
// Only when tofu is what runs the plan. A consumer who replaced plan.command
// with a script of their own initialises in it, which is usually why they
// replaced it.
package stacks

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Runner is one tofu binary and one repository root.
type Runner struct {
	// Tofu is $TOFU, or "tofu" on the PATH. The tests stub the planner, as
	// they stub the formatter and the scanner.
	Tofu string
	// RepoRoot is the repository being worked on. A stack is a directory
	// under it, named in config relative to it.
	RepoRoot string
}

// Dir is the stack's directory: <RepoRoot>/<stack>, joined by a slash and
// nothing else, the way the scripts joined them. Not cleaned: the argv the
// stub records is the argv the suite asserts on, and a path that has been
// tidied is a different string.
func (r Runner) Dir(stack string) string {
	return r.RepoRoot + "/" + stack
}

// Init is `tofu init` in the stack. A planned stack gets a REAL init (with
// the backend), not -backend=false: it is a stack the plan step plans, and a
// real init serves both `validate` and `plan` without initializing twice. A
// validate-only stack is never planned, so a `-backend=false` init is all it
// needs: enough for `tofu validate` to see provider schemas, without touching
// state or credentials it does not need for that.
func (r Runner) Init(stack string, backend bool) *exec.Cmd {
	if backend {
		return exec.Command(r.Tofu, "-chdir="+r.Dir(stack), "init", "-input=false")
	}
	return exec.Command(r.Tofu, "-chdir="+r.Dir(stack), "init", "-backend=false", "-input=false")
}

// Validate is `tofu validate -no-color` in the stack. -no-color because its
// output lands in a file that a requester reads (see Run).
func (r Runner) Validate(stack string) *exec.Cmd {
	return exec.Command(r.Tofu, "-chdir="+r.Dir(stack), "validate", "-no-color")
}

// PlanCommand is the argv for plan.command in the stack: the command split
// on whitespace, every {stack} replaced by the stack's directory, and a
// leading `tofu` replaced by the configured binary (see the package header).
// The split happens before the substitution, so a repository path with a
// space in it stays one argument — the split is of what the operator wrote,
// not of what it expanded to.
//
// An empty command is an error: nothing would run the plan, and "no plan"
// must never look like "a plan with nothing in it".
func (r Runner) PlanCommand(command, stack string) ([]string, error) {
	argv := strings.Fields(command)
	if len(argv) == 0 {
		return nil, errors.New("plan.command is empty, so nothing would run the plan")
	}
	for i, a := range argv {
		argv[i] = strings.ReplaceAll(a, "{stack}", r.Dir(stack))
	}
	if argv[0] == "tofu" {
		argv[0] = r.Tofu
	}
	return argv, nil
}

// InitFirst reports whether a plan command needs the stack initialised
// before it runs: only when its first word is `tofu` (see the package
// header). A consumer's own script is assumed to initialise in it.
func InitFirst(command string) bool {
	f := strings.Fields(command)
	return len(f) > 0 && f[0] == "tofu"
}

// Run runs cmd with its stdout and stderr attached directly to the two files
// — the child writes to the file descriptors themselves, and nothing in this
// process sits between tofu and the disk. Both may be the same file, and a
// file opened for append carries on from where an earlier command left it.
//
// Never end a plan early: read its stdout to completion, or to a file.
// Killing tofu mid-plan strands a state lock. Never pipe a plan into another
// process: a SIGPIPE from a short reader kills tofu before it releases its
// state lock. Redirect, then read the file. So the plan is never a pipe into
// this process: the file is the reader, and the file reads everything.
//
// Always `-no-color` into a file. -no-color is not cosmetic: without it tofu
// writes ANSI escapes into the file and whoever reads it next has to strip
// them. A run on issue #33 spent two of its nineteen shell calls on
// `sed -r 's/\x1b\[[0-9;]*m//g'`.
func Run(cmd *exec.Cmd, stdout, stderr *os.File) error {
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
