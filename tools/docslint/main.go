// docslint holds the shape of this repository's records.
//
// The charter names invariants; the register lists every live decision with
// the invariant it serves, the observation that should retire it, and a
// section in the same file that records it. None of that survives contact
// with a busy week unless something refuses a row that skips it — the same
// reason tests/contract.test.sh holds the wiring's shape rather than trusting
// it. docs/history/ is link-checked and nothing more: it records how the
// decisions were reached, and is not read for what they are.
//
// It is Go and the standard library because this repository pins exactly one
// binary and adding a dependency to check a document is a decision, not a
// convenience (AGENTS.md). What is checked here is not really markdown
// structure anyway: it is the referential integrity between two documents,
// which no tree parser would answer for.
//
// Run it directly (`make lint-docs`), or let `go test ./...` run it against
// this tree — TestTheRecordsInThisRepository does exactly that.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "repository root to check")
	flag.Parse()

	findings, err := lint(os.DirFS(*root))
	if err != nil {
		fmt.Fprintf(os.Stderr, "docslint: %v\n", err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "\ndocslint: %d problem(s). docs/charter.md says what these fields are for.\n", len(findings))
		os.Exit(1)
	}
	fmt.Println("docslint: the records agree")
}
