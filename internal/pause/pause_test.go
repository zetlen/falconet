package pause

import (
	"bytes"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

const maxCount = 5000

// text is random input shaped like a body: lines of random length, some
// empty, backticks in runs, and — one time in four — no trailing newline.
type text []byte

func (text) Generate(r *rand.Rand, size int) reflect.Value {
	var b []byte
	lines := r.Intn(size + 1)
	for i := 0; i < lines; i++ {
		for j, n := 0, r.Intn(40); j < n; j++ {
			switch r.Intn(10) {
			case 0:
				b = append(b, bytes.Repeat([]byte{'`'}, 1+r.Intn(5))...)
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

// --- the comment's shape ------------------------------------------------------

func TestCommentShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Input
		want string
	}{
		{"preamble alone", Input{Preamble: "Parked."}, "Parked.\n"},
		{"branch named and linked",
			Input{Preamble: "P.", Branch: "issue-36-x", ServerURL: "https://github.com", Repository: "o/r"},
			"P.\n\nThe commits are pushed to the branch `issue-36-x`. No pull request is open for it.\n\nhttps://github.com/o/r/tree/issue-36-x\n"},
		{"branch named, no server, no link",
			Input{Preamble: "P.", Branch: "issue-36-x", Repository: "o/r"},
			"P.\n\nThe commits are pushed to the branch `issue-36-x`. No pull request is open for it.\n"},
		{"branch named, no repository, no link",
			Input{Preamble: "P.", Branch: "issue-36-x", ServerURL: "https://github.com"},
			"P.\n\nThe commits are pushed to the branch `issue-36-x`. No pull request is open for it.\n"},
		{"empty branch is no pointer at all",
			Input{Preamble: "P.", ServerURL: "https://github.com", Repository: "o/r"},
			"P.\n"},
		{"prose body, unfenced",
			Input{Preamble: "P.", Body: []byte("One?\n\nTwo?\n")},
			"P.\n\nOne?\n\nTwo?\n"},
		{"titled body, fenced and collapsed",
			Input{Preamble: "P.", Body: []byte("Error: x\n"), BodyTitle: "validation output"},
			"P.\n\n<details><summary>validation output</summary>\n\n```\nError: x\n```\n\n</details>\n"},
		{"run log last",
			Input{Preamble: "P.", Body: []byte("x\n"), RunURL: "https://example.invalid/run/1"},
			"P.\n\nx\n\n(Run log: https://example.invalid/run/1)\n"},
		{"everything, in order",
			Input{Preamble: "P.", Branch: "b", ServerURL: "s", Repository: "o/r",
				Body: []byte("log\n"), BodyTitle: "t", RunURL: "u"},
			"P.\n\nThe commits are pushed to the branch `b`. No pull request is open for it.\n\ns/o/r/tree/b\n\n<details><summary>t</summary>\n\n```\nlog\n```\n\n</details>\n\n(Run log: u)\n"},
	} {
		if got := string(Comment(tc.in)); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// The bash closed the fence with printf '```' straight after `cat`, so a body
// without a trailing newline put the fence on the body's last line, where
// markdown does not see it. The port always closes it.
func TestATitledBodyAlwaysClosesItsFence(t *testing.T) {
	got := string(Comment(Input{Preamble: "P.", Body: []byte("no newline at end"), BodyTitle: "t", RunURL: "u"}))
	want := "P.\n\n<details><summary>t</summary>\n\n```\nno newline at end\n```\n\n</details>\n\n(Run log: u)\n"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestTheFenceOutrunsTheBodysBackticks(t *testing.T) {
	check(t, func(body text) bool {
		out := Comment(Input{Preamble: "P.", Body: []byte(body), BodyTitle: "t"})
		if len(body) == 0 {
			return string(out) == "P.\n"
		}
		open := []byte("<details><summary>t</summary>\n\n")
		i := bytes.Index(out, open)
		if i < 0 {
			return false
		}
		rest := out[i+len(open):]
		nl := bytes.IndexByte(rest, '\n')
		fence := rest[:nl]
		if bytes.Trim(fence, "`") != nil || len(fence) < 3 {
			return false
		}
		inside := rest[nl+1 : len(rest)-len(fence)-len("\n\n</details>\n")]
		return assembleLongestRun(inside) < len(fence) && bytes.HasSuffix(out, append(append([]byte{}, fence...), "\n\n</details>\n"...))
	})
}

func assembleLongestRun(b []byte) int {
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

// --- the cap ------------------------------------------------------------------

// note is the cut note, and where it begins in a truncated body.
func note(where string) []byte {
	return []byte("\n[ ... cut here: the rest is in the run log,\n      " + where + " ]\n")
}

// Truncation never splits a line, never exceeds its budget, and always ends
// in the note, for any body and any budget.
func TestTruncateNeverSplitsALineOrExceedsTheBudget(t *testing.T) {
	check(t, func(body text, budget uint16) bool {
		limit := int(budget)
		out := Truncate([]byte(body), limit, "W")
		if !bytes.HasSuffix(out, note("W")) {
			return false
		}
		kept := out[:len(out)-len(note("W"))]
		if len(kept) > limit {
			return false
		}
		if !bytes.HasPrefix([]byte(body), kept) {
			return false
		}
		// Whole lines: what is kept is empty or ends at a line break, and
		// the break is the body's own.
		return len(kept) == 0 || kept[len(kept)-1] == '\n'
	})
}

func TestCommentCutsOnlyWhatIsOverTheCap(t *testing.T) {
	check(t, func(body text, budget uint16) bool {
		limit := int(budget) + 1
		out := Comment(Input{Preamble: "P.", Body: []byte(body), Limit: limit, RunURL: "u"})
		if len(body) == 0 {
			return string(out) == "P.\n\n(Run log: u)\n"
		}
		if len(body) <= limit {
			return bytes.Equal(out, append(append([]byte("P.\n\n"), body...), "\n(Run log: u)\n"...))
		}
		return bytes.Contains(out, note("u"))
	})
}

func TestTruncateIsSedsAnswerAtTheEdges(t *testing.T) {
	for _, tc := range []struct {
		body  string
		limit int
		want  string
	}{
		// sed '$d' drops the last line whether or not it was complete.
		{"a\nb\nc\n", 6, "a\nb\n"},
		{"a\nb\nc", 5, "a\nb\n"},
		{"a\nb\nc\n", 4, "a\n"},
		{"a\nb\nc\n", 3, "a\n"},
		{"abc", 2, ""},
		{"\n", 1, ""},
		{"a\n\n", 3, "a\n"},
		{"", 0, ""},
		{"a\nb", 0, ""},
	} {
		got := string(Truncate([]byte(tc.body), tc.limit, "W"))
		if got != tc.want+string(note("W")) {
			t.Errorf("Truncate(%q, %d): got %q, want %q", tc.body, tc.limit, got, tc.want+string(note("W")))
		}
	}
}

// The original was `head -c N | sed '$d'`. This holds the port to that, on
// whatever head and sed this machine has — BSD on a Mac, GNU on the runners —
// for random bodies and budgets. It skips where there is no shell.
func TestTruncateAgreesWithHeadAndSed(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to differ against")
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 150; i++ {
		body := []byte(text{}.Generate(r, 30).Interface().(text))
		limit := r.Intn(len(body) + 5)
		cmd := exec.Command(sh, "-c", fmt.Sprintf("head -c %d | sed '$d'", limit))
		cmd.Stdin = bytes.NewReader(body)
		want, err := cmd.Output()
		if err != nil {
			t.Fatalf("sh: %v", err)
		}
		got := Truncate(body, limit, "W")
		got = got[:len(got)-len(note("W"))]
		if !bytes.Equal(got, want) {
			t.Errorf("body %q limit %d: go %q, head|sed %q", body, limit, got, want)
		}
	}
}

func TestWhere(t *testing.T) {
	if Where("u") != "u" || Where("") != "the Actions tab of this repository" {
		t.Error("Where")
	}
}

// --- the parking labels -------------------------------------------------------

func TestLabelIsAnAllowlistOfTwo(t *testing.T) {
	if err := Label("needs-info", "needs-info", "ready-for-human"); err != nil {
		t.Error(err)
	}
	if err := Label("escalated", "awaiting-reply", "escalated"); err != nil {
		t.Error(err)
	}
	err := Label("ready-for-human", "awaiting-reply", "escalated")
	if err == nil || !strings.Contains(err.Error(), "awaiting-reply") || !strings.Contains(err.Error(), "escalated") {
		t.Errorf("a refusal names the labels that would have worked: %v", err)
	}
	if err := Label("", "", "x"); err == nil {
		t.Error("an empty label never matches, even an empty config entry")
	}
}
