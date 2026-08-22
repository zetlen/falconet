package main

// scan — the unlisted door to internal/scan, so that scan.test.sh spawns the
// guard through the same binary as every verb. Not vocabulary: see the
// package's header for why it is not a verb and must not become one.

import (
	"fmt"
	"os"
	"strings"

	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/scan"
)

const scanUsageText = `scan — read the text this pipeline is about to publish and stop the run if
any of it is shaped like a credential. Internal: the commit verb is its only
caller, and this door exists for the test suite.

Modes:
  falconet scan [--staged] [--] [FILE ...]

    FILE      scanned if it exists and is non-empty; a missing or empty
              file is not a finding and not an error, because the pipeline
              legitimately produces runs with no questions, or no message
    --staged  additionally scan ` + "`git diff --cached`" + ` of the repository this
              is run in — the change as it is about to be committed

Prints, on STDOUT, one line naming each target that matched, and nothing
else. Names only: this never repeats a matched value, on any stream,
because its caller writes that text into a comment on a public-facing
issue. gitleaks' own output goes to STDERR with --redact, so the run log
gets the rule that fired and the line number — enough to triage — with the
secret itself replaced by REDACTED.

Exit codes: 0 = nothing matched
            1 = the scan could not be run (gitleaks missing, or it died).
                Fail closed: the caller must treat this exactly as unsafe,
                never as clean.
            2 = usage error (including --help, which must not exit 0 —
                0 means "scanned, nothing found")
            3 = at least one target matched. Nothing may be published.

$GITLEAKS overrides the binary, for the tests and for a local run.
`

func scanUsage() int {
	fmt.Fprint(os.Stderr, scanUsageText)
	return 2
}

func runScan(args []string) int {
	staged := false
	var files []string
	for len(args) > 0 {
		switch a := args[0]; {
		case a == "--staged":
			staged = true
			args = args[1:]
		case a == "-h" || a == "--help":
			return scanUsage()
		case a == "--":
			files = append(files, args[1:]...)
			args = nil
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", a)
			return scanUsage()
		default:
			files = append(files, a)
			args = args[1:]
		}
	}
	if !staged && len(files) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to scan: pass at least one file, or --staged")
		return scanUsage()
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
		return 1
	}
	root, err := repo.Root(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}

	scanner := &scan.Scanner{Gitleaks: envOr("GITLEAKS", "gitleaks"), Root: root, Stderr: os.Stderr}
	hit, err := scanner.Scan(files, staged, func(label string) { fmt.Println(label) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan: %v\n", err)
		return 1
	}
	if hit {
		return scan.Hit
	}
	return 0
}
