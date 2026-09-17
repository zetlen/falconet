package forge

import (
	"strings"
	"testing"
)

func TestRunnerMismatch(t *testing.T) {
	for _, c := range []struct {
		name                string
		kind, gitea, github string
		want                string // "" admits; otherwise a fragment of the refusal
	}{
		{"github on a workstation", "github", "", "", ""},
		{"gitea on a workstation", "gitea", "", "", ""},
		{"github under GitHub Actions", "github", "", "true", ""},
		// act_runner sets GITHUB_ACTIONS as well as GITEA_ACTIONS.
		{"gitea under Gitea Actions", "gitea", "true", "true", ""},
		{"gitea under Gitea Actions without GITHUB_ACTIONS", "gitea", "true", "", ""},
		{"github under Gitea Actions", "github", "true", "true", "set forge to gitea"},
		{"github under Gitea Actions without GITHUB_ACTIONS", "github", "true", "", "set forge to gitea"},
		{"gitea under GitHub Actions", "gitea", "", "true", "running under GitHub Actions with forge set to gitea"},
		// Only the exact word the runners write counts as a runner.
		{"a GITEA_ACTIONS that is not true", "github", "1", "", ""},
		{"a GITHUB_ACTIONS that is not true", "gitea", "", "yes", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := RunnerMismatch(c.kind, c.gitea, c.github)
			switch {
			case c.want == "" && got != "":
				t.Errorf("RunnerMismatch(%q, %q, %q) = %q, want no refusal", c.kind, c.gitea, c.github, got)
			case c.want != "" && !strings.Contains(got, c.want):
				t.Errorf("RunnerMismatch(%q, %q, %q) = %q, want a refusal containing %q", c.kind, c.gitea, c.github, got, c.want)
			}
		})
	}
}

func TestOnARunner(t *testing.T) {
	for _, c := range []struct {
		name          string
		gitea, github string
		want          bool
	}{
		{"a workstation", "", "", false},
		{"GitHub Actions", "", "true", true},
		{"Gitea Actions", "true", "true", true},
		{"GITEA_ACTIONS alone", "true", "", true},
		{"a GITEA_ACTIONS that is not true", "1", "", false},
		{"a GITHUB_ACTIONS that is not true", "", "yes", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := OnARunner(c.gitea, c.github); got != c.want {
				t.Errorf("OnARunner(%q, %q) = %v, want %v", c.gitea, c.github, got, c.want)
			}
		})
	}
}
