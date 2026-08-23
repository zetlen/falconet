// Package validate is the two guards the validate verb stops in, the report
// it writes when a check fails, and the lines it prints into the run log —
// the words, held apart from the subprocesses that produce the facts they
// describe. The verb itself, cmd/falconet/validate.go, is the sequence: git,
// the stacks, the files, and the exit code.
//
// Nothing here touches the filesystem or runs a process: the verb hands in
// the changed-path list, the handoff directory's name, and the bytes tofu
// wrote, and gets back a decision or a section of the report. That is what
// lets the smuggle guard be held to a table and a property rather than to
// the two fixtures a suite can carry.
//
// # Who reads the report
//
// validation-failure.txt is posted verbatim as a comment on the REQUESTER's
// issue — no agent ever reads it — so it explains what happened to someone
// who asked for a DNS record and is owed an answer. It gives no instructions,
// because there is nobody here to instruct.
//
// No `set -e`, and nothing like it: this verb's job is to collect ALL the
// failures in one pass. There is no amending stage to feed any more — a
// failed validation goes straight to a human — so the one report this writes
// is the only report anybody gets, and it had better be complete. Every
// section below APPENDS, and the verb never stops at the first one, with
// three exceptions: the two guards (no commit, a smuggled handoff directory),
// which are the whole point, and the plan step, which stops at the first
// failed plan — plan.txt is gone by then, and a later stack's plan would
// have nowhere to land.
package validate

import (
	"bytes"
	"fmt"
	"strings"
)

// --- 1. a commit must exist -------------------------------------------------
//
// Unreachable as the pipeline now stands, and kept anyway. The outcome is
// decided before this verb is called: the commit verb makes the commit, and
// the workflow only reaches validation on `success`, which means there is
// one. This guard is what catches that stopping being true.

// ReportNoCommit is the whole report for a run whose HEAD is still the base.
// base is the resolved commit, in full: the requester is told the sha the run
// started from, which is the one thing about the run they can find.
func ReportNoCommit(base string) string {
	return lines(
		"## No commit on the working branch",
		"",
		"HEAD is still "+base+" — the commit this run started from — so nothing",
		"was recorded for this request. There is nothing to validate, plan",
		"or review.",
		"",
		"Nothing about the request caused this. The step that makes the",
		"commit reported that it had made one, and then there was none, so",
		"the fault is in the pipeline. The run log linked above has the",
		"whole story; someone should read it before this request is tried",
		"again.")
}

// --- 2. the commit must not carry CI's own handoff files --------------------
//
// The handoff dir is gitignored, and the only thing that stages anything now
// is the commit verb, which passes an explicit vetted pathspec and
// never `-f`. The implementing agent holds no Bash at all, so it cannot
// force-add anything; this arm is unreachable too, for the same kind of reason
// as arm 1. It stays because of what it costs versus what it prevents: one
// prefix test per path, against a commit that would put CI's scratch into
// the pull request and hand the reviewing agent its own evidence — the
// request, the plan, this very report — as part of the change under review.
//
// Matched as a literal prefix, not as a regex. This was an ERE with
// $HANDOFF_DIR interpolated raw, which was harmless while the name was a
// constant and is not now that it comes from config: a value carrying `(` or
// `[` produces a broken pattern, grep exits 2, and the `if` reads that as "no
// match" -- a guard that fails OPEN. The `"?` in that pattern caught git's
// quoted form, which it uses for paths containing control characters or
// quotes; Smuggled strips the quotes before the prefix test, as the loop that
// replaced the grep did.

// Smuggled is the guard: the entries of `git diff --name-only` that are the
// handoff directory or lie under it, in the form git printed them — quoted
// where git quoted them — so the report names what git would. name is the
// handoff directory's NAME, the basename of the resolved directory, never a
// pattern. An empty name matches nothing: a guard with no name to hold has
// nothing to refuse, and must not refuse everything by accident.
func Smuggled(changed []string, name string) []string {
	if name == "" {
		return nil
	}
	var hits []string
	for _, p := range changed {
		u := unquote(p)
		if u == name || strings.HasPrefix(u, name+"/") {
			hits = append(hits, p)
		}
	}
	return hits
}

// unquote strips git's quoted form, exactly as the bash stripped it: one
// trailing `"` if there is one, then one leading `"` if there is one. The
// escapes inside are left as they are — the test is a prefix test on the
// directory's name, and a name that needed quoting is not one this pipeline
// will ever resolve a handoff directory to.
func unquote(p string) string {
	p = strings.TrimSuffix(p, `"`)
	p = strings.TrimPrefix(p, `"`)
	return p
}

// ReportSmuggled is the whole report for a refused commit: the paths, as git
// printed them, under the name the requester can look for.
func ReportSmuggled(name string, paths []string) string {
	return lines(
		"## The commit contains CI's own handoff files",
		"",
		"These committed paths are inside "+name+"/:",
		"",
		strings.TrimSuffix(Indent(strings.Join(paths, "\n")+"\n"), "\n"),
		"",
		name+"/ is where each stage of this pipeline leaves files for",
		"the next one — the request, the plan, the diff, this report. It is",
		"listed in .gitignore and it is not part of any change. Committing it",
		"would ship CI's internals in the pull request and would hand the",
		"reviewing stage its own notes as part of the change to review.",
		"",
		"Nothing about the request caused this either. These paths are",
		"ignored, so only a deliberate force-add can commit them, and the",
		"only thing that stages files in this pipeline is a script that",
		"names every path it stages. Something upstream is wrong. The",
		"branch and the run log linked above have the rest.")
}

// --- the collected sections -------------------------------------------------
//
// Each is appended to the report as the run goes, in the order the stacks
// are configured, planned stacks first.

// SectionStackMissing reports a configured stack that is not a directory.
// A configured stack that is not there is a configuration error rather than
// a validation failure. Reported rather than fatal, so the other stacks are
// still checked and the report says which key named a directory that is not
// in the repository — message is config.StackMissing's sentence, which names
// the key, the file, and what belongs in it.
func SectionStackMissing(stack, message string) string {
	return "## the configured stack " + stack + "/ is not in this repository\n\n" + message + "\n\n"
}

// SectionValidateFailed carries what `init` and `validate` wrote, both
// streams, as one gate. The heading says "validate" even when it was `init`
// that died, because the two are one gate from the requester's side and
// splitting the wording would mean explaining the difference to someone who
// did not ask.
func SectionValidateFailed(stack string, output []byte) string {
	return "## tofu validate failed (" + stack + "/)\n\n" + string(output) + "\n"
}

// SectionPlanNotAttempted says why there is no plan, rather than leaving the
// reader to infer it from the absence of one.
func SectionPlanNotAttempted() string {
	return "## tofu plan was not attempted\n\n`tofu validate` failed above, so a plan would only repeat it.\n\n"
}

// SectionPlanFailed carries the plan's stderr and then everything it had
// written to stdout before it gave up — the partial plan is evidence too.
func SectionPlanFailed(stack string, stderr, partial []byte) string {
	return "## tofu plan failed (" + stack + "/)\n\n" + string(stderr) +
		"\n### plan output before the failure\n\n" + string(partial) + "\n"
}

// GuardNote is the two lines that follow a failed plan on STDERR, never in
// the report.
//
// A failing guard shows up above as a precondition error, and the guard
// is authoritative. That sentence used to be in the report; it is here
// instead, because the report is posted verbatim to the REQUESTER, who
// asked for a DNS record and is not the person being told not to weaken
// a guard. The file's own header promises it gives no instructions,
// because there is nobody there to instruct — and this was the one path
// that broke that promise.
const GuardNote = "a failing guard shows up as a precondition error; the guard is\n" +
	"authoritative — quote it, never weaken it\n"

// PlanHeading separates one planned stack's output from the next in
// plan.txt. All planned stacks land in one plan.txt, because the handoff
// protocol names one file and assemble attaches one file. With more than one
// they are separated by this heading; with the default single stack the file
// is exactly what it always was.
func PlanHeading(stack string) string {
	return "## " + stack + "\n\n"
}

// --- the run log ------------------------------------------------------------
//
// This verb does not have a one-word stdout. Five of the six verbs print an
// outcome; this one prints the whole plan into the run log on purpose,
// because that is the untruncated copy a PR body's truncation note points a
// reviewer at. Its verdict is its exit code.

// CommitLine names the commit under test and the commit it is measured
// against, the latter by its first seven characters.
func CommitLine(short, base string) string {
	return "commit: " + short + " on top of " + prefix(base, 7)
}

// ValidateOK is one stack's clean validate.
func ValidateOK(stack string) string {
	return "tofu validate (" + stack + "/): OK"
}

// PlanOK is one stack's clean plan, with its line count as `wc -l` counts:
// the newlines. Printed as a number — GNU wc's answer, which is what the
// runners printed; BSD wc pads the count with spaces, and a Mac's run log
// carried the padding.
func PlanOK(stack string, plan []byte) string {
	return fmt.Sprintf("tofu plan (%s/): OK (%d lines)", stack, Lines(plan))
}

// PlanBegin and PlanEnd bracket the plan in the run log. When a PR body has
// to truncate the plan to fit GitHub's 65536-character limit, this is the
// untruncated copy the truncation note points a reviewer at.
func PlanBegin(stack string) string { return "----- begin tofu plan (" + stack + "/) -----" }

// PlanEnd closes what PlanBegin opened.
func PlanEnd(stack string) string { return "----- end tofu plan (" + stack + "/) -----" }

// Lines is `wc -l`: the number of newlines.
func Lines(b []byte) int {
	return bytes.Count(b, []byte{'\n'})
}

// Indent is `sed 's/^/  /'`: two spaces at the start of every line. A final
// line with no newline is indented and left without one, as GNU sed leaves
// it; an empty input is an empty output.
func Indent(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	start := true
	for i := 0; i < len(text); i++ {
		if start {
			b.WriteString("  ")
			start = false
		}
		b.WriteByte(text[i])
		if text[i] == '\n' {
			start = true
		}
	}
	return b.String()
}

// SplitLines is the changed-path list as the `while read` loop saw it: one
// entry per newline-terminated line. A last line without a newline is read
// too — `read` would have dropped it, but git never writes one, and a path
// is not something to lose to a missing byte.
func SplitLines(b []byte) []string {
	var out []string
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		out = append(out, string(line))
	}
	return out
}

// lines joins report lines, each terminated.
func lines(ls ...string) string {
	return strings.Join(ls, "\n") + "\n"
}

// prefix is the first n bytes of s, or all of it.
func prefix(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
