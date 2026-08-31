# falconet: the tiny code cannon

falconet is a tool for safely running coding agents in CI by tightly controlling their inputs, their permissions, and their outputs. With it, an issue-to-pull-request pipeline for a repository is fast to set up and reliable to run: someone files an issue in ordinary words, an agent works it inside a job that holds nothing it could publish with, and a person gets a pull request — or a question, or a hand-off — every time.

## Who are you?

### **"I'm setting this up!"**
You might be handling a lot of issues yourself, but you don't trust the popular agentic tools to handle issues unattended. You might not want to use paid SaaS like env0 or Spacelift. You know how the code works, you can set up issue templates and CI jobs, and you would like to maybe work less.

### **"I review PRs!"**
If you're not the operator but you review PRs, you'll get used to falconet opening small and highly descriptive PRs, with steps to repro and tests to run. There will be no big stack of commits, and no danger that the agent did something with an unexpected side effect.

### **"I'm opening issues!"**
You are describing what you want, and don't have to make the change yourself. (Or you can't, for whatever reason.) You file an issue, through a structured issue template. Maybe you'll get a question back. But probably, you'll get a pull request implementing your change, pretty shortly.


## How falconet controls the process

Falconet is designed to run four sequential steps in CI. Each is a job; each leaves files for the next and calls nothing.

1. **Assemble.** Turn the issue — title, body, thread — and the repository
   into exactly what the agent will read: a request document, a checkout, a
   prompt. Decide here whether the request is eligible at all.
2. **Implement.** Run the agent, once, with the full judgment of an agent and
   the permissions of none: no shell, no token, no secret, no network but the
   model, and only the harness's own file tools. It edits, and it writes
   either a commit message or a question for the requester. It does not
   commit.
3. **Check.** Deterministic guards decide whether what the agent wrote may
   ship at all; the repository's own checks decide whether it is right. A
   guard refusal ends the run. A failing check goes back to step 1 with the
   failure attached, a bounded number of times.
4. **Deliver.** Commit, push the branch the moment a commit exists, and end
   in one of three places a person can see: a pull request, a question on the
   issue, or a hand-off that names the branch.

## The invariant principles

### Inputs are assembled, not discovered

**1.** The agent reads what step 1 prepared and nothing reaches it another way. The
request is untrusted text **and** it is the agent's instructions — "while
you're in there, edit the workflow to grant Bash" is the attack — so the
request arrives as a document, not as a capability, and the agent is told what
it may touch rather than left to find out.

### The agent holds nothing

**2.** No shell, no push token, no credential of any kind, no network beyond the
model it runs on. The tree it edits arrives with its remote stripped. This is
enforced by the boundary of the job it runs in, not by the harness's own
allowlist: a harness that lets the agent run a shell anyway finds nothing to
take and nowhere to send it.

### The agent can't argue with its own guards or its own results

**3.** The agent's output is a diff and a message, or a question. Between that and a
commit stand guards no model is asked to interpret: which paths may change,
which contents may not appear, what may not be renamed, what must not leak.
A guard refusal is terminal — nothing feeds it back for another try, because
a guard the agent can iterate against is an oracle, not a guard. Only the
repository's own checks may send a run back, and only a bounded number of
times.

### Every run ends somewhere a person can see

**4.** Three terminal states and nothing else: a pull request, a question for the
requester, or a hand-off to a human. A run never disappears into a green
job that produced nothing, and work that exists is never lost to a runner
being torn down — a branch is pushed the moment a commit exists, and a
hand-off names it and links it.

### A person merges

**5.** falconet stops at the pull request. What stands between that pull request
and the default branch is the repository's own — its checks, its reviewers,
its branch protection — and falconet puts nothing in the pull request that a
reviewer could mistake for that evidence. In an infrastructure repository the
evidence is the plan the repository's plan bot posts; falconet's part is that
the pull request is of the right change, on a branch the bot will see.

## Where each step lives

| Step | In the tree |
| --- | --- |
| Assemble | `falconet prepare`: eligibility, the claim, the branch, and `request.md` in the handoff directory |
| Implement | the `implement` job of `.github/workflows/falconet.yml`: `permissions: {}`, the tree from an artifact with its remote stripped, a grant of exactly `Read,Edit,Write,Grep,Glob` |
| Check | `falconet check`, the repository's own check (`check.command`) after every agent pass, with the workflow owning the loop: a failing check goes back to a fresh pass, at most `max-attempts` times. Then the guards in `falconet commit` — path allowlist, content denylist, rename refusal, secret scan, and the config file itself — once, terminally. |
| Deliver | `falconet push` the moment a commit exists, then the pull request — or `falconet pause` for a question or a hand-off, including a change whose check still fails at the cap |

[The decision register](docs/decisions.md) holds every live decision with the
principle it serves and the observation that should retire it.
[`docs/history/`](docs/history/) is the archive of how those decisions were
reached. [operating](docs/operating.md) covers the credentials only the
operator can create.

## Install it in your repository

Eight steps, by hand, and each ends with **Check:** — how to see that it
worked before you go on. Every `gh` command here runs from inside the
repository you are installing into; `gh` and `jq` are your tools, on your
machine, not things falconet needs.

1. [Check the repository qualifies](#1-check-the-repository-qualifies)
2. [Ignore the handoff directory](#2-ignore-the-handoff-directory)
3. [Create the GitHub App and store its two secrets](#3-create-the-github-app-and-store-its-two-secrets)
4. [Store the Anthropic API key](#4-store-the-anthropic-api-key)
5. [Create the four labels](#5-create-the-four-labels)
6. [Write `.github/falconet.json`](#6-write-githubfalconetjson)
7. [Add the caller workflow](#7-add-the-caller-workflow)
8. [File the canary](#8-file-the-canary)

Nothing is vendored and nothing of falconet's is checked out into your
repository: the caller workflow names a tag of this repository, and every job
installs the binary that tag vouches for. Upgrading is changing the tag.

### 1. Check the repository qualifies

- **A plan bot on pull requests.** Atlantis, dflook's `terraform-plan`, or
  whatever already posts a plan when a person opens a pull request; it must
  plan pull requests opened by the App from step 3 too. falconet never runs
  `tofu`, and a pull request nothing plans is a pull request nobody can
  review. Nothing can check this for you, which is why step 8 ends by
  reading the bot's comment.
- **Issues enabled.** `gh api repos/{owner}/{repo} --jq .has_issues` → `true`.
- **Actions may run workflows from outside the repository.**
  `gh api repos/{owner}/{repo}/actions/permissions --jq .allowed_actions`
  must be `all`, or `selected` with `zetlen/falconet`, `actions/*` and
  `anthropics/claude-code-action` in the list.
  A repository restricted to local actions stops before any of this runs.
- **Linux x64 runners.** The action installs a pinned `linux_x64` release
  asset of gitleaks and checks its digest, so macOS or ARM fails the
  checksum; falconet itself is compiled for whatever the runner is.
- **A clean tree on a fresh checkout.** Three verbs read `git status`. If a
  hook or generator leaves untracked files behind on checkout, gitignore them.

If `gh api repos/{owner}/{repo}/actions/permissions/workflow` says
`default_workflow_permissions` is `read` — the default for new repositories —
that is fine: step 7's caller workflow grants what it needs explicitly.

**Check:** the `has_issues` and `allowed_actions` calls above answer `true` and `all` — or
`selected`, with the three names in `selected-actions`'s `patterns_allowed`.

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

The whole PEM, header and footer lines included, and then the download
deleted: the repository secret is the only copy that should exist outside
GitHub.

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
alert on it. Each agent pass is capped at 40 turns; a run makes at most
three passes (step 7's `max-attempts`), and the agent's job is capped at
60 minutes.

**Check:** `gh secret list` shows `ANTHROPIC_API_KEY`.

### 5. Create the four labels

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

All four before the first run: `pause` says `failure` and fails its step
when the label it was asked for cannot be put on the issue, which is at
precisely the moment falconet is trying to tell somebody something.

An issue form with `labels: ["infra-request"]` in its front matter means
requesters never have to label anything. A checkbox whose text is `Not
eligible for AI agents` (`issue.opt_out_text`) lets them keep a request away
from the agent.

**Check:** `gh label list --json name --jq '.[].name' | grep -cxE 'infra-request|needs-info|ready-for-human|needs-plan-review'` → `4`.

### 6. Write `.github/falconet.json`

One key is required, `paths.allow`: the paths the agent may change. It has
no default, because an allowlist you did not write is a choice made for you
— a default that admitted `*.tf` in a Pulumi repository would be exactly
that — and `commit` refuses to run until it names something. The smallest
useful file is that key and, if the repository has one, the check that
decides whether the agent's change is right:

```json
{
  "paths": { "allow": ["dns/*.tf"] },
  "check": { "command": ["make", "test"] }
}
```

The file is merged **over** the defaults: naming one key changes one thing.
Arrays replace wholesale rather than append, because an allowlist that grows
by accident is not an allowlist. A malformed file is a hard failure with the
parse error, never a silent fall back to defaults. And the file is never the
agent's to change, whatever it says the agent may touch: a run that edits
it, or creates one where none was committed, is refused before its contents
are consulted.

Every key, with its default:

| Key | Default | What it is |
| --- | --- | --- |
| `paths.allow` | none — **required** | Globs the agent's change must stay inside; `*` crosses `/`, so `*.tf` matches `dns/records.tf`. Anything outside is refused and nothing is committed. The shipped prompt tells the agent this list, at `{allow}`. |
| `paths.deny_content` | `[]` | Strings refused anywhere in a changed file, in this order. The shipped prompt tells the agent this list, at `{deny}`; empty, and the prompt says nothing about refused content. For an OpenTofu repository the origin's list was `data "external"`, `provisioner`, `local-exec`, `remote-exec`, `templatefile(`, `filebase64(`, `file(` — the constructs that run a command or read a file during a plan. For a repository whose program is code, a string list is a tripwire and not a wall; the honest shape there is an allowlist over a data surface the program reads, and no denylist. |
| `check.command` | `[]` | The repository's own check — tests, a linter, a build — as an argv, run from the repository root with no shell: `["make", "test"]`, `["npm", "test"]`, `["go", "test", "./..."]`. Several commands is a script or a Makefile target. Empty, and `falconet check` says `skipped`. Its output goes to the run log, and on a failure the last 64 KiB of it to `check-failure.txt` in the handoff directory, which the next agent pass reads. |
| `issue.queue_label` | `infra-request` | The label that makes an issue eligible. |
| `issue.blocking_labels` | `needs-info`, `ready-for-human`, `do-not-apply`, `wontfix` | Any of these present and the issue is ineligible. Need not exist. |
| `issue.opt_out_text` | `Not eligible for AI agents` | A ticked checkbox with this text makes the issue ineligible. |
| `issue.branch_prefix` | `issue-` | Branches are `<prefix><number>-<slug>`. |
| `issue.in_flight_prefixes` | `["issue-", "claude/issue-"]` | An open PR from a branch with any of these prefixes and this number means "already in flight". |
| `labels.needs_info` / `labels.human` / `labels.pr` | `needs-info` / `ready-for-human` / `needs-plan-review` | Step 5's labels, if you named them differently. |
| `prompts.implement` | the shipped [`prompts/implement.md`](prompts/implement.md), embedded in the binary | Path, relative to your repository root, of a prompt of your own for the agent. Absent, the shipped one is used. Either is rendered by `falconet prompt implement`: `{handoff}`, `{workspace}`, `{allow}` and `{deny}` are substituted from this file. |
| `prompts.pause_needs_info` | the shipped [`prompts/pause-needs-info.md`](prompts/pause-needs-info.md), embedded in the binary | Likewise, for the question posted back to a requester. |
| `handoff_dir` | `.falconet` | Where the verbs leave files for each other. Gitignore it if you move it. |

**The shipped prompt names nothing of any particular repository's.** It
tells the agent what `paths.allow` and `paths.deny_content` say — the
guard's own config, so what the agent is told it may touch is what the
commit stage enforces — and binds it to your repository's `AGENTS.md` and
README. Standing facts you want the agent to take as given (what is a
sandbox and what is live, where each kind of thing lives, which files it
must never weaken) go in `AGENTS.md`, where they bind a person too. A prompt
of your own is for when the wording itself should differ: copy
[the file](prompts/implement.md) into your repository as
`prompts/implement.md` byte for byte, so the placeholders stay
placeholders, edit it, and point `prompts.implement` at the copy.
`{handoff}`, `{workspace}`, `{allow}` and `{deny}` in it are substituted at
run time, by `falconet prompt implement` — which is why that command's
output is not the copy to commit: it has already put this machine's paths
and this file's lists where the placeholders were.

**Check:** `jq -e '.paths.allow | length > 0' .github/falconet.json` → `true`;
every `prompts.*` path names a file under the repository root:
`test -f "$(jq -r .prompts.implement .github/falconet.json)"`; and, from a
clean checkout, `falconet check` prints `pass` (or `skipped`, with no
`check.command`) — it runs the command exactly as the agent job will.

### 7. Add the caller workflow

One file, `.github/workflows/infra-requests.yml`, and this is the whole of
it:

<!-- caller-workflow-template -->
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
```
<!-- /caller-workflow-template -->

| Input | Required | Default | What it is |
| --- | --- | --- | --- |
| `issue` | yes | — | The issue number to work. |
| `config` | no | `.github/falconet.json` | Path to the config file. |
| `runs-on` | no | `ubuntu-latest` | Must stay Linux x64. |
| `max-attempts` | no | `3` | How many agent passes a run may spend getting `check.command` to pass: 1, 2 or 3. Each pass is a fresh agent context with the check's failure in front of it; at the cap the work is committed, pushed and handed off. With no `check.command` the first pass is the only one. |

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
  expressions there — and it is the one coordinate: the workflow at that ref
  compiles falconet, in every job, from this repository's tree at that ref.
  `main` is where the template starts and it moves; put a tag there —
  `@v1.0.0` — as step 8 says.
- **It coexists with a stock `claude.yml`.** If you already run
  `anthropics/claude-code-action` on issue events, that one starts on an
  `@claude` mention and this one on the queue label. Don't write `@claude` in
  an infra request unless you want both.

**Check:** before pushing, read the file against the template above: it
uses the reusable workflow, and its `permissions:` block grants `contents:
write`, `issues: write` and `pull-requests: write`, which is what the widest
job inside declares. After pushing, `gh workflow list` shows `infra
requests`.

### 8. File the canary

Pick the smallest change your repository can carry — one DNS record, one
tag — and file it the way a requester would, via the form or:

```sh
gh issue create --label infra-request \
  --title "Canary: add a TXT record for falconet" \
  --body "Please add a TXT record named falconet-canary on example.com with the value \"hello\"."
```

Then watch. `gh run watch` follows it, or the Actions tab:

| When | What you should see |
| --- | --- |
| within a minute | A comment on the issue: *Thanks — this request has been picked up and is being worked on automatically.* That is **gate** saying `ready`: eligibility passed, the issue is assigned and the branch exists. |
| next | **implement**: one agent pass, then `falconet check` — your `check.command`, or `skipped` — and, if it failed, another pass with the failure in front of it, up to `max-attempts`. Then every guard, once, and the commit. The agent's only output that outlives the run is its commit message. |
| next | **publish**: the push first — `issue-<n>-canary-add-a-txt-record-for-falconet` appears on the remote before anything else happens — then the pull request. |
| within ~15 minutes, or ~45 with three passes | One of exactly three endings on the issue, below. |
| always | **contain** runs whatever happened above, and if the issue is still open with neither a pause label nor an open PR, it pauses it `ready-for-human` with a link to the run. |

The three endings:

| Ending | What it looks like | What to do |
| --- | --- | --- |
| **A pull request**, labelled `needs-plan-review` | Title is the agent's commit subject. Body is its explanation, and nothing else; your plan bot's comment with the plan follows. | Read the plan the bot posted. It should show the canary's resources and nothing else — anything else is drift, not the agent. Then **close the PR without merging** unless you mean to apply it; in a repository that deploys on merge, the merge *is* the apply. Delete the branch, close the issue. |
| **A question**, labelled `needs-info` | A comment asking the requester something. | Answer it in a comment. That comment re-enters the pipeline: the label is cleared and the same issue is worked again with the answer in hand. |
| **A hand-off**, labelled `ready-for-human` | A comment saying why a person is needed, linking the branch if one was pushed and the run. | Read the reason. It is one of the guards refusing, and the text names which — or the check still failing at the cap, in which case the branch is pushed, the check's output is folded under the comment, and the change is yours to finish or discard. |

The ending that is *not* on that list — a red run and an issue with only the
acknowledgment, or nothing at all — is a failed gate, and it is silent. See
[Troubleshooting](#troubleshooting).

**Pin a tag.** The ref in `uses:` is the one coordinate: the workflow at
`@v1.0.0` compiles, in every job, the binary from this repository's tree at
`v1.0.0`. Put the tag there — never `main`, which moves:

```yaml
    uses: zetlen/falconet/.github/workflows/falconet.yml@v1.0.0
```

If you are upgrading a caller from the bash era, **delete its `falconet-ref:`
input** as well. It no longer exists — there is no checkout left for it to
choose — and a reusable workflow rejects an input it does not declare when
the caller's file is loaded, so the run is a `startup_failure` with nothing
on the issue.

**Check:** one of the three endings on the issue — and on a pull request,
your plan bot's comment showing the canary's resources and nothing else. No
comment means the bot is not planning falconet's pull requests; that is the
bot's configuration, and it has to be fixed before the next request.

### Troubleshooting

| What you see | Why | Do |
| --- | --- | --- |
| The run is `startup_failure`: no jobs, no logs, and nothing on the issue at all | The caller grants less than a job inside declares, or passes an input the workflow does not declare — `falconet-ref`, from a bash-era caller. GitHub checks both when the workflow file is loaded, so nothing runs and nobody is told — including the requester. | Step 7's `permissions:` block, verbatim; no `falconet-ref:`. |
| **gate** is red and the issue has no comment | `prepare` hard-failed before the acknowledgment — the one failure the requester never hears about, because `contain` is conditioned on the gate having said `ready`. | Open the run; the last lines of **Prepare** name the cause. The usual one is the next row. |
| A pull request with no plan comment on it | Your plan bot is not planning pull requests the App opens — a bot that only plans a member's pull requests, or a path filter falconet's branch does not match. | The bot's configuration. Nothing in falconet decides this. |
| `prepare: working tree is dirty before the agent ran:`, listing paths | Something in your repository creates untracked files on checkout. | Gitignore them. |
| `Could not find installation` at `create-github-app-token` | The App exists but is not installed on this repository, or the App ID is wrong. | Step 3: the App's **Install App** page with this repository selected, and `FALCONET_APP_ID` against the App ID on its page. |
| `Resource not accessible by integration` | The caller's `permissions:` block is missing, or the App lacks one of its three permissions. | Steps 3 and 7. |
| `sha256sum: WARNING: 1 computed checksum did NOT match` in the gitleaks install step | The runner is not Linux x64 — gitleaks' pinned asset is the Linux x86-64 one, and the digest is checked before anything is installed — or the asset was replaced, which is what the digest exists to catch. | `runs-on: ubuntu-latest`. A replaced asset is not yours to fix; do not run it. |
| `go: github.com/zetlen/falconet/cmd/falconet@vX.Y.Z: … unknown revision` in the falconet install step | The ref on the workflow's `uses:` line — the ref the action compiles falconet at — names a tag that does not exist: typed by hand, or not yet pushed. | Pin a tag from the tags page. |
| Paused `ready-for-human`: *The agent changed files it is not allowed to change … Refused paths: .falconet/…* | A run by hand with the handoff directory not ignored. | Step 2. |
| `paths.allow is empty — set it in .github/falconet.json` in the Commit step, and the run ends in **contain**'s hand-off | The config names no allowlist, and `commit` refuses to guess one. | Step 6: `paths.allow`. |
| Paused `ready-for-human`: *The agent changed .github/falconet.json, which is where the rules for what it may change are read from* | The request talked the agent into editing the config — widening the allowlist, say — which is refused before the new contents are consulted. | Nothing, unless the config should change, in which case a person changes it. Read the request for what it was trying to get past the guard. |
| `check: could not run [...]` in a Check step, and the run ends in **contain**'s hand-off | `check.command` names a program the runner does not have, or its first element is not on `PATH`. A check that could not run is neither a pass nor a failure the agent can act on, so the job stops. | Step 6: an argv the runner can start, or a setup step of your own before the check runs. Test it with `falconet check` from a clean checkout. |
| Paused `ready-for-human`: *the repository's own check fails on it and I could not get it passing* | The agent's change failed `check.command` on every pass it was allowed. The branch is pushed and the check's output is in the comment. | Read the output. A check that fails on the base tree too fails every run; fix that first. |
| `could not add label <name> to #N: …` in a pause step, and the word `failure` | The label could not be put on the issue: one of step 5's labels is missing, or the App lacks Issues: write. The comment was still posted if it could be, and `contain` tries again. | Step 5; then step 3's permissions. |
| Two runs, two PRs, one issue | The caller lacks the `concurrency` block. | Step 7. |
| The PR's explanation talks about a sandbox or a tenant you do not have | `prompts.implement` names a copy of the shipped prompt from before it stopped carrying the origin's standing facts. | Step 6: edit the copy, or delete it and the key so the shipped prompt is used. |

### Known limits

- **The change is checked only as well as `check.command` checks it.** With
  no command configured, nothing validates or formats the change before the
  pull request, and the plan bot is what says so. The guards — the path
  allowlist, the content denylist, the secret scan — decide whether a change
  may ship, never whether it is right.
- **A check that fails on the base tree fails every run.** The check runs
  after the agent's first pass, not before it, and does not know which
  failures the agent caused. Keep the default branch green, or the loop
  spends its passes on a failure nobody asked the agent to fix.
- **The plan bot is yours to run.** falconet cannot tell whether one is
  configured, or whether it plans the App's pull requests; the canary is the
  check.
- **A failed gate is silent to the requester.** See the first troubleshooting
  row. Watch the first run.
- **`@main` moves.** Pin a tag.
- **Nothing checks that the App is installed.** The repository's Settings →
  GitHub Apps says, and so does the first run's `create-github-app-token`
  step.
- **Never put issue text in `args`.** If you call `action.yml` directly, its
  `args` input is split on whitespace and reaches a shell. Issue titles,
  bodies and comments are attacker-controlled, and the reason every verb
  takes files rather than strings is so that text never travels that way.

## The binary on your machine

Nothing in the install needs it: every job of the caller workflow compiles
its own from this repository at the tag the workflow names, and the eight
steps are `gh` and a browser. On a laptop the binary runs the verbs by hand
— the same `prepare`, `check`, `commit`, `push` and `pause` the workflow
runs, from a checkout, with the loop as a shell loop around `claude -p` and
`falconet check` — and the test suite runs through it.

```sh
go install github.com/zetlen/falconet/cmd/falconet@v1.0.0
```

That is the whole of it. Name the newest tag from
[the tags page](https://github.com/zetlen/falconet/tags); the `go` command
fetches the module at that tag through Go's module proxy, checks it against
the checksum database, compiles it for the machine you are on, and leaves it
at `$(go env GOPATH)/bin/falconet` — put that directory on your `PATH` if it
is not there already. It is the same command the action runs in every CI
job, at the tag your caller workflow names. It needs a Go at least as new as
the `go` line in this repository's `go.mod`; `GOTOOLCHAIN=auto`, the
default, fetches one if yours is older.

No release page, no asset to pick, no checksum to compare by hand, and
nothing for macOS to quarantine: the file was compiled here, not fetched by
a browser.

**Check:** `falconet version` prints the tag and the Go it was built with — a
v1.0.0 build on an Apple-silicon Mac says:

```
falconet v1.0.0 (go1.26.7 darwin/arm64)
```

A `go install` of a commit rather than a tag reports the pseudo-version the
`go` command recorded instead of a tag, and a build from a checkout says
`dev`; either runs.

## Running the tests

```sh
make test                    # build, go test ./..., then the suite through dist/falconet
bash tests/run.sh            # the suite alone (make build first)
bash tests/run.sh prepare    # just the files whose name contains "prepare"
make check                   # go vet, staticcheck, errcheck, govulncheck at ci.yml's pins
```

The suite is the acceptance bar and the incident record. Every case spawns
`$FALCONET <verb>` — `dist/falconet`, or another build of the same contract —
and reads stdout, the exit code and files on disk; nothing reaches inside its
subject. It stubs `gitleaks` with a bash script handed in through
`$GITLEAKS`, whose argv is part of the contract. GitHub is
[`tests/fixtures/fake-github.py`](tests/fixtures/fake-github.py), a loopback
server that answers from fixtures and records what it was asked; the verbs
reach it through the real `gh`, whose requests follow `GITHUB_API_URL`, so
the adapter is exercised end to end and a test token goes nowhere but
loopback. Pushes land only in bare repositories under a temp directory;
nothing touches the network, GitHub or any credential.

`go test ./...` covers what the suite cannot see from outside a process: unit
and property tests (`testing/quick`) beside the guard logic — a pause
comment's truncation never splits a line and never exceeds its budget, the
fence outruns every backtick run, the denylist matches in config order, the config merge, the
slug and the in-flight pattern, the dispatcher's lists in step with what it
implements. `go vet`, `staticcheck`, `errcheck` and `govulncheck` run in CI
beside it, and `make check` runs the same four at the same pinned versions
on a laptop: an ignored error is a red build. The suite needs bash, git, jq,
awk, python3 (stdlib only) and `gh`; `go test` needs Go.

## Support

None promised. This is built for one operator's infrastructure repository and
made public because someone else may find the shape useful. Issues and pull
requests may go unanswered; fork freely.

## License

MIT. See [LICENSE](LICENSE).
