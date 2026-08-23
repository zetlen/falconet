package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

// maxCount is a hundred times testing/quick's default: the sentinel rule is
// the guard, and a hundred random messages is not a search.
const maxCount = 10000

func check(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

const emphasis = "#*`_"
const punct = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

func pick(r *rand.Rand, set string) byte { return set[r.Intn(len(set))] }

func randomCase(r *rand.Rand, s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' && r.Intn(2) == 0 {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// decorated is a sentinel alone on a line, dressed the way a model dresses
// one: any case, emphasis characters and whitespace before it, emphasis and
// punctuation after it, whitespace and a carriage return at the very end,
// and — for the two-word sentinels — any run of blanks between the words.
// Everything here is what Normalize must see through.
type decorated struct {
	text  string
	means Verdict
}

func (decorated) Generate(r *rand.Rand, _ int) reflect.Value {
	s := Sentinels[r.Intn(len(Sentinels))]
	words := strings.Split(s.Line, " ")
	var b strings.Builder
	for i := 0; i < r.Intn(4); i++ {
		b.WriteByte(pick(r, emphasis+" \t"))
	}
	for i, w := range words {
		if i > 0 {
			for j := 0; j < 1+r.Intn(3); j++ {
				b.WriteByte(pick(r, " \t"))
			}
		}
		b.WriteString(randomCase(r, w))
	}
	for i := 0; i < r.Intn(4); i++ {
		b.WriteByte(pick(r, emphasis+punct))
	}
	for i := 0; i < r.Intn(3); i++ {
		b.WriteByte(pick(r, " \t"))
	}
	if r.Intn(3) == 0 {
		b.WriteByte('\r')
	}
	return reflect.ValueOf(decorated{b.String(), s.Means})
}

// prose is a run of lines none of which is a sentinel: every non-empty line
// carries a digit, and no sentinel does. Some lines are empty, some are
// whitespace, some mention approval in passing.
type prose []string

func (prose) Generate(r *rand.Rand, size int) reflect.Value {
	var lines []string
	for i := 0; i < r.Intn(size+1)%6; i++ {
		switch r.Intn(6) {
		case 0:
			lines = append(lines, "")
		case 1:
			lines = append(lines, strings.Repeat(" ", 1+r.Intn(3)))
		case 2:
			lines = append(lines, fmt.Sprintf("I would have written %s if the TTL were in range (%d).",
				Sentinels[r.Intn(len(Sentinels))].Line, r.Intn(100)))
		default:
			var b strings.Builder
			for j := 0; j < 1+r.Intn(8); j++ {
				if j > 0 {
					b.WriteByte(' ')
				}
				for k := 0; k < 1+r.Intn(7); k++ {
					b.WriteByte(pick(r, "abcdefghijklmnopqrstuvwxyz0123456789"))
				}
			}
			fmt.Fprintf(&b, " %d", r.Intn(10))
			lines = append(lines, b.String())
		}
	}
	return reflect.ValueOf(prose(lines))
}

func join(parts ...[]string) string {
	var all []string
	for _, p := range parts {
		all = append(all, p...)
	}
	return strings.Join(all, "\n")
}

// --- the properties ----------------------------------------------------------

// A sentinel alone on any line, in any case, wrapped in any combination of
// the four emphasis characters and trailing punctuation, is found — on that
// line, with that meaning.
func TestASentinelAloneOnAnyLineIsFound(t *testing.T) {
	check(t, func(before prose, d decorated, after prose) bool {
		r, ok := Parse(join(before, []string{d.text}, after))
		return ok && r.Line == len(before) && r.Verdict == d.means
	})
}

// A sentinel sharing its line with text — anything that is not emphasis,
// punctuation or whitespace — is not a verdict, before or after it.
func TestASentinelWithCommentaryOnItsLineIsNot(t *testing.T) {
	check(t, func(before prose, d decorated, after prose, n uint8, first bool) bool {
		commentary := fmt.Sprintf("looks good %d", n)
		line := d.text + " " + commentary
		if first {
			line = commentary + " " + d.text
		}
		_, ok := Parse(join(before, []string{line}, after))
		return !ok
	})
}

// Of two standalone sentinels, the first wins, whatever the second says.
func TestOfTwoStandaloneSentinelsTheFirstWins(t *testing.T) {
	check(t, func(before prose, first decorated, between prose, second decorated, after prose) bool {
		r, ok := Parse(join(before, []string{first.text}, between, []string{second.text}, after))
		return ok && r.Line == len(before) && r.Verdict == first.means && r.Sentinel == Normalize(first.text)
	})
}

// The body is the original lines after the sentinel line — leading blank
// lines dropped, trailing ones too — and never the sentinel line or anything
// before it. Computed here a second way, from the line index.
func TestTheBodyIsWhatFollowsTheSentinelAndNothingBefore(t *testing.T) {
	check(t, func(before prose, d decorated, after prose) bool {
		msg := join(before, []string{d.text}, after)
		r, ok := Parse(msg)
		if !ok {
			return false
		}
		lines := strings.Split(msg, "\n")[r.Line+1:]
		start := 0
		for start < len(lines) && lines[start] == "" {
			start++
		}
		end := len(lines)
		for end > start && lines[end-1] == "" {
			end--
		}
		want := strings.Join(lines[start:end], "\n")
		if r.Body != want {
			return false
		}
		// Nothing before the sentinel line, and not the line itself, is in
		// the body: the body is a suffix of the message's lines.
		return strings.HasSuffix(strings.TrimRight(msg, "\n"), r.Body)
	})
}

// A body that is blank yields empty: nothing after the sentinel, or only
// empty lines, is ""; lines of whitespace are kept (sed's `/./` sees a
// space) but IsBlank says what they are.
func TestABlankBodyYieldsEmpty(t *testing.T) {
	check(t, func(before prose, d decorated, empties uint8, spaces uint8) bool {
		var trailer []string
		for i := 0; i < int(empties)%5; i++ {
			trailer = append(trailer, "")
		}
		r, ok := Parse(join(before, []string{d.text}, trailer))
		if !ok || r.Body != "" {
			return false
		}
		for i := 0; i < int(spaces)%5; i++ {
			trailer = append(trailer, strings.Repeat(" \t", 1+i))
		}
		r, ok = Parse(join(before, []string{d.text}, trailer))
		return ok && IsBlank(r.Body)
	})
}

// --- the examples ------------------------------------------------------------

func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"APPROVED", "APPROVED"},
		{"**APPROVED**", "APPROVED"},
		{"`APPROVED`", "APPROVED"},
		{"## APPROVED", "APPROVED"},
		{"Approved.", "APPROVED"},
		{"## **Approved.**", "APPROVED"},
		{"  \tchanges   requested!!!\r", "CHANGES REQUESTED"},
		{"Changes-Requested", "CHANGES-REQUESTED"},
		{"CHANGES REQUIRED", "CHANGES REQUIRED"},
		{"__rejected__", "REJECTED"},
		// Commentary on the line stays on the line.
		{"APPROVED - looks good to me, ship it.", "APPROVED - LOOKS GOOD TO ME, SHIP IT"},
		// The period goes; the space before it stays. This is the pipeline's
		// order, and it is kept rather than improved.
		{"APPROVED .", "APPROVED "},
		// Leading punctuation that is not emphasis is not stripped.
		{"(APPROVED)", "(APPROVED"},
		// An underscore inside a word is deleted like any other.
		{"CHANGES_REQUESTED", "CHANGESREQUESTED"},
		// Non-ASCII bytes are neither space, punctuation nor letters.
		{"APPROVED\u2003", "APPROVED\u2003"},
		{"approved\u0131", "APPROVED\u0131"},
		{"", ""},
		{"!!!", ""},
		{"   ", ""},
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseExamples(t *testing.T) {
	for _, tc := range []struct {
		name, msg string
		want      Verdict
		line      int
		body      string
	}{
		{"first line", "APPROVED\nAdds one employee.\n\nWhat the plan shows.", Approved, 0, "Adds one employee.\n\nWhat the plan shows."},
		{"under a preamble", "Let me summarize.\n\nCHANGES REQUESTED\n\nThe record was added.", Rejected, 2, "The record was added."},
		{"CRLF keeps its returns in the body", "Preamble line.\r\n\r\nAPPROVED\r\nAdds one employee.\r", Approved, 2, "Adds one employee.\r"},
		{"a CR-only line is not blank", "APPROVED\n\r\nbody", Approved, 0, "\r\nbody"},
		{"both, rejection first", "CHANGES REQUESTED\n\nI would have written APPROVED if.\n\nAPPROVED", Rejected, 0, "I would have written APPROVED if.\n\nAPPROVED"},
		{"both, approval first", "APPROVED\n\nHad it touched a guard:\n\nCHANGES REQUESTED", Approved, 0, "Had it touched a guard:\n\nCHANGES REQUESTED"},
		{"bare", "APPROVED", Approved, 0, ""},
		{"trailing blank lines are not body", "APPROVED\n\n\n", Approved, 0, ""},
	} {
		r, ok := Parse(tc.msg)
		if !ok || r.Verdict != tc.want || r.Line != tc.line || r.Body != tc.body {
			t.Errorf("%s: got %+v (ok=%v), want verdict %v line %d body %q", tc.name, r, ok, tc.want, tc.line, tc.body)
		}
	}
	for _, msg := range []string{
		"",
		"I have approved changes like this one, and this is\napproved in spirit, but I cannot say\nAPPROVED here without qualification. Approved subject to that.",
		"APPROVED - looks good to me, ship it.",
		"Verdict: APPROVED",
		"(APPROVED)",
		"APPROVED .",
	} {
		if r, ok := Parse(msg); ok {
			t.Errorf("Parse(%q) found %+v", msg, r)
		}
	}
}

// The verbatim message from run 32093607680, kept byte for byte in
// tests/fixtures/ so that no rewrite of the parser can quietly stop handling
// it. The suite holds it through the verb; this holds it at the seam.
func TestRun32093607680(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/review-final-message-run-32093607680.txt")
	if err != nil {
		t.Skip(err)
	}
	r, ok := Parse(strings.TrimRight(string(raw), "\n"))
	if !ok || r.Verdict != Approved || r.Line != 2 {
		t.Fatalf("got %+v, ok=%v", r, ok)
	}
	if !strings.HasPrefix(r.Body, "This adds Ozamataz Buckshank") || strings.Contains(r.Body, "Confirmed there's only one commit") {
		t.Errorf("body: %q", r.Body)
	}
	if !strings.HasSuffix(r.Body, "people-count bump from 7 to 8. Nothing else changes.") {
		t.Errorf("the rest of the description must survive verbatim: %q", r.Body)
	}
}

func TestIsBlankAndOpener(t *testing.T) {
	for s, want := range map[string]bool{"": true, " \t\r\n\v\f": true, "x": false, " x ": false, "\u00a0": false} {
		if IsBlank(s) != want {
			t.Errorf("IsBlank(%q) = %v", s, !want)
		}
	}
	if got := Opener("\n  \n\r\nthe opener\nnext", 120); got != "the opener" {
		t.Errorf("Opener = %q", got)
	}
	if got := Opener("", 120); got != "" {
		t.Errorf("Opener of nothing = %q", got)
	}
	long := strings.Repeat("é", 100) // 200 bytes
	if got := Opener(long, 121); got != strings.Repeat("é", 60) {
		t.Errorf("Opener cuts on a character boundary: %d bytes", len(got))
	}
}

func TestFinal(t *testing.T) {
	for _, tc := range []struct{ name, log, want string }{
		{"one result", `[{"type":"system"},{"type":"result","result":"APPROVED\nbody"}]`, "APPROVED\nbody"},
		{"the last result wins", `[{"type":"result","result":"REJECTED"},{"type":"assistant"},{"type":"result","result":"APPROVED"}]`, "APPROVED"},
		{"the last result wins even with no result field", `[{"type":"result","result":"APPROVED"},{"type":"result"}]`, ""},
		{"a single object", `{"type":"result","result":"APPROVED"}`, "APPROVED"},
		{"no result entry", `[{"type":"system"}]`, ""},
		{"a result that is null", `[{"type":"result","result":null}]`, ""},
		{"a result that is false", `[{"type":"result","result":false}]`, ""},
		{"a result that is a number", `[{"type":"result","result":5}]`, ""},
		{"a result that is an object", `[{"type":"result","result":{"a":"APPROVED"}}]`, ""},
		{"a type that is not a string", `[{"type":1,"result":"APPROVED"}]`, ""},
		{"elements that are not objects", `[1,"x",null,[{"type":"result","result":"no"}],true]`, ""},
		{"a top-level scalar", `"APPROVED"`, ""},
		{"empty", ``, ""},
		{"whitespace", "  \n\t", ""},
		{"malformed", `[{"type":"result","result":"APPROVED"}`, ""},
		{"trailing garbage", `[{"type":"result","result":"APPROVED"}] x`, ""},
		{"one object per line", "{\"type\":\"system\"}\n{\"type\":\"result\",\"result\":\"APPROVED\\nbody\"}\n", "\nAPPROVED\nbody"},
		{"trailing newlines in the message go", `[{"type":"result","result":"APPROVED\n\n\n"}]`, "APPROVED"},
		{"a NUL survives", `[{"type":"result","result":"A\u0000B"}]`, "A\x00B"},
	} {
		if got := Final([]byte(tc.log)); got != tc.want {
			t.Errorf("%s: Final = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// --- against the tools the bash used --------------------------------------

const jqFilter = `
  (if type == "array" then . else [.] end)
  | map(select(.type? == "result"))
  | last
  | if . == null then "" else (.result // "") end
`

// Final is held to jq itself, on random log shapes, where there is a jq:
// the bash ran exactly this filter and `$(...)` stripped the trailing
// newlines. NUL is left out: jq -r prints it and bash's handling of it is
// the one thing the two shells disagree on.
func TestFinalAgreesWithJq(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("no jq to differ against")
	}
	r := rand.New(rand.NewSource(1))
	value := func() any {
		switch r.Intn(6) {
		case 0:
			return "APPROVED\nline " + fmt.Sprint(r.Intn(9))
		case 1:
			return nil
		case 2:
			return r.Intn(3)
		case 3:
			return map[string]any{"a": "APPROVED"}
		case 4:
			return false
		default:
			return "text\n\n"
		}
	}
	entry := func() any {
		switch r.Intn(5) {
		case 0:
			return map[string]any{"type": "result", "result": value()}
		case 1:
			return map[string]any{"type": "result"}
		case 2:
			return map[string]any{"type": "system"}
		case 3:
			return r.Intn(2)
		default:
			return []any{map[string]any{"type": "result", "result": "no"}}
		}
	}
	for i := 0; i < 300; i++ {
		var values []any
		for j := 0; j < 1+r.Intn(3); j++ {
			if r.Intn(4) == 0 {
				values = append(values, entry())
				continue
			}
			var arr []any
			for k := 0; k < r.Intn(5); k++ {
				arr = append(arr, entry())
			}
			values = append(values, arr)
		}
		var log bytes.Buffer
		for _, v := range values {
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			log.Write(b)
			log.WriteByte('\n')
		}
		cmd := exec.Command(jq, "-r", jqFilter)
		cmd.Stdin = bytes.NewReader(log.Bytes())
		out, err := cmd.Output()
		want := strings.TrimRight(string(out), "\n")
		if err != nil {
			want = ""
		}
		// jq -r renders a non-string result as JSON text, which can never
		// normalize to a sentinel; Final reads it as no message. Compare
		// only where the last result was a string or nothing.
		if got := Final(log.Bytes()); got != want && !strings.ContainsAny(want, "{}[]") && !isNumberish(want) {
			t.Errorf("log %s:\n  go %q\n  jq %q", log.String(), got, want)
		}
	}
}

func isNumberish(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		switch line {
		case "0", "1", "2", "true", "false":
			return true
		}
	}
	return false
}

// Normalize is held to the pipeline it replaces — tr, tr, sed — in the C
// locale, on random lines with emphasis, punctuation, blanks, returns and
// bytes above ASCII, where there is a shell. Whatever tr and sed this
// machine has: BSD on a Mac, GNU on the runners.
func TestNormalizeAgreesWithTrAndSed(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to differ against")
	}
	r := rand.New(rand.NewSource(2))
	alphabet := "abcAPROVEDchangesrequitd 0123\t\r" + emphasis + punct + "\xc3\xa9\xe2\x80\x83\x80"
	var lines []string
	for i := 0; i < 400; i++ {
		var b strings.Builder
		for j := 0; j < r.Intn(30); j++ {
			b.WriteByte(alphabet[r.Intn(len(alphabet))])
		}
		lines = append(lines, b.String())
	}
	lines = append(lines, "APPROVED .", "## **Approved.**", "(APPROVED)", "changes   requested!!!\r")
	script := `tr -d '#*` + "`" + `_\r' | tr '[:lower:]' '[:upper:]' | sed -E 's/[[:space:]]+/ /g; s/^ +//; s/ +$//; s/[[:punct:]]+$//'`
	cmd := exec.Command(sh, "-c", script)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	want := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(want) != len(lines) {
		t.Fatalf("the pipeline changed the line count: %d in, %d out", len(lines), len(want))
	}
	for i, line := range lines {
		if got := Normalize(line); got != want[i] {
			t.Errorf("Normalize(%q) = %q, tr|tr|sed says %q", line, got, want[i])
		}
	}
}
