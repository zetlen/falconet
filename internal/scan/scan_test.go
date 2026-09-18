package scan

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRelative(t *testing.T) {
	for _, c := range []struct{ path, root, want string }{
		{"/private/var/w/repo/.falconet/commit-msg.txt", "/private/var/w/repo", ".falconet/commit-msg.txt"},
		{"/private/var/w/repo/msg.txt", "/private/var/w/repo", "msg.txt"},
		{"/elsewhere/msg.txt", "/private/var/w/repo", "/elsewhere/msg.txt"},
		// A root that is a string prefix but not a directory is not trimmed.
		{"/private/var/w/repo2/msg.txt", "/private/var/w/repo", "/private/var/w/repo2/msg.txt"},
		{"/private/var/w/repo", "/private/var/w/repo", "/private/var/w/repo"},
	} {
		if got := Relative(c.path, c.root); got != c.want {
			t.Errorf("Relative(%q, %q) = %q, want %q", c.path, c.root, got, c.want)
		}
	}
}

// stub writes a gitleaks stand-in: a shell script that records its
// arguments, swallows stdin, says something on the stream it is told to,
// and exits as told. The suite's stubs are the same shape.
func stub(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "gitleaks")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$STUB_CALLS\"\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// finder is a stub that matches a token-shaped line on stdin and nothing
// else, exiting with whatever --exit-code it was handed.
const finder = `code=1; prev=""
for a in "$@"; do [ "$prev" = "--exit-code" ] && code="$a"; prev="$a"; done
if grep -q 'gh[ps]_[A-Za-z0-9]\{36\}'; then echo "leaks found: 1" >&2; exit "$code"; fi
echo "no leaks found" >&2; exit 0
`

func fakeToken() string { return "ghp_" + "0123456789abcdefghijABCDEFGHIJ012345" }

// gitRepo makes dir a repository with one commit holding files, and returns
// its physical root. The scan reads gitleaks's configuration from HEAD, so
// every Root is a repository.
func gitRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "base")
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestScanDiscipline(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh for a stub")
	}
	dir := t.TempDir()
	root := gitRepo(t, dir, nil)
	calls := filepath.Join(dir, "calls.txt")
	t.Setenv("STUB_CALLS", calls)
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	clean := write("msg.txt", "Add a record\n\nBecause.\n")
	dirty := write("questions.md", "- Which zone? The token I read was "+fakeToken()+"\n")
	empty := write("empty.txt", "")
	missing := filepath.Join(dir, "no-such-file.txt")

	var stderr bytes.Buffer
	s := &Scanner{Gitleaks: stub(t, dir, finder), Root: root, Stderr: &stderr}
	var matched []string
	report := func(label string) { matched = append(matched, label) }

	hit, err := s.Scan([]string{clean, missing, empty, dirty}, false, report)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !hit || !reflect.DeepEqual(matched, []string{"questions.md"}) {
		t.Errorf("hit=%v matched=%q; want only the dirty channel, named relative to the root", hit, matched)
	}
	if strings.Contains(stderr.String()+strings.Join(matched, ""), fakeToken()) {
		t.Error("the matched value was repeated")
	}
	if !strings.Contains(stderr.String(), "no leaks found") {
		t.Errorf("gitleaks' own stderr did not reach Stderr: %q", stderr.String())
	}
	argv, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(argv), "\n"); got != 2 {
		t.Errorf("gitleaks ran %d times; the missing and empty files must be skipped", got)
	}
	if !strings.Contains(string(argv), "--ignore-gitleaks-allow --no-banner --no-color --redact --verbose --exit-code 3") {
		t.Errorf("argv: %q", argv)
	}
}

func TestScanFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh for a stub")
	}
	dir := t.TempDir()
	gitRepo(t, dir, nil)
	t.Setenv("STUB_CALLS", filepath.Join(dir, "calls.txt"))
	file := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(file, []byte("anything at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nothing := func(string) {}

	missing := &Scanner{Gitleaks: filepath.Join(dir, "nope"), Root: dir}
	if _, err := missing.Scan([]string{file}, false, nothing); err == nil {
		t.Error("a missing gitleaks was a clean scan")
	} else if !isNotRun(err) || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a missing gitleaks: %v", err)
	}

	broken := &Scanner{Gitleaks: stub(t, dir, "cat >/dev/null; echo 'FTL failed to load config' >&2; exit 1\n"), Root: dir}
	if _, err := broken.Scan([]string{file}, false, nothing); !isNotRun(err) {
		t.Errorf("gitleaks' own fatal exit (1) was not a NotRun: %v", err)
	}

	// The chatty case: findings on stdout, and an exit of 3. The finding
	// goes to Stderr; the label is all the caller gets.
	var stderr bytes.Buffer
	chatty := &Scanner{Gitleaks: stub(t, dir, "cat >/dev/null\nprintf 'Finding:     the header was REDACTED\\n'\necho 'leaks found: 1' >&2\nexit 3\n"), Root: dir, Stderr: &stderr}
	var matched []string
	hit, err := chatty.Scan([]string{file}, false, func(l string) { matched = append(matched, l) })
	if err != nil || !hit {
		t.Fatalf("chatty: hit=%v err=%v", hit, err)
	}
	if !strings.Contains(stderr.String(), "Finding:") {
		t.Error("the finding did not go to Stderr")
	}
	if len(matched) != 1 || strings.Contains(matched[0], "Finding") {
		t.Errorf("matched = %q", matched)
	}
}

// TestAHitBeforeAFailureStaysReported: the labels are reported as found, so
// a caller printing them prints what was found before the scan died, and
// then that it died.
func TestAHitBeforeAFailureStaysReported(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh for a stub")
	}
	dir := t.TempDir()
	gitRepo(t, dir, nil)
	t.Setenv("STUB_CALLS", filepath.Join(dir, "calls.txt"))
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	for _, f := range []string{first, second} {
		if err := os.WriteFile(f, []byte("text\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Hits on the first call, dies on the second.
	body := "n=$(wc -l <\"$STUB_CALLS\"); cat >/dev/null; [ \"$n\" -le 1 ] && exit 3; exit 1\n"
	s := &Scanner{Gitleaks: stub(t, dir, body), Root: dir}
	var matched []string
	hit, err := s.Scan([]string{first, second}, false, func(l string) { matched = append(matched, l) })
	if !isNotRun(err) {
		t.Fatalf("err = %v", err)
	}
	if !hit || len(matched) != 1 || !strings.HasSuffix(matched[0], "first.txt") {
		t.Errorf("hit=%v matched=%q", hit, matched)
	}
}

func isNotRun(err error) bool {
	var nr *NotRun
	return errors.As(err, &nr)
}

// TestGitleaksConfigIsTheScansOwn: gitleaks runs outside the tree, with no
// GITLEAKS_ variable, with gitleaks:allow comments ignored, and with the
// config and ignore file committed at HEAD — never the working tree's copies.
// See "What gitleaks is configured by" in scan.go.
func TestGitleaksConfigIsTheScansOwn(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh for a stub")
	}
	// The stub records where it ran, whether a GITLEAKS_ variable reached it,
	// and the config and ignore file it was handed.
	record := `cat >/dev/null
prev=""; config=""; ignore=""
for a in "$@"; do
  [ "$prev" = "--config" ] && config="$a"
  [ "$prev" = "--gitleaks-ignore-path" ] && ignore="$a"
  prev="$a"
done
{ echo "cwd=$(pwd -P)"; echo "env=$(env | grep -c '^GITLEAKS_')"
  echo "config=$(cat "$config")"; echo "ignore=$(cat "$ignore/.gitleaksignore" 2>/dev/null)"
} >"$STUB_OUT"
exit 0
`
	for _, c := range []struct {
		name       string
		committed  map[string]string
		wantConfig string
		wantIgnore string
	}{
		{"committed", map[string]string{".gitleaks.toml": "title = \"base\"", ".gitleaksignore": "base-fingerprint"}, `title = "base"`, "base-fingerprint"},
		{"none committed", nil, "[extend]\nuseDefault = true", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			root := gitRepo(t, dir, c.committed)
			// The agent's copies, in the working tree.
			for name, content := range map[string]string{
				".gitleaks.toml":  "title = \"agent\"",
				".gitleaksignore": "agent-fingerprint",
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out := filepath.Join(t.TempDir(), "out.txt")
			t.Setenv("STUB_OUT", out)
			t.Setenv("STUB_CALLS", filepath.Join(t.TempDir(), "calls.txt"))
			t.Setenv("GITLEAKS_CONFIG", filepath.Join(root, ".gitleaks.toml"))
			t.Setenv("GITLEAKS_CONFIG_TOML", "title = \"env\"")
			msg := filepath.Join(root, "msg.txt")
			if err := os.WriteFile(msg, []byte("text\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			s := &Scanner{Gitleaks: stub(t, t.TempDir(), record), Root: root, Stderr: &bytes.Buffer{}}
			if _, err := s.Scan([]string{msg}, false, func(string) {}); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(got), "\n")
			cwd := strings.TrimPrefix(lines[0], "cwd=")
			if cwd == root || strings.HasPrefix(cwd, root+"/") {
				t.Errorf("gitleaks ran inside the tree: %s", cwd)
			}
			if lines[1] != "env=0" {
				t.Errorf("a GITLEAKS_ variable reached gitleaks: %s", lines[1])
			}
			body := strings.Join(lines[2:], "\n")
			if !strings.Contains(body, "config="+c.wantConfig) {
				t.Errorf("config: got %q, want %q", body, c.wantConfig)
			}
			if !strings.Contains(body, "ignore="+c.wantIgnore+"\n") {
				t.Errorf("ignore file: got %q, want %q", body, c.wantIgnore)
			}
			if strings.Contains(body, "agent") {
				t.Errorf("the working tree's copy was read: %q", body)
			}
		})
	}
}
