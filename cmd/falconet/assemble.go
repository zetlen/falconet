package main

// assemble — the first verb the binary answers for itself (ADR-0006 D3 step
// 2). The guard logic is internal/assemble, which carries the PR #28 record;
// this file is the flags, the files, and the exit code, and nothing else.

import (
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/zetlen/falconet/internal/assemble"
)

const assembleUsageText = `assemble — build a pull-request body that always ships the WHOLE plan.

Usage:
  falconet assemble --body FILE --plan FILE --issue N --out FILE
                    [--run-url URL] [--plan-url URL] [--limit N]

  --body     PR description with NO plan output in it — the body of the
             implementing agent's commit message, whose prompt tells it
             not to quote, summarize or abridge the plan
  --plan     full ` + "`tofu plan -no-color`" + ` output
  --issue    issue number; a "Closes #N" line is appended after the body
  --out      destination for the assembled markdown
  --run-url  workflow run URL, cited by the truncation note when
             --plan-url is absent
  --plan-url download URL for the plan uploaded as a workflow artifact.
             Optional: every caller that omits it gets exactly the
             unflagged output, byte for byte. When given, its link is
             always printed next to the plan block — even when the plan
             fit inline — so a reviewer never has to fall back to the run
             log to get the untruncated file. On overflow, the truncation
             note cites THIS url instead of the run log.
  --limit    maximum body size (default 65536, GitHub's hard limit)

The plan is wrapped in <details><summary>tofu plan output</summary> and a
fence long enough to survive any backticks the plan itself contains.

If the assembled body would exceed --limit, the PLAN is truncated — never
the description, never the "Closes" line: whole lines only, the first 70%
and last 30% of the remaining budget, with a note in place of the elision
that says how many lines were dropped and where the untruncated plan can
be read. Nothing is ever dropped silently and nothing is ever summarized.

Exit codes: 0 = written, 1 = the description alone exceeds --limit
            (nothing written), 2 = usage error.
`

func assembleUsage() int {
	fmt.Fprint(os.Stderr, assembleUsageText)
	return 2
}

// digits is what --issue and --limit must be, whole. The issue number goes
// into the body verbatim, so "a number" is a claim worth checking rather
// than an assumption.
var digits = regexp.MustCompile(`^[0-9]+$`)

func runAssemble(args []string) int {
	var body, plan, issue, out, runURL, planURL string
	limit := strconv.Itoa(assemble.DefaultLimit)

	for len(args) > 0 {
		flag := args[0]
		// value is the flag's argument, or a usage error naming the flag and
		// what it wanted.
		value := func(what string) (string, bool) {
			if len(args) < 2 {
				fmt.Fprintf(os.Stderr, "%s needs %s\n", flag, what)
				return "", false
			}
			return args[1], true
		}
		var v string
		var ok bool
		switch flag {
		case "--body":
			v, ok = value("a file")
			body = v
		case "--plan":
			v, ok = value("a file")
			plan = v
		case "--issue":
			v, ok = value("a number")
			issue = v
		case "--out":
			v, ok = value("a file")
			out = v
		case "--run-url":
			v, ok = value("a URL")
			runURL = v
		case "--plan-url":
			v, ok = value("a URL")
			planURL = v
		case "--limit":
			v, ok = value("a number")
			limit = v
		case "-h", "--help":
			return assembleUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return assembleUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}

	if body == "" || plan == "" || issue == "" || out == "" {
		return assembleUsage()
	}
	if !isRegularFile(body) {
		fmt.Fprintf(os.Stderr, "no such body file: %s\n", body)
		return 2
	}
	if !isRegularFile(plan) {
		fmt.Fprintf(os.Stderr, "no such plan file: %s\n", plan)
		return 2
	}
	if !digits.MatchString(issue) {
		fmt.Fprintln(os.Stderr, "--issue must be a number")
		return 2
	}
	if !digits.MatchString(limit) {
		fmt.Fprintln(os.Stderr, "--limit must be a number")
		return 2
	}
	lim, err := strconv.Atoi(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--limit is too large: %s\n", limit)
		return 2
	}

	bodyBytes, err := os.ReadFile(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", body, err)
		return 1
	}
	planBytes, err := os.ReadFile(plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot read %s: %v\n", plan, err)
		return 1
	}

	result, err := assemble.Assemble(assemble.Input{
		Body:    bodyBytes,
		Plan:    planBytes,
		Issue:   issue,
		RunURL:  runURL,
		PlanURL: planURL,
		Limit:   lim,
	})
	if err != nil {
		// A refusal: nothing has been written, and nothing will be.
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(out, result.Body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot write %s: %v\n", out, err)
		return 1
	}
	fmt.Println(result.Summary())
	return 0
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
