package repo

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func physical(t *testing.T, dir string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoot(t *testing.T) {
	t.Run("$FALCONET_REPO names the repository explicitly, symlinks resolved", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("FALCONET_REPO", dir)
		got, err := Root("/somewhere/else")
		if err != nil {
			t.Fatal(err)
		}
		if got != physical(t, dir) {
			t.Errorf("got %q, want %q", got, physical(t, dir))
		}
	})
	t.Run("a $FALCONET_REPO that names nothing is a legible failure", func(t *testing.T) {
		t.Setenv("FALCONET_REPO", filepath.Join(t.TempDir(), "nope"))
		_, err := Root(".")
		if err == nil || !strings.Contains(err.Error(), "$FALCONET_REPO names no directory") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("inside a git work tree, its top level — from a subdirectory too", func(t *testing.T) {
		t.Setenv("FALCONET_REPO", "")
		dir := t.TempDir()
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		sub := filepath.Join(dir, "dns")
		if out, err := exec.Command("mkdir", "-p", sub).CombinedOutput(); err != nil {
			t.Fatalf("mkdir: %v: %s", err, out)
		}
		got, err := Root(sub)
		if err != nil {
			t.Fatal(err)
		}
		if got != physical(t, dir) {
			t.Errorf("got %q, want %q", got, physical(t, dir))
		}
	})
	t.Run("outside git, the working directory itself, and no error", func(t *testing.T) {
		t.Setenv("FALCONET_REPO", "")
		dir := t.TempDir()
		got, err := Root(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != dir {
			t.Errorf("got %q, want %q", got, dir)
		}
	})
}

func TestRepository(t *testing.T) {
	clone := func(t *testing.T, origin string) string {
		t.Helper()
		dir := t.TempDir()
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		if origin != "" {
			if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", origin).CombinedOutput(); err != nil {
				t.Fatalf("git remote add: %v: %s", err, out)
			}
		}
		return dir
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GITHUB_SERVER_URL", "")

	t.Run("$GITHUB_REPOSITORY wins over any remote", func(t *testing.T) {
		t.Setenv("GITHUB_REPOSITORY", "zetlen/wayfinders-infra")
		owner, name, err := Repository(clone(t, "https://gitlab.com/other/place.git"))
		if err != nil || owner != "zetlen" || name != "wayfinders-infra" {
			t.Errorf("got (%q, %q, %v)", owner, name, err)
		}
	})
	t.Run("a malformed $GITHUB_REPOSITORY is an error, not a fall-through to the remote", func(t *testing.T) {
		t.Setenv("GITHUB_REPOSITORY", "noslash")
		_, _, err := Repository(clone(t, "https://github.com/o/r.git"))
		if err == nil || !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("the origin remote, in each shape git writes", func(t *testing.T) {
		t.Setenv("GITHUB_REPOSITORY", "")
		for _, origin := range []string{
			"https://github.com/zetlen/wayfinders-infra",
			"https://github.com/zetlen/wayfinders-infra.git",
			"git@github.com:zetlen/wayfinders-infra.git",
			"ssh://git@github.com/zetlen/wayfinders-infra.git",
		} {
			owner, name, err := Repository(clone(t, origin))
			if err != nil || owner != "zetlen" || name != "wayfinders-infra" {
				t.Errorf("%s: got (%q, %q, %v)", origin, owner, name, err)
			}
		}
	})
	t.Run("from a subdirectory of the clone too", func(t *testing.T) {
		t.Setenv("GITHUB_REPOSITORY", "")
		dir := clone(t, "https://github.com/o/r")
		sub := filepath.Join(dir, "dns")
		if out, err := exec.Command("mkdir", "-p", sub).CombinedOutput(); err != nil {
			t.Fatalf("mkdir: %v: %s", err, out)
		}
		owner, name, err := Repository(sub)
		if err != nil || owner != "o" || name != "r" {
			t.Errorf("got (%q, %q, %v)", owner, name, err)
		}
	})
	t.Run("an enterprise host, through $GITHUB_SERVER_URL", func(t *testing.T) {
		t.Setenv("GITHUB_REPOSITORY", "")
		t.Setenv("GITHUB_SERVER_URL", "https://github.example.com")
		owner, name, err := Repository(clone(t, "git@github.example.com:o/r.git"))
		if err != nil || owner != "o" || name != "r" {
			t.Errorf("got (%q, %q, %v)", owner, name, err)
		}
		_, _, err = Repository(clone(t, "https://github.com/o/r"))
		if err == nil || !strings.Contains(err.Error(), "github.example.com") {
			t.Errorf("github.com is not the enterprise host: got %v", err)
		}
	})
	for name, origin := range map[string]string{
		"no remote":           "",
		"a non-GitHub remote": "https://gitlab.com/o/r.git",
		"a local path":        "/srv/git/r.git",
	} {
		t.Run(name+" is an error naming both sources", func(t *testing.T) {
			t.Setenv("GITHUB_REPOSITORY", "")
			_, _, err := Repository(clone(t, origin))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range []string{"set GITHUB_REPOSITORY=owner/name", "origin is on github.com"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not name %q: %v", want, err)
				}
			}
		})
	}
	t.Run("outside a repository entirely", func(t *testing.T) {
		t.Setenv("GITHUB_REPOSITORY", "")
		_, _, err := Repository(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "no origin remote") {
			t.Errorf("got %v", err)
		}
	})
}
