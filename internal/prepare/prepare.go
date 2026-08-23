// Package prepare is the eligibility gate — the rules that decide whether an
// issue is this pipeline's to work — as pure functions over an issue
// snapshot, the event that woke the run, the config, and the open
// pull-request list; and the two things the ready path derives from the
// snapshot without judgment, the request in markdown and the branch name.
// The verb itself, cmd/falconet/prepare.go, is the flags, the files, git,
// tofu, the GitHub calls and the exit code.
//
// Nothing here touches the filesystem or the network: the verb hands in the
// labels, the state, the body, the event's three facts and the pull list, and
// gets back a decision and a reason. That is what lets the slug, the in-flight
// pattern and the opt-out match be held to properties rather than to the
// handful of fixtures a suite can carry.
//
// # Where this came from, and the one thing it changes
//
// This verb is the only one with no ancestor script. It was inline YAML in the
// origin workflow's first stage, and its eligibility half was not even that —
// it was a job-level `if:` expression, evaluated before checkout. That is why
// it moves: an `if:` runs before the repository exists, so it can never read
// the config file, and gating there would fork eligibility into YAML-in-CI and
// nothing-locally for a project whose whole rule is one code path. The cost is
// runner-seconds on ineligible events. Paid willingly (ADR-0003).
//
// # needs-info is both a blocking label and the way back in
//
// The origin admitted two kinds of run: an issue gains the queue label while
// carrying no parked state, or a human replies on an issue that is already
// parked needs-info. The second is the re-entry path, and issue #25 is why it
// exists — the requester answered the question, and clearing the label by hand
// is something requesters usually cannot do.
//
// So needs-info blocks a first entry and admits a reply, and a flat precedence
// list cannot say both. Two modes:
//
//	re-entry   the event is an issue_comment on an issue (not a PR), the
//	           commenter is not a bot, the queue label is present, and the
//	           needs-info label is present. Or --re-entry says so.
//	entry      everything else.
//
// In entry mode every blocking label blocks. In re-entry mode the needs-info
// label is the ticket in, and the other blocking labels still block.
//
// Re-entry is never INFERRED from the comment thread here — "the last comment
// is not mine" is a judgment, and a verb that reaches a different answer on an
// unchanged issue is not a gate. The caller says so, or the event does.
package prepare

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Mode is which of the two kinds of run this is (see the package header).
type Mode int

const (
	// Entry is a first run on the issue: every blocking label blocks.
	Entry Mode = iota
	// ReEntry is a requester's reply on an issue parked needs-info: that
	// label is the ticket in, and the others still block.
	ReEntry
)

func (m Mode) String() string {
	if m == ReEntry {
		return "re-entry"
	}
	return "entry"
}

// Rules is the part of the config the gate reads.
type Rules struct {
	QueueLabel       string
	OptOutText       string
	NeedsInfo        string
	BranchPrefix     string
	InFlightPrefixes []string
	BlockingLabels   []string
}

// Snapshot is what the gate reads of an issue — from the event payload when
// there is one, from the API otherwise. Labels are names; State is as the
// source spelled it; Body is the issue text, empty for a null body.
type Snapshot struct {
	Labels []string
	State  string
	Body   string
}

// Event is the three facts the gate reads from a webhook payload: the action,
// whether the "issue" is a pull request, and whether the commenter is a bot.
type Event struct {
	Action      string
	PullRequest bool
	Bot         bool
}

// HasLabel is whether labels carries name exactly — a whole label, compared
// as a string, never as a prefix or a pattern.
func HasLabel(labels []string, name string) bool {
	for _, l := range labels {
		if l == name {
			return true
		}
	}
	return false
}

// InferMode reads the re-entry shape off the event, exactly: a human comment
// on an issue that is parked needs-info and still queued. `.issue.pull_request`
// is what distinguishes a PR comment from an issue comment. With no event, or
// any other shape, the mode is Entry; --re-entry is the caller's to add.
func InferMode(ev *Event, labels []string, r Rules) Mode {
	if ev != nil && ev.Action == "created" && !ev.PullRequest && !ev.Bot &&
		HasLabel(labels, r.QueueLabel) && HasLabel(labels, r.NeedsInfo) {
		return ReEntry
	}
	return Entry
}

// NotAWayIn is the short-circuit that runs before any rule: a bot comment, or
// a comment on a pull request, is not a way in.
func NotAWayIn(ev *Event) bool {
	return ev != nil && ev.Action == "created" && (ev.PullRequest || ev.Bot)
}

// Open is rule 0's reading of the state: `gh` says OPEN, a webhook says open,
// and a payload that carries no state at all is read as open, as the bash's
// `case` did.
func Open(state string) bool {
	switch state {
	case "open", "OPEN", "":
		return true
	}
	return false
}

// Blocked is rule 1: the first configured blocking label the issue carries,
// in config order, with needs-info passed over in re-entry mode. An empty
// entry in the list blocks nothing.
//
// Exact-line, and fixed-string: a label named needs-info-later must not block,
// and a configured label may contain regex metacharacters.
func Blocked(labels []string, mode Mode, r Rules) (label string, blocked bool) {
	for _, b := range r.BlockingLabels {
		if b == "" {
			continue
		}
		if mode == ReEntry && b == r.NeedsInfo {
			continue
		}
		if HasLabel(labels, b) {
			return b, true
		}
	}
	return "", false
}

// OptOutPattern is rule 2's pattern: a checked markdown checkbox carrying the
// configured text, matched case-insensitively. The origin's CI form was an
// unanchored substring test, which meant the sentence appearing anywhere —
// quoted from another issue, say — opted the issue out; the human-facing
// skill anchored it to a list item. Anchored is right, and the
// leading-whitespace tolerance is a widening of both, because issue forms
// indent nested checkboxes.
//
// A configured value is data, and it is about to be part of a regex: the text
// is quoted, so every character in it means itself. The whitespace class is
// ASCII's, as grep's was under the C locale CI ran in.
func OptOutPattern(text string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)^[[:space:]]*[-*] \[[xX]\] ` + regexp.QuoteMeta(text))
}

// OptedOut is whether any line of the body is the ticked box. Per line, as
// grep read it: the pattern is anchored at a line's start and nowhere else,
// so a CR before the line break, or text after the sentence, changes nothing.
func OptedOut(body string, pattern *regexp.Regexp) bool {
	for _, line := range strings.Split(body, "\n") {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

// Gate is rules 0 to 3, in the bash's order, and returns the reason the issue
// is ineligible — the line that goes to stderr, because "ineligible" on its
// own is not a diagnostic — or "" when every one of them admits it. Rule 4,
// the open pull requests, is InFlight: it needs the network, and is asked
// only of an issue these four admitted.
func Gate(issue int, s Snapshot, mode Mode, r Rules) string {
	// --- rule 0: the issue is open ------------------------------------------
	//
	// The origin checked this on both admission paths. It is the cheapest
	// and most obviously terminal fact, and the containment step checked it
	// first too. `gh` says OPEN, a webhook says open.
	if !Open(s.State) {
		return fmt.Sprintf("issue #%d is %s", issue, s.State)
	}

	// --- rule 1: no blocking label ------------------------------------------
	//
	// See Blocked: exact, fixed-string, needs-info passed over on re-entry.
	if label, blocked := Blocked(s.Labels, mode, r); blocked {
		return fmt.Sprintf("issue #%d carries the blocking label '%s'", issue, label)
	}

	// --- rule 2: the opt-out box is not ticked ------------------------------
	//
	// See OptOutPattern for why it is anchored and why leading whitespace is
	// tolerated.
	if OptedOut(s.Body, OptOutPattern(r.OptOutText)) {
		return fmt.Sprintf("issue #%d has the opt-out box ticked", issue)
	}

	// --- rule 3: the queue label is present ---------------------------------
	if !HasLabel(s.Labels, r.QueueLabel) {
		return fmt.Sprintf("issue #%d is not labelled '%s'", issue, r.QueueLabel)
	}
	return ""
}

// Pull is the part of an open pull request rule 4 reads: its number and the
// branch it comes from.
type Pull struct {
	Number int
	Head   string
}

// --- rule 4: no open pull request is already carrying it --------------------
//
// In flight means an OPEN PULL REQUEST, never a branch. Since every run pushes
// its branch, a leftover branch is the ordinary state of a retried issue, and
// keying on branches would let one suppress every later run on the issue.
//
// The regex is built from config and passed through the ENVIRONMENT, never
// spliced into the filter text. And the result is captured whole before it is
// inspected: never `gh ... | grep -q`, because grep -q exits at the first match
// and can SIGPIPE gh, which under pipefail turns a FOUND match into a non-zero
// pipeline — the opposite of the answer just computed.
//
// Here the first of those is regexp.QuoteMeta on every prefix, and the second
// is a slice: the verb fetches the whole list, then InFlight walks it.

// InFlightPattern is `^(prefix1|prefix2…)<issue>-`, every prefix quoted so
// that a `.` or a `+` in one means itself. issue.in_flight_prefixes is the
// list; when it is empty, issue.branch_prefix stands in alone, so a consumer
// who configured only the prefix still has its own branches recognised.
func InFlightPattern(issue int, r Rules) *regexp.Regexp {
	var alts []string
	for _, p := range r.InFlightPrefixes {
		if p == "" {
			continue
		}
		alts = append(alts, regexp.QuoteMeta(p))
	}
	if len(alts) == 0 {
		alts = []string{regexp.QuoteMeta(r.BranchPrefix)}
	}
	return regexp.MustCompile("^(" + strings.Join(alts, "|") + ")" + strconv.Itoa(issue) + "-")
}

// InFlight is every open pull request whose head branch is this issue's, as
// `#<number> <branch>`, in the order the list came. An empty answer means the
// issue is not in flight.
func InFlight(issue int, pulls []Pull, r Rules) []string {
	re := InFlightPattern(issue, r)
	var hits []string
	for _, p := range pulls {
		if re.MatchString(p.Head) {
			hits = append(hits, fmt.Sprintf("#%d %s", p.Number, p.Head))
		}
	}
	return hits
}

// InFlightReason is the stderr line for a found match: the matches
// comma-joined, as `tr '\n' ','` joined them.
func InFlightReason(issue int, hits []string) string {
	return fmt.Sprintf("issue #%d already has an open PR: %s — nothing to do", issue, strings.Join(hits, ","))
}

// --- the request, in markdown -----------------------------------------------

// Comment is one comment of the thread, as the request renders it.
type Comment struct {
	Login     string
	CreatedAt string
	Body      string
}

// Request is request.md: the issue, then the comment thread oldest first,
// byte for byte what the bash's `jq -r` wrote — the heading, two newlines, the
// body, two newlines, and then, when there are comments, the thread heading
// and each comment as `### <login> — <created_at>`, a blank line, its body and
// a newline, joined by a newline; and the one newline `-r` adds at the end. A
// comment with no login is "unknown", as `// "unknown"` made it.
//
// Built from the snapshot taken before the acknowledgment was posted, which is
// why the acknowledgment is not in it: the agents should read the requester's
// words, not this pipeline's.
func Request(number int, title, body string, comments []Comment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Issue #%d: %s\n\n%s\n\n", number, title, body)
	if len(comments) > 0 {
		b.WriteString("## Comment thread (oldest first)\n\n")
		for i, c := range comments {
			if i > 0 {
				b.WriteByte('\n')
			}
			login := c.Login
			if login == "" {
				login = "unknown"
			}
			fmt.Fprintf(&b, "### %s — %s\n\n%s\n", login, c.CreatedAt, c.Body)
		}
	}
	b.WriteByte('\n')
	return b.String()
}

// Ack is the acknowledgment, on entry only. Someone who has just answered a
// question is already mid-conversation with this system and does not need to
// be greeted again.
//
// It exists because the next thing this pipeline says can be twenty minutes
// away, and silence after filing a request reads as nothing happened. It is
// scripted so it costs no tokens and cannot be rephrased into something that
// overpromises: a machine is doing the work, and a person still decides.
const Ack = "Thanks — this request has been picked up and is being worked on automatically.\n\n" +
	"You'll hear back here when there's a change ready for review, or if we need more detail from you. Nothing takes effect until a person has reviewed it.\n"

// --- the branch name --------------------------------------------------------

// SlugLimit is how many bytes of the slug survive the cut.
const SlugLimit = 40

// Slug is the branch name's tail, from the issue title and nothing else. The
// branch name is mechanics, not judgment, and should never cost an agent a
// tool call. The title is used for the slug and nothing else — the
// pull-request title comes from the commit subject the agent writes.
//
// Lower-case; every run of anything outside [a-z0-9] becomes one dash;
// leading and trailing dashes go; the first 40 bytes; trailing dashes go
// AGAIN. Both trailing-dash strips are load-bearing: the cut can land mid-run
// and leave one behind, and `issue-42-` is a perfectly valid ref name that
// nothing downstream would have caught. Nothing sluggable at all is
// "request".
//
// Byte-wise, as `tr '[:upper:]' '[:lower:]' | sed` under the C locale were:
// A–Z fold to a–z, and every other byte — a non-ASCII letter included — is a
// separator. Unicode case folding would have let a Kelvin sign become a k;
// the C locale never did, and the result is [a-z0-9-] either way.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for i := 0; i < len(title); i++ {
		c := title[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
			fallthrough
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > SlugLimit {
		s = s[:SlugLimit]
	}
	s = strings.TrimRight(s, "-")
	if s == "" {
		return "request"
	}
	return s
}

// BranchName is `<branch_prefix><issue>-<slug>`.
func BranchName(prefix string, issue int, slug string) string {
	return prefix + strconv.Itoa(issue) + "-" + slug
}
