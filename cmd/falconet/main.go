// falconet — turn a plain-language infrastructure request into a pull request
// carrying a real plan, and stop there.
//
// This is a dispatcher and nothing else. It resolves a verb and hands over,
// so the verb owns its own stdout, its own exit code, and its own argument
// parsing. Nothing is interpreted on the way through: a verb that prints one
// word prints one word, and this file is not in a position to add to it.
//
//	falconet <verb> [args]
//
// The six verbs are the stages of the pipeline (docs/adr/0003-the-cli-surface.md).
// They never call each other; they pass files through the handoff directory.
//
// `prompt`, `scan`, `config` and `review-verdict` are unlisted on purpose:
// public in the sense that they work, not in the sense that they are
// vocabulary. The first two are internal plumbing, `config` is what the
// config file resolves to, and `review-verdict` ships unwired (ADR-0003, as
// amended 2026-08-22). Each is reachable here so that the test suite spawns
// it through the same door as every verb.
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
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/handoff"
)

// version is stamped at build time: -ldflags "-X main.version=v0.1.0", which
// is what the Makefile's release targets pass and what a release asset
// carries. A binary built any other way says "dev" — except the one other way
// ADR-0006 D6 blesses, `go install …@<tag>`, which passes no ldflags at all
// and is handled in runVersion.
var version = "dev"

const usageText = `Usage: falconet <verb> [args]

  prepare   gate an issue, assign it, open a branch, capture a baseline plan
  commit    read the agent's work off the tree and commit it through the guards
  push      get the branch onto the remote the moment a commit exists
  validate  validate and plan every configured stack, collecting failures
  pause     put an issue into a terminal state and say so where it will be read
  assemble  build a pull-request body carrying the whole plan
  doctor    check a repository against the install steps, and say which are missing
  init      do the install steps: the labels, the secrets, the files, one commit
  version   print the version and the toolchain this binary was built with

Run ` + "`falconet <verb> -h`" + ` for a verb's own options.
`

// The verbs, and the unlisted doors. Both lists are the dispatcher's whole
// knowledge of what exists; a name in neither is a usage error.
var (
	verbs    = []string{"prepare", "commit", "push", "pause", "validate", "assemble"}
	unlisted = []string{"prompt", "scan", "config", "review-verdict"}
)

// native is what this binary implements itself. Every other known verb is
// handed to its bash script by fallback until its port lands (ADR-0006 D3
// step 2); the map grows one entry per port, and fallback is deleted in #19.
var native = map[string]func(args []string) int{
	"version":        runVersion,
	"prepare":        runPrepare,
	"config":         runConfig,
	"assemble":       runAssemble,
	"commit":         runCommit,
	"scan":           runScan,
	"push":           runPush,
	"pause":          runPause,
	"validate":       runValidate,
	"prompt":         runPrompt,
	"review-verdict": runReviewVerdict,
	"doctor":         runDoctor,
	"init":           runInit,
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
	if fn, ok := native[verb]; ok {
		return fn(rest)
	}
	return fallback(verb, rest)
}

func known(verb string) bool {
	if _, ok := native[verb]; ok {
		return true
	}
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

// fallback hands a verb this binary has not implemented yet to its bash
// script, and is the mechanism that keeps every commit of the port green
// against the whole suite (ADR-0006 D3). It is deliberately SILENT: the
// script gets stdin, stdout, stderr and the exit code untouched, exactly as
// bin/falconet's exec gave them — review-verdict.test.sh asserts an empty
// stderr on a run that comes through here, and prepare.test.sh reads stderr
// in ten places. The scripts locate lib/ from their own $BASH_SOURCE, so the
// path is all they need from us.
//
// Deleted in #19, along with the scripts.
func fallback(verb string, args []string) int {
	home := os.Getenv("FALCONET_HOME")
	if home == "" {
		fmt.Fprintf(os.Stderr, "falconet: verb '%s' is not implemented in this binary yet "+
			"(set FALCONET_HOME to a falconet checkout to run its bash implementation)\n", verb)
		return 1
	}
	target := filepath.Join(home, "libexec", "falconet", verb+".sh")
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: verb '%s' is not implemented yet (no %s)\n", verb, target)
		return 1
	}
	if info.Mode()&0o111 == 0 {
		fmt.Fprintf(os.Stderr, "falconet: verb '%s' is not executable (%s)\n", verb, target)
		return 1
	}
	argv := append([]string{target}, args...)
	err = syscall.Exec(target, argv, os.Environ())
	// Exec replaces this process; it only returns when it could not.
	fmt.Fprintf(os.Stderr, "falconet: cannot exec %s: %v\n", target, err)
	return 1
}

func runVersion(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "falconet: version takes no arguments")
		return 2
	}
	fmt.Printf("falconet %s (%s %s/%s)\n", resolvedVersion(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return 0
}

// resolvedVersion is what this binary calls itself: the build-time stamp,
// else the module version the go command recorded, else "dev". version
// prints it; init pins the caller workflow's `uses:` to it (ADR-0006 D6:
// one coordinate).
//
// `go install github.com/zetlen/falconet/cmd/falconet@v0.1.0` is the
// second install path ADR-0006 D6 names, and it accepts no ldflags: the
// module proxy hands the go command a source zip, so nothing can stamp
// `version` on the way through and every binary installed that way would
// have said "dev" — for the one audience, people on laptops, whose only
// way to know what they are running is this line. The go command records
// the module version it resolved, so ask for it.
//
// Only when the stamp is absent, so a release asset always reports the
// tag it was built for and never something a proxy computed. "(devel)" is
// what a local `go build` puts there, which is what "dev" already says.
func resolvedVersion() string {
	v := version
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			if m := info.Main.Version; m != "" && m != "(devel)" {
				v = m
			}
		}
	}
	return v
}

// --- config -----------------------------------------------------------------
//
// Print what internal/config and internal/handoff would tell a verb. Unlisted
// on purpose: the libraries have no process to spawn at, and the suite's rule
// is that no test reaches inside its subject. This is that process, and the
// bash libexec/falconet/config.sh answers the same tests.

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
