package main

// summary — write the run's panel to its page.
//
// Unlisted, like config and scan: it works, and it is not vocabulary. The
// workflow runs it as the last step of gate, when prepare did not say ready,
// and of contain, which runs whenever prepare did, so every run whose gate
// ran has one panel. internal/summary decides what the panel says and holds
// the rule that nothing from outside renders as markdown; this file is the
// environment it is read from, the file it is appended to, and the exit code.
//
// The exit code is 0, always, usage errors included. That is the one place
// this verb departs from the uniform exit codes: the panel reports a run, and
// a report that failed its step would change the run it reports.

import (
	"fmt"
	"os"

	"github.com/zetlen/falconet/internal/summary"
)

const summaryUsageText = `summary — write the run's panel to its page.

Modes:
  falconet summary --job gate|contain

Appends one markdown panel to $GITHUB_STEP_SUMMARY, or prints it on stdout
when that is not set. It is read from the environment the workflow sets:

  FALCONET_ISSUE          the issue number
  FALCONET_MAX_ATTEMPTS   the max-attempts input (contain)
  FALCONET_PREPARE        toJSON(steps.prepare) (gate)
  FALCONET_NEEDS          toJSON(needs) (contain)
  FALCONET_JOB_STATUS     job.status in the job the panel is written from
  FALCONET_CHECK          contain's steps.check.outcome
  FALCONET_PAUSE          contain's steps.pause.outcome
  GITHUB_SERVER_URL, GITHUB_REPOSITORY
                          what the issue, pull request and branch links are
                          built from; anything else is not linked

Exit code: 0, always. A report must not change the outcome it reports.
`

func runSummary(args []string) int {
	job := ""
	for len(args) > 0 {
		switch args[0] {
		case "--job":
			if len(args) > 1 {
				job = args[1]
				args = args[2:]
				continue
			}
			args = args[1:]
		default:
			if args[0] != "-h" && args[0] != "--help" {
				fmt.Fprintf(os.Stderr, "unknown argument: %s\n", args[0])
			}
			fmt.Fprint(os.Stderr, summaryUsageText)
			return 0
		}
	}

	panel := summary.Fallback
	if job == "gate" || job == "contain" {
		r := summary.Run{
			Job:         job,
			Issue:       os.Getenv("FALCONET_ISSUE"),
			Server:      os.Getenv("GITHUB_SERVER_URL"),
			Repository:  os.Getenv("GITHUB_REPOSITORY"),
			MaxAttempts: os.Getenv("FALCONET_MAX_ATTEMPTS"),
			JobStatus:   os.Getenv("FALCONET_JOB_STATUS"),
			Check:       os.Getenv("FALCONET_CHECK"),
			Pause:       os.Getenv("FALCONET_PAUSE"),
		}
		summary.ParseJSON(os.Getenv("FALCONET_PREPARE"), &r.Prepare)
		summary.ParseJSON(os.Getenv("FALCONET_NEEDS"), &r.Needs)
		panel = summary.Render(r)
	} else {
		fmt.Fprintf(os.Stderr, "summary: --job must be gate or contain, not %q; writing the fallback panel\n", job)
	}

	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		fmt.Print(panel)
		return 0
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "summary: cannot open $GITHUB_STEP_SUMMARY: %v\n", err)
		return 0
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(panel); err != nil {
		fmt.Fprintf(os.Stderr, "summary: cannot write $GITHUB_STEP_SUMMARY: %v\n", err)
	}
	return 0
}
