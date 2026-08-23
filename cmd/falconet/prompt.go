package main

// prompt — print a prompt, from the config's override if there is one and
// from the shipped copy otherwise.
//
// Unlisted on purpose. It exists so the action wrappers can keep prompts
// config-driven without embedding heredocs in YAML, which is how a prompt
// picks up the indentation of the block scalar it was written in and starts
// rendering as a code block. It is public in the sense that it works, not in
// the sense that it is vocabulary.
//
// The name is looked up at `prompts.<name>` in the config — with `-` folded to
// `_`, so `falconet prompt pause-needs-info` finds `prompts.pause_needs_info`.
// An override is a path relative to the repository root. With no override the
// shipped `prompts/<name>.md` is printed.
//
// Two placeholders are substituted on the way out, which is what lets one
// prompt serve CI and a workstation:
//
//	{handoff}     the absolute handoff directory
//	{workspace}   the absolute repository root
//
// The origin's prompt spelled these as `${{ github.workspace }}/.ci-handoff/`,
// an Actions template expression that means nothing anywhere else — and the
// whole point of a CLI-first design is that the same prompt text is what runs
// locally.
//
// The shipped copy is the prompts package, embedded in this binary: the bash
// read it from under FALCONET_HOME, and its default config pointed every
// consumer at a path in their own repository instead (issue #3). There is no
// default to point anywhere now; the config key is an override or it is
// absent.
//
// Exit codes: 0 = printed, 1 = no such prompt, 2 = usage error.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/handoff"
	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/prompts"
)

const promptUsageText = `prompt — print a prompt, from the config's override if there is one and
from the shipped copy otherwise.

Modes:
  falconet prompt NAME [--config FILE] [--out-dir DIR]

NAME is looked up at prompts.<name> in the config, with - folded to _, so
"falconet prompt pause-needs-info" finds prompts.pause_needs_info. A value
there is a path relative to the repository root (absolute allowed) and it
must exist; with none, the shipped prompts/<name>.md embedded in this
binary is printed. Two placeholders are substituted on the way out:

  {handoff}     the absolute handoff directory: --out-dir, else
                handoff_dir from config against the repository root.
                Resolved, not created — printing a prompt is a read.
  {workspace}   the absolute repository root

Unlisted on purpose: public in the sense that it works, not in the sense
that it is vocabulary.

Exit codes: 0 = printed, 1 = no such prompt, 2 = usage error.
`

func promptUsage() int {
	fmt.Fprint(os.Stderr, promptUsageText)
	return 2
}

func runPrompt(args []string) int {
	var name, explicit, outDir string
	for len(args) > 0 {
		flag := args[0]
		value := func(what string) (string, bool) {
			if len(args) < 2 || args[1] == "" {
				fmt.Fprintf(os.Stderr, "%s needs %s\n", flag, what)
				return "", false
			}
			return args[1], true
		}
		switch {
		case flag == "--config":
			v, ok := value("a file")
			if !ok {
				return 2
			}
			explicit = v
			args = args[2:]
		case flag == "--out-dir":
			v, ok := value("a directory")
			if !ok {
				return 2
			}
			outDir = v
			args = args[2:]
		case flag == "-h" || flag == "--help":
			return promptUsage()
		case strings.HasPrefix(flag, "-"):
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return promptUsage()
		default:
			if name != "" {
				fmt.Fprintln(os.Stderr, "one prompt at a time")
				return 2
			}
			name = flag
			args = args[1:]
		}
	}
	if name == "" {
		return promptUsage()
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
		return 1
	}
	// --out-dir is the caller's: resolved against where they stand, before
	// this process moves to the repository root to read config there.
	if outDir != "" && !filepath.IsAbs(outDir) {
		outDir = filepath.Join(cwd, outDir)
	}
	root, err := repo.Root(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot enter %s: %v\n", root, err)
		return 1
	}
	cfg, err := config.Load(explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}

	key := strings.ReplaceAll(name, "-", "_")
	var text []byte
	if override := cfg.Schema.Prompts[key]; override != "" {
		path := override
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if !isRegularFile(path) {
			fmt.Fprintf(os.Stderr, "prompt: '%s' points at a file that is not there: %s\n", name, override)
			return 1
		}
		text, err = os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", path, err)
			return 1
		}
	} else {
		var ok bool
		if text, ok = prompts.Read(name); !ok {
			fmt.Fprintf(os.Stderr, "prompt: no prompt named '%s'\n", name)
			return 1
		}
	}

	// The handoff directory is resolved but NOT created: printing a prompt is a
	// read, and a caller asking what the text says should not leave a directory
	// behind. handoff.Init creates, so this repeats its resolution instead.
	hd := handoff.Resolve(outDir, cfg, root)

	// The file's bytes, trailing newlines stripped and exactly one put back:
	// what `out="$(cat "$path")"` followed by `printf '%s\n'` always printed.
	out := string(bytes.TrimRight(text, "\n"))
	out = strings.ReplaceAll(out, "{handoff}", hd)
	out = strings.ReplaceAll(out, "{workspace}", root)
	if _, err := os.Stdout.WriteString(out + "\n"); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot write to stdout: %v\n", err)
		return 1
	}
	return 0
}
