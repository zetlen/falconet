// Package assemble builds a pull-request body that always ships the WHOLE
// plan.
//
// PR #28 shipped a plan the agent had abridged by hand: literal
// "# ... omitted here for length" comments inside the code fence. AGENTS.md
// requires every PR to carry pasted `tofu plan` output, and the human who
// approved that one was reading the agent's summary of the evidence instead
// of the evidence. No prompt fixes that reliably. Assembling the body
// mechanically from the plan file removes the opportunity: the agent writes
// prose, this package attaches the plan, and the two never mix.
//
// The plan is wrapped in <details><summary>tofu plan output</summary> and a
// fence long enough to survive any backticks the plan itself contains.
//
// If the assembled body would exceed the limit, the PLAN is truncated — never
// the description, never the "Closes" line. The truncation is deterministic:
// whole lines only, keeping the first 70% and last 30% of the remaining
// budget, with the elision replaced by a note that states how many lines were
// dropped and where the untruncated plan can be read (the plan URL if given,
// else the run log, where validate echoes it in full). Nothing is ever
// dropped silently and nothing is ever summarized.
//
// Sizes are counted in bytes, which is conservative: GitHub's limit is in
// characters, and a byte count can only ever make us truncate sooner.
//
// Nothing here touches the filesystem: the verb parses its flags and reads
// its files, and hands bytes in. That is what lets every seam below be held
// to a property over arbitrary input (assemble_test.go) rather than to the
// handful of examples a fixture can carry.
package assemble

import (
	"bytes"
	"fmt"
	"strings"
)

// DefaultLimit is GitHub's hard limit on a pull-request body.
const DefaultLimit = 65536

// NoteReserve is the slice of the budget kept back for the truncation note.
// Reserve a fixed slice of the budget for the note so its own length never
// has to be solved for; coming in under the limit is fine, going over is
// not.
const NoteReserve = 640

// Input is what a body is assembled from.
type Input struct {
	// Body is the PR description with NO plan output in it — the body of the
	// implementing agent's commit message, whose prompt tells it not to
	// quote, summarize or abridge the plan.
	Body []byte
	// Plan is the full `tofu plan -no-color` output.
	Plan []byte
	// Issue is the issue number, as the caller wrote it; a "Closes #N" line
	// is appended after the body.
	Issue string
	// RunURL is the workflow run URL, cited by the truncation note when
	// PlanURL is absent.
	RunURL string
	// PlanURL is the download URL for the plan uploaded as a workflow
	// artifact (added for issue #46). Optional: every caller that omits it
	// gets exactly the unflagged output, byte for byte. When given, its link
	// is always printed next to the plan block — even when the plan fit
	// inline — so a reviewer never has to fall back to the run log to get
	// the untruncated file. On overflow, the truncation note cites THIS url
	// instead of the run log: a direct download beats sending a human
	// hunting through a step's log output for the same text.
	PlanURL string
	// Limit is the maximum body size, in bytes.
	Limit int
}

// Result is an assembled body, with the account of how it was assembled
// that the verb prints on stdout.
type Result struct {
	// Body is the assembled markdown.
	Body []byte
	// Lines is how many lines the plan had.
	Lines int
	// Truncated says whether any of the plan was elided.
	Truncated bool
	// Dropped is how many lines were elided; 0 when the plan fit.
	Dropped int
	// Where is where the note says the untruncated plan can be read; empty
	// when the plan fit.
	Where string
}

// Assemble builds the body. An error is a refusal, and means nothing should
// be written: the description alone exceeds the limit, or the body is still
// over the limit after the plan was truncated, which can only happen when a
// URL is so long the note outgrows its reserve.
func Assemble(in Input) (*Result, error) {
	plan := Normalize(in.Plan)
	fence := Fence(plan)
	head := header(in, fence)
	foot := footer(fence)

	// A budget of exactly zero is not a refusal: an empty plan fits in it,
	// and the body is then exactly the limit, which is what a limit permits.
	// The original refused at zero too, with a message that said "over" of
	// a body that was not — the property tests found it (assemble_test.go).
	overhead := len(head) + len(foot)
	budget := in.Limit - overhead
	if budget < 0 {
		return nil, fmt.Errorf("the description alone is %d bytes, over the %d limit — "+
			"refusing to truncate a human-facing description", overhead, in.Limit)
	}

	lines := bytes.Count(plan, []byte{'\n'})
	if len(plan) <= budget {
		return &Result{Body: concat(head, plan, foot), Lines: lines}, nil
	}

	// Truncating.
	avail := budget - NoteReserve
	if avail < 0 {
		avail = 0
	}
	keptHead, keptTail := Truncate(plan, avail)
	kept := bytes.Count(keptHead, []byte{'\n'}) + bytes.Count(keptTail, []byte{'\n'})
	dropped := lines - kept
	if dropped < 0 {
		dropped = 0
	}

	how, where := citation(in)
	body := concat(head, keptHead, note(dropped, lines, in.Limit, how, where), keptTail, foot)
	if len(body) > in.Limit {
		return nil, fmt.Errorf("assembled body is still %d bytes, over the %d limit", len(body), in.Limit)
	}
	return &Result{Body: body, Lines: lines, Truncated: true, Dropped: dropped, Where: where}, nil
}

// Summary is the one line the verb prints on stdout.
func (r *Result) Summary() string {
	if !r.Truncated {
		return fmt.Sprintf("PR body: %d bytes, full plan attached (%d lines)", len(r.Body), r.Lines)
	}
	return fmt.Sprintf("PR body: %d bytes, plan truncated (%d of %d lines elided, note points at %s)",
		len(r.Body), r.Dropped, r.Lines, r.Where)
}

// Normalize the plan: guarantee a trailing newline so the closing fence never
// ends up glued to the last plan line. An empty plan stays empty.
func Normalize(plan []byte) []byte {
	out := make([]byte, len(plan), len(plan)+1)
	copy(out, plan)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out
}

// Fence is a fence one backtick longer than the longest run of backticks in
// the plan, and never shorter than the three that markdown needs.
func Fence(plan []byte) string {
	n := 3
	if longest := LongestBacktickRun(plan); longest >= 3 {
		n = longest + 1
	}
	return strings.Repeat("`", n)
}

// LongestBacktickRun is the length of the longest run of consecutive
// backticks anywhere in b; 0 when there are none.
func LongestBacktickRun(b []byte) int {
	longest, run := 0, 0
	for _, c := range b {
		if c == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	return longest
}

// Truncate keeps the first 70% and the last 30% of avail bytes of the plan,
// on whole lines. Head is a prefix of plan and tail is a suffix; the two never
// overlap, and together they never exceed avail. A plan that fits in avail is
// returned whole, as the head: there is nothing to elide.
//
// A byte cut can land mid-line (head -c / tail -c, in the original); drop the
// partial line at the seam so the kept text is always whole lines. The line
// at the seam goes even when the cut landed exactly on its boundary — that
// is what `sed '$d'` and `sed '1d'` did, and it is the cheap side to err on:
// one more line in the note's count, never a fragment in the body.
func Truncate(plan []byte, avail int) (head, tail []byte) {
	if avail >= len(plan) {
		return plan, nil
	}
	headBudget := avail * 70 / 100
	tailBudget := avail - headBudget
	head = wholeHead(plan[:min(headBudget, len(plan))])
	tail = wholeTail(plan[max(len(plan)-tailBudget, 0):])
	return head, tail
}

// wholeHead drops the last line of a prefix cut, whether the cut left it
// partial or whole.
func wholeHead(chunk []byte) []byte {
	s := chunk
	if n := len(s); n > 0 && s[n-1] == '\n' {
		s = s[:n-1]
	}
	i := bytes.LastIndexByte(s, '\n')
	return chunk[:i+1]
}

// wholeTail drops the first line of a suffix cut, whether the cut left it
// partial or whole.
func wholeTail(chunk []byte) []byte {
	i := bytes.IndexByte(chunk, '\n')
	if i < 0 {
		return chunk[:0]
	}
	return chunk[i+1:]
}

// header is everything before the plan: the description, the "Closes" line,
// the artifact link, the <details> block and the opening fence.
func header(in Input, fence string) []byte {
	var b bytes.Buffer
	b.Write(in.Body)
	fmt.Fprintf(&b, "\nCloses #%s\n\n", in.Issue)
	// Printed whether or not the plan below needs truncating: a body that
	// fits still benefits from a one-click download of the exact same file,
	// and a reviewer should never have to notice truncation before finding
	// the link.
	//
	// The blank line after it is new. The original built this line with
	// `$(printf '…%s\n\n')`, and command substitution strips trailing
	// newlines, so the link was glued onto `<details>`; it rendered only
	// because a browser closes a paragraph when a details element opens
	// inside one. The printf said what was meant.
	if in.PlanURL != "" {
		fmt.Fprintf(&b, "Full plan output (workflow artifact, 30-day retention): %s\n\n", in.PlanURL)
	}
	fmt.Fprintf(&b, "<details><summary>tofu plan output</summary>\n\n%s\n", fence)
	return b.Bytes()
}

// footer closes the fence and the <details> block.
func footer(fence string) []byte {
	return []byte(fence + "\n\n</details>\n")
}

// citation is how, and where, the note says the whole plan can be read.
//
// A direct download beats a pointer into a log a human has to search through
// — so the artifact URL wins whenever one was given, and the run log is only
// a fallback for callers that never got one.
func citation(in Input) (how, where string) {
	if in.PlanURL != "" {
		return "downloadable in full, unredacted, as a workflow artifact (30-day retention) at:", in.PlanURL
	}
	where = "the workflow run log for this pull request"
	if in.RunURL != "" {
		where = in.RunURL
	}
	return `printed in the "Validate" step of:`, where
}

// note is what stands in for the elided lines: how many, of how many, and
// where the whole plan is. It says so in the body itself, where the person
// reading the plan is, rather than only in a log.
func note(dropped, total, limit int, how, where string) []byte {
	rule := "[ ---------------------------------------------------------------- ]"
	return []byte(fmt.Sprintf("\n%s\n"+
		"[ %d of %d lines of plan output are omitted HERE so that this\n"+
		"[ pull-request body fits GitHub's %d-character limit. They were\n"+
		"[ neither summarized nor rewritten. The complete, untruncated plan is\n"+
		"[ %s\n"+
		"[   %s\n"+
		"%s\n\n",
		rule, dropped, total, limit, how, where, rule))
}

func concat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
