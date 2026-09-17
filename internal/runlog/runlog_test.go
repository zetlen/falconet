package runlog

import (
	"bytes"
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/quick"
	"unicode"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// --- workflow commands -------------------------------------------------------

func TestNeutralBreaksWhatTheRunnerWouldReadAsACommand(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"::endgroup::", "> ::endgroup::"},
		{"::add-mask::secret", "> ::add-mask::secret"},
		{"  \t::stop-commands::token", ">   \t::stop-commands::token"},
		{"\u00a0::group::x", "> \u00a0::group::x"},
		{"\u2003::error::x", "> \u2003::error::x"},
		{"\ufeff::warning::x", "> \ufeff::warning::x"},
		{"##[group]x", `##\[group]x`},
		{" ##[endgroup]", ` ##\[endgroup]`},
		{"agent: done ##[endgroup]", `agent: done ##\[endgroup]`},
		{"tool: Bash x ##[add-mask]a ##[stop-commands]t", `tool: Bash x ##\[add-mask]a ##\[stop-commands]t`},
		{"###[group]", `###\[group]`},
		{"::group::x ##[endgroup]", `> ::group::x ##\[endgroup]`},
		{"agent: ::endgroup::", "agent: ::endgroup::"},
		{": :endgroup", ": :endgroup"},
		{"#[group]", "#[group]"},
		{"std::vec", "std::vec"},
		{"", ""},
	} {
		if got := Neutral(tc.in); got != tc.want {
			t.Errorf("Neutral(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLinesSplitsAsTheRunnerDoes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"a\nb\n", []string{"a", "b"}},
		{"a\r\nb", []string{"a", "b"}},
		{"a\r::endgroup::", []string{"a", "> ::endgroup::"}},
		{"a\n\nb", []string{"a", "", "b"}},
		{"\n", []string{""}},
		{"", nil},
	} {
		if got := Lines(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Lines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// runnerLines is how the runner reads a log, written here apart from the
// package's own splitting so the property below does not grade itself: a
// line ends at "\n", "\r\n" or a lone "\r".
func runnerLines(log string) []string {
	return regexp.MustCompile(`\r\n|\r|\n`).Split(log, -1)
}

// runnerCommand is the runner's test for a command, written apart from
// Neutral for the same reason, and wider than the runner's own: `::` after
// leading white space (ActionCommand.TryParseV2), or `##[` anywhere in the
// line (ActionCommand.TryParse, which looks for it with IndexOf).
func runnerCommand(line string) bool {
	rest := strings.TrimLeftFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.Is(unicode.Zs, r) || r == '\ufeff'
	})
	return strings.HasPrefix(rest, "::") || strings.Contains(line, "##[")
}

// hostile builds text that leans on the rule: command prefixes behind
// whitespace, carriage returns and newlines, and runs long enough to be cut.
func hostile(r *rand.Rand) string {
	pieces := []string{"::", "##[", "endgroup::", "add-mask::", " ", "\t", "\u00a0", "\u2028", "\u3000", "#", "[", "add-mask]",
		"\r", "\n", "\r\n", "x", "é", "\ufeff", "::group::", strings.Repeat("a", 300), "\"", "{", "}"}
	var b strings.Builder
	for i, n := 0, r.Intn(60); i < n; i++ {
		b.WriteString(pieces[r.Intn(len(pieces))])
	}
	return b.String()
}

type hostileText string

func (hostileText) Generate(r *rand.Rand, _ int) reflect.Value {
	if r.Intn(4) == 0 {
		v, _ := quick.Value(reflect.TypeOf(""), r)
		return reflect.ValueOf(hostileText(v.String()))
	}
	return reflect.ValueOf(hostileText(hostile(r)))
}

// No line the runner reads out of what a Sink printed is a workflow command,
// whatever text went in.
func TestNoPrintedLineIsACommand(t *testing.T) {
	property := func(text hostileText) bool {
		var buf bytes.Buffer
		NewSink(&buf).Print(string(text))
		for _, line := range runnerLines(buf.String()) {
			if runnerCommand(line) {
				t.Logf("command-shaped line %q from %q", line, string(text))
				return false
			}
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 3000}); err != nil {
		t.Error(err)
	}
}

// The same holds for everything a harness prints, in either format, however
// its bytes are split across writes: the renderer's truncation and the
// JSON strings' own line breaks included.
func TestNoLineAHarnessPrintsIsACommand(t *testing.T) {
	property := func(text hostileText, seed int64, asJSON bool) bool {
		r := rand.New(rand.NewSource(seed))
		payload := []byte(string(text))
		if asJSON {
			payload = []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":` +
				quote(string(text)) + `},{"type":"tool_use","id":"t","name":` + quote(string(text)) +
				`,"input":{"file_path":` + quote(string(text)) + `}}]}}` + "\n" +
				`{"type":` + quote(string(text)) + `}` + "\n" +
				`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","is_error":true,"content":` +
				quote(string(text)) + `}]}}` + "\n")
		}
		for _, format := range Formats {
			var buf bytes.Buffer
			h := NewHarness(&buf, format)
			writeInPieces(r, h.Stdout, payload)
			writeInPieces(r, h.Stderr, payload)
			h.Close()
			for _, line := range runnerLines(buf.String()) {
				if runnerCommand(line) {
					t.Logf("%s: command-shaped line %q", format, line)
					return false
				}
			}
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range strings.ToValidUTF8(s, "?") {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&0xf])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func writeInPieces(r *rand.Rand, w interface{ Write([]byte) (int, error) }, p []byte) {
	for len(p) > 0 {
		n := 1 + r.Intn(len(p))
		_, _ = w.Write(p[:n])
		p = p[n:]
	}
}

// --- lines, as they arrive -------------------------------------------------

// A finished line is handed on at its line break, before anything else is
// written and without a Close: that is what makes the log live.
func TestALineIsHandedOnWhenItEnds(t *testing.T) {
	var got []string
	lw := NewLineWriter(MaxLine, func(b []byte) { got = append(got, string(b)) })
	_, _ = lw.Write([]byte("first"))
	if len(got) != 0 {
		t.Fatalf("an unfinished line was handed on: %q", got)
	}
	_, _ = lw.Write([]byte(" line\nsecond"))
	if !reflect.DeepEqual(got, []string{"first line"}) {
		t.Fatalf("after the first line break: %q", got)
	}
	_ = lw.Close()
	if !reflect.DeepEqual(got, []string{"first line", "second"}) {
		t.Fatalf("after Close: %q", got)
	}
	_, _ = lw.Write([]byte("late\n"))
	if len(got) != 2 {
		t.Fatalf("a write after Close was handed on: %q", got)
	}
}

func TestLineBreaksSplitOnceHoweverTheyArrive(t *testing.T) {
	text := "a\r\nb\rc\n\nd"
	want := []string{"a", "b", "c", "", "d"}
	for cut := 0; cut <= len(text); cut++ {
		var got []string
		lw := NewLineWriter(MaxLine, func(b []byte) { got = append(got, string(b)) })
		_, _ = lw.Write([]byte(text[:cut]))
		_, _ = lw.Write([]byte(text[cut:]))
		_ = lw.Close()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("split at %d: %q, want %q", cut, got, want)
		}
	}
}

func TestAnOverlongTextLineIsShownInPiecesOnRuneBoundaries(t *testing.T) {
	var got []string
	lw := NewLineWriter(8, func(b []byte) { got = append(got, string(b)) })
	_, _ = lw.Write([]byte("abcdefgé€xyz\n"))
	_ = lw.Close()
	if strings.Join(got, "") != "abcdefgé€xyz" {
		t.Fatalf("pieces %q do not add up to the line", got)
	}
	for _, piece := range got {
		if !utf8.ValidString(piece) {
			t.Errorf("piece %q splits a character", piece)
		}
	}
}

func TestAnOverlongRecordIsReportedOnceAndNotShown(t *testing.T) {
	var got []string
	lw := NewRecordWriter(8, func(b []byte) { got = append(got, string(b)) },
		func(n int) { got = append(got, "long:"+strings.Repeat("#", n)) })
	_, _ = lw.Write([]byte("short\n0123456789abcdef\nafter\n"))
	_ = lw.Close()
	want := []string{"short", "long:" + strings.Repeat("#", 16), "after"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// --- the Claude Code CLI's stream-json ----------------------------------------

func TestClaudeRendersEachLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{"not JSON is shown as text", `hello from the harness`, []string{"hello from the harness"}},
		{"truncated JSON is shown as text", `{"type":"assistant","message":{"content":[`, []string{`{"type":"assistant","message":{"content":[`}},
		{"a JSON value that is not an object is text", `[1,2,3]`, []string{`[1,2,3]`}},
		{"a blank line is nothing", "   ", nil},
		{"an object with no type is one line", `{"hello":"world"}`, []string{"event: a JSON line with no type"}},
		{"an unknown type is one line, never the object", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","secret":"x"}}`, []string{"event: rate_limit_event"}},
		{"a known type in an unknown shape is one line", `{"type":"result","num_turns":"many"}`, []string{"event: result, in a shape this renderer cannot read"}},
		{"a partial message is nothing", `{"type":"stream_event","event":{}}`, nil},
		{"a system event other than init is its subtype", `{"type":"system","subtype":"compact_boundary"}`, []string{"system: compact_boundary"}},
		{"a refused tool call names the tool, and the message is the tool error's", `{"type":"system","subtype":"permission_denied","tool_name":"WebFetch","tool_use_id":"t","decision_reason_type":"mode","message":"Permission to use WebFetch has been denied."}`,
			[]string{"tool refused: WebFetch"}},
		{"a thinking-token estimate is not shown", `{"type":"system","subtype":"thinking_tokens","estimated_tokens":50,"estimated_tokens_delta":50}`, nil},
		{"a hook starting is not shown", `{"type":"system","subtype":"hook_started","hook_name":"SessionStart:startup"}`, nil},
		{"a hook's progress is not shown", `{"type":"system","subtype":"hook_progress","hook_name":"SessionStart:startup","stdout":"x"}`, nil},
		{"a hook that succeeded is not shown", `{"type":"system","subtype":"hook_response","hook_name":"SessionStart:startup","outcome":"success","output":"{}"}`, nil},
		{"a hook that did not succeed is one line", `{"type":"system","subtype":"hook_response","hook_name":"PreToolUse:Edit","outcome":"error","stderr":"boom"}`,
			[]string{"hook: PreToolUse:Edit ended in error"}},
		{"a task's progress is not shown", `{"type":"system","subtype":"task_progress","task_id":"t","description":"d","usage":{}}`, nil},
		{"a session state change is not shown", `{"type":"system","subtype":"session_state_changed","state":"running"}`, nil},
		{"init names the model, version and permission mode", `{"type":"system","subtype":"init","cwd":"/r","model":"claude-opus-5","claude_code_version":"2.1.274","permissionMode":"dontAsk","tools":["Bash"]}`,
			[]string{"session: model claude-opus-5, Claude Code 2.1.274, permission mode dontAsk"}},
		{"text is the agent's", `{"type":"assistant","message":{"content":[{"type":"text","text":"Reading.\nThen editing."}]}}`, []string{"agent: Reading.\n       Then editing."}},
		{"a blank line in text is left blank", `{"type":"assistant","message":{"content":[{"type":"text","text":"One.\n\nTwo."}]}}`, []string{"agent: One.\n\n       Two."}},
		{"thinking is not shown", `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`, nil},
		{"an unknown block is one line", `{"type":"assistant","message":{"content":[{"type":"server_tool_use","id":"x"}]}}`, []string{"agent: a server_tool_use block"}},
		{"a message that is not blocks is one line", `{"type":"assistant","message":{"content":"text"}}`, []string{"agent: a message this renderer cannot read"}},
		{"a tool call names its tool and path", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"1","name":"Read","input":{"file_path":"/elsewhere/a.md"}}]}}`, []string{"tool: Read /elsewhere/a.md"}},
		{"a grep names its pattern and where", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"1","name":"Grep","input":{"pattern":"a\nb","glob":"*.go"}}]}}`, []string{"tool: Grep a b in *.go"}},
		{"a tool with no known input is its name", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"1","name":"TodoWrite","input":{"todos":[]}}]}}`, []string{"tool: TodoWrite"}},
		{"a shell command is on one line", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"1","name":"Bash","input":{"command":"ls\n::endgroup::"}}]}}`, []string{"tool: Bash ls ::endgroup::"}},
		{"a successful tool result is not shown", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"1","content":"the whole file"}]}}`, nil},
		{"a failed tool result is one line", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"9","is_error":true,"content":[{"type":"text","text":"no\nsuch file"}]}]}}`, []string{"tool error: no such file"}},
		{"a prompt is not shown", `{"type":"user","message":{"content":"the prompt"}}`, nil},
		{"a finished session", `{"type":"result","subtype":"success","is_error":false,"num_turns":1,"duration_ms":950,"total_cost_usd":0.01}`, []string{"result: finished — 1 turn, 950ms, $0.0100"}},
		{"a session at the turn cap says so", `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":40,"duration_ms":3723000,"errors":["Reached maximum number of turns (40)"]}`,
			[]string{"result: stopped at the turn cap (--max-turns) — 40 turns, 62m03s; Reached maximum number of turns (40)"}},
		{"refused tool calls are counted and named", `{"type":"result","subtype":"success","num_turns":3,"permission_denials":[{"tool_name":"WebFetch"},{"tool_name":"Bash"},{"tool_name":"Bash"}]}`,
			[]string{"result: finished — 3 turns, 3 tool calls refused (Bash, WebFetch)"}},
		{"a result with nothing else is still a line", `{"type":"result","subtype":"error_during_execution"}`, []string{"result: stopped by an error during execution"}},
		{"an unknown ending is named", `{"type":"result","subtype":"error_new_kind","duration_ms":42100}`, []string{"result: ended: error_new_kind — 42.1s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (&Claude{}).Render([]byte(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Render(%s)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAToolErrorNamesTheToolThatFailed(t *testing.T) {
	c := &Claude{}
	c.Render([]byte(`{"type":"system","subtype":"init","cwd":"/repo"}`))
	if got := c.Render([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/repo/docs/a.md"}}]}}`)); !reflect.DeepEqual(got, []string{"tool: Edit docs/a.md"}) {
		t.Errorf("tool call: %q", got)
	}
	if got := c.Render([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"String not found"}]}}`)); !reflect.DeepEqual(got, []string{"tool error: Edit: String not found"}) {
		t.Errorf("tool error: %q", got)
	}
}

func TestLongTextIsCutWithAMarker(t *testing.T) {
	long := strings.Repeat("é", maxText) // two bytes each
	got := (&Claude{}).Render([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"` + long + `"}]}}`))
	if len(got) != 1 {
		t.Fatalf("got %d lines", len(got))
	}
	if !strings.HasSuffix(got[0], " … [2000 more bytes not shown]") || !utf8.ValidString(got[0]) {
		t.Errorf("cut text ends %q", got[0][len(got[0])-60:])
	}
	many := strings.Repeat(`line\n`, maxTextLines+5)
	got = (&Claude{}).Render([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"` + many + `"}]}}`))
	if n := strings.Count(got[0], "\n") + 1; n != maxTextLines || !strings.HasSuffix(got[0], "line … [5 more lines not shown]") {
		t.Errorf("%d lines, ending %q", n, got[0][len(got[0])-40:])
	}
	detailLong := strings.Repeat("x", maxDetail+50)
	got = (&Claude{}).Render([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"1","name":"Read","input":{"file_path":"` + detailLong + `"}}]}}`))
	if want := "tool: Read " + strings.Repeat("x", maxDetail) + "…"; got[0] != want {
		t.Errorf("long path: %q", got[0])
	}
}

// The fixtures: one captured from the Claude Code CLI, and one built from the
// Agent SDK's documented SDKMessage types, with hostile text in it. Each
// renders to its golden file, whole, through the same writer implement uses.
func TestFixturesRenderToTheirGoldenFiles(t *testing.T) {
	for _, name := range []string{"claude-captured", "claude-documented"} {
		t.Run(name, func(t *testing.T) {
			in, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			h := NewHarness(&buf, ClaudeStreamJSON)
			_, _ = h.Stdout.Write(in)
			h.Close()
			golden := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("rendered:\n%s\nwant:\n%s", buf.String(), want)
			}
			if bytes.Contains(buf.Bytes(), []byte(`{"`)) {
				t.Errorf("a JSON object reached the log:\n%s", buf.String())
			}
			for _, line := range runnerLines(buf.String()) {
				if runnerCommand(line) {
					t.Errorf("command-shaped line %q", line)
				}
			}
		})
	}
}

func TestTextIsShownAsItIs(t *testing.T) {
	var buf bytes.Buffer
	h := NewHarness(&buf, Text)
	_, _ = h.Stdout.Write([]byte("{\"type\":\"result\"}\nplain\n\n::endgroup::\nno newline"))
	_, _ = h.Stderr.Write([]byte("on stderr\n"))
	h.Close()
	want := "{\"type\":\"result\"}\nplain\n\n> ::endgroup::\non stderr\nno newline\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestFormats(t *testing.T) {
	for _, f := range []string{"text", "claude-stream-json"} {
		if !Known(f) {
			t.Errorf("%s is not known", f)
		}
	}
	for _, f := range []string{"", "json", "claude", "TEXT"} {
		if Known(f) {
			t.Errorf("%q is known", f)
		}
	}
}
