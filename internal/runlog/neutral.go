// Package runlog is what falconet prints into a run log on another
// program's behalf: the harness's output as `falconet implement` shows it,
// and the check command's output as `falconet check` passes it through.
//
// # Workflow commands
//
// The Actions runner reads every line a step prints for workflow commands.
// A line whose first non-space characters are `::` is one: `::group::` and
// `::endgroup::` fold the log, `::add-mask::` hides text,
// `::stop-commands::` turns the commands off until a token, `::error::`
// annotates the run. The legacy form is `##[` followed by a command name and
// `]`, and the runner looks for it anywhere in a line, not only at its start.
// The runner splits lines on a lone carriage return as well as on a newline.
//
// The harness reads the issue, and the issue is attacker-controlled text; the
// check command runs whatever the agent's change made of the repository's
// tests. Either can print a line that closes the group falconet opened
// around it, so the deciding line that follows is folded away, or that
// masks the check's word, or that stops workflow commands for the rest of
// the step. So no line falconet prints from either can be read as a
// command: every line is split out exactly as the runner will split it,
// every `##[` in it is printed as `##\[`, and a line whose first non-space
// characters are `::` is printed behind a fixed prefix. The rule is applied
// last, to the lines as they are written, so no truncation, rendering or
// embedded line break upstream of it can produce a line it has not seen.
package runlog

import (
	"io"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Prefix is what a line that starts like a `::` command is printed behind.
// It is not whitespace, so the runner's trim stops at it.
const Prefix = "> "

// legacy is the legacy command form's opening, which the runner finds
// anywhere in a line, and broken is what each one is printed as: the same
// characters to a reader, and no `##[` in them to the runner.
const (
	legacy = "##["
	broken = `##\[`
)

// startsLikeCommand reports whether the runner could read line as a `::`
// command: `::` after leading whitespace. The whitespace is a superset of
// what the runner trims, so the answer errs towards a prefix.
func startsLikeCommand(line string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(line, space), "::")
}

// space is Go's notion of white space widened by the Unicode separator
// categories and three characters some runtimes have counted as spaces.
func space(r rune) bool {
	return unicode.IsSpace(r) || unicode.In(r, unicode.Zs, unicode.Zl, unicode.Zp) ||
		r == '\u180e' || r == '\u200b' || r == '\ufeff'
}

// Neutral returns one line with no line break in it, safe to print: every
// `##[` in it broken, and behind Prefix when it starts like a `::` command.
// Replacing `##[` cannot make a new one, because what is put in ends in
// `\[` and a `##[` cannot overlap another.
func Neutral(line string) string {
	line = strings.ReplaceAll(line, legacy, broken)
	if startsLikeCommand(line) {
		return Prefix + line
	}
	return line
}

// Lines returns text split the way the runner splits a log, on "\n", "\r\n"
// and a lone "\r", with each line made Neutral. A final line break ends the
// last line rather than starting an empty one.
func Lines(text string) []string {
	out := split(text)
	for i := range out {
		out[i] = Neutral(out[i])
	}
	return out
}

// split is Lines without Neutral.
func split(text string) []string {
	var out []string
	for text != "" {
		i := strings.IndexAny(text, "\r\n")
		if i < 0 {
			out = append(out, text)
			break
		}
		out = append(out, text[:i])
		if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			i++
		}
		text = text[i+1:]
	}
	return out
}

// Sink is the one place lines reach the log. Every write is whole lines,
// each Neutral, under one lock, so the harness's two streams never
// interleave inside a line.
type Sink struct {
	mu sync.Mutex
	w  io.Writer
}

// NewSink writes to w.
func NewSink(w io.Writer) *Sink { return &Sink{w: w} }

// Print writes text as lines: split, made Neutral, each ended with "\n".
// Nothing is written for empty text.
func (s *Sink) Print(text string) {
	lines := Lines(text)
	if len(lines) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = io.WriteString(s.w, strings.Join(lines, "\n")+"\n")
}

// Line writes one line that a LineWriter split out, blank or not. Should it
// carry a line break after all, it is printed as the lines it holds.
func (s *Sink) Line(line string) {
	if strings.ContainsAny(line, "\r\n") {
		s.Print(line)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = io.WriteString(s.w, Neutral(line)+"\n")
}

// MaxLine is the longest line a LineWriter holds before it hands the bytes
// on as a line of their own. A program that prints megabytes with no line
// break is still shown, in pieces, and never holds memory hostage.
const MaxLine = 64 << 10

// LineWriter is an io.Writer that hands each complete line to a function
// as soon as its line break arrives, which is what makes the log live: a
// line is shown when it is finished, not when the program exits. A line
// break is "\n", "\r\n" or a lone "\r", as the runner reads them. Close
// hands on what is left.
type LineWriter struct {
	mu      sync.Mutex
	buf     []byte
	max     int
	line    func([]byte)
	long    func(n int)
	pending bool // the last byte seen was a "\r" that may be half of "\r\n"
	skipped int  // bytes of an overlong line dropped so far, when long is set
	closed  bool
}

// NewLineWriter calls line with each line, without its line break, holding
// at most max bytes of an unfinished one and handing on a longer line in
// pieces. The slice is only valid during the call.
func NewLineWriter(max int, line func([]byte)) *LineWriter {
	return &LineWriter{max: max, line: line}
}

// NewRecordWriter is a LineWriter for lines that mean nothing in pieces, one
// JSON document each: a line longer than max is dropped whole, and long is
// called once, at its line break, with how many bytes it had.
func NewRecordWriter(max int, line func([]byte), long func(n int)) *LineWriter {
	return &LineWriter{max: max, line: line, long: long}
}

// Write never fails: a log that refuses a line would stop the program
// writing it.
func (lw *LineWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.closed {
		return len(p), nil
	}
	for _, b := range p {
		if lw.pending {
			lw.pending = false
			if b == '\n' {
				continue
			}
		}
		switch b {
		case '\n':
			lw.flush()
		case '\r':
			lw.flush()
			lw.pending = true
		default:
			if lw.skipped > 0 {
				lw.skipped++
				continue
			}
			lw.buf = append(lw.buf, b)
			if len(lw.buf) >= lw.max {
				if lw.long != nil {
					lw.skipped = len(lw.buf)
					lw.buf = lw.buf[:0]
					continue
				}
				lw.cut()
			}
		}
	}
	return len(p), nil
}

// cut hands on a full buffer as a line, ending it on a rune boundary so a
// multi-byte character is not split across two lines.
func (lw *LineWriter) cut() {
	end := len(lw.buf)
	for i := 0; i < utf8.UTFMax && end-i > 0; i++ {
		if utf8.RuneStart(lw.buf[end-i-1]) {
			if !utf8.FullRune(lw.buf[end-i-1:]) {
				end = end - i - 1
			}
			break
		}
	}
	if end == 0 {
		end = len(lw.buf)
	}
	rest := append([]byte(nil), lw.buf[end:]...)
	lw.buf = lw.buf[:end]
	lw.flush()
	lw.buf = append(lw.buf, rest...)
}

func (lw *LineWriter) flush() {
	if lw.skipped > 0 {
		lw.long(lw.skipped)
		lw.skipped = 0
		return
	}
	lw.line(lw.buf)
	lw.buf = lw.buf[:0]
}

// Close hands on an unfinished last line, if there is one, and drops
// anything written afterwards.
func (lw *LineWriter) Close() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.closed {
		return nil
	}
	if len(lw.buf) > 0 || lw.skipped > 0 {
		lw.flush()
	}
	lw.closed = true
	return nil
}
