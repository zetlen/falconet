package main

import (
	"strings"
	"testing"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/prompts"
)

// A template shaped like the shipped prompt: the paragraph that names the
// denylist stands between two others, and the placeholders sit inside
// sentences, where a dangling "contains ." would be read by the agent.
const template = "Edit {allow}. Nothing else.\n\n" +
	"The same stage refuses a file that contains {deny}, wherever it\nappears.\n\n" +
	"Write to {handoff}/commit-msg.txt under {workspace}.\n"

// paths.deny_content may be empty — an operator whose checks run no content
// at all has nothing to deny — and then the sentence about refused content
// must go, not read "contains ." or "contains {deny}". The paragraph is the
// unit that goes, so the prompt's author decides its shape and the renderer
// never has to know where a sentence ends.
func TestAnEmptyDenylistTakesItsParagraphWithIt(t *testing.T) {
	got := render(template, "/h", "/w", []string{"*.tf"}, nil)
	want := "Edit `*.tf`. Nothing else.\n\nWrite to /h/commit-msg.txt under /w."
	if got != want {
		t.Errorf("render with no denylist:\n got: %q\nwant: %q", got, want)
	}
	for _, leftover := range []string{"{deny}", "contains", "refuses", "\n\n\n"} {
		if strings.Contains(got, leftover) {
			t.Errorf("render with no denylist left %q behind: %q", leftover, got)
		}
	}
}

// With something to deny, the paragraph stays and the entries are spelled
// as the operator wrote them, in config order — the guard tests them in
// that order, and the prompt must not tell a different story.
func TestADenylistIsRenderedAsWrittenAndInOrder(t *testing.T) {
	got := render(template, "/h", "/w", []string{"*.tf"}, []string{`data "external"`, "templatefile(", "file("})
	want := "The same stage refuses a file that contains `data \"external\"`, `templatefile(` or `file(`, wherever it\nappears."
	if !strings.Contains(got, want) {
		t.Errorf("render:\n got: %q\nwant it to contain: %q", got, want)
	}
}

// Globs are the operator's, character for character: `*` and `**` and a
// directory prefix are the allowlist, and a prompt that "tidied" them would
// tell the agent about a guard that does not exist.
func TestGlobsAreRenderedAsWritten(t *testing.T) {
	for _, tc := range []struct {
		allow []string
		want  string
	}{
		{[]string{"*.tf"}, "Edit `*.tf`."},
		{[]string{"docs/*.md", "config/**"}, "Edit `docs/*.md` or `config/**`."},
		{[]string{"a/**/*.yaml", "b/*.json", "c d.txt"}, "Edit `a/**/*.yaml`, `b/*.json` or `c d.txt`."},
		// An empty allowlist refuses every path; the prompt says so rather
		// than printing "Edit ." and letting the agent guess.
		{nil, "Edit nothing."},
	} {
		got := render(template, "/h", "/w", tc.allow, []string{"x"})
		if !strings.Contains(got, tc.want) {
			t.Errorf("render(allow=%q):\n got: %q\nwant it to contain: %q", tc.allow, got, tc.want)
		}
	}
}

// One pass over the text: a substituted value is never itself scanned for
// placeholders, so a handoff directory literally named `{allow}` is a
// directory, not an expansion.
func TestSubstitutionIsOnePass(t *testing.T) {
	got := render("{handoff} {allow}", "/tmp/{allow}", "/w", []string{"*.tf"}, nil)
	if want := "/tmp/{allow} `*.tf`"; got != want {
		t.Errorf("render: got %q, want %q", got, want)
	}
}

// The shipped prompt, rendered against the built-in defaults, is the prompt
// every adopter's agent reads until they override it. It carries every
// placeholder the verb knows and nothing of the repository falconet was
// extracted from; the guards it describes are the config's.
func TestTheShippedPromptRendersFromTheDefaults(t *testing.T) {
	t.Setenv("FALCONET_CONFIG", "")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.File != "" {
		t.Fatalf("config.Load read %s; this test wants the defaults", cfg.File)
	}
	text, ok := prompts.Read("implement")
	if !ok {
		t.Fatal("no shipped implement prompt")
	}
	for _, placeholder := range []string{"{handoff}", "{allow}", "{deny}"} {
		if !strings.Contains(string(text), placeholder) {
			t.Errorf("the shipped prompt does not use %s", placeholder)
		}
	}

	got := render(string(text), "/h", "/w", cfg.Schema.Paths.Allow, cfg.Schema.Paths.DenyContent)
	for _, want := range []string{
		"Scripts do the mechanics; you do the judgment.",
		"files whose path matches `*.tf`.",
		"contains `data \"external\"`, `provisioner`, `local-exec`, `remote-exec`, `templatefile(`, `filebase64(` or `file(`,",
		"/h/request.md",
		"/h/commit-msg.txt",
		"/h/needs-info.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the rendered default prompt lacks %q", want)
		}
	}
	for _, leftover := range []string{
		"{handoff}", "{workspace}", "{allow}", "{deny}",
		// The origin repository, which used to be the prompt's standing facts.
		"Namecheap", "Google Workspace", "papernapkin", "records-*.tf", "guards*.tf",
		// The origin's tooling: the prompt is for whatever checks the
		// repository runs on its pull requests.
		"tofu", "terraform", "HCL",
		// The narrow grant, by name: the prompt must not suggest a shell.
		"Bash",
	} {
		if strings.Contains(got, leftover) {
			t.Errorf("the rendered default prompt contains %q", leftover)
		}
	}

	// The same prompt with nothing to deny: the guard paragraph is gone and
	// the paragraphs around it are still separated by one blank line.
	got = render(string(text), "/h", "/w", cfg.Schema.Paths.Allow, nil)
	for _, leftover := range []string{"{deny}", "refuses a changed file", "\n\n\n"} {
		if strings.Contains(got, leftover) {
			t.Errorf("the default prompt with an empty denylist contains %q", leftover)
		}
	}
}
