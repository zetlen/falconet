# ADR-0003 — The CLI surface: stage verbs, one JSON config file

**Status:** Accepted · 2026-08-20
**Builds on:** [ADR-0002](0002-extract-the-pipeline-into-falconet.md)

## Context

ADR-0002 decided what falconet is and how it gets built: CLI-first, bash
extracted near-verbatim, one agent pass with all guards, strangled into Bun
later, thin action wrappers around one code path. It did not decide the CLI's
surface — which operations are public, how they fit together, or where
configuration lives. This record settles that, before the first working
commit.

The surface questions had real alternatives, and they were settled against the
same test the HANDOFF brief prescribed: read `infra-issues.yml` stage by stage
alongside the human-facing skill that worked the same queue — where the two
agree is the real interface. The rejected alternatives are part of the record:
mirroring the script names one-to-one (faithful, but it leaves stage 1's
orchestration in action YAML, which is two code paths), and grouping by domain
(`issue park`, `git prepare`) — a taxonomy at six verbs is YAGNI.

Three further decisions landed here: the config file's format, the handoff
protocol between verbs, and one deliberate change to runtime behavior —
eligibility moves out of the workflow's job-level `if:` and into the CLI.

## Decision

### Scope: stage-level only

A script becomes a public `falconet` subcommand if and only if the original
workflow called it directly. Everything a stage sourced stays internal with
it. Two consequences, both deliberate:

- `ci-secret-scan.sh` does **not** become a verb. It was only ever invoked by
  the commit stage — once over the agent's drafts, once `--staged` before
  `git commit` — and it stays exactly there: `lib/` bash, invoked by the
  commit verb, with its fail-closed exit discipline (1 = scanner broken,
  3 = finding) intact. The guard survives whole; it just isn't vocabulary.

  > **Amended during execution (2026-08-20).** This paragraph said *sourced*.
  > The commit verb invokes the scan as a subprocess, and the distinction is
  > load-bearing rather than incidental: the scan's stdout is the list of
  > channels that matched, the commit verb's stdout is exactly one outcome
  > word, and capturing the former is what stops it becoming the latter.
  > Sourcing would remove the boundary that makes the capture possible.
- `ci-review-verdict.sh` ships **unwired** in `libexec/` as the reference
  verdict protocol, per ADR-0002. No verb, no caller.

The stage-level criterion yields five verbs. The sixth, `prepare`, is stage
1's scripted half — in-flight check, claim, ack, `request.md`, branch,
`BASE_SHA`, clean-tree assertion, baseline plan — which in the original lived
as inline YAML. It folds into the CLI so the composite action, the reusable
workflow, and the human-facing skill all call the same verbs. One code path.

### The verbs

Uniform exit discipline everywhere: **0** = outcome determined, **1** =
refused mechanically or check failed, **2** = usage. Verbs that decide
something print exactly one outcome word on stdout — the contract
`ci-commit-change.sh` established, extended to `prepare`.

| Verb | Consumes | Produces | Words |
|---|---|---|---|
| `prepare --issue N` | event JSON (`FALCONET_EVENT_PATH`), config | handoff: `request.md`, `plan-baseline.txt`, `base-sha.txt`, `branch.txt`; exports `BRANCH`, `BASE_SHA`; claim + ack comment | `ready` \| `in-flight` \| `ineligible` |
| `commit` | working tree, handoff | `commit-subject.txt`, `commit-body.md`, `failure-reason.txt`; all guards ride here: path allowlist, content denylist, internal secret scan (drafts then `--staged`), per-file `tofu fmt`, vetted-path-only `git add`, refusal precedence over needs-info | `success` \| `needs-info` \| `failure` |
| `push --branch N [--base-sha S]` | handoff | tokenless origin + one-shot single-quoted credential helper, `--force-with-lease`; exports `PUSHED_BRANCH` **only** when the push lands | — |
| `validate --base S` | config stacks | `plan.txt`, `diff.patch`, `changed-files.txt`, `validation-failure.txt`; plan stacks get real init, `validate_only` get `-backend=false` | — |
| `park --issue N --label L --preamble T [--body F] [--body-title T] [--branch B] [--unassign U] [--run-url R]` | handoff, `PUSHED_BRANCH` | comment + label + best-effort unassign; 60000-char cap, whole-line truncation with explicit cut note | — |
| `assemble --body F --plan F --issue N [--plan-url U] [--run-url R] --out F` | handoff | `pr.md`; whole plan in the body, 70/30 head/tail whole-line truncation, fence = longest-backtick-run + 1, exit 1 if the description alone exceeds the limit | — |

`prepare`'s third word is the one deliberate behavior change. Eligibility —
queue label present, no blocking labels, opt-out checkbox unchecked — is
checked by the CLI, not by the workflow's job-level `if:`. A job `if:`
evaluates before checkout, so it can never read the config file; gating there
would fork eligibility into YAML-in-CI and nothing-locally for a project whose
rule is one code path. The cost: ineligible events spin a runner for seconds
instead of zero. Paid willingly.

### The config file

One JSON file, discovered at `.github/falconet.json`; `--config` flag and
`FALCONET_CONFIG` override. JSON was chosen over YAML and JSONC: strict
syntax parsed by `jq` (already a hard requirement), prompt overrides as
relative paths instead of inline block scalars (which `jq` can preview in
tests and development), and no `yq` pinned binary. The Bun port can re-grade
YAML cheaply if a human-facing need for block scalars shows up.

Every key is optional; defaults reproduce the origin repository's behavior.

```json
{
  "handoff_dir": ".falconet",
  "issue": {
    "queue_label": "infra-request",
    "opt_out_text": "Not eligible for AI agents",
    "branch_prefix": "issue-",
    "in_flight_prefixes": ["issue-", "claude/issue-"],
    "blocking_labels": ["needs-info", "ready-for-human", "do-not-apply", "wontfix"]
  },
  "labels": {
    "needs_info": "needs-info",
    "human": "ready-for-human",
    "pr": "needs-plan-review"
  },
  "paths": {
    "allow": ["*.tf"],
    "deny_content": [
      "data \"external\"",
      "provisioner",
      "local-exec",
      "remote-exec",
      "templatefile(",
      "filebase64(",
      "file("
    ]
  },
  "stacks": {
    "plan": ["dns"],
    "validate_only": ["workspace", "site"]
  },
  "plan": {
    "command": "tofu -chdir={stack} plan -no-color -input=false -refresh=false -lock=false"
  },
  "prompts": {
    "implement": "prompts/implement.md",
    "park_needs_info": "prompts/park-needs-info.md"
  }
}
```

> **Amended during execution (2026-08-20).** The `park` signature above
> originally omitted `--body-title`, which folds `--body` into a collapsed,
> fenced `<details>` block. The flag exists in the ported script and the
> routing table needs it: validation logs and plan errors are machine output
> and belong in a fence, while `needs-info.md` and `failure-reason.txt` are
> already prose written for a human and must not be. Dropping the flag would
> have meant one of those two shapes rendering wrongly. The verb keeps it.

`prepare` builds its in-flight regex from `issue.branch_prefix` and
`issue.in_flight_prefixes`, reproducing `^(claude/)?issue-<n>-` by default.
Prompt overrides are paths relative to the repo root, read by the CLI — not
inline strings — because `jq` beats a YAML parser every time a test wants to
probe the config. `yq` does not get pinned.

### The handoff protocol

Verbs never call each other. They pass files in `handoff_dir`, as the stage
scripts always did:

```text
request.md                prepare → implement agent reads first
plan-baseline.txt         prepare → implement agent (baseline drift is not theirs to fix)
base-sha.txt              prepare → validate --base, push --base-sha
branch.txt                prepare → wrappers (BRANCH)
commit-msg.txt            AGENT WRITES → commit reads        (the agent contract)
needs-info.md             AGENT WRITES (only if blocked) → commit reads, park --body
failure-reason.txt        commit → park --body
validation-failure.txt    validate → park --body
plan.txt                  validate → assemble --plan, artifact upload
diff.patch                validate → future review protocol; human reviewers
changed-files.txt         validate → area-label routing
commit-subject.txt        commit → PR title (never issue title)
commit-body.md            commit → assemble --body
pr.md                     assemble → gh pr create --body-file
```

CI-facing exports (`BRANCH`, `BASE_SHA`, `PUSHED_BRANCH`, `VALIDATED`) append
to `$GITHUB_ENV` only when that path is writable — the original pattern —
while handoff files are written always, so the identical verb sequence runs
on a workstation with no GitHub context. `PUSHED_BRANCH` is written only when
the push lands; every downstream `--branch` reads `PUSHED_BRANCH`, never
`BRANCH`.

### The wrappers

The composite action (`action.yml`) is setup plus pass-through: it pins and
installs `gitleaks`, OpenTofu and jq — each under the pin discipline — then
runs `falconet <verb>`.

The reusable workflow (`.github/workflows/falconet.yml`, `on: workflow_call`)
is the documented job graph: gate → implement (agent job, blanked env) →
validate → publish → containment (`if: always()`). Job-level separation is the
security model: the agent job never holds the token; the scripted jobs never
run the agent. Tokens are minted per-step by `actions/create-github-app-token`
against a consumer-provided App ID and private key. The agent step stays on
`anthropics/claude-code-action@v1` until the Bun strangler reaches invocation
— which ports last, per ADR-0002 D1. One internal, unlisted helper keeps
prompts config-driven without YAML-embedded heredocs: `falconet prompt <name>`
prints the config's `prompts.<name>` path override if set, else the shipped
`prompts/<name>.md`.

### Routing and containment

| Outcome | Route |
|---|---|
| `prepare` = `in-flight` / `ineligible` | subsequent steps gate on the outcome; nothing parked — duplicate and ineligible are silent no-ops |
| `commit` = `success` | push (unconditionally) → validate → assemble → PR |
| `commit` = `needs-info` | push → park `needs-info` with `needs-info.md` |
| `commit` = `failure` | push → park `ready-for-human` with `failure-reason.txt` |
| `validate` exit 1 | park `ready-for-human` with `validation-failure.txt` |
| anything exits before push | containment: terminal-state check (closed / needs-info / ready-for-human / open PR across both branch prefixes), else park `ready-for-human` with run link, `${PUSHED_BRANCH:-}`-safe |

Push-immediately is unconditional and precedes routing: every run leaves its
branch on the remote, so stale branches are ordinary and the `GITHUB_RUN_ID`
collision rename does the bookkeeping.

### Tests

The harness ports unchanged: `lib.sh`, `run.sh [filter]`, stub `gh`, no
network, bare-repo pushes. No new tools beyond the pinned set.
On top of the ported suite: dispatcher contract tests (usage → 2, one-word
stdout, `--config`/`FALCONET_CONFIG` precedence, `$GITHUB_ENV` optional), the
`prepare` gate matrix from fixtures, and config parsing including
`deny_content` ordering. The workflow-contract invariants retarget at
falconet's own reusable workflow: agent toolset exactly
`Read,Edit,Write,Grep,Glob` with no Bash, exactly one validate step, no
repair loop, branch pushed exactly once and only by `push`, every park call
passing `--branch` `$PUSHED_BRANCH`, pinned binaries before first use,
`review-verdict` referenced zero times, PR title from `commit-subject.txt`.
The prove-guards pattern — break each guard on purpose, assert it refuses —
travels here from the origin repository as a follow-on deliverable.

### Explicitly not in v1

No apply or deploy. No review agent (the verdict protocol ships unwired).
No plan-on-PR (#68 stays deferred). GitHub and Claude Code are hardcoded;
OpenTofu is assumed, but `plan.command` is a string, so a terraform binary
works. Forge-agnosticism is a stated non-goal and the README says so. No
fixture repository yet — the origin repository is the integration environment
from the first working commit, per ADR-0002 move 3.

## Consequences

The verbs are the stable interface the Bun strangler ports behind, one at a
time, each port answering to the carried tests — nothing in this record names
an implementation language beyond the first, and nothing outside `libexec/`
should learn one. JSON keeps the pinned-binary count at one (gitleaks), so
the setup stays minimal and the Bun port can revisit YAML cheaply. Moving
eligibility
into `prepare` spends runner-seconds on ineligible events to keep one code
path and one source of truth. And the whole surface — six verbs, one file —
is small enough to keep the promise ADR-0002 made: the pipeline as a thing,
not a tangle.
