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
