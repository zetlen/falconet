package main

// The one place in cmd/falconet that names a forge. A verb that talks to a
// forge asks forgeFor for its kit and uses nothing else: the client comes
// from kit.connect and the event file is read through kit.decode, so no verb
// imports an adapter or knows which forge answers it.

import (
	"github.com/zetlen/falconet/internal/forge"
	"github.com/zetlen/falconet/internal/github"
	"github.com/zetlen/falconet/internal/prepare"
)

// kit is what a verb needs from its forge: a client, and a reader of the
// event file.
type kit struct {
	// connect builds the client that talks to the forge's API at apiURL
	// with token.
	connect func(apiURL, token string) forge.Client
	// decode turns the event file's bytes, already known to be a JSON
	// object, into what the gate reads.
	decode func(raw []byte) (prepare.Event, prepare.Snapshot, error)
}

// forgeFor is the forge the verbs talk to. GitHub is the forge: the client
// is the `gh` adapter and the reader is GitHub's.
func forgeFor() kit {
	return kit{
		connect: func(apiURL, token string) forge.Client {
			return github.NewGH(apiURL, token)
		},
		decode: github.DecodeEvent,
	}
}
