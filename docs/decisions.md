# The decision register

Every live decision in falconet, with the [charter](charter.md) invariant it
serves and the observation that should retire it. This page is an index, not
an argument: the reasoning and the measurements are in the record each row
links, and nothing here restates them.

Read this before proposing a change to how falconet is built. A decision is
not a rule — it is a choice with a shelf life, and the **Reopen when** column
is the shelf life, written by whoever made the choice, before they had a stake
in defending it. If you can point at a row's trigger in the present, that row
is open. Say so, and write the record.

Decisions absent from this table are absent because nobody made them. That is
a finding, not a formatting error.

| Decision | Serves | Reopen when | Record |
| --- | --- | --- | --- |
| The pipeline is falconet's own code, not `gh-aw` | I5, I6 | this repository acquires the threat model gh-aw is sized for: strangers triggering workflows | [ADR-0002](adr/0002-extract-the-pipeline-into-falconet.md) |
| One agent pass, holding nothing it could publish with | I5 | never for convenience — a second pass changes I5, and that goes to the operator | [ADR-0002](adr/0002-extract-the-pipeline-into-falconet.md) |
| No second, reviewing agent | I6 | a review harness clears the bar the first one failed: an independent, uncontaminated read of diff, message and plan, worth more than it costs | [ADR-0002](adr/0002-extract-the-pipeline-into-falconet.md) |
| GitHub, Claude Code and OpenTofu are the platform; forge-agnosticism is a non-goal | I6 | an adopter exists on another forge — and there is one adopter | [ADR-0002](adr/0002-extract-the-pipeline-into-falconet.md) |
| Stage-level verbs, one JSON config file | I6 | a caller needs an operation no verb exposes, or config needs a type JSON cannot carry | [ADR-0003](adr/0003-the-cli-surface.md) |
| Packaged as a reusable workflow plus a composite action | I6 | the credentials or setup it demands outgrow what an adopter can check in the README's steps | [ADR-0003](adr/0003-the-cli-surface.md) |
| Verbs never call each other; they leave files in `.falconet/` | I4 | the pipeline stops being a job graph | [ADR-0003](adr/0003-the-cli-surface.md) |
| Every assertion crosses a process boundary | I2, I5 | a property cannot be observed from outside a process — and then it is a Go unit test beside the guard, which `go test ./...` already runs | [ADR-0004](adr/0004-the-strangler-reaffirmed.md) |
| The language is Go | I2, I5, I6 | a guard cannot be expressed safely in it, or the operator stops being able to read the guards | [ADR-0006](adr/0006-the-rewrite-is-in-go.md) |
| falconet's own GitHub client; `gh` and `jq` are not runtime dependencies | I6 | the client's subset stops covering what a verb needs, by more than a dependency would cost | [ADR-0006](adr/0006-the-rewrite-is-in-go.md) |
| Setup is two verbs and a token the operator mints | I6 | `init` cannot do a step, and the manual path becomes the only path | [ADR-0006](adr/0006-the-rewrite-is-in-go.md) |
| A GitHub App, registered purely as a credential | I4, I6 | GitHub offers an identity that needs no App, or registration stops fitting inside `init` | [ADR-0006](adr/0006-the-rewrite-is-in-go.md) |
| One release asset per target, digest in the tree before the tag | I6 | the build stops reproducing, or an adopter needs a target the four assets miss | [ADR-0006](adr/0006-the-rewrite-is-in-go.md) |
| `plan-env` is one secret whose values are all static strings | I6 | a stack needs a credential that cannot be a static string — OIDC, workload identity | [operating.md](operating.md) |
| The plan follows the diff, and the layout is discovered | I3 | a real repository layout appears that discovery reads wrongly | [ADR-0007](adr/0007-the-plan-follows-the-change.md) |
| Never narrow a plan with `-target` | I1 | never. This is I1 in mechanism form and it moves only when I1 does | [ADR-0007](adr/0007-the-plan-follows-the-change.md) |

## Superseded records, kept

[ADR-0004](adr/0004-the-strangler-reaffirmed.md) and
[ADR-0005](adr/0005-the-agent-job-is-handed-its-source.md) are superseded and
are kept for the incidents that produced them. One row above still cites
ADR-0004: the language it defended is gone, and the constraint it discovered —
no test reaches inside its subject — outlived it, which is the ordinary way a
superseded record stays worth reading.
