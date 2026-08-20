# ADR-0004 — The Bun strangler, reconsidered and reaffirmed

**Status:** Accepted · 2026-08-20
**Reaffirms:** [ADR-0002](0002-extract-the-pipeline-into-falconet.md) D1
**Supersedes:** nothing

## Context

ADR-0002 D1 decided the rewrite is a strangler: bash lands first and is
replaced subcommand-by-subcommand behind a stable CLI interface, each port
answering to the carried tests. Before executing the port plan that implements
ADR-0003, the operator reopened it — proposing to port to Bun *now*, on two
grounds:

1. **The bash is not battle-tested.** It ran for a few commits in
   `wayfinders-infra` and then the pipeline was retired. "It worked" is a
   weaker claim than the strangler's premise assumes.
2. **It is a large amount of shell.** The operator's estimate was five
   thousand lines.

Both grounds deserved measurement rather than deference, so the corpus was
measured.

### What the measurement found

**The shell is smaller than it looks.** `scripts/` is 1,626 lines, of which
857 are comments and 110 are blank. **The executable logic is 659 lines**
across all seven scripts:

| script | total | comment | code |
|---|---|---|---|
| `ci-commit-change.sh` | 398 | 211 | 160 |
| `ci-validate.sh` | 281 | 143 | 126 |
| `ci-pr-body.sh` | 189 | 68 | 100 |
| `ci-park-issue.sh` | 166 | 65 | 88 |
| `ci-secret-scan.sh` | 196 | 114 | 69 |
| `ci-review-verdict.sh` | 202 | 120 | 68 |
| `ci-push-branch.sh` | 194 | 136 | 48 |

The five-thousand-line figure counts the tests (1,610 lines) and the
1,307-line provenance workflow alongside the scripts. Sixty percent of
`scripts/` is the incident record, and that record is prose — it is
language-independent and survives any port verbatim.

**The test suite does not know what language it is testing.** 177 assertions
across six files, every one of them black-box: spawn a process, assert on
stdout, exit code, and files on disk. No test sources the script under test —
the only `source` in the suite is `tests/lib.sh` itself, plus one read of a
`$GITHUB_ENV` file a script produced. The subject is always reached across a
process boundary.

## Decision

**Bash stays. ADR-0002 D1 stands unchanged.** The port proceeds as ADR-0003's
verb surface implemented in bash, and the Bun strangler remains Phase 4.

The operator's first ground is **conceded and reframed**: the bash is indeed
not proven as code. But what needed proving was never the code. It is the
*incidents* — PR #28's hand-abridged plan, run 32093607680's stranded branch,
issue #41's token in a commit message, issue #25's re-entry path — and each of
those is recorded twice, in a comment and in a test, both of which outlive any
implementation. The bash is the least durable of the three expressions and the
least of what would be lost.

The second ground **does not survive measurement**. 659 lines is not a
maintenance burden that justifies stacking an unvalidated rewrite on top of an
unvalidated integration. Nothing in this repository has ever run as an Action;
there is no `.github/` at all. Porting now would put two suspects behind every
failure in the first live run — "is the job graph wrong" and "did the port
change behavior" — and the job graph is the part with no prior art.

### The new constraint this decision adds

The measurement produced something ADR-0002 did not know, and it is the reason
this reaffirmation is cheap rather than grudging:

**The tests are language-agnostic, and they must stay that way.** Because
every assertion crosses a process boundary, the suite adjudicates a Bun
implementation exactly as well as a bash one. This makes the deferred port
*cheaper than ADR-0002 assumed* — it is why deferring costs little. It also
means the strangler need not be incremental for the tests to stay meaningful;
ADR-0002 D1's subcommand-by-subcommand sequencing is a risk-management choice,
not a requirement imposed by the tests.

So: **no test may reach inside its subject.** No sourcing the verb under test,
no asserting on bash internals, no helper that only a shell implementation
could satisfy. A test that couples to bash spends the option this decision is
preserving. The three tests that copy a script into a scratch repository to
exercise its self-location are the ones to watch — they are the closest the
suite comes to knowing its subject is a shell script.

### What was also weighed

Recorded so the next reconsideration starts further along:

- **Where Bun genuinely wins is narrow but real.** This program is mostly
  subprocess orchestration — git, gh, tofu, gitleaks — which is bash's home
  turf and becomes `Bun.spawn` either way. The win is concentrated in the
  guard logic: byte-budget truncation on whole-line seams, the 70/30 head/tail
  split, fence-length normalization, markdown-stripped sentinel matching, the
  ordered content denylist. That is where the incidents live and where bash is
  worst. Expect the orchestration half to be a lateral move.
- **Bun costs a build dependency.** ADR-0003 valued a setup that pins exactly
  one binary. `bun build --compile` or `setup-bun` adds one. Partly offset:
  config becomes `JSON.parse` and `jq` stops being a runtime requirement —
  which weakens one of ADR-0003's stated reasons for JSON over YAML without
  disturbing the conclusion.
- **ADR-0002 D1's other half is untouched either way.** The agent invocation
  ports last. That is an integration with `anthropics/claude-code-action@v1`,
  not a guard, and moving to the Claude Agent SDK is a separate bet that
  should not be folded into a language change.

## Consequences

The port plan (`pre-execution-plan.md`) executes as written: `git mv` plus
rewiring, twelve tasks, green suite after each. Nothing changes in it because
of this record.

The Bun port stays Phase 4 and stays cheap, on one condition that is now
written down: the suite keeps testing a process, not a shell. If a future
reconsideration wants to move the whole surface at once rather than
subcommand-by-subcommand, the tests permit it — ADR-0002 D1's incrementalism
can be revisited on its own merits without revisiting the language.

And the trigger for reopening this is no longer a feeling about line count.
It is either of: a guard whose logic bash cannot express safely, or the
integration proving stable enough that a rewrite is the only remaining
unknown.
