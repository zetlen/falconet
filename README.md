# falconet

A falconet is a small cannon: one precise shot, aimed by hand, at a target you
picked on purpose. This one turns a plain-language infrastructure request into
a reviewed pull request carrying a real `tofu plan` — and then stops, because
applying is a human's job.

**Status: the CLI works; nothing has run as an Action yet.** All six verbs
are implemented and tested — `bash tests/run.sh`, 12 files, 435 assertions.
The composite action and the reusable workflow are written and their
structural invariants are tested, but they have never executed on a real
runner. See [Where this stands](#where-this-stands).

## What it does

Someone files an issue that says, in ordinary words, what they want changed.
falconet:

1. claims the issue, opens a branch, and captures a baseline plan
2. runs **one** agent pass with a deliberately narrow toolset — it edits
   config and writes a commit message, and holds no shell and no push token
3. commits through deterministic guards that an agent cannot talk its way past
4. validates and plans, for real
5. opens a pull request whose body carries the **entire** plan, labelled for
   human review

Every exit is a terminal state: a pull request, a question for the requester,
or a hand-off to a human. A request never disappears into a green run that
produced nothing.

What it will **not** do is apply anything. The gate at the end is a person.

## The verbs

Six of them, one per stage. They never call each other; they pass files
through a handoff directory, so the same sequence runs in CI and on a
workstation. Uniform exit codes: **0** an outcome was determined, **1**
refused or a check failed, **2** usage. The verbs that decide something
print exactly one word on stdout.

| Verb | What it does | Words |
| --- | --- | --- |
| `prepare --issue N` | eligibility gate, claim, branch, baseline plan | `ready` `in-flight` `ineligible` |
| `commit` | every guard, then the commit the agent cannot make | `success` `needs-info` `failure` |
| `push --branch B` | the branch onto the remote, the moment a commit exists | — |
| `validate --base S` | validate and plan each stack, collecting failures | — |
| `park --issue N --label L` | a terminal state, said where the requester reads it | — |
| `assemble --plan F --out F` | a PR body carrying the whole plan | — |

Configuration is one optional JSON file at `.github/falconet.json`
([schema](docs/adr/0003-the-cli-surface.md#the-config-file)); every key has a
default and the defaults reproduce the pipeline this was extracted from.

## Using it

Two things only the operator can do — registering a GitHub App as a
credential and cutting an API key with a budget alert — are described in
[operating](docs/operating.md). Then, from a consuming repository:

```yaml
jobs:
  infra-request:
    uses: zetlen/falconet/.github/workflows/falconet.yml@main
    with:
      issue: ${{ github.event.issue.number }}
    secrets:
      app-id: ${{ secrets.FALCONET_APP_ID }}
      app-private-key: ${{ secrets.FALCONET_APP_PRIVATE_KEY }}
      anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

Or run a verb by hand, against the repository you are standing in:

```sh
falconet validate --base "$(git rev-parse main)"
```

## Why it exists

It was built inside an OpenTofu repository ([the provenance is
here](docs/provenance/)) and worked well enough that the surrounding repo
became mostly pipeline: ~4,700 lines of workflow and shell against ~1,000
lines of actual infrastructure. Two attempts to escape that — adopting an
off-the-shelf agentic workflow, or trimming in place — both concluded the same
way: the workflow is a good tool wearing a repository as a costume.

So it becomes a tool. [ADR-0002](docs/adr/0002-extract-the-pipeline-into-falconet.md)
is the founding record, including the measurements that killed the
off-the-shelf option.

## Design commitments

- **Deterministic mechanics, agent judgment.** The agent decides *what* the
  change is. Shell does the branching, committing, pushing, planning and PR
  assembly — so those steps cannot be skipped, improvised, or argued out of.
- **Guards are incident-shaped.** Every one of them exists because something
  went wrong once. They are documented with the incident that caused them.
- **One agent, one context.** An earlier design ran a second reviewing agent;
  measurements showed the second cold context cost more than it caught. Its
  verdict protocol survives here as a reference implementation, unwired.
- **The plan is the evidence.** It goes in the PR body in full, never
  abridged, because a human approving a summary of evidence is not review.
- **Opinionated on purpose.** GitHub and Claude Code are assumed. OpenTofu is
  the shape. Being agnostic across forges is an explicit non-goal.

## Where this stands

| Piece | State |
| --- | --- |
| `bin/`, `lib/`, `libexec/falconet/` — the CLI | six verbs, working |
| `tests/` — 12 files, 435 assertions | passing (`bash tests/run.sh`) |
| `action.yml` + `.github/workflows/falconet.yml` | written, invariants tested, never run |
| `prompts/` | extracted from the provenance |
| `docs/adr/` — the decisions | [0002](docs/adr/0002-extract-the-pipeline-into-falconet.md) founding, [0003](docs/adr/0003-the-cli-surface.md) the surface, [0004](docs/adr/0004-the-strangler-reaffirmed.md) the language |
| `docs/provenance/` — the retired orchestrator | reference only |
| A live run | **not yet** — the next thing that has to happen |
| Bun rewrite | deferred on purpose ([ADR-0004](docs/adr/0004-the-strangler-reaffirmed.md)) |

The port from stage-shaped scripts to a coherent CLI is done; [the plan it
followed](docs/adr/pre-execution-plan.md) records what changed and what was
found on the way. What has not happened is a real run: development *is*
integration here, so the next step is a consuming repository and a canary
issue of known shape.

Two further documents: [operating](docs/operating.md) covers the credentials
only the operator can create and where the pieces live; [AGENTS.md](AGENTS.md)
is what to read before changing anything here.

## Running the tests

```sh
bash tests/run.sh            # all of them
bash tests/run.sh handover   # just the files matching "handover"
```

They stub `gh`, push only into bare repositories under a temp directory, and
never touch the network, GitHub, OpenTofu, or any credential.

## Support

None promised. This is built for one operator's infrastructure repository and
made public because someone else may find the shape useful. Issues and pull
requests may go unanswered; fork freely.

## License

MIT. See [LICENSE](LICENSE).
