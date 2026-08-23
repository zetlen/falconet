// Package prompts is the shipped prompts, embedded in the binary.
//
// They are embedded because of issue #3. The bash `prompt` verb documented
// "with no override the shipped prompts/<name>.md is printed" and could not
// do it: the default config itself set prompts.implement to
// prompts/implement.md, a path relative to the CONSUMER's repository, so a
// consumer that had not copied the prompts in met "points at a file that is
// not there", and the shipped copy — in the tool's own checkout — was
// unreachable by any documented move. A binary has no checkout for a
// default to fail to resolve against: the prompt is in it, the default
// config no longer names a path, and the config key is an override and
// nothing else. The bug is impossible rather than fixed.
//
// The .md files stay beside this file, at the path the README links and the
// path the first consumer's AGENTS.md prescribes diffing its copy against.
package prompts

import "embed"

// FS holds every prompts/*.md, by file name, at compile time: `go build` and
// `go install` both carry them, and `go vet` refuses a pattern that matches
// nothing.
//
//go:embed *.md
var FS embed.FS

// Read returns the shipped prompt called name — the file name without its
// .md — and whether there is one. The bash built "<tool>/prompts/$NAME.md"
// from the name as given, so `prompt ../README` printed falconet's own
// README; an embed.FS opens nothing but a clean relative file name, so a
// name with a path in it is simply not a prompt.
func Read(name string) ([]byte, bool) {
	data, err := FS.ReadFile(name + ".md")
	if err != nil {
		return nil, false
	}
	return data, true
}
