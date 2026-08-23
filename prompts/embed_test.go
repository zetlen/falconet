package prompts

import (
	"strings"
	"testing"
)

// The three the README and the workflow name, by the name a caller gives the
// verb, dashes and all.
func TestTheShippedPromptsAreEmbedded(t *testing.T) {
	for name, want := range map[string]string{
		"implement":        "Scripts do the mechanics; you do the judgment.",
		"pause-needs-info": "I need a bit more from you",
		"pause-failure":    "This one needs a person.",
	} {
		data, ok := Read(name)
		if !ok {
			t.Errorf("Read(%q): not embedded", name)
			continue
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("Read(%q) lacks %q", name, want)
		}
	}
}

// A name is a file name and nothing else. The bash verb pasted it into a
// path, so `prompt ../README` printed the README.
func TestANameWithAPathInItIsNotAPrompt(t *testing.T) {
	for _, name := range []string{"../README", "../prompts/implement", "./implement", "/implement", "", ".", "..", "pause_needs_info", "nosuch"} {
		if _, ok := Read(name); ok {
			t.Errorf("Read(%q) found something", name)
		}
	}
}
