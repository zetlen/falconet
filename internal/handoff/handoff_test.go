package handoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zetlen/falconet/internal/config"
)

func defaults(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("FALCONET_CONFIG", "")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return cfg, dir
}

func TestInit(t *testing.T) {
	cfg, dir := defaults(t)
	cases := []struct{ name, explicit, want string }{
		{"the configured default, under cwd", "", filepath.Join(dir, ".falconet")},
		{"an explicit absolute path wins", filepath.Join(dir, "elsewhere"), filepath.Join(dir, "elsewhere")},
		{"an explicit relative path resolves against cwd", "rel-dir", filepath.Join(dir, "rel-dir")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Init(c.explicit, cfg, dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			if info, err := os.Stat(got); err != nil || !info.IsDir() {
				t.Errorf("%s was not created", got)
			}
		})
	}
	t.Run("a directory that cannot be made is an error, not a silent path", func(t *testing.T) {
		file := filepath.Join(dir, "a-file")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Init(filepath.Join(file, "under-a-file"), cfg, dir); err == nil {
			t.Error("expected an error")
		}
	})
}

// Resolve is Init's answer without Init's directory: the prompt verb names
// the handoff directory in text, and must not create it.
func TestResolveNamesTheDirectoryAndLeavesNothingBehind(t *testing.T) {
	cfg, dir := defaults(t)
	cases := []struct{ name, explicit, want string }{
		{"the configured default, under cwd", "", filepath.Join(dir, ".falconet")},
		{"an explicit absolute path wins", filepath.Join(dir, "elsewhere"), filepath.Join(dir, "elsewhere")},
		{"an explicit relative path resolves against cwd", "rel-dir", filepath.Join(dir, "rel-dir")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Resolve(c.explicit, cfg, dir)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			if _, err := os.Stat(got); !os.IsNotExist(err) {
				t.Errorf("%s exists: Resolve must not create", got)
			}
		})
	}
	t.Run("Init is Resolve plus the directory", func(t *testing.T) {
		want := Resolve("", cfg, dir)
		got, err := Init("", cfg, dir)
		if err != nil || got != want {
			t.Errorf("Init = %q, %v; Resolve = %q", got, err, want)
		}
	})
}

func TestGitHubEnvAppend(t *testing.T) {
	dir := t.TempDir()
	t.Run("unset: a silent no-op", func(t *testing.T) {
		t.Setenv("GITHUB_ENV", "")
		if err := GitHubEnvAppend("BRANCH=x"); err != nil {
			t.Error(err)
		}
	})
	t.Run("unwritable: a silent no-op", func(t *testing.T) {
		t.Setenv("GITHUB_ENV", filepath.Join(dir, "no-such-dir", "gh_env"))
		if err := GitHubEnvAppend("BRANCH=x"); err != nil {
			t.Error(err)
		}
	})
	t.Run("writable: the lines land, appended", func(t *testing.T) {
		path := filepath.Join(dir, "gh_env")
		t.Setenv("GITHUB_ENV", path)
		if err := GitHubEnvAppend("BRANCH=issue-1-x"); err != nil {
			t.Fatal(err)
		}
		if err := GitHubEnvAppend("A=1", "B=2"); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "BRANCH=issue-1-x\nA=1\nB=2\n" {
			t.Errorf("got %q", got)
		}
	})

	// A value is one line. Actions parses $GITHUB_ENV line by line, so a
	// line break inside a value is further variables in every later step —
	// and the values that travel this way are branch names, from issue
	// titles. Refused before anything is written, writable or not.
	refused := []struct{ name, line string }{
		{"a newline in the value", "BRANCH=issue-1-x\nEVIL=1"},
		{"a carriage return in the value", "BRANCH=issue-1-x\rEVIL=1"},
		{"a key that is not a variable name", "BRANCH NAME=x"},
		{"a key with a newline", "BRANCH\nEVIL=x"},
		{"no = at all", "BRANCH"},
		{"an empty key", "=x"},
	}
	for _, c := range refused {
		t.Run("refused: "+c.name, func(t *testing.T) {
			path := filepath.Join(dir, "refused_"+c.name)
			t.Setenv("GITHUB_ENV", path)
			if err := GitHubEnvAppend("OK=1", c.line); err == nil {
				t.Fatal("expected an error")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("nothing may be written when any line is refused; %s exists", path)
			}
		})
	}
	t.Run("an empty value is fine: clearing a variable is a real thing to say", func(t *testing.T) {
		t.Setenv("GITHUB_ENV", filepath.Join(dir, "empty_value"))
		if err := GitHubEnvAppend("PUSHED_BRANCH="); err != nil {
			t.Error(err)
		}
	})
}

// readOutputs parses $GITHUB_OUTPUT the way the runner does, both forms, so
// a value is asserted on what a later step would see.
func readOutputs(t *testing.T, text string) map[string]string {
	t.Helper()
	out := map[string]string{}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if name, delim, ok := strings.Cut(line, "<<"); ok && !strings.Contains(name, "=") {
			var value []string
			for i++; i < len(lines) && lines[i] != delim; i++ {
				value = append(value, lines[i])
			}
			if i == len(lines) {
				t.Fatalf("delimiter %q never closes", delim)
			}
			out[name] = strings.Join(value, "\n")
			continue
		}
		name, value, _ := strings.Cut(line, "=")
		out[name] = value
	}
	return out
}

func TestGitHubOutputAppend(t *testing.T) {
	dir := t.TempDir()
	t.Run("unset: a silent no-op", func(t *testing.T) {
		t.Setenv("GITHUB_OUTPUT", "")
		if err := GitHubOutputAppend("reason", "x"); err != nil {
			t.Error(err)
		}
	})
	t.Run("a value that would end a name=value line, or pose as the next output, stays one value", func(t *testing.T) {
		path := filepath.Join(dir, "gh_output")
		t.Setenv("GITHUB_OUTPUT", path)
		values := map[string]string{
			"reason":  "issue #4 carries the blocking label 'x\noutcome=ready'\n",
			"second":  "EOF\nFALCONET_\n<<",
			"outcome": "ineligible",
		}
		for _, name := range []string{"reason", "second", "outcome"} {
			if err := GitHubOutputAppend(name, values[name]); err != nil {
				t.Fatal(err)
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := readOutputs(t, string(raw))
		want := map[string]string{
			"reason":  "issue #4 carries the blocking label 'x\noutcome=ready'",
			"second":  "EOF\nFALCONET_\n<<",
			"outcome": "ineligible",
		}
		if len(got) != len(want) {
			t.Errorf("outputs %q, want %q", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %q, want %q", k, got[k], v)
			}
		}
	})
	t.Run("refused: a name that is not one", func(t *testing.T) {
		t.Setenv("GITHUB_OUTPUT", filepath.Join(dir, "refused"))
		if err := GitHubOutputAppend("a\nb", "x"); err == nil {
			t.Error("expected an error")
		}
	})
}
