package main

// The one place in cmd/falconet that names a forge. A verb that talks to a
// forge asks forgeFor for its kit and uses nothing else: the client comes
// from kit.connect and the event file is read through kit.decode, so no verb
// imports an adapter or knows which forge answers it.

import (
	"errors"
	"fmt"
	"os"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/forge"
	"github.com/zetlen/falconet/internal/gitea"
	"github.com/zetlen/falconet/internal/github"
	"github.com/zetlen/falconet/internal/prepare"
)

// kit is what a verb needs from its forge: a client, and a reader of the
// event file.
type kit struct {
	// connect builds the client that talks to the forge's API with token.
	// The API is the one forgeFor resolved.
	connect func(token string) (forge.Client, error)
	// decode turns the event file's bytes, already known to be a JSON
	// object, into what the gate reads.
	decode func(raw []byte) (prepare.Event, prepare.Snapshot, error)
}

// forgeFor is the forge the config names. A verb calls it right after the
// config is read, before it opens the event file or sends anything, so every
// refusal here costs no file and no network.
func forgeFor(cfg config.Schema) (kit, error) {
	// See forge.RunnerMismatch: a runner of one forge with the other forge
	// configured would read its event file with the wrong reader.
	if reason := forge.RunnerMismatch(cfg.Forge, os.Getenv("GITEA_ACTIONS"), os.Getenv("GITHUB_ACTIONS")); reason != "" {
		return kit{}, errors.New(reason)
	}
	switch cfg.Forge {
	case "github":
		apiURL := forge.APIURLFromEnv()
		return kit{
			connect: func(token string) (forge.Client, error) {
				return github.NewGH(apiURL, token), nil
			},
			decode: github.DecodeEvent,
		}, nil
	case "gitea":
		// See gitea.CheckEnv: the instance and the bot's login come from the
		// environment, beside the token they describe, and not from the
		// committed config.
		apiURL, bot := os.Getenv("GITHUB_API_URL"), os.Getenv("FALCONET_BOT_LOGIN")
		if err := gitea.CheckEnv(apiURL, bot); err != nil {
			return kit{}, err
		}
		return kit{
			connect: func(token string) (forge.Client, error) {
				return gitea.New(apiURL, token, bot)
			},
			decode: func(raw []byte) (prepare.Event, prepare.Snapshot, error) {
				return gitea.DecodeEvent(raw, bot)
			},
		}, nil
	default:
		// config.Load refuses any other value; a Schema built some other way
		// is not read as either forge.
		return kit{}, fmt.Errorf("forge must be github or gitea, not %q", cfg.Forge)
	}
}
