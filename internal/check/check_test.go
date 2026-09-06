package check

import (
	"bytes"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

// output is random input shaped like a command's: lines of random length,
// some empty, one time in four with no trailing newline — written in random
// pieces, because a subprocess's stdout arrives in whatever chunks the pipe
// hands over and the tail must not care.
type output struct {
	whole  []byte
	pieces [][]byte
}

func (output) Generate(r *rand.Rand, size int) reflect.Value {
	var b []byte
	for i, lines := 0, r.Intn(size+1); i < lines; i++ {
		for j, n := 0, r.Intn(60); j < n; j++ {
			b = append(b, byte('a'+r.Intn(26)))
		}
		b = append(b, '\n')
	}
	if len(b) > 0 && r.Intn(4) == 0 {
		b = b[:len(b)-1]
	}
	var pieces [][]byte
	for rest := b; len(rest) > 0; {
		n := 1 + r.Intn(len(rest))
		pieces = append(pieces, rest[:n])
		rest = rest[n:]
	}
	return reflect.ValueOf(output{whole: b, pieces: pieces})
}

func tailOf(o output, limit int) *Tail {
	t := &Tail{Limit: limit}
	for _, p := range o.pieces {
		if _, err := t.Write(p); err != nil {
			panic(err)
		}
	}
	return t
}

func property(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: 3000}); err != nil {
		t.Error(err)
	}
}

// --- the tail's properties -------------------------------------------------

func TestTheTailNeverExceedsItsBudget(t *testing.T) {
	property(t, func(o output, limit uint8) bool {
		l := int(limit)%200 + 1
		got := tailOf(o, l).Bytes()
		return len(got) <= l
	})
}

func TestTheTailCountsEveryByte(t *testing.T) {
	property(t, func(o output, limit uint8) bool {
		return tailOf(o, int(limit)%200+1).Total == len(o.whole)
	})
}

func TestTheTailIsTheEndOfTheOutput(t *testing.T) {
	property(t, func(o output, limit uint8) bool {
		got := tailOf(o, int(limit)%200+1).Bytes()
		return bytes.HasSuffix(o.whole, got)
	})
}

// Whatever is kept starts where a line starts — the beginning of the output
// when nothing was dropped, the byte after a newline otherwise — so the
// agent never reads half a line and mistakes it for a whole one.
func TestTheTailStartsOnALineBoundaryWhenAnythingWasDropped(t *testing.T) {
	property(t, func(o output, limit uint8) bool {
		tl := tailOf(o, int(limit)%200+1)
		got := tl.Bytes()
		if tl.Dropped() == 0 {
			return bytes.Equal(got, o.whole)
		}
		if len(got) == 0 {
			// No whole line fit: nothing kept is nothing mid-line.
			return true
		}
		start := len(o.whole) - len(got)
		return start > 0 && o.whole[start-1] == '\n'
	})
}

func TestDroppedPlusKeptIsTotal(t *testing.T) {
	property(t, func(o output, limit uint8) bool {
		tl := tailOf(o, int(limit)%200+1)
		return tl.Dropped()+len(tl.Bytes()) == tl.Total
	})
}

// A write larger than the whole budget replaces everything: the kept bytes
// are the end of THAT write, not of whatever came before it.
func TestAWriteLargerThanTheBudgetReplacesEverything(t *testing.T) {
	tl := &Tail{Limit: 8}
	_, _ = tl.Write([]byte("first\n"))
	_, _ = tl.Write([]byte("xx\nabcdef\n"))
	if got, want := string(tl.Bytes()), "abcdef\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if tl.Total != 6+10 {
		t.Errorf("Total = %d, want 16", tl.Total)
	}
}

// A cut that lands exactly on a line boundary keeps the whole line after
// it: the budget is not spent dropping a line that was already whole.
func TestACutOnABoundaryKeepsTheWholeBudget(t *testing.T) {
	tl := &Tail{Limit: 10}
	_, _ = tl.Write([]byte("aaaa\nbbbb\ncccc\n"))
	if got, want := string(tl.Bytes()), "bbbb\ncccc\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestOneLineLongerThanTheBudgetKeepsNothingWhole(t *testing.T) {
	tl := &Tail{Limit: 4}
	_, _ = tl.Write([]byte("abcdefgh"))
	if got := tl.Bytes(); len(got) != 0 {
		t.Errorf("kept %q, want nothing: no whole line fits", got)
	}
	if tl.Dropped() != 8 {
		t.Errorf("Dropped = %d, want 8", tl.Dropped())
	}
}

func TestTheZeroValueKeepsTailLimit(t *testing.T) {
	tl := &Tail{}
	big := bytes.Repeat([]byte("x\n"), TailLimit)
	_, _ = tl.Write(big)
	if got := len(tl.Bytes()); got != TailLimit {
		t.Errorf("kept %d bytes, want %d", got, TailLimit)
	}
}

// --- the report --------------------------------------------------------------

func TestReportNamesTheCommandAndHowItEnded(t *testing.T) {
	tl := &Tail{}
	_, _ = tl.Write([]byte("FAIL: TestX\n"))
	got := string(Report([]string{"go", "test", "./..."}, "exit status 1", tl))
	for _, want := range []string{
		"command: go test ./...\n",
		"ended:   exit status 1\n",
		"Its output, whole:\n\nFAIL: TestX\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "not\nhere") {
		t.Errorf("report claims a cut when nothing was dropped:\n%s", got)
	}
}

func TestReportSaysWhatWasCutAndWhereTheRestIs(t *testing.T) {
	tl := &Tail{Limit: 10}
	_, _ = tl.Write([]byte("aaaa\nbbbb\ncccc\n"))
	got := string(Report([]string{"make", "test"}, "exit status 2", tl))
	if !strings.Contains(got, "the first 5 of 15 bytes are not\nhere; the run log has all of it") {
		t.Errorf("report does not account for the cut:\n%s", got)
	}
	if !strings.HasSuffix(got, "bbbb\ncccc\n") {
		t.Errorf("report does not end with the kept tail:\n%s", got)
	}
}

func TestReportOfASilentCommandSaysSo(t *testing.T) {
	got := string(Report([]string{"true"}, "exit status 1", &Tail{}))
	if !strings.Contains(got, "It printed nothing.\n") {
		t.Errorf("report:\n%s", got)
	}
}

// The report always ends in a newline, so the fence the pause verb puts
// after it starts a line of its own.
func TestReportEndsInANewline(t *testing.T) {
	property(t, func(o output, limit uint8) bool {
		tl := tailOf(o, int(limit)%200+1)
		r := Report([]string{"x"}, "exit status 1", tl)
		return bytes.HasSuffix(r, []byte("\n"))
	})
}
