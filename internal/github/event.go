package github

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/zetlen/falconet/internal/prepare"
)

// eventPayload is the part of a webhook payload the gate reads, every field
// optional: `.issue.labels[].name`, `.issue.body` (null is ""),
// `.issue.state` (null is "open"), `.action` (null is ""), whether
// `.issue.pull_request` is set, `.sender.login`, `.sender.type`, and
// `.label.name`, the label a labeled event added.
type eventPayload struct {
	Action string `json:"action"`
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
		Type  string `json:"type"`
	} `json:"sender"`
	Label struct {
		Name string `json:"name"`
	} `json:"label"`
}

// DecodeEvent reads the gate's inputs out of a GitHub `issues` or
// `issue_comment` payload. It reads no file: the caller has already checked
// that raw is a JSON object. A payload whose fields have the wrong types is
// an error, and the error is what json says about it.
func DecodeEvent(raw []byte) (prepare.Event, prepare.Snapshot, error) {
	var ev prepare.Event
	var snap prepare.Snapshot
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
	ev.Action = p.Action
	// Set means anything but null and false.
	pr := bytes.TrimSpace(p.Issue.PullRequest)
	ev.PullRequest = len(pr) > 0 && string(pr) != "null" && string(pr) != "false"
	// GitHub's event reader: the sender is the account that labelled,
	// opened, reopened or commented, and GitHub marks an App's or an Actions
	// token's account with type Bot. A labeled event carries the one label
	// it added.
	ev.Sender = p.Sender.Login
	// GitHub gives every user object a type, so a sender with a login and
	// no type is another forge's payload. Gitea's is one, and read here its
	// bot's own needs-info comment is a person's reply, which the pipeline
	// would answer. However the forge came to be github, from the default, a
	// mistyped key or a workstation no runner marks, that payload is refused.
	if p.Sender.Login != "" && p.Sender.Type == "" {
		return prepare.Event{}, prepare.Snapshot{}, fmt.Errorf("the sender %q has no type, and every GitHub sender has one: the event is not GitHub's", p.Sender.Login)
	}
	ev.Bot = p.Sender.Type == "Bot"
	if ev.Action == "labeled" && p.Label.Name != "" {
		ev.Added = []string{p.Label.Name}
	}
	return ev, snap, nil
}
