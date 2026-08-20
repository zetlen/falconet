# falconet

A falconet is a small cannon: one precise shot, aimed by hand, at a target you
picked on purpose. This one turns a plain-language infrastructure request into
a reviewed pull request carrying a real `tofu plan` — and then stops, because
applying is a human's job.

**Status: not a working tool yet.** This repository currently holds the
extracted corpus and the record that motivated it. Nothing here runs as a
GitHub Action today. See [Where this stands](#where-this-stands).

## What it will do

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
| `scripts/` — the seven pipeline stages | extracted verbatim, tested |
| `tests/` — 6 files, ~1,600 lines | passing (`bash tests/run.sh`) |
| `docs/provenance/` — the retired orchestrator, its prompts, the alternatives | reference only |
| `docs/adr/` — the decisions | ADR-0002 founding, [0003](docs/adr/0003-the-cli-surface.md) the CLI surface, [0004](docs/adr/0004-the-strangler-reaffirmed.md) the language |
| CLI entry point | designed, not written |
| Composite action + reusable workflow | designed, not written |
| Config file | [designed](docs/adr/0003-the-cli-surface.md#the-config-file), not written |
| Bun rewrite | deferred on purpose ([ADR-0004](docs/adr/0004-the-strangler-reaffirmed.md)) |

The scripts are stage-shaped, not subcommand-shaped: they were called by a
workflow, not by each other. Turning them into one CLI is the next job, and
[the port plan](docs/adr/pre-execution-plan.md) is how it proceeds — twelve
tasks, each ending with a green test suite.

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
