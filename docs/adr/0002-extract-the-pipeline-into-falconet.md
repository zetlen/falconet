# ADR-0002 — Extract the pipeline into falconet

**Status:** Accepted · 2026-08-20
**Supersedes:** ADR-0001, which decided to replace the hand-rolled issue
pipeline with gh-aw. It is not in this tree and never was: it was written in
the origin repository, before the extraction this record decides. Every
reference to it below is to a record that lives there.
**Serves:** I5 (the agent is a suspect), I6 (adoption stays inside one
operator's reach)
**Reopen when:** strangers can trigger this pipeline. That is the threat model
gh-aw is sized for and this repository is not; the watchdog tax measured here
is a bargain against it and a permanent loss without it.

## Context

ADR-0001 decided to replace the hand-rolled issue pipeline with gh-aw, and its
Phase 1 spike ran. The spike did what a spike is for: it settled arguments with
numbers, and the numbers cut both ways.

What gh-aw proved:

- **The warm-context claim is true.** One agent holding one context for the
  whole job: 7.7K fresh input tokens against 776K served from cache
  (run 32316513015). The staged pipeline's two cold contexts were the
  token-eater ADR-0001 said they were.
- **The containment composes.** The firewall on a hosted runner reported 35
  requests, all to api.anthropic.com, zero blocked, and safe-outputs opened a
  correct, labelled PR.

What it also proved:

- **The watchdog costs ~44% of the worker** on a small task (agent 108.2 AIC,
  detection 47.3 AIC) — a permanent tax on every run, sized for a threat model
  this repository does not have.
- **The shape is for a different repository.** Pre-activation role checks,
  integrity filtering, a threat-detection job between the agent and any write:
  gh-aw is built for busy public repositories where strangers trigger
  workflows. Here there is one operator, one collaborator, and a human apply
  gate at the end of everything. All three Phase 1 gaps were closed
  (97b5669) — the machinery worked — but every fix was a workaround for a
  default aimed at someone else.
- And the thing the numbers do not carry: for the brief period the homegrown
  workflow ran end to end, filing a plain-language issue and getting back a
  planned, labelled PR was the best experience this repository has produced.
  That experience is the product. Renting an approximation of it is the wrong
  trade for a repo whose standing commitment is less SaaS and more self-run
  OSS.

The proportion argument from ADR-0001 stands untouched: ~4,700 lines of
pipeline against 1,051 lines of OpenTofu cannot be this repository's shape.
ADR-0001 read that as "adopt someone else's pipeline". This record reads it as
"the pipeline is its own project".

## Decision

Three moves.

**1. This repository goes back to being an OpenTofu repository.** The staged
pipeline (`infra-issues.yml`) and the gh-aw replacement (`infra-request.md`
and its compiled lock, `aw.json`, `infra-plan-comment.yml`, `.github/aw/`)
are both removed. What stays is everything that was never agent machinery —
`ci.yml`, `deploy.yml` and its human apply gate, the intake form and the
label vocabulary — plus, deliberately, `scripts/` with its tests and the
`work-infra-issues` skill: the skill works the queue from an operator's
session in the interim, and the scripts are the corpus the new project
extracts.

**2. The workflow becomes falconet.** A separate project — public from day
one, MIT, `zetlen/falconet` — a small cannon: one precise shot per issue.

- **CLI-first.** The bash stages extracted near-verbatim as subcommands,
  with their tests, which encode this repository's incident history (PR #28's
  abridged plan; run 32093607680's stranded branch). Orchestration and
  prompts live in thin composite-action wrappers.
- **One agent pass, all guards.** The implementing agent keeps its narrow
  toolset; every deterministic guard survives — path policy, content
  denylist, secret scan, push-immediately, full-plan-in-body,
  single-terminal-state. The independent review agent stays dropped:
  ADR-0001 risk 9 stands, including its bar for any future replacement.
- **Configurable where this repo taught us to be** — stacks, path policy,
  labels, plan command, prompt overrides — and hardcoded elsewhere: GitHub
  and Claude Code are the platform, OpenTofu is the shape. Forge-agnosticism
  is a non-goal.
- **Its own identity, no service.** A GitHub App registered purely as a
  credential — no webhooks, nothing hosted. The action mints installation
  tokens, output is authored by `falconet[bot]`, and App-token pushes fire
  `pull_request` events normally, which retires the empty-commit hack the
  gh-aw spike needed.
- **Its own bill.** `ANTHROPIC_API_KEY`, so what the pipeline costs stays a
  number, priced apart from the operator's subscription.

**3. Development is integration.** There is no build-then-integrate phase:
falconet's orchestrator is Actions YAML, which cannot run outside a consuming
repository. This repo consumes `falconet@main` from its first working commit
and is its integration environment — a canary issue of known shape exercises
the pipeline, and the prove-guards pattern (break each guard on purpose,
check it refuses) travels into falconet. A dedicated fixture repository is
deferred, on purpose, until falconet stops being a personal project.

## Decisions taken (2026-08-20)

- **D1 — The rewrite is a strangler, in Bun.** Bash lands first and is
  replaced subcommand-by-subcommand behind a stable CLI interface, each port
  answering to the carried tests. The agent invocation ports **last**:
  `anthropics/claude-code-action@v1` runs the agent step until every guard is
  in Bun and proven, then the Claude Agent SDK takes the loop — which is what
  makes the identical pipeline runnable from a workstation.
- **D2 — Gates stay off, and only a person moves them.** A prototype-phase
  decision, recorded in AGENTS.md ("Development posture") so agents stop
  reporting it: rulesets and required checks are deliberately disabled and
  are flipped on only to confirm they still work. Safeguards are brought
  down by the human operator, manually, always — never by an agent.
- **D3 — plan-on-PR stays deferred (#68).** `ci.yml` remains credential-free
  and the contract test keeps enforcing it. The plan reaches reviewers in the
  PR body, as AGENTS.md requires.
- **D4 — `CLAUDE_CODE_OAUTH_TOKEN` survives, amending ADR-0001 D5.** D5 said
  revoke it when `infra-issues.yml` is deleted; `claude.yml` (the @claude
  mention handler) still authenticates with it, so it lives exactly as long
  as `claude.yml` does. falconet never reads it.

## Migration

- **Phase 0 — the record, then the removal.** The gh-aw line of work is
  committed finished (97b5669) before this ADR's PR deletes it; the spike's
  evidence is the context above.
- **Phase 1 — prove the substrate.** One hand-worked cycle end to end:
  issue → skill → PR with plan → human `plan-approved` → merge → apply →
  receipt, with `ci.yml` green throughout.
- **Phase 2 — bootstrap falconet.** Extract scripts and tests, wrap them,
  write the config file, register the App, cut a fresh API key with a budget
  alert.
- **Phase 3 — consume.** `falconet@main` works this queue; the skill becomes
  a shell around the falconet CLI — one code path locally and in CI; the
  canary ritual begins.
- **Phase 4 — the strangler runs** (D1).

Issues #71, #72, #74, #89 and #91 are retargeted at falconet rather than
closed, per ADR-0001's own instruction: they remain real, just not this
repository's problem.

## Consequences

The repository's agentic surface shrinks to `claude.yml` and a skill; its CI
surface is fmt, validate and the test suite; DNS still cannot change without
a person applying a label. We take on a second repository and the maintenance
of a published action — accepted with eyes open, because the workflow was the
best thing this repository has produced and it deserves to exist as a thing
rather than a tangle. And the loop worth keeping comes back on its own
schedule, not at the end of a waterfall.
