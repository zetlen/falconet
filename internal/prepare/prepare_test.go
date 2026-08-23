package prepare

import (
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/quick"
)

const maxCount = 10000

func check(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// The defaults, as the config carries them.
var defaults = Rules{
	QueueLabel:       "infra-request",
	OptOutText:       "Not eligible for AI agents",
	NeedsInfo:        "needs-info",
	BranchPrefix:     "issue-",
	InFlightPrefixes: []string{"issue-", "claude/issue-"},
	BlockingLabels:   []string{"needs-info", "ready-for-human", "do-not-apply", "wontfix"},
}

// --- the gate, rule by rule and in order ---------------------------------------

func TestGateRulesInOrder(t *testing.T) {
	queued := []string{"infra-request"}
	for _, tc := range []struct {
		name string
		snap Snapshot
		mode Mode
		want string
	}{
		{"queued, open, unblocked", Snapshot{Labels: queued, State: "open", Body: "Please add MX."}, Entry, ""},
		{"gh spells it OPEN", Snapshot{Labels: queued, State: "OPEN"}, Entry, ""},
		{"no state at all is open", Snapshot{Labels: queued}, Entry, ""},
		{"closed", Snapshot{Labels: queued, State: "closed"}, Entry, "issue #42 is closed"},
		{"CLOSED", Snapshot{Labels: queued, State: "CLOSED"}, Entry, "issue #42 is CLOSED"},
		{"rule 0 before rule 1: closed and blocked says closed",
			Snapshot{Labels: []string{"infra-request", "wontfix"}, State: "closed"}, Entry, "issue #42 is closed"},
		{"each blocking label, in config order",
			Snapshot{Labels: []string{"wontfix", "do-not-apply"}, State: "open"}, Entry,
			"issue #42 carries the blocking label 'do-not-apply'"},
		{"needs-info blocks a first entry",
			Snapshot{Labels: []string{"infra-request", "needs-info"}, State: "open"}, Entry,
			"issue #42 carries the blocking label 'needs-info'"},
		{"and admits a re-entry",
			Snapshot{Labels: []string{"infra-request", "needs-info"}, State: "open"}, ReEntry, ""},
		{"re-entry admits needs-info only — the other blocking labels still block",
			Snapshot{Labels: []string{"infra-request", "needs-info", "do-not-apply"}, State: "open"}, ReEntry,
			"issue #42 carries the blocking label 'do-not-apply'"},
		{"a label that merely starts like a blocking one does not block",
			Snapshot{Labels: []string{"infra-request", "needs-information"}, State: "open"}, Entry, ""},
		{"rule 1 before rule 2: blocked and opted out says blocked",
			Snapshot{Labels: []string{"infra-request", "wontfix"}, State: "open", Body: "- [x] Not eligible for AI agents"}, Entry,
			"issue #42 carries the blocking label 'wontfix'"},
		{"the opt-out box", Snapshot{Labels: queued, State: "open", Body: "- [x] Not eligible for AI agents"}, Entry,
			"issue #42 has the opt-out box ticked"},
		{"rule 2 before rule 3: opted out and not queued says opted out",
			Snapshot{Labels: []string{"bug"}, State: "open", Body: "- [x] Not eligible for AI agents"}, Entry,
			"issue #42 has the opt-out box ticked"},
		{"not queued", Snapshot{Labels: []string{"bug"}, State: "open"}, Entry, "issue #42 is not labelled 'infra-request'"},
		{"the queue label is matched exactly, not as a prefix",
			Snapshot{Labels: []string{"infra-request-later"}, State: "open"}, Entry, "issue #42 is not labelled 'infra-request'"},
		{"no labels at all", Snapshot{State: "open"}, Entry, "issue #42 is not labelled 'infra-request'"},
		{"a null body is not a crash", Snapshot{Labels: queued, State: "open", Body: ""}, Entry, ""},
	} {
		if got := Gate(42, tc.snap, tc.mode, defaults); got != tc.want {
			t.Errorf("%s: Gate = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBlockedSkipsEmptyEntriesAndHonoursConfigOrder(t *testing.T) {
	r := defaults
	r.BlockingLabels = []string{"", "wontfix", "do-not-apply"}
	label, blocked := Blocked([]string{"do-not-apply", "wontfix"}, Entry, r)
	if !blocked || label != "wontfix" {
		t.Errorf("Blocked = %q, %v; want wontfix first, in config order", label, blocked)
	}
	// An empty configured entry blocks nothing, even an issue with an
	// empty-named label.
	if _, blocked := Blocked([]string{""}, Entry, r); blocked {
		t.Error("an empty blocking entry blocked")
	}
	// A custom needs-info name is the one passed over on re-entry.
	r.BlockingLabels = []string{"awaiting-reply", "wontfix"}
	r.NeedsInfo = "awaiting-reply"
	if _, blocked := Blocked([]string{"awaiting-reply"}, ReEntry, r); blocked {
		t.Error("re-entry did not pass over the configured needs-info label")
	}
	if _, blocked := Blocked([]string{"awaiting-reply"}, Entry, r); !blocked {
		t.Error("entry did not block on the configured needs-info label")
	}
}

// labelText is a label drawn from GitHub's label alphabet, never empty.
type labelText string

func (labelText) Generate(r *rand.Rand, size int) reflect.Value {
	const alphabet = "abcdefghijklmnopqrstuvwxyz-_. :/()[]+*?"
	n := 1 + r.Intn(size+1)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[r.Intn(len(alphabet))])
	}
	return reflect.ValueOf(labelText(b.String()))
}

// A label that merely starts like a blocking one — or ends like one, or
// contains one — does not block; only the label itself does.
func TestALabelThatMerelyStartsLikeABlockingOneDoesNotBlock(t *testing.T) {
	check(t, func(blocking labelText, extra labelText) bool {
		r := defaults
		r.BlockingLabels = []string{string(blocking)}
		r.NeedsInfo = "never-this"
		near := []string{string(blocking) + string(extra), string(extra) + string(blocking), string(extra) + string(blocking) + string(extra)}
		if _, blocked := Blocked(near, Entry, r); blocked {
			return false
		}
		_, blocked := Blocked([]string{string(blocking)}, Entry, r)
		return blocked
	})
}

// --- the opt-out box -------------------------------------------------------------

func TestOptOutShapes(t *testing.T) {
	re := OptOutPattern("Not eligible for AI agents")
	for _, tc := range []struct {
		body string
		want bool
	}{
		{"- [x] Not eligible for AI agents", true},
		{"- [X] not eligible for ai agents", true},
		{"* [x] Not eligible for AI agents", true},
		{"  - [x] Not eligible for AI agents", true},
		{"\t- [x] Not eligible for AI agents", true},
		{"Some words first.\n\n- [x] Not eligible for AI agents\n\nAnd after.", true},
		{"- [x] Not eligible for AI agents\r\n", true},
		{"- [x] Not eligible for AI agents, really", true},
		{"- [ ] Not eligible for AI agents", false},
		{"I do not think this is [x] Not eligible for AI agents, really", false},
		{"> - [x] Not eligible for AI agents", false},
		{"-[x] Not eligible for AI agents", false},
		{"- [x]Not eligible for AI agents", false},
		{"- [x] Not eligible for AI agent", false},
		{"", false},
		{"Please add MX.", false},
	} {
		if got := OptedOut(tc.body, re); got != tc.want {
			t.Errorf("OptedOut(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestOptOutTextIsQuotedNotInterpreted(t *testing.T) {
	re := OptOutPattern("no (AI) please.")
	if !OptedOut("- [x] no (AI) please.", re) {
		t.Error("the configured text did not match itself")
	}
	if OptedOut("- [x] no AI pleaseX", re) {
		t.Error("the parentheses and the dot were read as regex")
	}
}

// blank is a run of ASCII whitespace, possibly empty.
type blank string

func (blank) Generate(r *rand.Rand, size int) reflect.Value {
	const ws = " \t\v\f\r"
	var b strings.Builder
	for i, n := 0, r.Intn(size+1); i < n; i++ {
		b.WriteByte(ws[r.Intn(len(ws))])
	}
	return reflect.ValueOf(blank(b.String()))
}

// phrase is an opt-out text: letters, spaces and regex metacharacters.
type phrase string

func (phrase) Generate(r *rand.Rand, size int) reflect.Value {
	const alphabet = "abcXYZ .()[]+*?|^$"
	n := 1 + r.Intn(size+1)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[r.Intn(len(alphabet))])
	}
	return reflect.ValueOf(phrase(b.String()))
}

func swapCase(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			c -= 'a' - 'A'
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// The opt-out match is a whole-line checkbox and nothing else: any indent,
// either marker, either tick, any case, with anything after it — and not the
// same sentence with one character before the marker, not unticked, and not
// the sentence alone.
func TestOptOutIsAWholeLineCheckboxAndNothingElse(t *testing.T) {
	check(t, func(text phrase, indent blank, star bool, upper bool, tail phrase, before bool) bool {
		re := OptOutPattern(string(text))
		marker, tick := "-", "x"
		if star {
			marker = "*"
		}
		if upper {
			tick = "X"
		}
		line := string(indent) + marker + " [" + tick + "] " + swapCase(string(text)) + string(tail)
		if !OptedOut("prose above\n"+line+"\nprose below", re) {
			return false
		}
		if OptedOut(string(indent)+marker+" [ ] "+string(text), re) {
			return false
		}
		if OptedOut(string(text), re) {
			return false
		}
		// One non-blank character before the marker — a quote, a word —
		// and it is prose, not a checkbox.
		return !OptedOut(">"+line, re) && !OptedOut("a"+line, re)
	})
}

// --- in flight ------------------------------------------------------------------

func TestInFlightMatchesTheSuiteShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pulls []Pull
		want  []string
	}{
		{"this issue's branch", []Pull{{57, "issue-42-add-mx"}}, []string{"#57 issue-42-add-mx"}},
		{"the legacy claude/ prefix", []Pull{{57, "claude/issue-42-20250101"}}, []string{"#57 claude/issue-42-20250101"}},
		{"issue 421 is not issue 42", []Pull{{57, "issue-421-other"}}, nil},
		{"issue 4 is not issue 42", []Pull{{57, "issue-4-other"}}, nil},
		{"anchored: a nested name is not this issue's branch", []Pull{{57, "feature/issue-42-x"}}, nil},
		{"no dash after the number", []Pull{{57, "issue-42"}}, nil},
		{"a collision-suffixed branch still counts", []Pull{{58, "issue-42-add-mx-99"}}, []string{"#58 issue-42-add-mx-99"}},
		{"two matches, in list order", []Pull{{57, "issue-42-a"}, {3, "other"}, {58, "claude/issue-42-b"}},
			[]string{"#57 issue-42-a", "#58 claude/issue-42-b"}},
		{"an empty list", nil, nil},
	} {
		if got := InFlight(42, tc.pulls, defaults); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: InFlight = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := InFlightReason(42, []string{"#57 issue-42-a", "#58 claude/issue-42-b"}); got != "issue #42 already has an open PR: #57 issue-42-a,#58 claude/issue-42-b — nothing to do" {
		t.Errorf("InFlightReason = %q", got)
	}
}

func TestInFlightPatternFallsBackToTheBranchPrefix(t *testing.T) {
	r := defaults
	r.InFlightPrefixes = nil
	r.BranchPrefix = "req-"
	if got := InFlightPattern(42, r).String(); got != `^(req-)42-` {
		t.Errorf("pattern = %q", got)
	}
	r.InFlightPrefixes = []string{"", ""}
	if got := InFlightPattern(42, r).String(); got != `^(req-)42-` {
		t.Errorf("empty entries should not count: pattern = %q", got)
	}
	r.InFlightPrefixes = []string{"a.b/", "c+"}
	if got := InFlightPattern(7, r).String(); got != `^(a\.b/|c\+)7-` {
		t.Errorf("every prefix is escaped: pattern = %q", got)
	}
}

// prefixText is a branch prefix: letters, slashes, and regex metacharacters.
type prefixText string

func (prefixText) Generate(r *rand.Rand, size int) reflect.Value {
	const alphabet = "abc-/.+*?()[]|^$\\"
	n := 1 + r.Intn(size+1)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[r.Intn(len(alphabet))])
	}
	return reflect.ValueOf(prefixText(b.String()))
}

// The in-flight regex is anchored and escapes every prefix: a head that is
// exactly prefix+N+"-"+anything matches; the same thing with a character
// before it does not; a neighbouring issue number does not; and a prefix's
// metacharacters mean themselves, so a head that would match the prefix read
// as a regex does not match.
func TestInFlightPatternIsAnchoredAndEscapesEveryPrefix(t *testing.T) {
	check(t, func(p1, p2 prefixText, n uint16, tail labelText, digit uint8) bool {
		issue := int(n) + 1
		r := defaults
		r.InFlightPrefixes = []string{string(p1), string(p2)}
		re := InFlightPattern(issue, r)
		num := strconv.Itoa(issue)
		for _, p := range []string{string(p1), string(p2)} {
			if !re.MatchString(p + num + "-" + string(tail)) {
				return false
			}
			if re.MatchString("x" + p + num + "-" + string(tail)) {
				return false
			}
			if re.MatchString(p + num + strconv.Itoa(int(digit%10)) + "-" + string(tail)) {
				return false
			}
			if re.MatchString(p + num) {
				return false
			}
			// The quoted prefix, as a regex, must match exactly the prefix.
			if quoted := regexp.MustCompile("^" + regexp.QuoteMeta(p) + "$"); !quoted.MatchString(p) {
				return false
			}
		}
		// A prefix carrying a metacharacter is not read as that
		// metacharacter: "a." must not admit "ab".
		if strings.Contains(string(p1), ".") && !strings.Contains(string(p1), "b") {
			loose := strings.ReplaceAll(string(p1), ".", "b")
			if re.MatchString(loose + num + "-" + string(tail)) {
				return false
			}
		}
		return true
	})
}

// --- the slug ---------------------------------------------------------------------

func TestSlugShapes(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"Add MX records for papernapkin.tech", "add-mx-records-for-papernapkin-tech"},
		{"An extremely long issue title that goes well past the limit", "an-extremely-long-issue-title-that-goes"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa - trailing", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-trailin"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa - trailing", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"!!! ???", "request"},
		{"", "request"},
		{"Zoë's café", "zo-s-caf"},
		{"--leading and trailing--", "leading-and-trailing"},
		{"UPPER lower 123", "upper-lower-123"},
		{"a\nb", "a-b"},
		{"KELVIN K", "kelvin"},
		{"日本語", "request"},
	} {
		if got := Slug(tc.title); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
	if got := BranchName("issue-", 42, "add-mx"); got != "issue-42-add-mx" {
		t.Errorf("BranchName = %q", got)
	}
}

var slugShape = regexp.MustCompile(`^[a-z0-9-]*$`)

// title is random bytes leaning on letters, punctuation and non-ASCII.
type title string

func (title) Generate(r *rand.Rand, size int) reflect.Value {
	const alphabet = "abcXYZ019 -_.!?éÖ日\xff\x00\t"
	var b strings.Builder
	for i, n := 0, r.Intn(size*3+1); i < n; i++ {
		b.WriteString(string([]rune(alphabet)[r.Intn(len([]rune(alphabet)))]))
	}
	return reflect.ValueOf(title(b.String()))
}

// The slug is always [a-z0-9-], never starts or ends in a dash, never exceeds
// 40 bytes, and is never empty.
func TestSlugIsAlwaysARefTail(t *testing.T) {
	check(t, func(tt title) bool {
		s := Slug(string(tt))
		return slugShape.MatchString(s) && s != "" && len(s) <= SlugLimit &&
			!strings.HasPrefix(s, "-") && !strings.HasSuffix(s, "-") &&
			!strings.Contains(s, "--")
	})
}

// The slug is held to the bash pipeline itself — tr, sed, cut, sed — under
// the C locale, on whatever tr and sed the machine has. Titles carry no
// newline here: sed worked per line, and a two-line title handed git a ref it
// refused, which the verb's own differential records rather than this test.
func TestSlugAgreesWithTrSedCut(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to differ against")
	}
	r := rand.New(rand.NewSource(7))
	const alphabet = "abcXYZ019 -_.!?éÖ日\xff\t"
	runes := []rune(alphabet)
	var titles []string
	for i := 0; i < 300; i++ {
		var b strings.Builder
		for j, n := 0, r.Intn(60); j < n; j++ {
			b.WriteString(string(runes[r.Intn(len(runes))]))
		}
		titles = append(titles, b.String())
	}
	titles = append(titles, "Add MX records for papernapkin.tech", "!!! ???", "Zoë's café",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa - trailing", "")
	script := `while IFS= read -r t; do
  s="$(printf '%s' "$t" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' | cut -c1-40 | sed -E 's/-+$//')"
  [ -n "$s" ] || s=request
  printf '%s\n' "$s"
done`
	cmd := exec.Command(sh, "-c", script)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.Stdin = strings.NewReader(strings.Join(titles, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	want := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(want) != len(titles) {
		t.Fatalf("the pipeline changed the line count: %d in, %d out", len(titles), len(want))
	}
	for i, tt := range titles {
		if got := Slug(tt); got != want[i] {
			t.Errorf("Slug(%q) = %q, tr|sed|cut|sed says %q", tt, got, want[i])
		}
	}
}

// --- the event --------------------------------------------------------------------

func TestInferModeAndNotAWayIn(t *testing.T) {
	parked := []string{"infra-request", "needs-info"}
	for _, tc := range []struct {
		name    string
		ev      *Event
		labels  []string
		mode    Mode
		notAWay bool
	}{
		{"no event", nil, parked, Entry, false},
		{"a human's comment on a parked, queued issue", &Event{Action: "created"}, parked, ReEntry, false},
		{"a bot's comment", &Event{Action: "created", Bot: true}, parked, Entry, true},
		{"a comment on a pull request", &Event{Action: "created", PullRequest: true}, parked, Entry, true},
		{"a comment on an issue not parked", &Event{Action: "created"}, []string{"infra-request"}, Entry, false},
		{"a comment on an issue not queued", &Event{Action: "created"}, []string{"needs-info"}, Entry, false},
		{"labeled", &Event{Action: "labeled"}, parked, Entry, false},
		{"opened", &Event{Action: "opened"}, parked, Entry, false},
		{"a bot label event is not a comment, so not the short-circuit", &Event{Action: "labeled", Bot: true}, parked, Entry, false},
	} {
		if got := InferMode(tc.ev, tc.labels, defaults); got != tc.mode {
			t.Errorf("%s: InferMode = %v, want %v", tc.name, got, tc.mode)
		}
		if got := NotAWayIn(tc.ev); got != tc.notAWay {
			t.Errorf("%s: NotAWayIn = %v, want %v", tc.name, got, tc.notAWay)
		}
	}
	if Entry.String() != "entry" || ReEntry.String() != "re-entry" {
		t.Error("the modes do not name themselves")
	}
}

// --- the request ------------------------------------------------------------------

func TestRequestShapes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		title    string
		body     string
		comments []Comment
		want     string
	}{
		{"no comments: the issue, and three newlines", "T", "b", nil, "# Issue #42: T\n\nb\n\n\n"},
		{"a null body is empty", "T", "", nil, "# Issue #42: T\n\n\n\n\n"},
		{"one comment", "T", "b", []Comment{{"zetlen", "2026-08-01T00:00:00Z", "bump"}},
			"# Issue #42: T\n\nb\n\n## Comment thread (oldest first)\n\n### zetlen — 2026-08-01T00:00:00Z\n\nbump\n\n"},
		{"two comments, joined by a blank line", "T", "b",
			[]Comment{{"a", "d1", "one"}, {"b", "d2", "two"}},
			"# Issue #42: T\n\nb\n\n## Comment thread (oldest first)\n\n### a — d1\n\none\n\n### b — d2\n\ntwo\n\n"},
		{"no login is unknown", "T", "b", []Comment{{"", "d", "x"}},
			"# Issue #42: T\n\nb\n\n## Comment thread (oldest first)\n\n### unknown — d\n\nx\n\n"},
		{"shell-shaped text travels verbatim", "T", "Add `$(touch /tmp/pwned)` please", nil,
			"# Issue #42: T\n\nAdd `$(touch /tmp/pwned)` please\n\n\n"},
	} {
		if got := Request(42, tc.title, tc.body, tc.comments); got != tc.want {
			t.Errorf("%s: Request = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Request is held to jq itself: the bash's filter over a gh-shaped snapshot,
// on random titles, bodies and threads, on whatever jq the machine has.
func TestRequestAgreesWithJq(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("no jq to differ against")
	}
	const filter = `
  "# Issue #\(.number): \(.title)\n\n\(.body // "")\n\n"
  + (if (.comments | length) > 0
     then "## Comment thread (oldest first)\n\n"
          + ([.comments[] | "### \(.author.login // "unknown") — \(.createdAt)\n\n\(.body // "")\n"] | join("\n"))
     else "" end)
`
	r := rand.New(rand.NewSource(3))
	runes := []rune("abc XYZ\n\t`$()é日#-")
	random := func() string {
		var b strings.Builder
		for i, n := 0, r.Intn(30); i < n; i++ {
			b.WriteRune(runes[r.Intn(len(runes))])
		}
		return b.String()
	}
	for i := 0; i < 200; i++ {
		number := r.Intn(1000) + 1
		title, body := random(), random()
		var comments []Comment
		var thread []map[string]any
		for j, n := 0, r.Intn(4); j < n; j++ {
			c := Comment{random(), random(), random()}
			comments = append(comments, c)
			entry := map[string]any{"author": map[string]any{"login": c.Login}, "createdAt": c.CreatedAt, "body": c.Body}
			if c.Login == "" {
				entry["author"] = map[string]any{"login": nil}
			}
			if r.Intn(5) == 0 {
				entry["body"] = nil
				comments[j].Body = ""
			}
			thread = append(thread, entry)
		}
		doc := map[string]any{"number": number, "title": title, "body": body, "comments": thread}
		if body == "" && r.Intn(2) == 0 {
			doc["body"] = nil
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(jq, "-r", filter)
		cmd.Stdin = strings.NewReader(string(raw))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("jq: %v", err)
		}
		if got := Request(number, title, body, comments); got != string(out) {
			t.Errorf("Request(%d, %q, %q, %v) = %q, jq says %q", number, title, body, comments, got, string(out))
		}
	}
}

func TestAckIsTwoParagraphsThatPromiseOnlyWhatIsTrue(t *testing.T) {
	if !strings.HasPrefix(Ack, "Thanks — this request has been picked up") ||
		!strings.Contains(Ack, "\n\n") ||
		!strings.HasSuffix(Ack, "Nothing takes effect until a person has reviewed it.\n") {
		t.Errorf("Ack = %q", Ack)
	}
}
