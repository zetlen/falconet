// Package park is the hand-over comment the park verb posts, and the two
// rules the verb holds it to — the parking-label allowlist and the cap — with
// the record of why above each. The verb itself, cmd/falconet/park.go, is the
// flags, the body file, the three GitHub calls, and the exit code.
//
// Nothing here touches the filesystem or the network: the verb hands in the
// preamble, the branch, the body's bytes and the run URL, and gets back the
// comment. That is what lets the cap be held to a property — never half a
// line, never over budget — rather than to the handful of fixtures a suite
// can carry.
//
// The staged pipeline has several places a request can legitimately stop:
// the implementing agent needs more information, validation failed, the
// reviewing agent did not approve, or a step simply died. Every one of them
// comes through park, so "stopped" always means the same three things
// happened — a comment, a label, and the claim released — and never means
// "silently nothing". A request that vanishes into an empty green run is the
// failure mode this repository cares about most.
//
// # The branch pointer
//
// --branch exists because of run 32093607680 (issue #36), which parked an
// issue saying "I prepared this change ... This one needs a person" when the
// only push in the pipeline sat behind an approved review, so the prepared
// change had been destroyed with the runner and the branch had never reached
// the remote. Work is pushed as soon as it exists now (the push verb); this
// is the other half of that fix — the hand-over comment says WHERE it is, in
// a link a person can click, rather than describing work the reader has no
// way to find.
//
// The pointer goes directly under the sentence that mentions it, and before
// any collapsed <details> block a reader might not open. One fixed wording,
// written once, here: "no pull request" is true of every path that comes
// through park, because a run that opened one does not park the issue.
//
// GITHUB_SERVER_URL / GITHUB_REPOSITORY are set in every Actions run. A
// local invocation may lack the first, and then names the branch without a
// link rather than printing a fabricated URL.
//
// # The body
//
// --body is extra detail appended after the preamble. With --body-title it
// is folded into a collapsed <details> block and fenced as code: that is for
// machine output (validation logs, plan errors). Without it the body is
// pasted as it is: that is for a --body that is already prose written for a
// human (needs-info.md, failure-reason.txt), which must not be fenced.
//
// # The cap
//
// The comment is capped at 60000 characters; if --body is longer it is cut
// on a line boundary with an explicit note pointing at --run-url. As
// everywhere else in this pipeline, content is dropped loudly or not at all.
//
// # The parking labels
//
// The two parking labels come from config. This stays an allowlist rather
// than becoming "any label the caller names": every route into this verb is
// one of the two terminal states, and a typo that invented a third would
// park an issue under a label nothing queries and no one is watching —
// which is the silent-disappearance failure this whole verb exists to
// prevent.
package park

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/zetlen/falconet/internal/assemble"
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
			fence := assemble.Fence(detail)
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

// Truncate is `head -c limit | sed '$d'`, then the note: the first limit
// bytes, less whatever follows the last line break among them. The line the
// budget fell inside goes whole — and so does a line that ended exactly at
// the budget, because sed could not tell the two apart and neither could a
// reader handed half of one. What remains is empty or ends in a newline, so
// the note always starts a line of its own.
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

// Label checks --label against the two parking labels from config, and
// names both when it is neither.
func Label(label, needsInfo, human string) error {
	if label == "" {
		return errors.New("--label needs a label")
	}
	if label == needsInfo || label == human {
		return nil
	}
	return fmt.Errorf("--label must be %s or %s (the two parking labels; set labels.needs_info and labels.human to change them)",
		needsInfo, human)
}
