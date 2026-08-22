package assemble

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

// maxCount is well above testing/quick's default of 100. These are the seams
// where PR #28 lived, and a hundred samples of random text is not a search.
const maxCount = 10000

// text is random input shaped like the thing: lines of random length, some
// empty, backticks in runs of random length, and — one time in four — no
// trailing newline. It never contains '[', ']' or '-', so the truncation
// note can be told apart from plan text by the tests that take a body back
// apart.
type text []byte

func (text) Generate(r *rand.Rand, size int) reflect.Value {
	var b []byte
	lines := r.Intn(size + 1)
	for i := 0; i < lines; i++ {
		for j, n := 0, r.Intn(60); j < n; j++ {
			switch r.Intn(10) {
			case 0:
				b = append(b, bytes.Repeat([]byte{'`'}, 1+r.Intn(6))...)
			case 1:
				b = append(b, ' ')
			default:
				b = append(b, byte('a'+r.Intn(26)))
			}
		}
		b = append(b, '\n')
	}
	if len(b) > 0 && r.Intn(4) == 0 {
		b = b[:len(b)-1]
	}
	return reflect.ValueOf(text(b))
}

func check(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// fenced takes an assembled body apart from the outside: the fence it used,
// and everything between the opening fence line and the closing one.
func fenced(body []byte) (fence string, inside []byte, ok bool) {
	open := []byte("<details><summary>tofu plan output</summary>\n\n")
	i := bytes.Index(body, open)
	if i < 0 {
		return "", nil, false
	}
	rest := body[i+len(open):]
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 {
		return "", nil, false
	}
	fence = string(rest[:nl])
	if strings.Trim(fence, "`") != "" || len(fence) < 3 {
		return "", nil, false
	}
	foot := []byte(fence + "\n\n</details>\n")
	if !bytes.HasSuffix(body, foot) {
		return "", nil, false
	}
	inside = rest[nl+1 : len(rest)-len(foot)]
	return fence, inside, true
}

// --- the properties ----------------------------------------------------------

// Truncation never splits a line and never exceeds the byte budget, for any
// input: the head is a prefix of the plan ending on a newline, the tail is a
// suffix starting right after one, neither is over its share, and the two
// never overlap.
func TestTruncationKeepsWholeLinesWithinBudget(t *testing.T) {
	check(t, func(p text, a uint32) bool {
		plan := Normalize([]byte(p))
		avail := int(a) % (2*len(plan) + 2) // past the end too: nothing to elide
		head, tail := Truncate(plan, avail)

		headBudget := avail * 70 / 100
		tailBudget := avail - headBudget
		if avail < len(plan) && (len(head) > headBudget || len(tail) > tailBudget) {
			return false
		}
		if len(head)+len(tail) > avail && avail < len(plan) {
			return false
		}
		if !bytes.HasPrefix(plan, head) || (len(head) > 0 && head[len(head)-1] != '\n') {
			return false
		}
		if !bytes.HasSuffix(plan, tail) {
			return false
		}
		if start := len(plan) - len(tail); len(tail) > 0 && start > 0 && plan[start-1] != '\n' {
			return false
		}
		return len(head) <= len(plan)-len(tail)
	})
}

// The fence is strictly longer than the longest backtick run anywhere in the
// plan, for any plan, and never shorter than the three markdown needs.
func TestFenceOutrunsEveryBacktickRun(t *testing.T) {
	check(t, func(p text) bool {
		fence := Fence([]byte(p))
		return len(fence) >= 3 && len(fence) > LongestBacktickRun([]byte(p))
	})
}

// The same property, held on the assembled body rather than on the plan:
// truncated or not, no line between the opening fence and the closing fence
// carries a run of backticks that could close the fence early. The
// description sits outside the fence; its backticks are its author's to
// close.
func TestNothingInsideTheFenceCanCloseIt(t *testing.T) {
	var fit, cut int
	check(t, func(p, d text, l uint32) bool {
		in := Input{Body: []byte(d), Plan: []byte(p), Issue: "28",
			RunURL: "https://example.invalid/runs/1", Limit: limitNear(d, p, l)}
		r, err := Assemble(in)
		if err != nil {
			return true // refused: nothing written, nothing to hold
		}
		if r.Truncated {
			cut++
		} else {
			fit++
		}
		fence, inside, ok := fenced(r.Body)
		if !ok {
			return false
		}
		for _, line := range bytes.Split(inside, []byte{'\n'}) {
			if LongestBacktickRun(line) >= len(fence) {
				return false
			}
		}
		return true
	})
	if fit == 0 || cut == 0 {
		t.Errorf("both paths must be exercised: fit=%d truncated=%d", fit, cut)
	}
}

// A plan under the limit is carried in full, byte for byte: the assembled
// body is the description, the Closes line, the block, the whole normalized
// plan, and the closing — and the limit is a ceiling, not an ingredient.
func TestAPlanUnderTheLimitIsCarriedWhole(t *testing.T) {
	check(t, func(p, d text) bool {
		plan := Normalize([]byte(p))
		in := Input{Body: []byte(d), Plan: []byte(p), Issue: "46",
			PlanURL: "https://example.invalid/artifacts/46", Limit: 1 << 30}
		r, err := Assemble(in)
		if err != nil {
			return false
		}
		if r.Truncated || r.Dropped != 0 || r.Where != "" || r.Lines != bytes.Count(plan, []byte{'\n'}) {
			return false
		}
		_, inside, ok := fenced(r.Body)
		if !ok || !bytes.Equal(inside, plan) {
			return false
		}
		if !bytes.HasPrefix(r.Body, []byte(d)) || !bytes.Contains(r.Body, []byte("\nCloses #46\n")) {
			return false
		}
		// The smallest limit that fits produces the identical body...
		in.Limit = len(r.Body)
		again, err := Assemble(in)
		if err != nil || !bytes.Equal(again.Body, r.Body) {
			return false
		}
		// ...and one byte less is either a truncation or a refusal, never a
		// body over the limit.
		in.Limit = len(r.Body) - 1
		less, err := Assemble(in)
		return err != nil || (less.Truncated && len(less.Body) <= in.Limit)
	})
}

// For any limit: a body that is written fits it; and when the plan was
// truncated, the note is there, the kept lines are a run from the start and
// a run to the end, and kept plus dropped is the whole plan.
func TestAWrittenBodyFitsAndAccountsForEveryLine(t *testing.T) {
	var fit, cut int
	check(t, func(p, d text, l uint32) bool {
		plan := Normalize([]byte(p))
		in := Input{Body: []byte(d), Plan: []byte(p), Issue: "1", Limit: limitNear(d, p, l)}
		r, err := Assemble(in)
		if err != nil {
			return true
		}
		if len(r.Body) > in.Limit {
			return false
		}
		_, inside, ok := fenced(r.Body)
		if !ok {
			return false
		}
		if !r.Truncated {
			fit++
			return bytes.Equal(inside, plan) && r.Dropped == 0
		}
		cut++
		// inside is head + note + tail; the note is the only thing in it that
		// the generator could not have written.
		i := bytes.Index(inside, []byte("\n[ ----"))
		end := []byte("---- ]\n\n")
		j := bytes.LastIndex(inside, end)
		if i < 0 || j < 0 {
			return false
		}
		head, note, tail := inside[:i], inside[i:j+len(end)], inside[j+len(end):]
		if !bytes.HasPrefix(plan, head) || !bytes.HasSuffix(plan, tail) {
			return false
		}
		if len(head) > len(plan)-len(tail) {
			return false
		}
		kept := bytes.Count(head, []byte{'\n'}) + bytes.Count(tail, []byte{'\n'})
		if kept+r.Dropped != r.Lines || r.Dropped < 1 {
			return false
		}
		return bytes.Contains(note, fmt.Appendf(nil, "[ %d of %d lines of plan output are omitted HERE", r.Dropped, r.Lines))
	})
	if fit == 0 || cut == 0 {
		t.Errorf("both paths must be exercised: fit=%d truncated=%d", fit, cut)
	}
}

// limitNear keeps a random limit in the range where the decision is made —
// below the overhead, between, and past the whole plan — rather than
// scattering it over sizes nothing here will ever reach.
func limitNear(d, p text, l uint32) int {
	return int(l) % (len(d) + len(p) + 2*NoteReserve + 1)
}

// --- the examples ------------------------------------------------------------

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"":      "",
		"a":     "a\n",
		"a\n":   "a\n",
		"a\n\n": "a\n\n",
		"\n":    "\n",
	} {
		if got := string(Normalize([]byte(in))); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFenceExamples(t *testing.T) {
	for in, want := range map[string]string{
		"":             "```",
		"no backticks": "```",
		"a `` b":       "```",
		"a ``` b":      "````",
		"x ```` y":     "`````",
		"`` ````` ``":  "``````",
	} {
		if got := Fence([]byte(in)); got != want {
			t.Errorf("Fence(%q) = %q, want %q", in, got, want)
		}
	}
}

// The seams, exactly as `head -c N | sed '$d'` and `tail -c N | sed '1d'`
// cut them: the line at the cut goes whether the cut left it partial or
// whole, and a cut with no newline in it keeps nothing.
func TestSeams(t *testing.T) {
	heads := map[string]string{
		"a\nb\n": "a\n", // cut on a boundary: the whole last line still goes
		"a\nb":   "a\n", // cut mid-line
		"a\n":    "",
		"a":      "",
		"abc":    "",
		"":       "",
	}
	for in, want := range heads {
		if got := string(wholeHead([]byte(in))); got != want {
			t.Errorf("wholeHead(%q) = %q, want %q", in, got, want)
		}
	}
	tails := map[string]string{
		"b\nc\n":    "c\n", // cut mid-line
		"\nc\n":     "c\n", // cut right after a newline: an empty first line goes
		"a\nb\nc\n": "b\nc\n",
		"\n":        "",
		"b":         "",
		"":          "",
	}
	for in, want := range tails {
		if got := string(wholeTail([]byte(in))); got != want {
			t.Errorf("wholeTail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateSplitsSeventyThirty(t *testing.T) {
	plan := []byte(strings.Repeat("0123456789\n", 100)) // 1100 bytes, 100 lines
	head, tail := Truncate(plan, 110)                   // 77 head, 33 tail
	if got := len(head); got != 66 {                    // 6 whole lines of the 7 cut
		t.Errorf("head = %d bytes, want 66", got)
	}
	if got := len(tail); got != 22 { // 2 whole lines of the 3 cut
		t.Errorf("tail = %d bytes, want 22", got)
	}
	if head, tail := Truncate(plan, 0); len(head)+len(tail) != 0 {
		t.Errorf("avail 0 kept %d bytes", len(head)+len(tail))
	}
	if head, tail := Truncate(plan, len(plan)); !bytes.Equal(head, plan) || len(tail) != 0 {
		t.Errorf("avail = len(plan) should keep the whole plan as the head, got %d + %d", len(head), len(tail))
	}
}

// The layout around the plan, byte for byte: a description without a
// trailing newline gets exactly one before "Closes", one with keeps its own.
func TestLayout(t *testing.T) {
	for body, want := range map[string]string{
		"abc":   "abc\nCloses #7\n\n<details><summary>tofu plan output</summary>\n\n```\nplan\n```\n\n</details>\n",
		"abc\n": "abc\n\nCloses #7\n\n<details><summary>tofu plan output</summary>\n\n```\nplan\n```\n\n</details>\n",
	} {
		r, err := Assemble(Input{Body: []byte(body), Plan: []byte("plan"), Issue: "7", Limit: DefaultLimit})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(r.Body); got != want {
			t.Errorf("body %q:\n got %q\nwant %q", body, got, want)
		}
		if got, want := r.Summary(), fmt.Sprintf("PR body: %d bytes, full plan attached (1 lines)", len(want)); got != want {
			t.Errorf("summary = %q, want %q", got, want)
		}
	}
	t.Run("the artifact link is printed even when the plan fit", func(t *testing.T) {
		r, err := Assemble(Input{Body: []byte("d\n"), Plan: []byte("p\n"), Issue: "7",
			PlanURL: "https://example.invalid/a", Limit: DefaultLimit})
		if err != nil {
			t.Fatal(err)
		}
		want := "d\n\nCloses #7\n\nFull plan output (workflow artifact, 30-day retention): https://example.invalid/a\n\n<details>"
		if !bytes.HasPrefix(r.Body, []byte(want)) {
			t.Errorf("got %q, want prefix %q", r.Body, want)
		}
	})
	t.Run("an empty plan is a fenced nothing", func(t *testing.T) {
		r, err := Assemble(Input{Body: []byte("d\n"), Plan: nil, Issue: "7", Limit: DefaultLimit})
		if err != nil {
			t.Fatal(err)
		}
		if want := "```\n```\n\n</details>\n"; !bytes.HasSuffix(r.Body, []byte(want)) || r.Lines != 0 {
			t.Errorf("got %q (%d lines)", r.Body, r.Lines)
		}
	})
}

// Where the note points: the artifact URL wins whenever one was given, the
// run URL is the fallback, and with neither the run log is named generically.
func TestTheNoteCitesTheBestPlaceItHas(t *testing.T) {
	plan := []byte(strings.Repeat("LINE xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n", 200))
	cases := []struct {
		name, runURL, planURL string
		how, where            string
	}{
		{"artifact over run log", "https://example.invalid/runs/99", "https://example.invalid/artifacts/46",
			"downloadable in full, unredacted, as a workflow artifact (30-day retention) at:", "https://example.invalid/artifacts/46"},
		{"run log when no artifact", "https://example.invalid/runs/99", "",
			`printed in the "Validate" step of:`, "https://example.invalid/runs/99"},
		{"the generic log with neither", "", "",
			`printed in the "Validate" step of:`, "the workflow run log for this pull request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Assemble(Input{Body: []byte("Description.\n"), Plan: plan, Issue: "46",
				RunURL: c.runURL, PlanURL: c.planURL, Limit: 5000})
			if err != nil {
				t.Fatal(err)
			}
			if !r.Truncated || r.Where != c.where {
				t.Errorf("truncated=%v where=%q, want %q", r.Truncated, r.Where, c.where)
			}
			for _, line := range []string{
				"[ ---------------------------------------------------------------- ]\n",
				fmt.Sprintf("[ %d of 200 lines of plan output are omitted HERE so that this\n", r.Dropped),
				"[ pull-request body fits GitHub's 5000-character limit. They were\n",
				"[ neither summarized nor rewritten. The complete, untruncated plan is\n",
				"[ " + c.how + "\n",
				"[   " + c.where + "\n",
			} {
				if !bytes.Contains(r.Body, []byte(line)) {
					t.Errorf("note lacks %q", line)
				}
			}
			if want := fmt.Sprintf("PR body: %d bytes, plan truncated (%d of 200 lines elided, note points at %s)",
				len(r.Body), r.Dropped, c.where); r.Summary() != want {
				t.Errorf("summary = %q, want %q", r.Summary(), want)
			}
			if c.planURL != "" && bytes.Contains(r.Body, []byte(c.runURL)) {
				t.Error("the run URL leaked into a body that has an artifact URL")
			}
		})
	}
}

func TestRefusals(t *testing.T) {
	t.Run("a body of exactly the limit is written; one byte over is refused", func(t *testing.T) {
		// The empty plan is the case the property tests found: the original
		// refused at a budget of zero, calling a body that equalled the
		// limit "over" it.
		for _, plan := range []string{"", "p\n"} {
			in := Input{Body: []byte("d\n"), Plan: []byte(plan), Issue: "1", Limit: DefaultLimit}
			r, err := Assemble(in)
			if err != nil {
				t.Fatal(err)
			}
			in.Limit = len(r.Body)
			exact, err := Assemble(in)
			if err != nil || !bytes.Equal(exact.Body, r.Body) {
				t.Errorf("plan %q at exactly the limit: %v", plan, err)
			}
			in.Limit = len(r.Body) - 1
			if over, err := Assemble(in); plan == "" && err == nil {
				t.Errorf("an empty plan one byte over the limit was written: %q", over.Body)
			} else if plan != "" && (err == nil || !strings.Contains(err.Error(), "still")) {
				// A one-line plan over by a byte: the budget is 1, the note
				// does not fit in it, and that is the other refusal.
				t.Errorf("plan %q one byte over: err = %v", plan, err)
			}
		}
	})
	t.Run("the description alone over the limit", func(t *testing.T) {
		r, err := Assemble(Input{Body: bytes.Repeat([]byte("d"), 100), Plan: []byte("p\n"), Issue: "1", Limit: 10})
		if r != nil || err == nil {
			t.Fatalf("expected a refusal, got %v, %v", r, err)
		}
		for _, want := range []string{"over the 10 limit", "refusing to truncate a human-facing description"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})
	t.Run("a URL so long the note outgrows its reserve", func(t *testing.T) {
		plan := []byte(strings.Repeat("line\n", 200))
		in := Input{Body: []byte("d\n"), Plan: plan, Issue: "1",
			PlanURL: "https://example.invalid/" + strings.Repeat("x", 2000)}
		in.Limit = len(header(in, "```")) + len(footer("```")) + 700
		r, err := Assemble(in)
		if r != nil || err == nil || !strings.Contains(err.Error(), "assembled body is still") {
			t.Fatalf("expected the still-over refusal, got %v, %v", r, err)
		}
	})
}
