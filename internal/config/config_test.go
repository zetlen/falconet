package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A fresh directory with no config in it, as the working directory, and no
// $FALCONET_CONFIG: resolution 4, the defaults alone.
func bare(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("FALCONET_CONFIG", "")
	return dir
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

func TestDefaultsStandAlone(t *testing.T) {
	bare(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.File != "" {
		t.Errorf("File = %q, want empty: nothing was read", cfg.File)
	}
	s := cfg.Schema
	checks := map[string]string{
		"handoff_dir":        s.HandoffDir,
		"issue.queue_label":  s.Issue.QueueLabel,
		"labels.human":       s.Labels.Human,
		"plan.command":       s.Plan.Command,
		"paths.allow[0]":     s.Paths.Allow[0],
		"stacks.plan[0]":     s.Stacks.Plan[0],
		"blocking_labels[3]": s.Issue.BlockingLabels[3],
	}
	want := map[string]string{
		"handoff_dir":        ".falconet",
		"issue.queue_label":  "infra-request",
		"labels.human":       "ready-for-human",
		"plan.command":       "tofu -chdir={stack} plan -no-color -input=false -refresh=false -lock=false",
		"paths.allow[0]":     "*.tf",
		"stacks.plan[0]":     "dns",
		"blocking_labels[3]": "wontfix",
	}
	for k, got := range checks {
		if got != want[k] {
			t.Errorf("%s = %q, want %q", k, got, want[k])
		}
	}
	// Order is load-bearing for the denylist: templatefile( before file(.
	deny := s.Paths.DenyContent
	tf, f := index(deny, "templatefile("), index(deny, "file(")
	if tf < 0 || f < 0 || tf > f {
		t.Errorf("deny_content order: templatefile( at %d, file( at %d in %v", tf, f, deny)
	}
	// prompts has no default (#3). The old one named a path relative to the
	// consumer's repository, which made the default an override and the
	// shipped prompt unreachable; an absent key is the embedded prompt.
	if len(s.Prompts) != 0 {
		t.Errorf("prompts default = %v, want none: the shipped prompt is the binary's, not a path", s.Prompts)
	}
}

func index(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// jq's `*`: objects recurse, everything else replaces.
func TestMerge(t *testing.T) {
	obj := func(s string) map[string]any {
		m, err := parseObject([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	cases := []struct{ name, base, over, want string }{
		{"a sibling key survives a sibling being set",
			`{"issue":{"a":"1","b":"2"}}`, `{"issue":{"a":"x"}}`, `{"issue":{"a":"x","b":"2"}}`},
		{"other sections are untouched",
			`{"issue":{"a":"1"},"labels":{"pr":"p"}}`, `{"issue":{"a":"x"}}`, `{"issue":{"a":"x"},"labels":{"pr":"p"}}`},
		{"an array replaces, it does not extend",
			`{"paths":{"allow":["*.tf"]}}`, `{"paths":{"allow":["*.tofu"]}}`, `{"paths":{"allow":["*.tofu"]}}`},
		{"a scalar replaces an object",
			`{"a":{"b":1}}`, `{"a":"flat"}`, `{"a":"flat"}`},
		{"an object replaces a scalar",
			`{"a":"flat"}`, `{"a":{"b":1}}`, `{"a":{"b":1}}`},
		{"null replaces, it does not delete",
			`{"a":{"b":1}}`, `{"a":null}`, `{"a":null}`},
		{"a new key is added",
			`{"a":"1"}`, `{"z":"26"}`, `{"a":"1","z":"26"}`},
		{"recursion goes all the way down",
			`{"a":{"b":{"c":1,"d":2}}}`, `{"a":{"b":{"c":9}}}`, `{"a":{"b":{"c":9,"d":2}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, over := obj(c.base), obj(c.over)
			got, _ := json.Marshal(Merge(base, over))
			want, _ := json.Marshal(obj(c.want))
			if string(got) != string(want) {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
	t.Run("inputs are not modified", func(t *testing.T) {
		base, over := obj(`{"a":{"b":1}}`), obj(`{"a":{"c":2}}`)
		_ = Merge(base, over)
		if _, ok := base["a"].(map[string]any)["c"]; ok {
			t.Error("Merge wrote into its base argument")
		}
	})
}

// The file's own document survives beside the merge, so a verb can tell
// what the operator set from what the defaults supplied.
func TestTheUsersDocumentIsKeptApartFromTheMerge(t *testing.T) {
	dir := bare(t)
	write(t, filepath.Join(dir, ".github", "falconet.json"), `{"prompts":{"implement":"mine.md"}}`)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	prompts, _ := cfg.User["prompts"].(map[string]any)
	if len(cfg.User) != 1 || prompts["implement"] != "mine.md" {
		t.Errorf("User: %v", cfg.User)
	}
	if cfg.Schema.Prompts["pause_needs_info"] != "prompts/pause-needs-info.md" {
		t.Errorf("the merge still carries the default: %v", cfg.Schema.Prompts)
	}
	if cfg.Schema.Prompts["implement"] != "mine.md" {
		t.Errorf("the merge carries the override: %v", cfg.Schema.Prompts)
	}
	t.Run("nil when no file was found", func(t *testing.T) {
		_ = bare(t)
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.User != nil {
			t.Errorf("User: %v", cfg.User)
		}
	})
}

func TestResolutionOrder(t *testing.T) {
	dir := bare(t)
	write(t, filepath.Join(dir, ".github", "falconet.json"), `{"issue":{"queue_label":"from-dot-github"}}`)
	write(t, filepath.Join(dir, "env.json"), `{"issue":{"queue_label":"from-env"}}`)
	write(t, filepath.Join(dir, "flag.json"), `{"issue":{"queue_label":"from-flag"}}`)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Schema.Issue.QueueLabel != "from-dot-github" || cfg.File != ".github/falconet.json" {
		t.Errorf(".github/falconet.json: got %q from %q", cfg.Schema.Issue.QueueLabel, cfg.File)
	}

	t.Setenv("FALCONET_CONFIG", filepath.Join(dir, "env.json"))
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Schema.Issue.QueueLabel != "from-env" {
		t.Errorf("$FALCONET_CONFIG should beat .github/falconet.json; got %q", cfg.Schema.Issue.QueueLabel)
	}

	cfg, err = Load(filepath.Join(dir, "flag.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Schema.Issue.QueueLabel != "from-flag" {
		t.Errorf("--config should beat $FALCONET_CONFIG; got %q", cfg.Schema.Issue.QueueLabel)
	}
}

// A config that cannot be read is never a silent default.
func TestRefusals(t *testing.T) {
	dir := bare(t)
	cases := []struct {
		name, content, explicit, want string
	}{
		{"malformed JSON", `{"issue": {"queue_label": }`, "", ".github/falconet.json is not valid JSON"},
		{"a second document", `{} {}`, "", "is not valid JSON: more than one JSON value"},
		{"not an object", `["a"]`, "", "is not valid JSON: the top-level value is not an object"},
		{"--config names nothing", "", filepath.Join(dir, "nope.json"), "--config names no file"},
		{"a value of the wrong type", `{"handoff_dir": ["x"]}`, "", "does not match the schema"},
		{"a prompt override of the wrong type", `{"prompts": {"implement": 5}}`, "", "does not match the schema"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.content != "" {
				write(t, filepath.Join(dir, ".github", "falconet.json"), c.content)
			}
			_, err := Load(c.explicit)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
	t.Run("$FALCONET_CONFIG names nothing", func(t *testing.T) {
		t.Setenv("FALCONET_CONFIG", filepath.Join(dir, "nope.json"))
		_, err := Load("")
		if err == nil || !strings.Contains(err.Error(), "$FALCONET_CONFIG names no file") {
			t.Errorf("got %v", err)
		}
	})
}

func TestGetAndArray(t *testing.T) {
	dir := bare(t)
	write(t, filepath.Join(dir, ".github", "falconet.json"),
		`{"paths":{"deny_content":["zzz(","aaa("]},"prompts":{"pause-needs-info":"x.md"},"n":7,"b":true}`)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) string {
		t.Helper()
		v, err := cfg.Get(path)
		if err != nil {
			t.Fatalf("Get(%q): %v", path, err)
		}
		return Raw(v)
	}
	if got := get(".issue.queue_label"); got != "infra-request" {
		t.Errorf("got %q", got)
	}
	if got := get(".nope.deeper"); got != "null" {
		t.Errorf("a missing path prints null, as jq does; got %q", got)
	}
	if got := get(`.prompts."pause-needs-info"`); got != "x.md" {
		t.Errorf("quoted segment: got %q", got)
	}
	if p := cfg.Schema.Prompts; len(p) != 1 || p["pause-needs-info"] != "x.md" {
		t.Errorf("a user's prompts are the whole map, there being no default: got %v", p)
	}
	if got := get(".n"); got != "7" {
		t.Errorf("number as written: got %q", got)
	}
	if got := get(".b"); got != "true" {
		t.Errorf("bool: got %q", got)
	}
	items, err := cfg.Array(".paths.deny_content")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || Raw(items[0]) != "zzz(" || Raw(items[1]) != "aaa(" {
		t.Errorf("a user's denylist keeps the order they wrote it in; got %v", items)
	}
	if items, err := cfg.Array(".nope"); err != nil || len(items) != 0 {
		t.Errorf("a missing array is empty, not an error; got %v, %v", items, err)
	}
	if _, err := cfg.Array(".handoff_dir"); err == nil {
		t.Error("a string is not an array, and iterating it is an error")
	}
	for _, bad := range []string{"", "issue", ".a..b", `.a."unterminated`} {
		if _, err := cfg.Get(bad); err == nil {
			t.Errorf("Get(%q) should be an error", bad)
		}
	}
}

func TestRawStructured(t *testing.T) {
	v := []any{"a", json.Number("1")}
	if got := Raw(v); got != "[\n  \"a\",\n  1\n]" {
		t.Errorf("structured values print as indented JSON; got %q", got)
	}
}

func TestStackMissing(t *testing.T) {
	bare(t)
	cfg, _ := Load("")
	msg := cfg.StackMissing("plan", "dns", "/repo")
	for _, want := range []string{".stacks.plan", `"dns"`, "/repo", ".github/falconet.json"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
}
