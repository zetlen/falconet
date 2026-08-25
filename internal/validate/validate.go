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

// Unquote is unquote, exported for the verb: the coverage check tests a
// changed path against the stack directories, and git's quoted form would
// miss every one of them.
func Unquote(p string) string { return unquote(p) }

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

// --- 3. the change must reach a stack this repository plans -----------------
//
// #23. v0.2.0 planned `stacks.plan` whatever the change was, and the path
// guard was `*.tf` anywhere, so a change in a directory the config had never
// heard of passed every check: the configured stacks were clean by
// construction — the diff was nowhere near them — and the pull request
// carried "No changes. Your infrastructure matches the configuration." as
// the entire plan of a change to a database tier. A true plan of the wrong
// stack is the most expensive kind of wrong answer, because it reads as
// evidence.
//
// Two ways a change can fail to reach a plan, and they are different
// sentences to the person who filed the request:
//
//   - Uncovered: it changed Terraform this repository treats as belonging to
//     no stack at all. Nothing validated it and nothing could plan it.
//   - Unplanned: it reached stacks, and none of them is one this repository
//     plans. Everything was validated; there is simply no plan for a human
//     to approve, so there is nothing to open a pull request about.
//
// Neither is the requester's doing and neither is a fault in the change, so
// both say so. Both are collected sections rather than a stop: the stacks
// still get validated, and a report that also carries a broken `.tf` is a
// better report than one that stops at the coverage line.

// SectionUncovered reports changed Terraform files that lie in no stack.
// stacks is every stack this run knew about, planned first; declared says
// whether the config named them or discovery found them, which is the whole
// difference between "add it to your config" and "falconet could not see it
// from the tree". configFile is where the config was read from.
func SectionUncovered(paths, stacks []string, declared bool, configFile string) string {
	body := []string{
		"## the change is in no stack this repository knows about",
		"",
		"These committed paths hold OpenTofu that belongs to none of the",
		"stacks this run checked:",
		"",
		strings.TrimSuffix(Indent(strings.Join(paths, "\n")+"\n"), "\n"),
		"",
		stackList("The stacks this run checked", stacks),
	}
	if declared {
		body = append(body,
			"They are the ones "+configFile+" names in .stacks. A directory",
			"holding .tf files that is named in neither .stacks.plan nor",
			".stacks.validate_only is a directory falconet will not guess",
			"about: it is not validated, it is not planned, and a pull request",
			"carrying some other stack's plan would be a plan of something",
			"this change does not touch.",
			"",
			"Nothing about the request caused this, and nothing is wrong with",
			"the change. "+configFile+" does not cover the whole repository",
			"yet. Someone who can edit it should add the directory above to",
			".stacks.plan — if a human applies it from a pull request — or to",
			".stacks.validate_only, and the request can be filed again.")
	} else {
		body = append(body,
			"They are every directory holding .tf files that this repository",
			"has, apart from ones another directory uses as a module. A .tf",
			"file at the repository root is in none of them: falconet runs",
			"`tofu -chdir=<stack>` and never plans the tree it stands in.",
			"",
			"Nothing about the request caused this, and nothing is wrong with",
			"the change. Someone who can edit "+configFile+" should say in",
			".stacks which directories are stacks and which of them a human",
			"applies, and the request can be filed again.")
	}
	return strings.Join(body, "\n") + "\n\n"
}

// SectionUnplanned reports a change that reached stacks, none of them
// planned. touched is what it reached, planned is what the repository plans.
func SectionUnplanned(touched, planned []string, declared bool, configFile string) string {
	body := []string{
		"## nothing this change touches is planned",
		"",
		stackList("The change reaches", touched),
		stackList("This repository plans", planned),
		"",
		"So there is no plan of this change for anybody to approve. The",
		"stacks above validated — the change parses and is well-formed — but",
		"a pull request from this pipeline exists to carry a `tofu plan` a",
		"human reads and approves, and planning some other stack instead",
		"would show a reviewer a plan of something this change does not",
		"touch.",
		"",
	}
	if declared {
		body = append(body,
			"Nothing about the request caused this, and nothing is wrong with",
			"the change. "+configFile+" lists in .stacks.plan the stacks a",
			"human applies from a pull request, and what this change reaches",
			"is not among them. Someone who can edit it should decide which",
			"it should be, and the request can be filed again.")
	} else {
		body = append(body,
			"Nothing about the request caused this, and nothing is wrong with",
			"the change. Someone who can edit "+configFile+" should say in",
			".stacks which directories a human applies from a pull request,",
			"and the request can be filed again.")
	}
	return strings.Join(body, "\n") + "\n\n"
}

// stackList is one labelled, indented list of stack names, as a paragraph.
// An empty list says so in words rather than printing a heading over
// nothing: "none" is the fact the reader needs.
func stackList(label string, names []string) string {
	if len(names) == 0 {
		return label + ": none.\n"
	}
	return label + ":\n\n" + Indent(strings.Join(names, "\n")+"\n")
}

// UncoveredLine and UnplannedLine are the run log's one-line forms of the two
// sections above, for the person reading the run rather than the issue.
func UncoveredLine(paths []string) string {
	return fmt.Sprintf("changed files in no stack: %s", strings.Join(paths, " "))
}

// UnplannedLine names what the change reached, when none of it is planned.
func UnplannedLine(touched []string) string {
	return "nothing planned: the change reaches " + strings.Join(touched, ", ")
}

// PlannedLine names the stacks this run will plan — every stack the config
// plans, not only the ones the change reached (see PlanUnreached) — so the
// run log says which of the repository's stacks the plan below is about
// before the plan appears.
func PlannedLine(planned []string) string {
	if len(planned) == 0 {
		return "planning: nothing (this repository plans no stacks)"
	}
	return "planning: " + strings.Join(planned, ", ")
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

// PlanHeading names the stack a plan is of. All planned stacks land in one
// plan.txt, because the handoff protocol names one file and assemble
// attaches one file, and every one of them is headed — including the only
// one, which is the case that mattered (#23).
//
// It used to be written only when there was more than one stack to separate,
// on the reasoning that a single stack's plan.txt should be exactly the bytes
// tofu wrote. Then a pull request carried "No changes. Your infrastructure
// matches the configuration." as its entire plan, over a diff that changed a
// database tier in a different directory, and there was nothing anywhere in
// the pull request saying which stack that plan was of. The heading is two
// lines and it makes that pull request obviously wrong at a glance. A
// reviewer who cannot see what they are approving a plan OF is not reviewing.
func PlanHeading(stack string) string {
	return "## " + stack + "\n\n"
}

// PlanUnreached is what stands in plan.txt where a stack's plan would be,
// when planning that stack failed and the change does not reach it.
//
// Every configured stack is planned, not only the ones the change reaches:
// the module graph cannot see a `terraform_remote_state` edge, so a change in
// one stack can move another's plan without touching a file in it, and the
// tool that knows which is `tofu plan` rather than a walk over `source =`.
// The cost of asking every stack is that a stack failing for its OWN reasons
// — a credential this repository does not hold, a backend that is down —
// would otherwise delete the plan a reviewer is waiting for and stop a pull
// request that has nothing to do with it. So a failure in a stack the change
// does not reach says so here, in the body, beside the evidence: a MISSING
// plan, named, rather than an absent one nobody can distinguish from an empty
// one. A failure in a stack the change DOES reach still stops everything,
// because that one is about this change.
func PlanUnreached(stack string) string {
	return "_falconet could not plan `" + stack + "`, and this change does not reach\n" +
		"it. This is a missing plan, not an empty one — the run log carries what\n" +
		"OpenTofu said. No other stack below is affected._\n\n"
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

// ValidateUnreachedLine is one stack's failed init-or-validate, in a stack
// the change does not reach. Planning every stack means initialising every
// stack's BACKEND, and a backend is a credential and a network: a stack this
// change has nothing to do with must not fail the run because its bucket was
// unreachable for ten seconds. It is said in the log and nowhere else.
func ValidateUnreachedLine(stack string) string {
	return "tofu validate (" + stack + "/): FAILED, and the change does not reach it — not fatal"
}

// PlanUnreachedLine is the run log's form of PlanUnreached: a plan that
// failed without failing the run, said in the log so the person reading it
// does not go looking for the stack in the report.
func PlanUnreachedLine(stack string) string {
	return "tofu plan (" + stack + "/): FAILED, and the change does not reach it — not fatal"
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
