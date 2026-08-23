// Package verdict turns the reviewing agent's final message into a verdict
// and a body, and is the whole of the verdict protocol's guard logic. The
// verb, cmd/falconet/review-verdict.go, is the flags, the execution log, the
// two files, and the exit code; it carries the record of why the protocol
// ships unwired. Nothing here touches the filesystem, which is what lets the
// sentinel rule be held to properties rather than to the handful of messages
// a suite can carry.
//
// The final `result` message is routed by the first SENTINEL LINE anywhere
// in it:
//
//	APPROVED           -> the rest is the pull-request body
//	CHANGES REQUESTED  -> the rest is the rejection
//
// Surrounding markdown emphasis (#, *, `, _) and trailing punctuation are
// stripped before matching, and the match is case-insensitive: be liberal
// about the formatting, strict about the words.
//
// # Why the whole message is scanned, and why the sentinel must stand alone
//
// This used to route on the FIRST NON-BLANK LINE only. The first live run of
// the staged pipeline (run 32093607680, issue #36) died on that. The reviewer
// approved — correctly, thoroughly, having read the entire patch — but opened
// with a line of preamble:
//
//	Confirmed there's only one commit in the patch (already read in full
//	above) touching only `people-employees.tf`.
//
//	APPROVED
//	This adds Ozamataz Buckshank, a new full-time hire, ...
//
// First non-blank line = the preamble, so: "unrecognized verdict sentinel" ->
// missing -> the issue was parked ready-for-human with a comment promising a
// prepared change, and the change was thrown away with the runner. A clean
// approval became a dead end because of one sentence of throat-clearing. The
// prompt already told that agent to put the sentinel first; it did it anyway,
// which is the whole argument for fixing the PARSER and not just the prompt.
//
// So the scan runs over every line and stops at the first one that IS a
// sentinel. "Is", not "starts with": the line must consist of nothing but the
// sentinel once emphasis and trailing punctuation are stripped. That
// strictness is what makes scanning the whole message safe — a reviewer who
// writes "I would have approved this if ..." in the middle of a rejection must
// not be read as approving, and a PR description that discusses approval must
// not hijack the verdict from three paragraphs up. The pre-#36 code matched a
// PREFIX (APPROVED*), which was tolerable while only line one was examined and
// is not tolerable across a whole document; a sentinel with commentary glued
// onto the same line is now unrecognized rather than guessed at.
//
// If both sentinels appear on their own lines, the first one wins. It is the
// reviewer's own document order and there is no better tie-break available;
// the alternative — refusing to route a message that contains both — turns a
// stated verdict into another "missing", which is the failure this section
// exists to record.
//
// # What the verb prints
//
// Prints exactly one word on stdout — approved | rejected | missing.
// "missing" means no usable verdict was found (no execution file, no result
// message, or no recognizable sentinel). The caller MUST treat that as "not
// approved" and park the issue; guessing on a reviewer's behalf is the one
// thing this whole stage exists to prevent.
package verdict

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// Verdict is what a sentinel means. There are two; the type exists so that
// the verb's switch has something to be exhaustive over.
type Verdict int

const (
	// Approved files the body as the pull-request description.
	Approved Verdict = iota + 1
	// Rejected files the body as the rejection posted to the requester.
	Rejected
)

// Sentinels is the one spelling of the alternation: the five normalized
// lines a reviewer may end with, and what each means. It is used to find the
// line and never re-derived to classify it — the Verdict travels with the
// match, so a spelling added here cannot be filed as anything but what this
// table says.
var Sentinels = []struct {
	Line  string
	Means Verdict
}{
	{"APPROVED", Approved},
	{"CHANGES REQUESTED", Rejected},
	{"CHANGES-REQUESTED", Rejected},
	{"CHANGES REQUIRED", Rejected},
	{"REJECTED", Rejected},
}

// Result is a verdict found in a message.
type Result struct {
	Verdict Verdict
	// Sentinel is the normalized line that matched, as it appears in Sentinels.
	Sentinel string
	// Line is the index of the sentinel line in the message, counting from 0.
	Line int
	// Body is every ORIGINAL line after the sentinel line — emphasis, carriage
	// returns and all — with leading blank lines dropped, joined by newlines,
	// with no trailing newline: the text the verb writes, which it terminates
	// with exactly one.
	Body string
}

// Parse scans the message for the first line that IS a sentinel, and returns
// the verdict and the body after it. Normalize is applied to EVERY line in
// one pass, and it works a line at a time, so line numbering survives the
// transformation and a hit indexes straight back into the untouched message.
//
// "Is" is the whole-line rule from the package header, and it is load-bearing
// rather than tidy: it is the only thing standing between a whole-document
// scan and prose that merely talks about approving. The bash spelled it as
// grep -x; here the normalized line must equal a sentinel, never merely begin
// with one.
//
// A reviewer's message is whatever the model emitted, and the scan is over
// bytes: one stray control byte must not change the answer. (The bash needed
// grep -a for that, or grep would have decided the whole thing was binary and
// answered "Binary file (standard input) matches" instead of a line number.)
func Parse(message string) (Result, bool) {
	lines := strings.Split(message, "\n")
	for i, line := range lines {
		n := Normalize(line)
		for _, s := range Sentinels {
			if n == s.Line {
				return Result{Verdict: s.Means, Sentinel: n, Line: i, Body: body(lines[i+1:])}, true
			}
		}
	}
	return Result{}, false
}

// body is the lines after the sentinel with leading blank lines dropped —
// `sed -E '/./,$!d'`: a line is blank only when it has no character at all,
// so a line holding a space or a bare carriage return is kept — joined with
// newlines and trailing newlines trimmed, as `$(...)` trimmed them.
func body(rest []string) string {
	for len(rest) > 0 && rest[0] == "" {
		rest = rest[1:]
	}
	return strings.TrimRight(strings.Join(rest, "\n"), "\n")
}

// Normalize is the per-line pass the sentinel is matched against. Be liberal
// about formatting: **APPROVED**, `APPROVED`, "## APPROVED", "Approved." all
// normalize to APPROVED. In order: delete the four emphasis characters and
// the carriage return; upper-case; collapse each run of whitespace to one
// space; trim both ends; strip trailing punctuation. The order matters and is
// kept — "APPROVED ." loses its period and keeps the space before it, and is
// not a sentinel.
//
// Every class is the C locale's: whitespace is space, tab, newline, vertical
// tab, form feed and carriage return; punctuation is the 32 ASCII marks; and
// upper-casing touches a-z only. The sentinels are ASCII, and a byte-wise
// `tr` never mapped a non-ASCII letter onto an ASCII one — Unicode case
// folding would (ı to I, ſ to S), and a line that was not a sentinel in the
// original pipeline must not become one here.
func Normalize(line string) string {
	b := make([]byte, 0, len(line))
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch c {
		case '#', '*', '`', '_', '\r':
			continue
		}
		if 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		if isSpace(c) {
			if len(b) > 0 && b[len(b)-1] == ' ' {
				continue
			}
			c = ' '
		}
		b = append(b, c)
	}
	b = bytes.TrimLeft(b, " ")
	b = bytes.TrimRight(b, " ")
	end := len(b)
	for end > 0 && isPunct(b[end-1]) {
		end--
	}
	return string(b[:end])
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

// isPunct is [[:punct:]] in the C locale: every printable ASCII byte that is
// neither a letter, a digit nor the space.
func isPunct(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}

// IsBlank is whether s is empty or nothing but C-locale whitespace —
// `[[ -z "${s//[[:space:]]/}" ]]`.
func IsBlank(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			return false
		}
	}
	return true
}

// Opener is the first non-blank line of a message, cut to at most max bytes
// on a character boundary. When a reviewer buries or mangles its verdict,
// that line is what a human debugging the run needs to see.
func Opener(message string, max int) string {
	for _, line := range strings.Split(message, "\n") {
		if IsBlank(line) {
			continue
		}
		if len(line) <= max {
			return line
		}
		cut := max
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		return line[:cut]
	}
	return ""
}

// Final is the text of the final `result` message in an execution log, as
// claude-code-action writes one: a JSON array of messages, or a single
// object. The LAST entry whose `type` is "result" is the one; its `result` is
// the message, and a missing `result` — or one that is not a string — is an
// empty message. An empty log, a log with no result entry, and a log that
// does not parse are all the empty message too: the verb answers "missing"
// for each, and says which on stderr.
//
// This is the jq filter the bash ran, including what jq does with more than
// one JSON value in the file: each value yields one message, and they are
// joined by newlines — so a log written one object per line still routes on
// the last result in it.
func Final(log []byte) string {
	dec := json.NewDecoder(bytes.NewReader(log))
	var messages []string
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		messages = append(messages, lastResult(v))
	}
	return strings.TrimRight(strings.Join(messages, "\n"), "\n")
}

func lastResult(v any) string {
	entries, ok := v.([]any)
	if !ok {
		entries = []any{v}
	}
	var last map[string]any
	for _, e := range entries {
		obj, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := obj["type"].(string); t == "result" {
			last = obj
		}
	}
	if last == nil {
		return ""
	}
	s, _ := last["result"].(string)
	return s
}
