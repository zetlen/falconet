// falconet — turn a plain-language infrastructure request into a pull request
// a person can review, and stop there.
//
// This is a dispatcher and nothing else. It resolves a verb and hands over,
// so the verb owns its own stdout, its own exit code, and its own argument
// parsing. Nothing is interpreted on the way through: a verb that prints one
// word prints one word, and this file is not in a position to add to it.
//
//	falconet <verb> [args]
//
// The five pipeline verbs are the stages of the pipeline (docs/decisions.md).
// They never call each other; they pass files through the handoff directory.
//
// `prompt`, `scan` and `config` are unlisted on purpose: public in the
// sense that they work, not in the sense that they are vocabulary. `prompt`
// is the workflow's plumbing — a prompt resolved without heredocs in YAML —
// `scan` is the commit verb's secret scan, and `config` is what the config
// file resolves to. Each is reachable here so that the test suite spawns it
// through the same door as every verb.
//
// Exit codes, uniform across every verb:
//
//	0  an outcome was determined (which outcome is on stdout, in one word)
//	1  refused mechanically, or a check failed
//	2  usage error — including -h/--help, because 0 would mean "ran, fine"
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/handoff"
)

const usageText = `Usage: falconet <verb> [args]

  prepare   gate an issue, assign it, open a branch, lay out the handoff
  check     run the repository's own check on the tree the agent left, and say
            whether it passed
  commit    read the agent's work off the tree and commit it through the guards
  push      get the branch onto the remote the moment a commit exists
  pause     put an issue into a terminal state and say so where it will be read
  version   print the version and the toolchain this binary was built with

Run ` + "`falconet <verb> -h`" + ` for a verb's own options.
`

// The verbs usage lists, and the unlisted doors. The two lists are the
// dispatcher's whole knowledge of what exists; a name in neither is a usage
// error.
var (
	verbs    = []string{"prepare", "check", "commit", "push", "pause", "version"}
	unlisted = []string{"prompt", "scan", "config"}
)

// native is what this binary answers for: one entry per name in the two
// lists above, and main_test.go holds the three in step. Through the port
// (ADR-0006 D3 step 2) a known verb without an entry here was handed to its
// bash script by a fallback; #19 deleted the scripts and the fallback with
// them, so a verb that is known and not implemented is a build defect the
// test refuses, never a runtime path.
var native = map[string]func(args []string) int{
	"version": runVersion,
	"prepare": runPrepare,
	"config":  runConfig,
	"check":   runCheck,
	"commit":  runCommit,
	"scan":    runScan,
	"push":    runPush,
	"pause":   runPause,
	"prompt":  runPrompt,
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		return usage("no verb given")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "-h", "--help", "help":
		return usage("")
	}
	if !known(verb) {
		return usage(fmt.Sprintf("unknown verb '%s'", verb))
	}
	fn, ok := native[verb]
	if !ok {
		// Unreachable while main_test.go passes. Said out loud rather than
		// panicked, because a dispatcher's stdout belongs to the verb's
		// outcome word and a stack trace is not one.
		fmt.Fprintf(os.Stderr, "falconet: verb '%s' is known and not implemented: a build defect\n", verb)
		return 1
	}
	return fn(rest)
}

func known(verb string) bool {
	for _, v := range verbs {
		if v == verb {
			return true
		}
	}
	for _, v := range unlisted {
		if v == verb {
			return true
		}
	}
	return false
}

// usage goes to stderr, always: stdout belongs to the outcome word. The exit
// code is 2 even for --help, because 0 would mean a verb ran and was happy.
func usage(complaint string) int {
	if complaint != "" {
		fmt.Fprintf(os.Stderr, "falconet: %s\n\n", complaint)
	}
	fmt.Fprint(os.Stderr, usageText)
	return 2
}

func runVersion(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "falconet: version takes no arguments")
		return 2
	}
	fmt.Printf("falconet %s (%s %s/%s)\n", resolvedVersion(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return 0
}

// resolvedVersion is what this binary calls itself: the module version the
// go command recorded, else "dev". version prints it.
//
// There is no build-time stamp. Every falconet that is not a checkout build
// is `go install github.com/zetlen/falconet/cmd/falconet@<ref>` — in CI
// through action.yml, on a laptop by hand — and that path accepts no
// ldflags: the module proxy hands the go command a source zip, so nothing
// could stamp a version on the way through. What the go command does
// record is the version it resolved the ref to, in the binary's build
// info: the tag itself for a tag, a pseudo-version for a branch or a
// commit. So ask for that. "(devel)" is what a local `go build` from a
// checkout puts there, and that is "dev".
func resolvedVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if m := info.Main.Version; m != "" && m != "(devel)" {
			return m
		}
	}
	return "dev"
}

// --- config -----------------------------------------------------------------
//
// Print what internal/config and internal/handoff would tell a verb. Unlisted
// on purpose: the libraries have no process to spawn at, and the suite's rule
// is that no test reaches inside its subject. This is that process, and
// config.test.sh is what it answers to.

const configUsageText = `config — print what the config file resolves to, and where the handoff goes.

Modes:
  config [--config FILE] file              the path that was read, or nothing
  config [--config FILE] get <jq-path>     one value
  config [--config FILE] array <jq-path>   one element per line, in order
  config [--config FILE] handoff [DIR]     the resolved handoff directory
  config [--config FILE] env <KEY=value>   append to $GITHUB_ENV, if any

--config is the flag each verb parses for itself, with the same meaning: an
explicit file that beats $FALCONET_CONFIG and ./.github/falconet.json.
Resolution is relative to the working directory, as it is for a verb.

Exit codes: 0 = printed, 1 = the config could not be read, 2 = usage.
`

func configUsage() int {
	fmt.Fprint(os.Stderr, configUsageText)
	return 2
}

func runConfig(args []string) int {
	explicit := ""
flags:
	for len(args) > 0 {
		switch a := args[0]; {
		case a == "--config":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "falconet: --config needs a file")
				return 2
			}
			explicit = args[1]
			args = args[2:]
		case a == "-h" || a == "--help":
			return configUsage()
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", a)
			return configUsage()
		default:
			break flags
		}
	}

	cfg, err := config.Load(explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
		return 1
	}

	op, rest := "", []string{}
	if len(args) > 0 {
		op, rest = args[0], args[1:]
	}
	arg := func(what string) (string, bool) {
		if len(rest) == 0 {
			fmt.Fprintf(os.Stderr, "falconet: config %s needs %s\n", op, what)
			return "", false
		}
		return rest[0], true
	}

	switch op {
	case "file":
		fmt.Println(cfg.File)
	case "get":
		path, ok := arg("a jq path")
		if !ok {
			return 2
		}
		v, err := cfg.Get(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		fmt.Println(config.Raw(v))
	case "array":
		path, ok := arg("a jq path")
		if !ok {
			return 2
		}
		items, err := cfg.Array(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		for _, item := range items {
			fmt.Println(config.Raw(item))
		}
	case "handoff":
		dir := ""
		if len(rest) > 0 {
			dir = rest[0]
		}
		resolved, err := handoff.Init(dir, cfg, cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		fmt.Println(resolved)
	case "env":
		kv, ok := arg("KEY=value")
		if !ok {
			return 2
		}
		if _, err := handoff.Init("", cfg, cwd); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
		if err := handoff.GitHubEnvAppend(kv); err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "config: unknown operation '%s'\n", op)
		return configUsage()
	}
	return 0
}

// digits is what an issue number must be, whole. The number goes into
// comments and bodies verbatim, so "a number" is a claim worth checking
// rather than an assumption.
var digits = regexp.MustCompile(`^[0-9]+$`)

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
