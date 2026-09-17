package gitea

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/zetlen/falconet/internal/prepare"
)

// The payloads below are shaped as Gitea's Actions notifier writes them
// (gitea services/actions/notifier.go:265 and :329): an
// api.IssuePayload or api.IssueCommentPayload whose sender is an api.User,
// which has a login and no type.
func TestDecodeEvent(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		ev   prepare.Event
		snap prepare.Snapshot
	}{
		{
			"a label added is labeled, and Added is what was added",
			`{"action":"label_updated","sender":{"id":3,"login":"maint"},
			  "changes":{"added_labels":[{"id":1,"name":"falconet"}],"removed_labels":null},
			  "issue":{"number":7,"state":"open","body":"x","labels":[{"id":1,"name":"falconet"}],"pull_request":null}}`,
			prepare.Event{Action: "labeled", Sender: "maint", Added: []string{"falconet"}},
			prepare.Snapshot{Labels: []string{"falconet"}, Body: "x", State: "open"},
		},
		{
			"a change that added and removed labels is labeled, as Gitea matches it",
			`{"action":"label_updated","sender":{"login":"maint"},
			  "changes":{"added_labels":[{"name":"falconet"}],"removed_labels":[{"name":"bug"}]},
			  "issue":{"state":"open","labels":[{"name":"falconet"}]}}`,
			prepare.Event{Action: "labeled", Sender: "maint", Added: []string{"falconet"}},
			prepare.Snapshot{Labels: []string{"falconet"}, State: "open"},
		},
		{
			// Gitea's replace route names the whole new set as added and the
			// whole old set as removed (gitea services/issue/label.go:85).
			"a replace that keeps the queue label added only what was not there",
			`{"action":"label_updated","sender":{"login":"maint"},
			  "changes":{"added_labels":[{"name":"bug"},{"name":"falconet"}],"removed_labels":[{"name":"falconet"}]},
			  "issue":{"state":"open","labels":[{"name":"bug"},{"name":"falconet"}]}}`,
			prepare.Event{Action: "labeled", Sender: "maint", Added: []string{"bug"}},
			prepare.Snapshot{Labels: []string{"bug", "falconet"}, State: "open"},
		},
		{
			"a replace with the same set changed nothing and stays label_updated",
			`{"action":"label_updated","sender":{"login":"maint"},
			  "changes":{"added_labels":[{"name":"falconet"}],"removed_labels":[{"name":"falconet"}]},
			  "issue":{"state":"open","labels":[{"name":"falconet"}]}}`,
			prepare.Event{Action: "label_updated", Sender: "maint"},
			prepare.Snapshot{Labels: []string{"falconet"}, State: "open"},
		},
		{
			"a replace that only dropped a label is unlabeled",
			`{"action":"label_updated","sender":{"login":"maint"},
			  "changes":{"added_labels":[{"name":"falconet"}],"removed_labels":[{"name":"bug"},{"name":"falconet"}]},
			  "issue":{"state":"open","labels":[{"name":"falconet"}]}}`,
			prepare.Event{Action: "unlabeled", Sender: "maint"},
			prepare.Snapshot{Labels: []string{"falconet"}, State: "open"},
		},
		{
			"an added label with no name added nothing",
			`{"action":"label_updated","sender":{"login":"maint"},
			  "changes":{"added_labels":[{"name":""}],"removed_labels":null},"issue":{}}`,
			prepare.Event{Action: "label_updated", Sender: "maint"},
			prepare.Snapshot{State: "open"},
		},
		{
			"a change that only removed labels is unlabeled",
			`{"action":"label_updated","sender":{"login":"maint"},
			  "changes":{"added_labels":null,"removed_labels":[{"name":"falconet"}]},
			  "issue":{"state":"open","labels":[]}}`,
			prepare.Event{Action: "unlabeled", Sender: "maint"},
			prepare.Snapshot{State: "open"},
		},
		{
			"a label_updated that says nothing of what changed stays label_updated",
			`{"action":"label_updated","sender":{"login":"maint"},"issue":{"labels":[{"name":"falconet"}]}}`,
			prepare.Event{Action: "label_updated", Sender: "maint"},
			prepare.Snapshot{Labels: []string{"falconet"}, State: "open"},
		},
		{
			"label_cleared is unlabeled",
			`{"action":"label_cleared","sender":{"login":"maint"},"changes":{"added_labels":null,"removed_labels":null},"issue":{}}`,
			prepare.Event{Action: "unlabeled", Sender: "maint"},
			prepare.Snapshot{State: "open"},
		},
		{
			"reopened passes through",
			`{"action":"reopened","sender":{"login":"maint"},"issue":{"state":"open"}}`,
			prepare.Event{Action: "reopened", Sender: "maint"},
			prepare.Snapshot{State: "open"},
		},
		{
			"a comment is created, on an issue",
			`{"action":"created","sender":{"login":"requester"},"is_pull":false,
			  "comment":{"body":"here is more"},
			  "issue":{"state":"open","labels":[{"name":"falconet"},{"name":"needs-info"}],"pull_request":null}}`,
			prepare.Event{Action: "created", Sender: "requester"},
			prepare.Snapshot{Labels: []string{"falconet", "needs-info"}, State: "open"},
		},
		{
			"is_pull marks a comment on a pull request",
			`{"action":"created","sender":{"login":"maint"},"is_pull":true,"issue":{}}`,
			prepare.Event{Action: "created", Sender: "maint", PullRequest: true},
			prepare.Snapshot{State: "open"},
		},
		{
			"issue.pull_request marks one too",
			`{"action":"created","sender":{"login":"maint"},"issue":{"pull_request":{"merged":false}}}`,
			prepare.Event{Action: "created", Sender: "maint", PullRequest: true},
			prepare.Snapshot{State: "open"},
		},
		{
			"the bot's own comment is the bot's, whatever the case of its login",
			`{"action":"created","sender":{"login":"Falconet-Bot"},"issue":{"labels":[{"name":"falconet"},{"name":"needs-info"}]}}`,
			prepare.Event{Action: "created", Sender: "Falconet-Bot", Bot: true},
			prepare.Snapshot{Labels: []string{"falconet", "needs-info"}, State: "open"},
		},
		{
			"a login that merely contains the bot's is a person",
			`{"action":"created","sender":{"login":"falconet-bot-2"},"issue":{}}`,
			prepare.Event{Action: "created", Sender: "falconet-bot-2"},
			prepare.Snapshot{State: "open"},
		},
		{
			"a type field is not read: Gitea sends none, and one in the file decides nothing",
			`{"action":"created","sender":{"login":"maint","type":"Bot"},"issue":{}}`,
			prepare.Event{Action: "created", Sender: "maint"},
			prepare.Snapshot{State: "open"},
		},
		{
			"no sender is an empty sender, which the gate refuses",
			`{"action":"reopened","issue":{}}`,
			prepare.Event{Action: "reopened"},
			prepare.Snapshot{State: "open"},
		},
		{
			"null body and state read as empty and open; unnamed labels are dropped",
			`{"issue":{"body":null,"state":null,"labels":[{"name":""},{"name":"falconet"}]}}`,
			prepare.Event{},
			prepare.Snapshot{Labels: []string{"falconet"}, State: "open"},
		},
		{
			"a closed issue is closed",
			`{"action":"reopened","sender":{"login":"maint"},"issue":{"state":"closed"}}`,
			prepare.Event{Action: "reopened", Sender: "maint"},
			prepare.Snapshot{State: "closed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, snap, err := DecodeEvent([]byte(tc.raw), "falconet-bot")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ev, tc.ev) {
				t.Errorf("event = %+v, want %+v", ev, tc.ev)
			}
			if !reflect.DeepEqual(snap, tc.snap) {
				t.Errorf("snapshot = %+v, want %+v", snap, tc.snap)
			}
		})
	}
}

func TestDecodeEventRefusesNoBot(t *testing.T) {
	for _, bot := range []string{"", "falconet[bot]", "a/b"} {
		_, _, err := DecodeEvent([]byte(`{"action":"created","sender":{"login":"maint"}}`), bot)
		if err == nil || !strings.Contains(err.Error(), "FALCONET_BOT_LOGIN") {
			t.Errorf("bot %q: got %v, want an error naming FALCONET_BOT_LOGIN", bot, err)
		}
	}
}

func TestDecodeEventRefusesWrongTypes(t *testing.T) {
	for _, raw := range []string{
		`{"sender":{"login":5}}`,
		`{"changes":{"added_labels":"falconet"}}`,
		`{"is_pull":"yes"}`,
	} {
		if _, _, err := DecodeEvent([]byte(raw), "falconet-bot"); err == nil {
			t.Errorf("%s: decoded, want an error", raw)
		}
	}
}

// No event whose sender is the bot, in any case, is ever a person's: an
// unmarked one is the pipeline answering its own question.
func TestNoEventFromTheBotIsEverAPersons(t *testing.T) {
	flip := func(s string, mask uint64) string {
		b := []byte(s)
		for i := range b {
			if mask&(1<<(uint(i)%64)) != 0 {
				switch c := b[i]; {
				case c >= 'a' && c <= 'z':
					b[i] = c - 'a' + 'A'
				case c >= 'A' && c <= 'Z':
					b[i] = c - 'A' + 'a'
				}
			}
		}
		return string(b)
	}
	prop := func(seed uint32, mask uint64, action uint8) bool {
		bot := "bot-" + strings.Repeat("x", int(seed%7)) + "Z9"
		actions := []string{"created", "label_updated", "reopened", "opened"}
		payload, err := json.Marshal(map[string]any{
			"action":  actions[int(action)%len(actions)],
			"sender":  map[string]any{"login": flip(bot, mask)},
			"changes": map[string]any{"added_labels": []map[string]string{{"name": "falconet"}}},
			"issue":   map[string]any{"labels": []map[string]string{{"name": "falconet"}, {"name": "needs-info"}}},
		})
		if err != nil {
			return false
		}
		ev, _, err := DecodeEvent(payload, bot)
		return err == nil && ev.Bot
	}
	if err := quick.Check(prop, nil); err != nil {
		t.Error(err)
	}
}
