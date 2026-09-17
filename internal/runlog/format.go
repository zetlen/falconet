package runlog

import (
	"fmt"
	"io"
)

// The output formats falconet can show a harness's output in. falconet knows
// formats, not harnesses: a format names the shape of the bytes a program
// prints, and any harness that prints that shape can name it.
const (
	// Text is lines, shown as they arrive.
	Text = "text"
	// ClaudeStreamJSON is one JSON event per line, as the Claude Code CLI
	// prints with `--output-format stream-json --verbose`, rendered as one
	// readable line per thing the agent did.
	ClaudeStreamJSON = "claude-stream-json"
)

// Formats is every format, in the order the docs list them.
var Formats = []string{Text, ClaudeStreamJSON}

// Known reports whether format is one of Formats.
func Known(format string) bool {
	for _, f := range Formats {
		if f == format {
			return true
		}
	}
	return false
}

// Harness is the pair of writers a harness's stdout and stderr go to, and
// what it prints reaches the log through them, line by line, as it arrives.
type Harness struct {
	Stdout, Stderr io.Writer
	closers        []*LineWriter
}

// NewHarness shows a harness's output on w in format. stderr is always
// text: a program's diagnostics are not its event stream. An unknown format
// is shown as text; the caller refuses one before it runs anything.
func NewHarness(w io.Writer, format string) *Harness {
	sink := NewSink(w)
	text := func(line []byte) { sink.Line(string(line)) }
	var stdout *LineWriter
	switch format {
	case ClaudeStreamJSON:
		r := &Claude{}
		stdout = NewRecordWriter(MaxEvent, func(line []byte) {
			for _, out := range r.Render(line) {
				sink.Print(out)
			}
		}, func(n int) {
			sink.Print(fmt.Sprintf("event: a line of %d bytes, longer than falconet reads, not shown", n))
		})
	default:
		stdout = NewLineWriter(MaxLine, text)
	}
	stderr := NewLineWriter(MaxLine, text)
	return &Harness{Stdout: stdout, Stderr: stderr, closers: []*LineWriter{stdout, stderr}}
}

// Close shows whatever each stream left unfinished.
func (h *Harness) Close() {
	for _, c := range h.closers {
		_ = c.Close()
	}
}

// NewText is a writer that shows everything written to it on w as Neutral
// lines, live. Close it to show an unfinished last line.
func NewText(w io.Writer) *LineWriter {
	sink := NewSink(w)
	return NewLineWriter(MaxLine, func(line []byte) { sink.Line(string(line)) })
}
