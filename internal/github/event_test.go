package github

import (
	"reflect"
	"testing"

	"github.com/zetlen/falconet/internal/prepare"
)

func TestDecodeEventReadsTheGatesInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		ev   prepare.Event
		snap prepare.Snapshot
	}{
		{
			"an empty object is an open issue with nothing on it",
			`{}`,
			prepare.Event{},
			prepare.Snapshot{State: "open"},
		},
		{
			"null body and state read as empty and open",
			`{"issue":{"body":null,"state":null,"pull_request":null}}`,
			prepare.Event{},
			prepare.Snapshot{State: "open"},
		},
		{
			"labels, body and state are the issue's; unnamed labels are dropped",
			`{"action":"reopened","issue":{"labels":[{"name":"falconet"},{"name":""},{"name":"needs-info"}],"body":"x","state":"closed"}}`,
			prepare.Event{Action: "reopened"},
			prepare.Snapshot{Labels: []string{"falconet", "needs-info"}, Body: "x", State: "closed"},
		},
		{
			"a pull_request object marks a pull request",
			`{"action":"created","issue":{"pull_request":{"url":"x"}}}`,
			prepare.Event{Action: "created", PullRequest: true},
			prepare.Snapshot{State: "open"},
		},
		{
			"a false pull_request is not one",
			`{"issue":{"pull_request":false}}`,
			prepare.Event{},
			prepare.Snapshot{State: "open"},
		},
		{
			"a sender of type Bot is a bot",
			`{"action":"created","sender":{"login":"falconet[bot]","type":"Bot"}}`,
			prepare.Event{Action: "created", Sender: "falconet[bot]", Bot: true},
			prepare.Snapshot{State: "open"},
		},
		{
			"a sender of type User is not",
			`{"action":"created","sender":{"login":"alice","type":"User"}}`,
			prepare.Event{Action: "created", Sender: "alice"},
			prepare.Snapshot{State: "open"},
		},
		{
			"a labeled event added its one label",
			`{"action":"labeled","label":{"name":"falconet"}}`,
			prepare.Event{Action: "labeled", Added: []string{"falconet"}},
			prepare.Snapshot{State: "open"},
		},
		{
			"a labeled event with no label name added nothing",
			`{"action":"labeled","label":{}}`,
			prepare.Event{Action: "labeled"},
			prepare.Snapshot{State: "open"},
		},
		{
			"any other action added nothing, whatever label it carries",
			`{"action":"unlabeled","label":{"name":"falconet"}}`,
			prepare.Event{Action: "unlabeled"},
			prepare.Snapshot{State: "open"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, snap, err := DecodeEvent([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ev, tc.ev) {
				t.Errorf("event: got %+v, want %+v", ev, tc.ev)
			}
			if !reflect.DeepEqual(snap, tc.snap) {
				t.Errorf("snapshot: got %+v, want %+v", snap, tc.snap)
			}
		})
	}
}

func TestDecodeEventAFieldOfTheWrongTypeIsAnError(t *testing.T) {
	for _, raw := range []string{
		`{"action":7}`,
		`{"issue":{"labels":"falconet"}}`,
		`{"issue":{"body":1}}`,
		`{"sender":{"type":true}}`,
	} {
		if _, _, err := DecodeEvent([]byte(raw)); err == nil {
			t.Errorf("%s: no error", raw)
		}
	}
}
