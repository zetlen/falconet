// Package prompts is the shipped prompts, embedded in the binary.
//
// They are embedded so that "with no override the shipped prompts/<name>.md
// is printed" needs no checkout to be true: the prompt is in the binary, the
// default config names no path, and the config key is an override and
// nothing else. A consumer's repository need not carry a copy for the
// default to resolve.
//
// The .md files stay beside this file, at the path the README links, so a
// consumer with a copy of its own has something to diff it against.
package prompts

import "embed"

// FS holds every prompts/*.md, by file name, at compile time: `go build` and
// `go install` both carry them, and `go vet` refuses a pattern that matches
// nothing.
//
//go:embed *.md
var FS embed.FS

// Read returns the shipped prompt called name — the file name without its
// .md — and whether there is one. An embed.FS opens nothing but a clean
// relative file name, so a name with a path in it (`../README`, say) is
// simply not a prompt rather than a file read off the tool's own tree.
func Read(name string) ([]byte, bool) {
	data, err := FS.ReadFile(name + ".md")
	if err != nil {
		return nil, false
	}
	return data, true
}
