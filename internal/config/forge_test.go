package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestForgeDefaultsToGitHub(t *testing.T) {
	bare(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Schema.Forge != "github" {
		t.Errorf("forge = %q, want github: a repository with no key changes nothing", cfg.Schema.Forge)
	}
}

func TestForgeGiteaLoads(t *testing.T) {
	dir := bare(t)
	write(t, filepath.Join(dir, ".github", "falconet.json"), `{"forge": "gitea"}`)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Schema.Forge != "gitea" {
		t.Errorf("forge = %q, want gitea", cfg.Schema.Forge)
	}
}

func TestAForgeThatIsNeitherIsRefused(t *testing.T) {
	for _, c := range []struct {
		name, content, want string
	}{
		{"another forge", `{"forge": "gitlab"}`, `forge must be github or gitea, not "gitlab"`},
		{"a spelling the reader does not know", `{"forge": "GitHub"}`, `forge must be github or gitea, not "GitHub"`},
		{"empty", `{"forge": ""}`, `forge must be github or gitea, not ""`},
		{"null", `{"forge": null}`, `forge must be github or gitea, not ""`},
		{"a number", `{"forge": 5}`, "does not match the schema"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := bare(t)
			write(t, filepath.Join(dir, ".github", "falconet.json"), c.content)
			_, err := Load("")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}
