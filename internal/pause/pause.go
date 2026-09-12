// Package pause is the hand-over comment the pause verb posts, and the two
// rules the verb holds it to — the pause-label allowlist and the cap — with
// the reason above each. The verb itself, cmd/falconet/pause.go, is the
// flags, the body file, the three GitHub calls, and the exit code.
//
// Nothing here touches the filesystem or the network: the verb hands in the
// preamble, the branch, the body's bytes and the run URL, and gets back the
// comment. That is what lets the cap be held to a property — never half a
// line, never over budget — rather than to the handful of fixtures a suite
// can carry.
//
// The staged pipeline has several places a request can legitimately stop:
// the agent needs more information, the repository's own check still fails
// at the attempt cap, no change could be prepared, or a step simply died.
// Every one of them comes through pause, so "stopped" always means the same
// three things happened — a label, a comment, and the claim released — and
// never means "silently nothing". A request that vanishes into an empty
// green run is the failure mode this repository cares about most.
//
// # The branch pointer
//
// --branch is how the hand-over comment says WHERE the work is, in a link a
// person can click. Work is pushed as soon as it exists (the push verb), and
// a comment that describes work the reader has no way to find promises
// nothing; a comment that says "I prepared this change" must point at it.
//
// The pointer goes directly under the sentence that mentions it, and before
// any collapsed <details> block a reader might not open. One fixed wording,
// written once, here: "no pull request" is true of every path that comes
// through pause, because a run that opened one does not pause the issue.
//
// GITHUB_SERVER_URL / GITHUB_REPOSITORY are set in every Actions run. A
// local invocation may lack the first, and then names the branch without a
// link rather than printing a fabricated URL.
//
// # The body
//
// --body is extra detail appended after the preamble. With --body-title it
// is folded into a collapsed <details> block and fenced as code: that is for
// machine output (a failing check, a stack trace). Without it the body is
// pasted as it is: that is for a --body that is already prose written for a
// human (needs-info.md, failure-reason.txt), which must not be fenced.
//
// # The cap
//
// The comment is capped at 60000 characters; if --body is longer it is cut
// on a line boundary with an explicit note pointing at --run-url. As
// everywhere else in this pipeline, content is dropped loudly or not at all.
//
// # The pause labels
//
// The two pause labels come from config. This stays an allowlist rather
// than becoming "any label the caller names": every route into this verb is
// one of the two terminal states, and a typo that invented a third would
// pause an issue under a label nothing queries and no one is watching —
// which is the silent-disappearance failure this whole verb exists to
// prevent.
package pause

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// CommentLimit is the cap on --body, in bytes. A GitHub comment holds 65,536
// characters; the body is cut at 60,000 so that the preamble, the branch
// pointer, the run link and the cut note itself always fit beside it.
const CommentLimit = 60000

// Input is everything the comment is built from.
type Input struct {
	// Preamble is the plain-language sentence the requester reads first.
	Preamble string
	// Branch is the pushed working branch, or empty where no commit exists.
	Branch string
	// ServerURL and Repository build the branch link; with either empty the
	// branch is named and not linked.
	ServerURL  string
	Repository string
	// Body is --body's content; empty means no body.
	Body []byte
	// BodyTitle, when set, folds Body into a collapsed, fenced block.
	BodyTitle string
	// RunURL is cited at the end, and by the cut note.
	RunURL string
	// LabelUnapplied, when set, names a pause label that could not be
	// applied. The comment then leads with a warning: the issue is not
	// actually parked and may be picked up again, so a repository admin
	// should look. See cmd/falconet/pause.go and issue #31.
	LabelUnapplied string
	// Limit overrides CommentLimit; zero means the default.
	Limit int
}

// Comment is the hand-over comment, in the order a reader needs it: the
// preamble, the pointer to the work, the detail, the run log.
func Comment(in Input) []byte {
	limit := in.Limit
	if limit <= 0 {
		limit = CommentLimit
	}
	var b bytes.Buffer
	b.WriteString(in.Preamble)
	b.WriteByte('\n')

	// The label is applied before this comment is built, so a label that
	// would not apply is reported here rather than silently claimed. Without
	// the label the issue is not in a terminal state and may be worked again,
	// which the reader needs to know, and a person needs to fix.
	if in.LabelUnapplied != "" {
		b.WriteByte('\n')
		fmt.Fprintf(&b, "> **Note:** I could not apply the `%s` label, so this issue is **not** parked and may be picked up again automatically. Please contact a repository admin.\n", in.LabelUnapplied)
	}

	if in.Branch != "" {
		b.WriteByte('\n')
		fmt.Fprintf(&b, "The commits are pushed to the branch `%s`. No pull request is open for it.\n", in.Branch)
		if in.ServerURL != "" && in.Repository != "" {
			b.WriteByte('\n')
			fmt.Fprintf(&b, "%s/%s/tree/%s\n", in.ServerURL, in.Repository, in.Branch)
		}
	}

	if len(in.Body) > 0 {
		detail := in.Body
		if len(detail) > limit {
			detail = Truncate(detail, limit, Where(in.RunURL))
		}
		b.WriteByte('\n')
		if in.BodyTitle != "" {
			// A closing fence only closes from the start of a line. A body
			// whose last line had no newline would carry the fence on that
			// line, the block would never close, and the run link and the
			// </details> after it would render inside it.
			if !bytes.HasSuffix(detail, []byte("\n")) {
				detail = append(append([]byte{}, detail...), '\n')
			}
			// Longer than any backtick run the body carries, as the
			// pull-request body's is: a validation log that happened to
			// contain ``` must not break out of its own block.
			fence := Fence(detail)
			fmt.Fprintf(&b, "<details><summary>%s</summary>\n\n%s\n", in.BodyTitle, fence)
			b.Write(detail)
			fmt.Fprintf(&b, "%s\n\n</details>\n", fence)
		} else {
			b.Write(detail)
		}
	}

	if in.RunURL != "" {
		fmt.Fprintf(&b, "\n(Run log: %s)\n", in.RunURL)
	}
	return b.Bytes()
}

// Where is what the cut note points at: the run URL, or the place a reader
// finds the run without one.
func Where(runURL string) string {
	if runURL != "" {
		return runURL
	}
	return "the Actions tab of this repository"
}

// Truncate is the first limit bytes, less whatever follows the last line
// break among them, then the note. The line the budget fell inside goes
// whole — and so does a line that ended exactly at the budget, so that one
// rule covers both and a reader is never handed half a line. What remains
// is empty or ends in a newline, so the note always starts a line of its
// own.
func Truncate(body []byte, limit int, where string) []byte {
	if limit < 0 {
		limit = 0
	}
	if limit > len(body) {
		limit = len(body)
	}
	head := bytes.TrimSuffix(body[:limit], []byte("\n"))
	if i := bytes.LastIndexByte(head, '\n'); i >= 0 {
		head = head[:i+1]
	} else {
		head = nil
	}
	out := make([]byte, 0, len(head)+len(where)+64)
	out = append(out, head...)
	out = append(out, "\n[ ... cut here: the rest is in the run log,\n      "...)
	out = append(out, where...)
	out = append(out, " ]\n"...)
	return out
}

// Label checks --label against the two pause labels from config, and
// names both when it is neither.
func Label(label, needsInfo, human string) error {
	if label == "" {
		return errors.New("--label needs a label")
	}
	if label == needsInfo || label == human {
		return nil
	}
	return fmt.Errorf("--label must be %s or %s (the two pause labels; set labels.needs_info and labels.human to change them)",
		needsInfo, human)
}

// Fence is a fence one backtick longer than the longest run of backticks in
// b, and never shorter than the three that markdown needs.
func Fence(b []byte) string {
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
	n := 3
	if longest >= 3 {
		n = longest + 1
	}
	return strings.Repeat("`", n)
}
