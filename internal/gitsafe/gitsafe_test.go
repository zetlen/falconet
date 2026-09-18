package gitsafe

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// git runs a setup command against dir with the same global/system scrub the
// package uses, so a developer's own git config cannot change what a test
// sees. It is the test's own hands, not the code under test.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.invalid")
	git(t, dir, "config", "user.name", "t")
	return dir
}

// A tree whose .git/config names an external diff driver: the demonstrated
// escape. A bare `git diff --cached` runs the driver; the hardened one must
// not, and must still produce the real diff rather than the driver's output.
func TestCommandDisablesExternalDiff(t *testing.T) {
	dir := initRepo(t)
	write(t, filepath.Join(dir, "f.txt"), "before\n")
	git(t, dir, "add", "f.txt")
	git(t, dir, "commit", "-qm", "base")
	write(t, filepath.Join(dir, "f.txt"), "after\n")
	git(t, dir, "add", "f.txt")

	marker := filepath.Join(dir, "ran")
	driver := filepath.Join(dir, "driver.sh")
	write(t, driver, "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(driver, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "config", "diff.external", "sh "+driver)

	out, err := Command(dir, "diff", "--cached", "--no-ext-diff", "--no-textconv").Output()
	if err != nil {
		t.Fatalf("hardened diff: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the external diff driver ran despite hardening")
	}
	if len(out) == 0 {
		t.Fatal("the hardened diff produced no output, so it did not run the real diff")
	}
}

func TestUntrusted(t *testing.T) {
	t.Run("a clean checkout is not flagged", func(t *testing.T) {
		if r := Untrusted(initRepo(t), ".falconet"); r != "" {
			t.Fatalf("clean repo flagged: %s", r)
		}
	})

	dangerous := map[string][2]string{
		"diff.external":    {"diff.external", "sh x.sh"},
		"core.fsmonitor":   {"core.fsmonitor", "sh x.sh"},
		"core.hooksPath":   {"core.hooksPath", "/tmp/hooks"},
		"a diff driver":    {"diff.d.command", "sh x.sh"},
		"a clean filter":   {"filter.f.clean", "sh x.sh"},
		"a smudge filter":  {"filter.f.smudge", "sh x.sh"},
		"an excludes file": {"core.excludesFile", "/tmp/hide"},
	}
	for name, kv := range dangerous {
		t.Run(name+" is flagged", func(t *testing.T) {
			dir := initRepo(t)
			git(t, dir, "config", kv[0], kv[1])
			if Untrusted(dir, ".falconet") == "" {
				t.Fatalf("%s (%s) not flagged", name, kv[0])
			}
		})
	}

	t.Run("a planted hook is flagged", func(t *testing.T) {
		dir := initRepo(t)
		write(t, filepath.Join(dir, ".git", "hooks", "pre-commit"), "#!/bin/sh\ntrue\n")
		if Untrusted(dir, ".falconet") == "" {
			t.Fatal("pre-commit hook not flagged")
		}
	})

	t.Run("a sample hook is not flagged", func(t *testing.T) {
		dir := initRepo(t)
		write(t, filepath.Join(dir, ".git", "hooks", "pre-commit.sample"), "#!/bin/sh\ntrue\n")
		if r := Untrusted(dir, ".falconet"); r != "" {
			t.Fatalf("sample hook flagged: %s", r)
		}
	})

	t.Run(".git/info/attributes is flagged", func(t *testing.T) {
		dir := initRepo(t)
		write(t, filepath.Join(dir, ".git", "info", "attributes"), "* filter=f\n")
		if Untrusted(dir, ".falconet") == "" {
			t.Fatal(".git/info/attributes not flagged")
		}
	})

	t.Run(".git/info/exclude naming the handoff directory is not flagged", func(t *testing.T) {
		dir := initRepo(t)
		write(t, filepath.Join(dir, ".git", "info", "exclude"),
			"# git ls-files --others --exclude-from=.git/info/exclude\n\n.falconet/\n.falconet/\n/.falconet\n")
		if r := Untrusted(dir, ".falconet"); r != "" {
			t.Fatalf("handoff exclude flagged: %s", r)
		}
	})

	t.Run(".git/info/exclude naming anything else is flagged", func(t *testing.T) {
		for _, entry := range []string{".gitleaks.toml", ".falconet/../x", "*", ".falconet*"} {
			dir := initRepo(t)
			write(t, filepath.Join(dir, ".git", "info", "exclude"), ".falconet/\n"+entry+"\n")
			if Untrusted(dir, ".falconet") == "" {
				t.Errorf("exclude entry %q not flagged", entry)
			}
		}
	})

	t.Run("a handoff directory outside the checkout excuses no entry", func(t *testing.T) {
		dir := initRepo(t)
		write(t, filepath.Join(dir, ".git", "info", "exclude"), ".falconet/\n")
		if Untrusted(dir, "") == "" {
			t.Fatal("exclude entry excused with no handoff directory inside the checkout")
		}
	})
}
