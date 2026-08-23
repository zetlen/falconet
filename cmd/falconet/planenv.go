package main

// plan-env — one secret, masked and turned into ordinary environment for the
// stacks that plan.
//
// Unlisted on purpose, on prompt's precedent: invoked by the workflow, not
// vocabulary. It replaces the bash that sat in falconet.yml as "Credentials
// for the stacks that plan" — in the two jobs that run tofu and in no other,
// because the agent job holds no credential of any kind — so that tofu sees
// what it would see on a workstation and no verb has to know the name of a
// cloud. The secret's shape is internal/planenv's, the same code init seals
// it with.
//
// Two things are the contract, and both are bytes:
//
//   - every non-empty line of every value is printed on stdout as
//     `::add-mask::<line>` BEFORE the value is written anywhere. add-mask is
//     per line, and a PEM is many lines. The runner reads workflow commands
//     off this process's stdout, which is why the workflow runs this as a
//     `run:` step and not through the action: the action captures stdout into
//     a step output, and a mask that lands in an output instead of the log
//     masks nothing.
//   - each variable is appended to $GITHUB_ENV in the delimiter form
//     (handoff.GitHubEnvAppendMultiline), which is legal whatever the value
//     holds — except the delimiter itself, which is refused.
//
// Every entry is checked before any is written: a secret with one bad entry
// exports nothing, rather than the entries that happened to sort before it.
// The bash wrote as it went. And an error names a key or a shape and never a
// value, which is planenv's rule, kept here.
//
// Exit codes: 0 = done, or no secret; 1 = the secret is not the shape;
// 2 = usage error.

import (
	"fmt"
	"os"

	"github.com/zetlen/falconet/internal/handoff"
	"github.com/zetlen/falconet/internal/planenv"
)

const planEnvUsageText = `plan-env — turn $FALCONET_PLAN_ENV into masked environment for the stacks
that plan.

Modes:
  falconet plan-env

Reads one JSON object of environment variables from $FALCONET_PLAN_ENV —
backend keys, provider tokens, TF_VAR_* — and for each entry, in name
order: prints ::add-mask:: for every non-empty line of the value, appends
the variable to $GITHUB_ENV in the delimiter form, and prints
"plan-env: set NAME". With no secret it says so and exits 0. Without a
$GITHUB_ENV the masks are still printed and nothing else is written.

Refuses with exit 1, naming the shape or the key and never a value: a
top-level value that is not an object, a value that is not a string, a key
that is not an environment-variable name, a value containing the
delimiter. Nothing is written when anything is refused.

Unlisted on purpose: invoked by the workflow, not vocabulary.

Exit codes: 0 = done, 1 = refused, 2 = usage error.
`

func planEnvUsage() int {
	fmt.Fprint(os.Stderr, planEnvUsageText)
	return 2
}

func runPlanEnv(args []string) int {
	if len(args) > 0 {
		if args[0] != "-h" && args[0] != "--help" {
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", args[0])
		}
		return planEnvUsage()
	}

	raw := os.Getenv("FALCONET_PLAN_ENV")
	if raw == "" {
		fmt.Println("no plan-env secret: the stacks must init and plan without one")
		return 0
	}

	entries, err := planenv.Parse([]byte(raw))
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan-env: %v\n", err)
		return 1
	}
	// All of them first. A refusal here has written nothing, and the
	// delimiter is the one thing the shape check cannot know about.
	for _, e := range entries {
		if err := handoff.CheckMultiline(e.Name, e.Value); err != nil {
			fmt.Fprintf(os.Stderr, "plan-env: %v\n", err)
			return 1
		}
	}

	for _, e := range entries {
		// The masks, then the value, then the word: the runner has been told
		// what to redact before the value exists anywhere it could read.
		for _, mask := range planenv.Masks(e.Value) {
			fmt.Println(mask)
		}
		if err := handoff.GitHubEnvAppendMultiline(e.Name, e.Value); err != nil {
			fmt.Fprintf(os.Stderr, "plan-env: %v\n", err)
			return 1
		}
		fmt.Printf("plan-env: set %s\n", e.Name)
	}
	return 0
}
