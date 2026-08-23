# falconet

A falconet is a small cannon: one precise shot, aimed by hand, at a target you
picked on purpose. This one turns a plain-language infrastructure request into
a reviewed pull request carrying a real `tofu plan` — and then stops, because
applying is a human's job.

**Status: it works. One issue has become one reviewed pull request, on a real
runner.** All six verbs are implemented and tested — `bash tests/run.sh`, 12
files, 480 assertions. The first consumer installed it on 2026-08-21 and the
canary reached a pull request the same evening, carrying the whole plan and
labelled for review, after five failed attempts that found five wiring bugs
nothing here could have found on its own: a caller granted less than a job
declares, an agent job that could not clone a private repository,
`upload-artifact` silently dropping a dot-directory, a log line on stdout
breaking a step's output, and an artifact rooted at a least common ancestor.
Each is fixed and each is a case. Three of the five were **fail-open** —
green steps that had done nothing. See [Where this stands](#where-this-stands).

## What it does

Someone files an issue that says, in ordinary words, what they want changed.
falconet:

1. assigns itself the issue, opens a branch, and captures a baseline plan
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

## Install it in your repository

The whole path, so you can see the end from the start:

1. [Check the repository qualifies](#1-check-the-repository-qualifies)
2. [Ignore the handoff directory](#2-ignore-the-handoff-directory)
3. [Create the GitHub App and store its two secrets](#3-create-the-github-app-and-store-its-two-secrets)
4. [Store the Anthropic API key](#4-store-the-anthropic-api-key)
5. [Store the planning environment](#5-store-the-planning-environment)
6. [Create the four labels](#6-create-the-four-labels)
7. [Write `.github/falconet.json`](#7-write-githubfalconetjson)
8. [Add the caller workflow](#8-add-the-caller-workflow)
9. [Run a canary issue](#9-run-a-canary-issue)

Each step ends with how to check it worked. Nothing is vendored: the caller
workflow checks this repository out at the ref you name and runs it from
there, so upgrading is changing a ref. Every `gh` command below runs from
inside the repository you are installing into.

### 1. Check the repository qualifies

- **OpenTofu, with each stack in its own subdirectory** — its own root
  module, its own backend, its own providers. falconet runs `tofu -chdir=<stack>`
  and never touches the repository root.
- **Issues enabled.** `gh api repos/{owner}/{repo} --jq .has_issues` → `true`.
- **Actions may run workflows from outside the repository.**
  `gh api repos/{owner}/{repo}/actions/permissions --jq .allowed_actions`
  must be `all`, or `selected` with `zetlen/falconet`, `actions/*`,
  `opentofu/setup-opentofu` and `anthropics/claude-code-action` in the list.
  A repository restricted to local actions stops before any of this runs.
- **Linux x64 runners.** The action installs a pinned
  `gitleaks_<version>_linux_x64` release and checks its digest, so macOS or
  ARM fails the checksum. `jq` must be present; `ubuntu-latest` has it.
- **A clean tree on a fresh checkout.** Two verbs read `git status`. If a
  hook or generator leaves untracked files behind on checkout, gitignore them.

If `gh api repos/{owner}/{repo}/actions/permissions/workflow` says
`default_workflow_permissions` is `read` — the default for new repositories —
that is fine: step 8's caller workflow grants what it needs explicitly.

### 2. Ignore the handoff directory

The verbs pass files to each other through `.falconet/`, which must never be
part of a change. Add it to `.gitignore` and commit:

```sh
printf '.falconet/\n' >> .gitignore
git add .gitignore && git commit -m "Ignore falconet's handoff directory"
```

**Check:** `git check-ignore -v .falconet/` names the line.

In CI the workflow excludes this path per clone whether or not you did this.
The entry is for running the verbs by hand — where `commit` would otherwise
refuse its own scratch files as paths outside the allowlist — and for the
human who runs `git add -A`.

### 3. Create the GitHub App and store its two secrets

A GitHub App registered purely as a credential: no webhook, nothing hosted.
On **github.com → Settings → Developer settings → GitHub Apps → New GitHub
App** (under the organisation's settings if the repository belongs to one):

| Field | Set it to |
| --- | --- |
| GitHub App name | Anything unique across GitHub. Comments and pull requests are authored as `<this name>[bot]`. |
| Homepage URL | The repository's URL; it is required and unused. |
| Webhook → Active | **Untick.** |
| Repository permissions | **Contents: Read and write**, **Issues: Read and write**, **Pull requests: Read and write**. Nothing else. |
| Where can this GitHub App be installed? | Only on this account. |

After **Create GitHub App**, on the App's page:

1. Note the **App ID** near the top.
2. Under **Private keys**, **Generate a private key**. A `.pem` downloads.
3. In the left sidebar, **Install App** → your account → **Only select
   repositories** → this repository → **Install**.

Then, from inside the repository:

```sh
gh secret set FALCONET_APP_ID --body '<the App ID>'
gh secret set FALCONET_APP_PRIVATE_KEY < ~/Downloads/<app-name>.<date>.private-key.pem
rm ~/Downloads/<app-name>.<date>.private-key.pem
```

The whole PEM, header and footer lines included.

**Check:** `gh secret list` shows both, and the repository's **Settings →
GitHub Apps** lists the App as installed. A run that fails at
`actions/create-github-app-token` with *Could not find installation* has
the App registered but not installed here.

An App rather than a PAT or `GITHUB_TOKEN` because pull requests opened with
`GITHUB_TOKEN` do not trigger workflows — your CI would never run on the PRs
falconet opens — and App-token pushes do. [operating.md](docs/operating.md)
says why each credential is the kind it is.

### 4. Store the Anthropic API key

```sh
gh secret set ANTHROPIC_API_KEY
```

An **API key** from the Anthropic console, not a Claude Code subscription
token: if you already run `anthropics/claude-code-action` with
`claude_code_oauth_token`, that secret is a different thing and will not work
here. A dedicated key keeps falconet's spend a separate number — set a budget
alert on it. The agent pass is capped at 40 turns and 30 minutes.

**Check:** `gh secret list` shows `ANTHROPIC_API_KEY`.

### 5. Store the planning environment

`FALCONET_PLAN_ENV` is one JSON object of environment variables — whatever you
export before `tofu init && tofu plan` in the stacks you will name in
`stacks.plan`. Backend keys, provider tokens, `TF_VAR_*`. falconet masks every
value and hands them to the two jobs that run tofu, and to no other.

```sh
# From a shell where the values are already exported:
jq -n '{
  AWS_ACCESS_KEY_ID:     env.AWS_ACCESS_KEY_ID,
  AWS_SECRET_ACCESS_KEY: env.AWS_SECRET_ACCESS_KEY,
  CLOUDFLARE_API_TOKEN:  env.CLOUDFLARE_API_TOKEN
}' | gh secret set FALCONET_PLAN_ENV
```

Or write the object to a file **outside** the repository, `jq -e 'type ==
"object"' < that-file`, then `gh secret set FALCONET_PLAN_ENV < that-file`
and delete it.

What belongs in it:

- **Only what the `stacks.plan` stacks need.** `validate_only` stacks are
  initialised with `-backend=false` and never configure a provider, so they
  need nothing.
- **Read-only credentials.** The default plan command runs with
  `-refresh=false -lock=false`, so a state credential that can read but not
  write or lock is enough, and falconet never applies. The repository this
  was extracted from planned with exactly such a pair.
- **Placeholders, where the provider allows.** A provider that makes no API
  calls during a refresh-less plan only has to be *configured*. The origin
  planned its DNS stack with placeholder registrar credentials — real values
  exist only in the job that applies, which is not this tool.
- **Contents, not paths.** A variable that names a file on your machine has
  nothing to point at on a runner. Use the provider's inline-contents
  variable if it has one; otherwise leave that stack in `validate_only`.

Multi-line values such as a PEM are fine; masking is per line. If every stack
you plan needs no credentials at all, skip this step.

**Check:** `gh secret list` shows `FALCONET_PLAN_ENV`. A stored secret cannot
be read back, so the `jq -e` above is the check that it parses.

### 6. Create the four labels

```sh
for l in infra-request needs-info ready-for-human needs-plan-review; do
  gh label create "$l" 2>/dev/null || echo "$l already exists"
done
```

| Label | Applied by | Config key |
| --- | --- | --- |
| `infra-request` | a person, to queue a request | `issue.queue_label` |
| `needs-info` | falconet, pausing a question back to the requester | `labels.needs_info` |
| `ready-for-human` | falconet, pausing a run a person has to take over | `labels.human` |
| `needs-plan-review` | falconet, on the pull request it opens | `labels.pr` |

All four before the first run: `gh issue edit --add-label` fails on a label
that does not exist, which turns a hand-over into a failed step at precisely
the moment falconet is trying to tell somebody something.

An issue form with `labels: ["infra-request"]` in its front matter means
requesters never have to label anything. A checkbox whose text is `Not
eligible for AI agents` (`issue.opt_out_text`) lets them keep a request away
from the agent.

**Check:** `gh label list --json name --jq '.[].name' | grep -cxE 'infra-request|needs-info|ready-for-human|needs-plan-review'` → `4`.

### 7. Write `.github/falconet.json`

Optional in principle — every key has a default — but the defaults name the
stacks this was extracted from, so in practice every repository sets
`stacks`. The file is merged **over** the defaults: naming one key changes
one thing. Arrays replace wholesale rather than append, because an allowlist
that grows by accident is not an allowlist. A malformed file is a hard
failure with jq's parse error, never a silent fall back to defaults.

```json
{
  "stacks": {
    "plan": ["dns"],
    "validate_only": ["workspace", "site"]
  }
}
```

The rule for sorting stacks: **`plan` is every stack a human will apply from
the pull request; `validate_only` is every other directory with `.tf` in
it.** A planned stack is initialised, planned, and its plan becomes the
evidence in the PR. A validate-only stack is initialised with
`-backend=false` and validated — a broken stack is still caught, and a
reviewer is never shown a diff their approval cannot act on.

Every key, with its default:

| Key | Default | What it is |
| --- | --- | --- |
| `stacks.plan` | `["dns"]` | Stacks to init, plan, and put in the PR. Directories. |
| `stacks.validate_only` | `["workspace", "site"]` | Stacks to validate without a backend. Directories. |
| `paths.allow` | `["*.tf"]` | Globs the agent's change must stay inside; `*` crosses `/`, so `*.tf` matches `dns/records.tf`. Anything outside is refused and nothing is committed. |
| `paths.deny_content` | `data "external"`, `provisioner`, `local-exec`, `remote-exec`, `templatefile(`, `filebase64(`, `file(` | Constructs refused anywhere in a changed `.tf`, in this order. |
| `plan.command` | `tofu -chdir={stack} plan -no-color -input=false -refresh=false -lock=false` | Run per planned stack. falconet runs `tofu init` first only when this starts with `tofu`; any other command owns its own initialisation. |
| `issue.queue_label` | `infra-request` | The label that makes an issue eligible. |
| `issue.blocking_labels` | `needs-info`, `ready-for-human`, `do-not-apply`, `wontfix` | Any of these present and the issue is ineligible. Need not exist. |
| `issue.opt_out_text` | `Not eligible for AI agents` | A ticked checkbox with this text makes the issue ineligible. |
| `issue.branch_prefix` | `issue-` | Branches are `<prefix><number>-<slug>`. |
| `issue.in_flight_prefixes` | `["issue-", "claude/issue-"]` | An open PR from a branch with any of these prefixes and this number means "already in flight". |
| `labels.needs_info` / `labels.human` / `labels.pr` | `needs-info` / `ready-for-human` / `needs-plan-review` | Step 6's labels, if you named them differently. |
| `prompts.implement` | the shipped [`prompts/implement.md`](prompts/implement.md), embedded in the binary | Path, relative to your repository root, of a prompt of your own for the agent. Absent, the shipped one is used. |
| `prompts.pause_needs_info` | the shipped [`prompts/pause-needs-info.md`](prompts/pause-needs-info.md), embedded in the binary | Likewise, for the question posted back to a requester. |
| `handoff_dir` | `.falconet` | Where the verbs leave files for each other. Gitignore it if you move it. |

**The one default that does not transfer is the prompt.** The shipped
[`prompts/implement.md`](prompts/implement.md) carries a "standing facts"
block describing the repository this came from — its registrar sandbox, its
scratch tenant — and the copy embedded in the binary is that one. To change
it, copy the file into your repository, replace that block with what is true
of yours, and point `prompts.implement` at the copy. `{handoff}` and
`{workspace}` in it are substituted at run time.

**Check:**

```sh
jq -e . .github/falconet.json > /dev/null && echo parses
jq -r '.stacks[][]' .github/falconet.json | while read -r s; do
  test -d "$s" && echo "ok       $s" || echo "MISSING  $s"
done
```

A configured name that is not a directory fails the gate with a message
naming the key, the file it came from, and what belongs there.

### 8. Add the caller workflow

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

# A called workflow can only narrow the caller's token, never widen it, so
# each of these must be at least what the widest job inside declares —
# `publish` declares `contents: write` to push. That check happens when the
# file is LOADED: grant less and the run is a `startup_failure` with no jobs,
# no logs and nothing on the issue.
#
# It is narrower than it reads. `implement`, the job that runs the agent,
# declares `permissions: {}` and holds no token at all; `gate` and `contain`
# narrow themselves back to `contents: read`. Only `publish` receives this,
# and it pushes with the App token in any case.
permissions:
  contents: write
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

| Input | Required | Default | What it is |
| --- | --- | --- | --- |
| `issue` | yes | — | The issue number to work. |
| `config` | no | `.github/falconet.json` | Path to the config file. |
| `falconet-ref` | no | `main` | Which falconet the jobs check out. |
| `runs-on` | no | `ubuntu-latest` | Must stay Linux x64. |

Three things about this file that are not obvious:

- **It triggers on every issue event and decides eligibility inside.** A
  job-level `if:` evaluates before checkout and can never read
  `.github/falconet.json`, so gating there would fork eligibility into
  YAML-in-CI and nothing-locally. `prepare` decides instead, reading the same
  config a workstation reads, and an ineligible event costs runner-seconds
  and stops. Eligible means: the issue is **open**, carries the **queue
  label**, carries none of the blocking labels, has no ticked opt-out box,
  and has no open pull request already on a branch for that number. A
  comment from a bot, or on a pull request, is never a way in. A comment
  from a person on an issue paused `needs-info` is the way back in.
- **The ref in `uses:` must be a literal** — GitHub does not expand
  expressions there. `falconet-ref` is a different thing: it chooses which
  falconet the inner checkout steps fetch. Keep the two in step or you will
  debug a version you are not running.
- **It coexists with a stock `claude.yml`.** If you already run
  `anthropics/claude-code-action` on issue events, that one starts on an
  `@claude` mention and this one on the queue label. Don't write `@claude` in
  an infra request unless you want both.

**Check:** after pushing, `gh workflow list` shows `infra requests`.

### 9. Run a canary issue

Pick the smallest change your planned stack can carry — one DNS record, one
tag — and file it the way a requester would, via the form or:

```sh
gh issue create --label infra-request \
  --title "Canary: add a TXT record for falconet" \
  --body "Please add a TXT record named falconet-canary on example.com with the value \"hello\"."
```

Then watch. `gh run watch` follows it, or the Actions tab:

| When | What you should see |
| --- | --- |
| within a minute | A comment on the issue: *Thanks — this request has been picked up and is being worked on automatically.* That is **gate** saying `ready`: eligibility passed, the issue is assigned and the branch exists, the baseline plan ran. |
| next | **implement**: one agent pass, then every guard, then the commit. The agent's only output that outlives the run is its commit message. |
| next | **publish**: the push first — `issue-<n>-canary-add-a-txt-record-for-falconet` appears on the remote before anything else happens — then validate, plan, and the pull request. |
| within ~15 minutes | One of exactly three endings on the issue, below. |
| always | **contain** runs whatever happened above, and if the issue is still open with neither a pause label nor an open PR, it pauses it `ready-for-human` with a link to the run. |

The three endings:

| Ending | What it looks like | What to do |
| --- | --- | --- |
| **A pull request**, labelled `needs-plan-review` | Title is the agent's commit subject. Body is its explanation, then the **entire** plan. | Read the plan. It should show the canary's resources and nothing else — anything else was already in the baseline plan, which is drift, not the agent. Then **close the PR without merging** unless you mean to apply it; in a repository that deploys on merge, the merge *is* the apply. Delete the branch, close the issue. |
| **A question**, labelled `needs-info` | A comment asking the requester something. | Answer it in a comment. That comment re-enters the pipeline: the label is cleared and the same issue is worked again with the answer in hand. |
| **A hand-off**, labelled `ready-for-human` | A comment saying why a person is needed, linking the branch if one was pushed and the run. | Read the reason. It is one of the guards refusing, or validation failing, and the text names which. |

The ending that is *not* on that list — a red run and an issue with only the
acknowledgment, or nothing at all — is a failed gate, and it is silent. See
[Troubleshooting](#troubleshooting).

Once a canary has reached a pull request, **pin the ref**. `main` moves, and
development is integration here. Put the SHA you ran in both places:

```yaml
    uses: zetlen/falconet/.github/workflows/falconet.yml@<sha>
    with:
      falconet-ref: <sha>
```

### Troubleshooting

| What you see | Why | Do |
| --- | --- | --- |
| The run is `startup_failure`: no jobs, no logs, and nothing on the issue at all | The caller grants less than a job inside declares. GitHub checks that when the workflow file is loaded, so nothing runs and nobody is told — including the requester. Until 2026-08-21 this README prescribed `contents: read`, which `publish` exceeds. | Step 8's `permissions:` block, verbatim. |
| **gate** is red and the issue has no comment | `prepare` hard-failed before the acknowledgment — the one failure the requester never hears about, because `contain` is conditioned on the gate having said `ready`. | Open the run; the last lines of **Prepare** name the cause. The usual ones are the next three rows. |
| `config .stacks.plan names "x", which is not a directory` (or `.stacks.validate_only`) | A name in step 7 is not a directory. | Step 7's check. |
| `prepare: tofu init failed in dns/ — the stack cannot be planned`, then OpenTofu's own text: *no valid credential sources*, *error configuring S3 Backend* | `FALCONET_PLAN_ENV` is missing, or missing the key the backend needs. | Step 5. |
| `working tree is dirty before the agent ran`, listing paths | Something in your repository creates untracked files on checkout. | Gitignore them. (An older falconet died here on `.falconet-tool/` itself; pin a ref at or after this README.) |
| `Could not find installation` at `create-github-app-token` | The App exists but is not installed on this repository, or the App ID is wrong. | Step 3. |
| `Resource not accessible by integration` | The caller's `permissions:` block is missing, or the App lacks one of its three permissions. | Steps 3 and 8. |
| `sha256sum: WARNING: 1 computed checksum did NOT match` | The runner is not Linux x64. | `runs-on: ubuntu-latest`. |
| Paused `ready-for-human`: *The agent changed files it is not allowed to change … Refused paths: .falconet/…* | A run by hand with the handoff directory not ignored. | Step 2. |
| Paused `ready-for-human`: *did not validate*, followed by OpenTofu output | Validation or the plan failed on the agent's change. The fenced output is tofu's own. | Read it. A credential error is step 5; anything else is the change. |
| `could not add label` / `label not found` in a pause step | One of step 6's labels is missing. | Step 6. |
| Two runs, two PRs, one issue | The caller lacks the `concurrency` block. | Step 8. |
| The PR's explanation talks about a sandbox or a tenant you do not have | The shipped prompt's standing facts are the origin's. | Step 7, `prompts.implement`. |

### Known limits

- **Static credentials only.** `plan-env` is values in a secret. No job
  declares `id-token: write`, and a caller can only narrow a called
  workflow's permissions, so a backend that authenticates by federated
  identity cannot be planned here yet. Without a static key, `"stacks":
  {"plan": []}` runs validate-only and puts an empty plan block in the PR —
  enough to exercise the wiring, not a place to stay.
- **The credentials are in the environment of the jobs that plan.** Every
  value is masked line by line, `tofu` runs with `-input=false`, and the
  secret scan stands between the agent's drafts and the issue — but the
  validation-failure text posted when a plan fails is OpenTofu's own output.
  Give falconet a credential scoped to what it plans, not your admin key.
- **A failed gate is silent to the requester.** See the first troubleshooting
  row. Watch the first run.
- **`@main` moves.** Pin a SHA once a run has succeeded.
- **Never put issue text in `args`.** If you call `action.yml` directly, its
  `args` input is split on whitespace and reaches a shell. Issue titles,
  bodies and comments are attacker-controlled, and the reason all six verbs
  take files rather than strings is so that text never travels that way.

## Install the binary on a workstation

Two ways, and one snag on macOS. Everything above installs falconet into a
*repository*, where the caller workflow checks this tree out at a ref; this
section is for a person who wants `falconet` on their own PATH.

Mid-port ([ADR-0006](docs/adr/0006-the-rewrite-is-in-go.md)) the binary
answers `version` and `config` itself and hands each verb to this repository's
bash, which it finds through `FALCONET_HOME`. It is released first precisely
because nothing in CI depends on it yet: the release path gets proven on
something small, and the verbs follow.

### From the release page

| Your machine | Asset |
| --- | --- |
| Apple silicon Mac | `falconet_darwin_arm64` |
| Intel Mac | `falconet_darwin_amd64` |
| Linux x86-64 | `falconet_linux_amd64` |
| Linux arm64 | `falconet_linux_arm64` |

Pick the tag you want from [the releases
page](https://github.com/zetlen/falconet/releases), then:

```sh
tag=v0.1.0
asset=falconet_darwin_arm64            # from the table above
base="https://github.com/zetlen/falconet/releases/download/$tag"

curl -fsSL -O "$base/$asset"
curl -fsSL -O "$base/checksums.txt"

# checksums.txt is sha256sum's own format, so the tool checks it for you.
shasum -a 256 --ignore-missing -c checksums.txt   # Linux: sha256sum --ignore-missing -c

chmod +x "$asset"
mkdir -p ~/.local/bin
mv "$asset" ~/.local/bin/falconet                 # anywhere on your PATH
```

**Check:** `falconet version` prints the tag you downloaded, and the Go it was
built with:

```
falconet v0.1.0 (go1.26.5 darwin/arm64)
```

Verify the checksum rather than trusting the download. A release tag is a
mutable pointer and an asset can be replaced — the same reason `action.yml`
pins gitleaks by digest as well as by version. falconet's own `linux_amd64`
digest is committed in this tree at
[`release/falconet_linux_amd64.sha256`](release/falconet_linux_amd64.sha256),
written before the tag exists; the release workflow rebuilds those bytes on a
runner and publishes nothing at all if they differ.

### The macOS quarantine snag

A file a **browser** downloads gets a `com.apple.quarantine` attribute, and
Gatekeeper will not run an unsigned, un-notarised binary that carries one.
Observed on macOS 26: it does not fail with a message — the process simply
hangs. Clear the attribute **before** the first run:

```sh
xattr -d com.apple.quarantine ~/.local/bin/falconet
```

Two things worth knowing. `curl` does not set the attribute, so the recipe
above never trips over this; only a browser download does. And clearing it
after a denial did not reliably help in testing — Gatekeeper had already
made up its mind about that file. Clear it first, or re-download with `curl`.

This is documented rather than solved. Signing and notarising means an Apple
Developer account and a signing identity in CI, which is the same level of
commitment [docs/operating.md](docs/operating.md) declines everywhere else.

### With Go

```sh
go install github.com/zetlen/falconet/cmd/falconet@v0.1.0
```

Nothing is quarantined this way: the file is compiled locally rather than
arriving through a browser. `falconet version` still prints the tag — no
version is stamped in on this path, so it reads the module version the `go`
command resolved.

One difference to expect: this builds with **your** Go, not the pinned one.
`GOTOOLCHAIN=auto`, the default, is a floor and not a pin — it fetches the
toolchain `go.mod` names only when yours is *older*, and quietly uses a newer
local Go otherwise. So `falconet version` may report a Go newer than the
release assets do. That is fine here, where nothing is being compared against
a digest; it is exactly what the release workflow must not do, and does not.

### No tap, no install script

There is no Homebrew tap and no `curl … | sh`. A tap is a second repository
to keep in step with every release, and an install script is a thing this
project would have to ask people to pipe into a shell. Both are the level of
commitment [docs/operating.md](docs/operating.md) has not made — the same
answer as no code of conduct and no marketplace listing.

## How it is built

Six verbs, one per stage. They never call each other; they pass files
through the handoff directory, so the same sequence runs in CI and on a
workstation. Uniform exit codes: **0** an outcome was determined, **1**
refused or a check failed, **2** usage. The verbs that decide something
print exactly one word on stdout.

| Verb | What it does | Words |
| --- | --- | --- |
| `prepare --issue N` | eligibility gate, assignment, branch, baseline plan | `ready` `in-flight` `ineligible` |
| `commit` | every guard, then the commit the agent cannot make | `success` `needs-info` `failure` |
| `push --branch B` | the branch onto the remote, the moment a commit exists | — |
| `validate --base S` | validate and plan each stack, collecting failures | — |
| `pause --issue N --label L` | a terminal state, said where the requester reads it | `success` `failure` |
| `assemble --plan F --out F` | a PR body carrying the whole plan | — |

The reusable workflow runs them as four jobs — **gate**, **implement**,
**publish**, **contain** — and the boundaries between the jobs are the
security model: the agent's job has `permissions: {}` and no secret but the
model key; the scripted jobs hold the token and do the mechanics.
[`.github/workflows/falconet.yml`](.github/workflows/falconet.yml) documents
the trade it makes and why.

The same verbs run by hand, against the repository you are standing in — no
workflow, no credentials beyond a `gh` login (`pause`, which speaks to the
API directly, wants `GH_TOKEN` and `GITHUB_REPOSITORY=owner/name` instead):

```sh
falconet validate --base "$(git rev-parse main)"
```

### Design commitments

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

## Where this stands

| Piece | State |
| --- | --- |
| `bin/`, `lib/`, `libexec/falconet/` — the CLI | six verbs, working |
| `tests/` — 12 files, 484 assertions | passing (`bash tests/run.sh`) |
| `action.yml` + `.github/workflows/falconet.yml` | written, wiring invariants tested, never run |
| credentials for the jobs that plan | one `plan-env` secret, static values only |
| `prompts/` | extracted from the provenance; the standing-facts block is the origin's |
| `docs/adr/` — the decisions | [0002](docs/adr/0002-extract-the-pipeline-into-falconet.md) founding, [0003](docs/adr/0003-the-cli-surface.md) the surface, [0004](docs/adr/0004-the-strangler-reaffirmed.md) the language, [0005](docs/adr/0005-the-agent-job-is-handed-its-source.md) how the agent job gets the code, [0006](docs/adr/0006-the-rewrite-is-in-go.md) Go, and why |
| `docs/provenance/` — the retired orchestrator | reference only |
| A live run | **yes.** 2026-08-21, `zetlen/wayfinders-infra` issue #106 → PR #108: acknowledgment inside a minute, one agent pass, every guard, a real plan, and a pull request carrying it in full — authored by the App, labelled `needs-plan-review`, and the consumer's own CI ran on it, which is what the App credential was for. |
| Rewrite | decided — Go, setup first ([ADR-0006](docs/adr/0006-the-rewrite-is-in-go.md)); not started |

The port from stage-shaped scripts to a coherent CLI is done; [the plan it
followed](docs/adr/pre-execution-plan.md) records what changed and what was
found on the way. Preparing a consumer, and then running one, has since found eight wiring bugs that
no unit test of a single verb could see — binaries not installed in the job
that needed them, stacks not initialised before the baseline plan, the tool's
own checkout dirtying the tree it was about to inspect, an install
document that told people to grant less than the workflow declares, an agent
job that could not obtain the source it was meant to edit, an upload that
silently dropped a dot-directory, a log line on stdout that broke the step
that printed it, and an artifact rooted somewhere neither job looked — each
now pinned by [`tests/contract.test.sh`](tests/contract.test.sh). Three of
them were failures that reported success. What has not
happened is a real run: development *is* integration here.

Two further documents: [operating](docs/operating.md) covers the credentials
only the operator can create and where the pieces live; [AGENTS.md](AGENTS.md)
is what to read before changing anything here.

## Running the tests

```sh
bash tests/run.sh            # all of them
bash tests/run.sh handover   # just the files matching "handover"
```

They stub `gh` for the verbs that still use it, serve a fake GitHub API on
loopback (`tests/fixtures/fake-github.py`) for the verbs that have moved off
it, push only into bare repositories under a temp directory, and never touch
the network, GitHub, OpenTofu, or any credential. The suite is green through
the Go binary: `make test`. `pause` (`park` until #5's rename) speaks to
the API directly, its tests serve the fake, and its bash was deleted with
the rename, ahead of the cutover.

## Support

None promised. This is built for one operator's infrastructure repository and
made public because someone else may find the shape useful. Issues and pull
requests may go unanswered; fork freely.

## License

MIT. See [LICENSE](LICENSE).
