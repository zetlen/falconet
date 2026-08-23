package stacks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/zetlen/falconet/internal/config"
)

// The configured default, read from the defaults themselves so this test
// fails if the two drift.
func defaultPlanCommand(t *testing.T) string {
	t.Helper()
	var defaults struct {
		Plan struct {
			Command string `json:"command"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(config.Defaults), &defaults); err != nil {
		t.Fatal(err)
	}
	return defaults.Plan.Command
}

func TestInitAndValidateArgv(t *testing.T) {
	r := Runner{Tofu: "/stub/tofu", RepoRoot: "/work/repo"}
	cases := []struct {
		name string
		cmd  *exec.Cmd
		want []string
	}{
		// The suite asserts on these as substrings of the stub's
		// space-joined argv: "-chdir=<root>/dns init -input=false" for a
		// planned stack, "-chdir=<root>/workspace init -backend=false" for
		// a validate-only one, and "init -backend=false" NOT for the
		// planned one.
		{"a planned stack gets a real init", r.Init("dns", true),
			[]string{"/stub/tofu", "-chdir=/work/repo/dns", "init", "-input=false"}},
		{"a validate-only stack gets no backend", r.Init("workspace", false),
			[]string{"/stub/tofu", "-chdir=/work/repo/workspace", "init", "-backend=false", "-input=false"}},
		{"validate is asked for no color", r.Validate("site"),
			[]string{"/stub/tofu", "-chdir=/work/repo/site", "validate", "-no-color"}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.cmd.Args, c.want) {
			t.Errorf("%s: argv %q, want %q", c.name, c.cmd.Args, c.want)
		}
		if c.cmd.Path != "/stub/tofu" {
			t.Errorf("%s: path %q, want the configured binary", c.name, c.cmd.Path)
		}
	}
	// Joined by a slash, never cleaned: the string the stub records is the
	// string the suite asserts on.
	if got := (Runner{RepoRoot: "/r/"}).Dir("dns"); got != "/r//dns" {
		t.Errorf("Dir did not join with a bare slash: %q", got)
	}
}

func TestPlanCommand(t *testing.T) {
	r := Runner{Tofu: "/stub/tofu", RepoRoot: "/work/repo"}
	cases := []struct {
		name, command string
		want          []string
	}{
		{"the configured default", defaultPlanCommand(t),
			[]string{"/stub/tofu", "-chdir=/work/repo/dns", "plan", "-no-color", "-input=false", "-refresh=false", "-lock=false"}},
		{"a tofu command with extra flags", "tofu -chdir={stack} plan -no-color -compact-warnings",
			[]string{"/stub/tofu", "-chdir=/work/repo/dns", "plan", "-no-color", "-compact-warnings"}},
		{"{stack} more than once", "tofu -chdir={stack} plan -var=dir={stack} -out={stack}/p.tfplan",
			[]string{"/stub/tofu", "-chdir=/work/repo/dns", "plan", "-var=dir=/work/repo/dns", "-out=/work/repo/dns/p.tfplan"}},
		{"a non-tofu first word is left alone", "./scripts/plan.sh {stack} --no-color",
			[]string{"./scripts/plan.sh", "/work/repo/dns", "--no-color"}},
		{"tofu later in the command is a word, not the binary", "env TOFU_LOG=debug tofu -chdir={stack} plan",
			[]string{"env", "TOFU_LOG=debug", "tofu", "-chdir=/work/repo/dns", "plan"}},
		{"runs of whitespace, tabs and newlines are one separator", "  tofu\t-chdir={stack}\n plan  ",
			[]string{"/stub/tofu", "-chdir=/work/repo/dns", "plan"}},
		{"{stack} alone", "{stack}", []string{"/work/repo/dns"}},
		{"no {stack} at all", "tofu plan", []string{"/stub/tofu", "plan"}},
	}
	for _, c := range cases {
		got, err := r.PlanCommand(c.command, "dns")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	t.Run("an empty command is an error", func(t *testing.T) {
		for _, empty := range []string{"", "   ", "\t\n"} {
			if argv, err := r.PlanCommand(empty, "dns"); err == nil {
				t.Errorf("PlanCommand(%q) = %q, want an error", empty, argv)
			}
		}
	})

	t.Run("the split is of what was written, so a root with a space survives", func(t *testing.T) {
		spaced := Runner{Tofu: "tofu", RepoRoot: "/Users/me/my repo"}
		got, err := spaced.PlanCommand("tofu -chdir={stack} plan", "dns")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"tofu", "-chdir=/Users/me/my repo/dns", "plan"}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("$TOFU unset is the bare word, on the PATH", func(t *testing.T) {
		bare := Runner{Tofu: "tofu", RepoRoot: "/r"}
		got, err := bare.PlanCommand("tofu plan", "s")
		if err != nil || got[0] != "tofu" {
			t.Errorf("got %q, %v", got, err)
		}
	})
}

// For any command and stack: every argument is one whitespace-free field of
// the command with {stack} expanded, the count is the field count, and no
// {stack} survives.
func TestPlanCommandProperties(t *testing.T) {
	r := Runner{Tofu: "/stub/tofu", RepoRoot: "/root"}
	f := func(words []string, stack string) bool {
		if strings.ContainsAny(stack, " \t\n\v\f\r") || strings.Contains(stack, "{stack}") {
			return true
		}
		var clean []string
		for _, w := range words {
			w = strings.Join(strings.Fields(w), "")
			if w != "" {
				clean = append(clean, w)
			}
		}
		command := strings.Join(clean, " \t ")
		argv, err := r.PlanCommand(command, stack)
		if len(clean) == 0 {
			return err != nil
		}
		if err != nil || len(argv) != len(clean) {
			return false
		}
		for i, a := range argv {
			want := strings.ReplaceAll(clean[i], "{stack}", r.Dir(stack))
			if i == 0 && want == "tofu" {
				want = r.Tofu
			}
			if a != want || strings.Contains(a, "{stack}") {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Error(err)
	}
}

func TestInitFirst(t *testing.T) {
	for command, want := range map[string]bool{
		"tofu -chdir={stack} plan -no-color -input=false -refresh=false -lock=false": true,
		"tofu plan":                true,
		"  tofu\tplan":             true,
		"tofu":                     true,
		"./plan.sh {stack}":        false,
		"env X=1 tofu plan":        false,
		"tofu-wrapper plan":        false,
		"/usr/local/bin/tofu plan": false,
		"":                         false,
		"   ":                      false,
	} {
		if got := InitFirst(command); got != want {
			t.Errorf("InitFirst(%q) = %v, want %v", command, got, want)
		}
	}
	if InitFirst(defaultPlanCommand(t)) != true {
		t.Error("the configured default is a tofu command and must be initialised first")
	}
}

// Run hands the child the files themselves. The suite's stub carries a
// tripwire for this — it records STDOUT-IS-A-PIPE when `[[ -p /dev/stdout ]]`
// — and this holds the same thing with sh, for a command whose stdout and
// stderr are two files and for one where they are the same file opened once.
func TestRunAttachesFilesNotPipes(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to probe with")
	}
	dir := t.TempDir()
	probe := `if [ -p /dev/stdout ]; then echo PIPE; else echo FILE; fi; echo err >&2; exit 3`

	t.Run("two files", func(t *testing.T) {
		out, err := os.Create(filepath.Join(dir, "out"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = out.Close() }()
		errf, err := os.Create(filepath.Join(dir, "err"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = errf.Close() }()
		err = Run(exec.Command(sh, "-c", probe), out, errf)
		var exit *exec.ExitError
		if err == nil || !errorsAs(err, &exit) || exit.ExitCode() != 3 {
			t.Fatalf("the exit status did not come back: %v", err)
		}
		if got := read(t, filepath.Join(dir, "out")); got != "FILE\n" {
			t.Errorf("stdout was %q; the child saw a pipe", got)
		}
		if got := read(t, filepath.Join(dir, "err")); got != "err\n" {
			t.Errorf("stderr was %q", got)
		}
	})

	t.Run("one file, both streams, two commands in sequence", func(t *testing.T) {
		f, err := os.Create(filepath.Join(dir, "both"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		_ = Run(exec.Command(sh, "-c", "echo one; echo two >&2"), f, f)
		_ = Run(exec.Command(sh, "-c", "echo three"), f, f)
		if got := read(t, filepath.Join(dir, "both")); got != "one\ntwo\nthree\n" {
			t.Errorf("the streams did not land in order in the one file: %q", got)
		}
	})
}

func errorsAs(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
