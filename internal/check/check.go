// Package check is what the check verb keeps of the repository's own check:
// a bounded tail of the command's output, and the file the next agent pass
// reads when the check failed. The verb itself, cmd/falconet/check.go, is
// the flags, the config, the subprocess, the file and the exit code.
//
// Nothing here touches the filesystem or runs a process: the verb streams
// the command's output through a Tail and hands the result to Report. That
// is what lets the cap be held to a property — never over budget, never
// starting mid-line when anything was dropped — rather than to fixtures.
//
// # Why a tail, and why bounded
//
// The check is the operator's command — a test suite, a linter, a build —
// and its output is whatever that prints: a few lines, or the whole of a
// verbose test run. The file this package writes is read by an agent in a
// fresh context, and a file that is most of a megabyte of passing tests is
// worse than one that is the last of them, where the failure is: test
// runners, compilers and linters put the verdict at the end. So the LAST
// bytes are kept, up to a budget, and the note at the top says how much of
// the beginning is not here and where it can be read (the run log, which
// got every byte). Content is dropped loudly or not at all, as everywhere
// else in this pipeline.
//
// # What feeds back, and what does not
//
// This file is the only thing a check failure feeds to the next agent pass
// (docs/decisions.md, "A check verb and a caller-owned loop"). A guard
// refusal — the path allowlist, the content denylist, the rename check, the
// secret scan — never reaches this package: a guard the agent can iterate
// against is an oracle, not a guard (principle 3), and the commit verb
// writes those refusals to failure-reason.txt, for a person.
package check

import (
	"bytes"
	"fmt"
	"strings"
)

// TailLimit is the budget for the output kept in check-failure.txt, in
// bytes. Sixty-four kibibytes is a few hundred lines past most failures'
// verdicts and well inside what an agent reads in one call; the full
// output is in the run log regardless.
const TailLimit = 64 << 10

// FailureFile is the handoff file the report goes to. The next agent pass
// looks for it by this name; the check verb removes it on a pass, so its
// presence means the last check failed.
const FailureFile = "check-failure.txt"

// Tail is an io.Writer that keeps the last Limit bytes written to it and
// counts every byte, kept or not. The zero value keeps TailLimit.
type Tail struct {
	// Limit is the budget; zero or negative means TailLimit.
	Limit int
	// Total is every byte written, kept or not.
	Total int
	buf   []byte
}

func (t *Tail) limit() int {
	if t.Limit <= 0 {
		return TailLimit
	}
	return t.Limit
}

// Write keeps the last limit+1 bytes of everything written so far: the
// budget, and one byte before it, which is how Bytes knows whether the
// budget begins on a line boundary.
func (t *Tail) Write(p []byte) (int, error) {
	t.Total += len(p)
	keep := t.limit() + 1
	if len(p) >= keep {
		t.buf = append(t.buf[:0], p[len(p)-keep:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - keep; over > 0 {
		// Shift in place rather than reslice: a reslice keeps the array
		// growing under a long-running check, and the whole point is a
		// bound.
		copy(t.buf, t.buf[over:])
		t.buf = t.buf[:keep]
	}
	return len(p), nil
}

// Bytes is what was kept. When nothing was dropped it is every byte
// written. When anything was, the output resumes at the first line
// boundary inside the budget — the partial line at the front goes with
// what was dropped, so the output never starts mid-line; a reader handed
// half a line cannot tell which half.
func (t *Tail) Bytes() []byte {
	if t.Total <= t.limit() {
		return t.buf
	}
	// buf holds the byte before the budget and then the budget. The first
	// newline at or after that byte is the first boundary inside it: at
	// index 0, the budget itself begins a line and nothing more goes.
	if i := bytes.IndexByte(t.buf, '\n'); i >= 0 {
		return t.buf[i+1:]
	}
	// One line longer than the budget: nothing kept is a whole line.
	return nil
}

// Dropped is how many bytes are not in Bytes.
func (t *Tail) Dropped() int {
	return t.Total - len(t.Bytes())
}

// Report is the text of check-failure.txt: what ran, how it ended, and its
// output — cut at the front, with a note saying so, when the tail dropped
// anything. command is the argv as configured; status is how the command
// ended, as os/exec reports it ("exit status 2", "signal: killed").
func Report(command []string, status string, tail *Tail) []byte {
	var b bytes.Buffer
	b.WriteString("The repository's own check failed on the tree as it is now.\n\n")
	fmt.Fprintf(&b, "    command: %s\n", strings.Join(command, " "))
	fmt.Fprintf(&b, "    ended:   %s\n\n", status)
	out := tail.Bytes()
	switch {
	case tail.Total == 0:
		b.WriteString("It printed nothing.\n")
	case tail.Dropped() > 0:
		fmt.Fprintf(&b, "Its output, from the end — the first %d of %d bytes are not\nhere; the run log has all of it:\n\n",
			tail.Dropped(), tail.Total)
	default:
		b.WriteString("Its output, whole:\n\n")
	}
	b.Write(out)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		b.WriteByte('\n')
	}
	return b.Bytes()
}
