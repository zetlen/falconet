# falconet

A falconet is a small cannon: one precise shot, aimed by hand, at a target you
picked on purpose. This one turns a plain-language infrastructure request into
a reviewed pull request carrying a real `tofu plan` — and then stops, because
applying is a human's job.

**Status: the CLI works; nothing has run as an Action yet.** All six verbs
are implemented and tested — `bash tests/run.sh`, 12 files, 456 assertions.
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

## Usage

Installing falconet into a repository is one workflow file, one config file,
four secrets and four labels. Nothing is vendored: the workflow checks this
repository out at the ref you pin and runs it from there, so upgrading is
changing a ref.

It assumes stacks in subdirectories, state in a remote backend, and providers
that want credentials. [Before the first real request](#before-the-first-real-request)
collects what is worth knowing before pointing it at a live queue.

### What the repository has to already be

An OpenTofu repository with issues enabled and its stacks in subdirectories,
on a **Linux x64 runner** — the action installs a pinned
`gitleaks_<version>_linux_x64` release and checks its digest, so macOS or ARM
fails the checksum. `jq` must be on the runner; `ubuntu-latest` has it.

Its Actions policy also has to permit workflows and actions from outside the
repository — falconet itself, plus `actions/checkout`,
`actions/create-github-app-token`, `actions/upload-artifact`,
`actions/download-artifact`, `opentofu/setup-opentofu` and
`anthropics/claude-code-action`. A repository or organisation restricted to
local actions blocks the call before any of this runs.

### 1. Four secrets

Two of them are things only a person can create, and
[operating](docs/operating.md) says why each is the kind of credential it is.

| Repository secret | What it is |
| --- | --- |
| `FALCONET_APP_ID` | A GitHub App registered purely as a credential — no webhook, nothing hosted. |
| `FALCONET_APP_PRIVATE_KEY` | That App's private key, whole PEM including the header and footer lines. |
| `ANTHROPIC_API_KEY` | An API key, not a subscription token, so falconet's spend stays a separate number. Set a budget alert on it. |
| `FALCONET_PLAN_ENV` | Everything your stacks need in their environment to `init` and `plan`, as one JSON object. Optional, but a stack with a remote backend does not come up without it. |

The App has to be **installed on the consuming repository**, with repository
permissions **Contents: read and write**, **Issues: read and write**, and
**Pull requests: read and write**. An App rather than a PAT or `GITHUB_TOKEN`
because pull requests opened with `GITHUB_TOKEN` do not trigger workflows —
CI would never run on the PRs falconet opens — and App-token pushes do.

`FALCONET_PLAN_ENV` is a JSON object of environment variables — state backend
keys, provider tokens, `TF_VAR_*` — and falconet masks every value and hands
them to the two jobs that run tofu, and to no other. Whatever you would put in
front of `tofu plan` on your own machine goes in here:

```json
{
  "AWS_ACCESS_KEY_ID": "...",
  "AWS_SECRET_ACCESS_KEY": "...",
  "CLOUDFLARE_API_TOKEN": "..."
}
```

One secret rather than an input per cloud, because a backend is an S3 key here
and a service account there, and an interface naming them all would be
guessing at your infrastructure. If your stacks plan with no credentials at
all, leave it out.

### 2. Four labels, created before the first run

| Label | Applied by | Config key |
| --- | --- | --- |
| `infra-request` | a person, to queue a request | `issue.queue_label` |
| `needs-info` | falconet, parking a question back to the requester | `labels.needs_info` |
| `ready-for-human` | falconet, parking a run a person has to take over | `labels.human` |
| `needs-plan-review` | falconet, on the pull request it opens | `labels.pr` |

Create all four up front. `gh issue edit --add-label` fails on a label that
does not exist, which turns a hand-over into a failed step at precisely the
moment falconet is trying to tell somebody something.

### 3. The caller workflow

One file, `.github/workflows/infra-requests.yml`, and this is the whole of it:

```yaml
name: infra requests

on:
  issues:
    types: [opened, labeled, reopened]
  issue_comment:
    types: [created]

# One run per issue. `opened` and `labeled` arrive seconds apart on a freshly
# filed request, and without this they are two runs racing to open two pull
# requests for the same issue.
concurrency:
  group: falconet-${{ github.event.issue.number }}
  cancel-in-progress: false

# A called workflow can only narrow the caller's token, never widen it. A
# repository whose default workflow permissions are read-only fails at job
# start without these, and the failure names a permission rather than a cause.
permissions:
  contents: read
  issues: write
  pull-requests: write

jobs:
  falconet:
    uses: zetlen/falconet/.github/workflows/falconet.yml@main
    with:
      issue: ${{ github.event.issue.number }}
    secrets:
      app-id: ${{ secrets.FALCONET_APP_ID }}
      app-private-key: ${{ secrets.FALCONET_APP_PRIVATE_KEY }}
      anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
      plan-env: ${{ secrets.FALCONET_PLAN_ENV }}
```

Triggering on every issue event and deciding eligibility inside is
deliberate. A job-level `if:` evaluates before checkout and can therefore
never read `.github/falconet.json`, so gating there would fork eligibility
into YAML-in-CI and nothing-locally. `prepare` decides instead, reading the
same config a workstation reads, and an ineligible event costs runner-seconds
and stops. It is eligible when the issue is **open**, carries the
**queue label**, carries none of the blocking labels, has no ticked opt-out
checkbox, and has no open pull request already on a branch for that issue.
A comment from a bot, or on a pull request, is never a way in.

The ref in `uses:` must be a literal — GitHub does not expand expressions
there. The `falconet-ref` input is a different thing: it chooses which
falconet the inner checkout steps fetch, and it defaults to `main`. Keep the
two in step or you will debug a version you are not running.

| Input | Required | Default | What it is |
| --- | --- | --- | --- |
| `issue` | yes | — | The issue number to work. |
| `config` | no | `.github/falconet.json` | Path to the config file. |
| `falconet-ref` | no | `main` | Which falconet the jobs check out. |
| `runs-on` | no | `ubuntu-latest` | Must stay Linux x64. |

### 4. The config file, when the defaults do not fit

Optional, at `.github/falconet.json`, merged **over** the defaults — so a file
naming one key changes one thing. Arrays are replaced wholesale rather than
appended to, because an allowlist that grows by accident is not an allowlist.
A malformed file is a hard failure with jq's parse error, never a silent fall
back to defaults.

`stacks` is the one key almost every repository sets, because the defaults
name the stacks this was extracted from. Shown here with the other two worth
knowing about:

```json
{
  "stacks": {
    "plan": ["dns"],
    "validate_only": ["workspace", "site"]
  },
  "paths": { "allow": ["*.tf"] },
  "issue": { "queue_label": "infra-request" }
}
```

`stacks.plan` are initialised, planned, and their plans become the evidence in
the pull request. `stacks.validate_only` are initialised with `-backend=false`
and validated, never planned, so they need no credentials. A name that is not
a directory fails with a message naming the key, the file it came from, and
what belongs there. falconet runs `tofu init` for the stacks it plans; if
`plan.command` is something other than tofu, that command owns
initialisation. `paths.allow` is the allowlist the implementing agent's change is
checked against — shell globs, in which `*` crosses `/`, so `*.tf` matches
`dns/records.tf`. Anything outside it is refused and the run commits nothing.
Every key, including the content denylist and the plan command, is in
[the schema](docs/adr/0003-the-cli-surface.md#the-config-file).

### 5. What a first run looks like

File an issue in ordinary words, label it `infra-request`, and watch four
jobs: **gate** (eligibility, claim, branch, baseline plan), **implement** (one
agent pass, then every guard, then the commit), **publish** (push first, then
validate, then the pull request), **contain** (a terminal state, whatever
happened above). The requester gets an acknowledgment within a minute of the
gate, and one of exactly three endings: a pull request labelled for review, a
question, or a hand-off to a person.

### Before the first real request

**Static credentials only — OIDC is not wired.** `plan-env` is values in a
secret. No job declares `id-token: write`, and a caller can only narrow a
called workflow's permissions rather than add one, so a repository that
authenticates its backend by federated identity cannot do that here yet. If
you have no static key to give it, `"stacks": {"plan": []}` runs
validate-only and puts an empty plan block in the pull request — enough to
exercise the wiring, and not a place to stay, since the plan is what a
reviewer approves.

**The credentials are in the environment of the jobs that plan.** That is
what makes them work, and it means anything those jobs print can in principle
carry one. Every value is masked line by line, `tofu` is run with
`-input=false`, and the secret scan stands between the agent's drafts and the
issue — but the validation-failure text that gets posted when a plan fails is
OpenTofu's own output. Give falconet a credential scoped to what it plans,
not your admin key.

**A failed gate is silent to the requester.** If `prepare` hard-fails — a
dirty tree, a baseline plan that will not run — the run goes red, and
`contain` does not fire, because it is conditioned on the gate having said
`ready`. The acknowledgment comment is posted before the baseline plan, so
the requester has been thanked and then hears nothing. Watch the first run.

**`@main` moves.** Development is integration here and `main` is what
consumers pin, so a change lands in your repository the next time an issue is
filed. Cut and pin a tag once a run has succeeded and you want it to stay
that way.

**Never put issue text in `args`.** If you call `action.yml` directly, its
`args` input is split on whitespace and reaches a shell. Issue titles, bodies
and comments are attacker-controlled, and the reason all six verbs take files
rather than strings is so that text never travels that way.

### Or by hand

The same verbs, against the repository you are standing in — no workflow, no
credentials beyond a `gh` login:

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
| `tests/` — 12 files, 456 assertions | passing (`bash tests/run.sh`) |
| `action.yml` + `.github/workflows/falconet.yml` | written, invariants tested, never run |
| credentials for the jobs that plan | one `plan-env` secret, static values only |
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
