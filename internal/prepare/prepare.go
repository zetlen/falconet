// Package prepare is the eligibility gate — the rules that decide whether an
// issue is this pipeline's to work — as pure functions over an issue
// snapshot, the event that woke the run, the config, and the open
// pull-request list; and the two things the ready path derives from the
// snapshot without judgment, the request in markdown and the branch name.
// The verb itself, cmd/falconet/prepare.go, is the flags, the files, git,
// the GitHub calls and the exit code.
//
// Nothing here touches the filesystem or the network: the verb hands in the
// labels, the state, the body, the event, the sender's permission and the
// pull list, and gets back a decision and a reason. That is what lets the slug, the in-flight
// pattern and the opt-out match be held to properties rather than to the
// handful of fixtures a suite can carry.
//
// # Why eligibility is code and not a workflow `if:`
//
// A job-level `if:` is evaluated before checkout, so it can never read the
// config file, and gating there would fork eligibility into YAML-in-CI and
// nothing-locally for a project whose whole rule is one code path. The cost
// is runner-seconds on ineligible events.
//
// # Who may start a run
//
// Every event names its sender, the account that labelled, opened, reopened
// or commented. NotAWayIn refuses, from the event alone, an event with no
// sender, a bot's event of any kind, a comment on a pull request, a comment
// on an issue that is not parked needs-info, an action that is none of
// opened, reopened, labeled and created, and a label event that did not add
// the queue label. SenderRule is then asked of an event the rules that need
// no network admitted: the sender holds write on the repository. The issue's
// author, its labels and any association are not read for this: a form puts
// labels on an issue for whoever files it. A requester without write answers
// a question, and a comment from someone with write moves the parked issue
// on. A run with no event has no sender and asks nothing, because the person
// holding the token is the one acting; inside GitHub Actions the verb refuses
// to run without an event.
//
// # needs-info is both a blocking label and the way back in
//
// A run starts from one of two shapes. The first is an entry: the queue label
// added, or the issue opened or reopened while it carries that label. The
// second is a comment on an issue parked needs-info. That is the re-entry
// path: the question has an answer on the thread, or a person with write
// moves the issue on, and clearing the label by hand is something requesters
// usually cannot do.
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
// unchanged issue is not a gate. The caller says so, or the event does. With
// an event, NotAWayIn reads the mode the event has, so the caller's
// --re-entry cannot make a comment a way in.
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
	// ReEntry is a comment on an issue parked needs-info, the answer to its
	// question or a person with write moving it on: that label is the
	// ticket in, and the others still block.
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

// Event is what the gate reads from the event that woke the run, in the
// forge's reader's words: the action, whether the "issue" is a pull
// request, the sender and whether it is a bot, and the labels a labeled
// event added.
type Event struct {
	Action      string
	PullRequest bool
	Bot         bool     // the sender is an automation account
	Sender      string   // the login whose act the event is
	Added       []string // the labels a labeled event added
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

// NotAWayIn is the short-circuit before any rule: the reason an event can
// start no run whatever the issue says, or "" (and "" for no event). mode is
// the one InferMode read off the same event.
func NotAWayIn(ev *Event, mode Mode, r Rules) string {
	if ev == nil {
		return ""
	}
	// The sender rule asks the forge about the account whose act the event
	// is. An event that names none has nobody to ask about, and a run
	// nobody may be asked about does not start.
	if ev.Sender == "" {
		return "the event names no sender"
	}
	// falconet's own labels and comments arrive as bot events on the issue
	// they were written to. Admitting one, of any action, is the pipeline
	// answering itself.
	if ev.Bot {
		return "the event's sender is a bot"
	}
	// A pull request's conversation is where a reviewer talks to a person.
	// The issues endpoint answers for pull requests too, so a comment there
	// arrives looking like a comment on an issue.
	if ev.Action == "created" && ev.PullRequest {
		return "a comment on a pull request"
	}
	// A comment is the answer to a question: it is a way in only on an issue
	// parked needs-info. A comment anywhere else, a maintainer's "can you add
	// detail?" on a queued issue included, starts nothing; a run on an issue
	// that is not parked starts from the queue label, applied again.
	if ev.Action == "created" && mode != ReEntry {
		return fmt.Sprintf("a comment is a way in only on an issue parked '%s'", r.NeedsInfo)
	}
	// The ways in are four actions. Any other one a caller subscribes to, an
	// edit, an unlabel, an assignment, approves nothing: a person with write
	// fixing a typo on a stranger's issue has not asked for a run.
	switch ev.Action {
	case "opened", "reopened", "labeled", "created":
	default:
		return fmt.Sprintf("a %q event is not a way in", ev.Action)
	}
	// A label event approves a run only by adding the queue label. Another
	// label, added by a person with write to an issue a form already
	// queued, is triage and not an approval of the stranger's request.
	if ev.Action == "labeled" && !HasLabel(ev.Added, r.QueueLabel) {
		return fmt.Sprintf("the label event did not add '%s'", r.QueueLabel)
	}
	return ""
}

// --- the sender rule: whoever caused this event can push ---------------------
//
// A run spends the operator's model key and runner minutes, and the agent
// works from text anyone can write. On a public repository anyone can file an
// issue, comment on one, reopen their own, or have an issue form apply the
// queue label for them, so none of those, done by just anyone, may start a
// run: a guard a stranger can run against as often as they can file an issue
// is an oracle (principle 3). A run goes ahead only when the account whose act
// the event is holds "write" or "admin" on the repository, as the forge
// adapter answers over every grant. Every other word is ineligible, silently,
// like every other ineligible event. The reason names the login and the
// threshold and not the answered word, because a public repository's run log
// is public.

// SenderRule is the reason the sender may not start a run, or "" when
// permission is exactly "write" or "admin".
func SenderRule(issue int, login, permission string) string {
	switch permission {
	case "write", "admin":
		return ""
	}
	return fmt.Sprintf("issue #%d: %s does not hold write on the repository", issue, login)
}

// Open is rule 0's reading of the state: `gh` says OPEN, a webhook says open,
// and a payload that carries no state at all is read as open.
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
// configured text, matched case-insensitively. Anchored to a list item, so
// the sentence appearing anywhere else — quoted from another issue, say —
// does not opt the issue out; leading whitespace is tolerated because issue
// forms indent nested checkboxes.
//
// A configured value is data, and it is about to be part of a regex: the text
// is quoted, so every character in it means itself. The whitespace class is
// ASCII's.
func OptOutPattern(text string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)^[[:space:]]*[-*] \[[xX]\] ` + regexp.QuoteMeta(text))
}

// OptedOut is whether any line of the body is the ticked box. Per line: the
// pattern is anchored at a line's start and nowhere else, so a CR before
// the line break, or text after the sentence, changes nothing.
func OptedOut(body string, pattern *regexp.Regexp) bool {
	for _, line := range strings.Split(body, "\n") {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

// Gate is rules 0 to 3, in order, and returns the reason the issue
// is ineligible — the line that goes to stderr, because "ineligible" on its
// own is not a diagnostic — or "" when every one of them admits it. Rule 4,
// the open pull requests, is InFlight: it needs the network, and is asked
// only of an issue these four admitted.
func Gate(issue int, s Snapshot, mode Mode, r Rules) string {
	// --- rule 0: the issue is open ------------------------------------------
	//
	// The cheapest and most obviously terminal fact, and the one the
	// workflow's contain job checks first as well. `gh` says OPEN, a webhook
	// says open.
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
// The pattern is built from config with regexp.QuoteMeta on every prefix, so
// a configured prefix is data and never a pattern. The answer is computed
// over the whole list: the verb fetches it, then InFlight walks it, so no
// early exit can turn a found match into a non-answer. And a list that
// could not be fetched at all is a refusal, not an empty list: a gate that
// says ready on an unknown opens a second pull request on the same issue.

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
// comma-joined.
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

// Request is request.md: the issue, then the comment thread oldest first —
// the heading, two newlines, the body, two newlines, and then, when there
// are comments, the thread heading and each comment as
// `### <login> — <created_at>`, a blank line, its body and a newline, joined
// by a newline; and one newline at the end. A comment with no login is
// "unknown".
//
// Built from the snapshot taken before the acknowledgment was posted, which is
// why the acknowledgment is not in it: the agent should read the requester's
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
// Byte-wise: A–Z fold to a–z, and every other byte — a non-ASCII letter
// included — is a separator. There is no Unicode case folding (a Kelvin sign
// is a separator, not a k), so the result is [a-z0-9-] whatever the title
// held.
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
