package handoff

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/quick"

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

func TestGitHubEnvAppendMultiline(t *testing.T) {
	dir := t.TempDir()
	t.Run("unset: a silent no-op", func(t *testing.T) {
		t.Setenv("GITHUB_ENV", "")
		if err := GitHubEnvAppendMultiline("KEY", "a\nb"); err != nil {
			t.Error(err)
		}
	})
	t.Run("unwritable: a silent no-op", func(t *testing.T) {
		t.Setenv("GITHUB_ENV", filepath.Join(dir, "no-such-dir", "gh_env"))
		if err := GitHubEnvAppendMultiline("KEY", "a\nb"); err != nil {
			t.Error(err)
		}
	})

	// The bytes, exactly: Actions reads the delimiter form line by line, so
	// the shape is the contract and a stray newline is a different variable.
	for _, c := range []struct{ name, key, value, want string }{
		{"one line", "AWS_ACCESS_KEY_ID", "AKIA", "AWS_ACCESS_KEY_ID<<FALCONET_PLAN_ENV_EOF\nAKIA\nFALCONET_PLAN_ENV_EOF\n"},
		{"a PEM", "KEY", "-----BEGIN\nabc\n-----END", "KEY<<FALCONET_PLAN_ENV_EOF\n-----BEGIN\nabc\n-----END\nFALCONET_PLAN_ENV_EOF\n"},
		{"a trailing newline is carried, not eaten", "KEY", "abc\n", "KEY<<FALCONET_PLAN_ENV_EOF\nabc\n\nFALCONET_PLAN_ENV_EOF\n"},
		{"an empty value", "EMPTY", "", "EMPTY<<FALCONET_PLAN_ENV_EOF\n\nFALCONET_PLAN_ENV_EOF\n"},
		{"a blank line inside", "KEY", "a\n\nb", "KEY<<FALCONET_PLAN_ENV_EOF\na\n\nb\nFALCONET_PLAN_ENV_EOF\n"},
	} {
		t.Run("writable: "+c.name, func(t *testing.T) {
			path := filepath.Join(dir, "ml_"+c.name)
			t.Setenv("GITHUB_ENV", path)
			if err := GitHubEnvAppendMultiline(c.key, c.value); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}

	t.Run("appended after a one-line write, in order", func(t *testing.T) {
		path := filepath.Join(dir, "mixed")
		t.Setenv("GITHUB_ENV", path)
		if err := GitHubEnvAppend("PUSHED_BRANCH=issue-1-x"); err != nil {
			t.Fatal(err)
		}
		if err := GitHubEnvAppendMultiline("A", "1"); err != nil {
			t.Fatal(err)
		}
		if err := GitHubEnvAppendMultiline("B", "2\n3"); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := "PUSHED_BRANCH=issue-1-x\nA<<FALCONET_PLAN_ENV_EOF\n1\nFALCONET_PLAN_ENV_EOF\nB<<FALCONET_PLAN_ENV_EOF\n2\n3\nFALCONET_PLAN_ENV_EOF\n"
		if string(got) != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	// Refused before anything is written, writable or not: a delimiter inside
	// a value ends it early and turns the rest into further variables; a key
	// that is not a name is a caller's bug and is still not written.
	refused := []struct{ name, key, value string }{
		{"the delimiter on a line of its own", "KEY", "a\nFALCONET_PLAN_ENV_EOF\nEVIL=1"},
		{"the delimiter inside a line", "KEY", "xFALCONET_PLAN_ENV_EOFx"},
		{"the delimiter as the whole value", "KEY", "FALCONET_PLAN_ENV_EOF"},
		{"a key with a dash", "AWS-KEY", "x"},
		{"a key starting with a digit", "1KEY", "x"},
		{"an empty key", "", "x"},
		{"a key with a newline", "KEY\nEVIL", "x"},
		{"a key with a space", "KEY NAME", "x"},
	}
	for _, c := range refused {
		t.Run("refused: "+c.name, func(t *testing.T) {
			path := filepath.Join(dir, "refused_ml_"+c.name)
			t.Setenv("GITHUB_ENV", path)
			if err := GitHubEnvAppendMultiline(c.key, c.value); err == nil {
				t.Fatal("expected an error")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("nothing may be written when the pair is refused; %s exists", path)
			}
			if err := CheckMultiline(c.key, c.value); err == nil {
				t.Error("CheckMultiline accepts what the writer refuses")
			}
		})
	}

	// An error names the variable and the shape, never the value: the values
	// that travel this way are credentials.
	t.Run("a refusal never quotes the value", func(t *testing.T) {
		err := CheckMultiline("KEY", "SECRET-VALUE-MARKER FALCONET_PLAN_ENV_EOF")
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), "SECRET-VALUE-MARKER") {
			t.Errorf("the error quotes the value: %q", err)
		}
	})
}

// For any name and any value without the delimiter in it, what lands in
// $GITHUB_ENV reads back as exactly that value the way Actions reads it:
// the first line is NAME<<delimiter, the last line is the delimiter, and
// the lines between, joined, are the value — however many lines it has and
// whether or not it ends in one.
func TestTheDelimiterFormRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip")
	t.Setenv("GITHUB_ENV", path)
	f := func(value string, suffix uint8) bool {
		if strings.Contains(value, MultilineDelimiter) {
			return true
		}
		name := "V" + strconv.Itoa(int(suffix))
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return false
		}
		if err := GitHubEnvAppendMultiline(name, value); err != nil {
			return false
		}
		got, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		text := string(got)
		if !strings.HasSuffix(text, "\n"+MultilineDelimiter+"\n") {
			return false
		}
		head := name + "<<" + MultilineDelimiter + "\n"
		if !strings.HasPrefix(text, head) {
			return false
		}
		middle := strings.TrimSuffix(strings.TrimPrefix(text, head), "\n"+MultilineDelimiter+"\n")
		return middle == value
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Error(err)
	}
}
