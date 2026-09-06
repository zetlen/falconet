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
// Four placeholders are substituted on the way out. Two are what let one
// prompt serve CI and a workstation:
//
//	{handoff}     the absolute handoff directory
//	{workspace}   the absolute repository root
//
// Neither is an Actions template expression: the same prompt text is what
// runs in CI and on a workstation.
//
// Two are what let one prompt serve every repository:
//
//	{allow}       paths.allow, each glob in backticks, as a list: `*.tf`,
//	              or `docs/*.md` or `config/**`
//	{deny}        paths.deny_content, the same way, in config order
//
// The guard reads the config; the prompt reads the same config, so what the
// agent is told it may touch is what the commit stage will enforce, and the
// prompt carries nothing of any particular repository's. Standing facts
// belong in the repository's own AGENTS.md, which the prompt binds the agent
// to, or in an override.
//
// An empty paths.deny_content has nothing to name, and a sentence reading
// "contains ." is worse than no sentence: every paragraph (a run of lines
// between blank lines) that contains {deny} is dropped when the list is
// empty. The paragraph is the unit so that the prompt's author, not this
// verb, decides where the sentence about refused content starts and ends.
//
// The shipped copy is the prompts package, embedded in this binary. No
// default points anywhere; the config key is an override or it is absent.
//
// Exit codes: 0 = printed, 1 = no such prompt, 2 = usage error.

import (
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
binary is printed. Four placeholders are substituted on the way out:

  {handoff}     the absolute handoff directory: --out-dir, else
                handoff_dir from config against the repository root.
                Resolved, not created — printing a prompt is a read.
  {workspace}   the absolute repository root
  {allow}       paths.allow from config, each glob in backticks, as a
                list: ` + "`*.tf`" + `, or ` + "`docs/*.md` or `config/**`" + `
  {deny}        paths.deny_content, the same way, in config order. When
                the list is empty, every paragraph containing {deny} is
                dropped rather than left naming nothing.

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

	out, rc := resolvePrompt(name, cfg, root, outDir)
	if rc != 0 {
		return rc
	}
	if _, err := os.Stdout.WriteString(out + "\n"); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot write to stdout: %v\n", err)
		return 1
	}
	return 0
}

// resolvePrompt is the whole of a prompt lookup: the config's override if
// there is one and the shipped copy otherwise, rendered against the handoff
// directory (outDir, or the config's, resolved and not created) and the
// repository root. It is shared by the prompt verb, which prints the text,
// and the implement verb, which hands it to the harness. Returns the text
// and 0, or "" and the exit code the verb should end with, having said why
// on stderr.
func resolvePrompt(name string, cfg *config.Config, root, outDir string) (string, int) {
	key := strings.ReplaceAll(name, "-", "_")
	var text []byte
	if override := cfg.Schema.Prompts[key]; override != "" {
		path := override
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if !isRegularFile(path) {
			fmt.Fprintf(os.Stderr, "prompt: '%s' points at a file that is not there: %s\n", name, override)
			return "", 1
		}
		var err error
		text, err = os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", path, err)
			return "", 1
		}
	} else {
		var ok bool
		if text, ok = prompts.Read(name); !ok {
			fmt.Fprintf(os.Stderr, "prompt: no prompt named '%s'\n", name)
			return "", 1
		}
	}

	// The handoff directory is resolved but NOT created: printing a prompt is a
	// read, and a caller asking what the text says should not leave a directory
	// behind. handoff.Init creates, so this repeats its resolution instead.
	hd := handoff.Resolve(outDir, cfg, root)
	return render(string(text), hd, root, cfg.Schema.Paths.Allow, cfg.Schema.Paths.DenyContent), 0
}

// render substitutes the four placeholders into a prompt's text. Trailing
// newlines are stripped and the caller puts exactly one back, so the output
// ends in one newline whatever the file ended in.
//
// Paragraphs naming {deny} go first, when there is nothing to deny; then one
// pass replaces every placeholder, so a value is never itself scanned for
// placeholders — a handoff directory called `{allow}` is a directory.
func render(text, handoff, workspace string, allow, deny []string) string {
	out := strings.TrimRight(text, "\n")
	if len(deny) == 0 {
		out = withoutParagraphsNaming(out, "{deny}")
	}
	return strings.NewReplacer(
		"{handoff}", handoff,
		"{workspace}", workspace,
		"{allow}", spoken(allow),
		"{deny}", spoken(deny),
	).Replace(out)
}

// withoutParagraphsNaming drops every paragraph — a run of lines between
// blank lines — that contains needle, and leaves the blank line between the
// paragraphs on either side of it.
func withoutParagraphsNaming(text, needle string) string {
	paragraphs := strings.Split(text, "\n\n")
	kept := paragraphs[:0]
	for _, p := range paragraphs {
		if !strings.Contains(p, needle) {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// spoken renders a config list the way a sentence names alternatives: each
// entry in backticks, exactly as the operator wrote it and in the order the
// guard tests it, joined with commas and a final "or". An empty list is the
// word "nothing" — an allowlist that matches nothing refuses every path, and
// the prompt should say so rather than print "matches ." and let the agent
// guess.
func spoken(items []string) string {
	if len(items) == 0 {
		return "nothing"
	}
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = "`" + s + "`"
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}
