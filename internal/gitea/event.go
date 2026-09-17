package gitea

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zetlen/falconet/internal/prepare"
)

// eventPayload is the part of a Gitea `issues` or `issue_comment` payload the
// gate reads, every field optional. Gitea's Actions notifier writes an
// api.IssuePayload (gitea services/actions/notifier.go:265), whose `changes`
// carries the labels a label change named as added and removed, or an
// api.IssueCommentPayload (notifier.go:329), which carries `is_pull`.
type eventPayload struct {
	Action  string `json:"action"`
	Changes struct {
		AddedLabels   []label `json:"added_labels"`
		RemovedLabels []label `json:"removed_labels"`
	} `json:"changes"`
	IsPull bool `json:"is_pull"`
	Issue  struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Body        *string         `json:"body"`
		State       *string         `json:"state"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// DecodeEvent reads the gate's inputs out of a Gitea `issues` or
// `issue_comment` payload, with bot the login of the user whose token the
// verbs hold. It reads no file: the caller has already checked that raw is a
// JSON object. A payload whose fields have the wrong types is an error.
func DecodeEvent(raw []byte, bot string) (prepare.Event, prepare.Snapshot, error) {
	var ev prepare.Event
	var snap prepare.Snapshot
	// Gitea's users carry no type, so the bot's login is the only thing that
	// tells falconet's own comments and labels from a person's. A reader with
	// no login to compare would call every event a person's, falconet's own
	// needs-info question included, and the pipeline would answer itself in
	// a loop that spends the model key.
	if !isLogin(bot) {
		return ev, snap, fmt.Errorf("FALCONET_BOT_LOGIN %q is not a login", bot)
	}
	var p eventPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return ev, snap, err
	}
	for _, l := range p.Issue.Labels {
		if l.Name != "" {
			snap.Labels = append(snap.Labels, l.Name)
		}
	}
	if p.Issue.Body != nil {
		snap.Body = *p.Issue.Body
	}
	snap.State = "open"
	if p.Issue.State != nil {
		snap.State = *p.Issue.State
	}

	// Gitea sends one action, label_updated, for any change to an issue's
	// labels, with the change's added and removed labels, and matches a
	// workflow's `labeled` and `unlabeled` types by them (gitea
	// modules/actions/workflows.go:405). The lists are what the request
	// named, not what changed: the replace route names the whole new set as
	// added and the whole old set as removed (gitea services/issue/label.go:85),
	// and the add routes name every label requested, one the issue already
	// carries included (label.go:33 and :43). A person with write who
	// replaces an already queued issue's labels to add another is triaging,
	// and the gate must not read the queue label they kept as added. So a
	// label named on both sides was on the issue before and after, and is
	// neither: Added is what the change named as added and not as removed. A
	// change that added a label is labeled, even when it also removed one; a
	// change that only removed labels, or cleared them all, is unlabeled.
	//
	// An add request that re-sends a label the issue already carries reads
	// the same as one that adds it, and nothing in the payload, whose issue
	// labels are the set after the change, tells the two apart. That request
	// named the queue label itself, and its sender still has to hold write.
	//
	// A label_updated that names no change, as a webhook delivery's payload
	// does (gitea services/webhook/notifier.go:591), keeps its own action and
	// is no way in, because nothing in it says the queue label was added.
	ev.Action = p.Action
	switch p.Action {
	case "label_updated":
		added, removed := names(p.Changes.AddedLabels), names(p.Changes.RemovedLabels)
		ev.Added = minus(added, removed)
		switch {
		case len(ev.Added) > 0:
			ev.Action = "labeled"
		case len(minus(removed, added)) > 0:
			ev.Action = "unlabeled"
		}
	case "label_cleared":
		ev.Action = "unlabeled"
	}

	// Gitea's issue_comment trigger matches comments on pull requests too
	// (gitea modules/actions/github.go:121), and a comment payload says so in
	// `is_pull`. `issue.pull_request` is null on an issue and an object on a
	// pull request.
	pr := bytes.TrimSpace(p.Issue.PullRequest)
	ev.PullRequest = p.IsPull || (len(pr) > 0 && string(pr) != "null")

	// The sender is the account that labelled, reopened or commented. The
	// bot is the account whose token falconet holds, compared without case
	// as Gitea compares logins; the client checks that login against its
	// token before its first request.
	ev.Sender = p.Sender.Login
	ev.Bot = strings.EqualFold(p.Sender.Login, bot)
	return ev, snap, nil
}

// names is the labels' names, without the empty ones.
func names(labels []label) []string {
	var out []string
	for _, l := range labels {
		if l.Name != "" {
			out = append(out, l.Name)
		}
	}
	return out
}

// minus is a without the names b holds, in a's order.
func minus(a, b []string) []string {
	var out []string
	for _, n := range a {
		if !prepare.HasLabel(b, n) {
			out = append(out, n)
		}
	}
	return out
}
