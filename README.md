# falconet: the tiny code cannon

falconet runs a coding agent in CI with tightly controlled inputs,
permissions and outputs. Someone files an issue in ordinary words; an agent
works it inside a job that holds nothing it could publish with; a person
gets a pull request, or a question, or a hand-off, every time. The agent is
yours to choose: the shipped default is the Claude Code CLI, and any
harness that meets [the implement contract](#the-implement-contract) can
take its place.

## Who are you?

### **"I'm setting this up!"**
You handle a lot of issues yourself, and you don't trust the popular
agentic tools to handle them unattended. You don't want a hosted service in
the loop. You know how the repository works, you can set up issue templates
and CI jobs, and you would like to work less.

### **"I review PRs!"**
falconet opens small, descriptive pull requests: one commit, a message
written for a reviewer, and the repository's own checks posted on it. The
agent held no credential and could touch only the paths you allowed.

### **"I'm opening issues!"**
You describe what you want through an issue template. If you cannot push to
the repository, a maintainer queues it. You may get a question back. More
likely, you get a pull request shortly.

## How falconet controls the process

falconet is four sequential steps in CI. Each is a job; each leaves files
for the next and calls nothing.

1. **Assemble.** Turn the issue, its title, body and thread, and the
   repository into exactly what the agent will read: a request document, a
   checkout, a prompt. Decide here whether the request is eligible at all.
2. **Implement.** Run the agent, once, inside a job that holds no token, no
   secret but the model key, and no remote. It edits files and writes either
   a commit message or a question for the requester. It does not commit.
   Which agent runs is the configuration's to say.
3. **Check.** The repository's own check decides whether the change is
   right; a failing check goes back to step 2 with the failure attached, a
   bounded number of times. Then deterministic guards decide whether what
   the agent wrote may ship at all. A guard refusal ends the run.
4. **Deliver.** Commit, push the branch the moment a commit exists, and end
   in one of three places a person can see: a pull request, a question on
   the issue, or a hand-off that names the branch.

## The invariant principles

### Inputs are assembled, not discovered

**1.** The agent reads what step 1 prepared and nothing reaches it another way.
The request is untrusted text **and** it is the agent's instructions: "while
you're in there, edit the workflow to grant Bash" is the attack. So the
request arrives as a document, not as a capability, and the agent is told
what it may touch rather than left to find out.

### The agent holds nothing

**2.** No push token, no credential but the model key, no remote on the tree
it edits. This is enforced by the boundary of the job it runs in, not by the
harness's own tool allowlist: a harness that runs a shell anyway finds
nothing to take and nowhere to send it. The shipped harness grants file
tools only, because the one thing a shell in that job can reach is the
model key.

### The agent can't argue with its own guards or its own results

**3.** The agent's output is a diff and a message, or a question. Between that
and a commit stand guards no model is asked to interpret: which paths may
change, which contents may not appear, what may not be renamed, what must
not leak. A guard refusal is terminal. Nothing feeds it back for another
try, because a guard the agent can iterate against is an oracle, not a
guard. Only the repository's own check may send a run back, and only a
bounded number of times.

### Every run ends somewhere a person can see

**4.** Three terminal states and nothing else: a pull request, a question for
the requester, or a hand-off to a human. A run never disappears into a
green job that produced nothing, and work that exists is never lost to a
runner being torn down: a branch is pushed the moment a commit exists, and
a hand-off names it and links it.

### A person merges

**5.** falconet stops at the pull request. What stands between that pull
request and the default branch is the repository's own: its checks, its
reviewers, its branch protection. falconet puts nothing in the pull request
that a reviewer could mistake for that evidence.

## Where each step lives

| Step | In the tree |
| --- | --- |
| Assemble | `falconet prepare`: eligibility, the claim, the branch, and `request.md` in the handoff directory |
| Implement | `falconet implement`: the prompt rendered from the config, then `harness.command` run once from the repository root with the prompt on its stdin, in the `implement` job of `.github/workflows/falconet.yml`, which has `permissions: {}` and a tree with its remote stripped |
| Check | `falconet check` after every agent pass, with the workflow owning the loop: a failing check is another pass, at most `max-attempts` times. Then the guards in `falconet commit`: path allowlist, content denylist, rename refusal, secret scan, the config file itself, and the checkout's own git machinery, once, terminally |
| Deliver | `falconet push` the moment a commit exists, then the pull request, or `falconet pause` for a question or a hand-off, including a change whose check still fails at the cap |

[The decision register](docs/decisions.md) holds every live decision, the
principle it serves, and the observation that should retire it.
[operating](docs/operating.md) covers the credentials only the operator can
create.

## The implement contract

The harness is any program. `falconet implement` starts it as an argv from
`harness.command`, with no shell, from the repository root, with the
process's environment, and with the rendered prompt on its stdin. The same
prompt is at `prompt.md` in the handoff directory, for a harness that takes
a file. When it exits 0, the verb says `done` and the check and commit verbs
read what it left.

What the harness finds:

- The repository, checked out on the working branch, at the base commit.
- `request.md` in the handoff directory: the issue's number, title, body and
  thread.
- `check-failure.txt` in the handoff directory, only when a previous pass of
  this run failed the repository's check: the command, how it ended, and the
  end of its output.
- The model key, in the environment variable the caller named
  (`ANTHROPIC_API_KEY` by default). Nothing else: no token, no remote.

What the harness must leave:

- The edited tree, uncommitted, touching only paths in `paths.allow`; and
  `commit-msg.txt` in the handoff directory, a commit subject, a blank line
  and a body written for a reviewer. Or:
- The tree untouched, and `needs-info.md` in the handoff directory: questions
  for the requester, in plain language.

A harness that exits non-zero has failed mechanically, and the run ends in a
hand-off. The default configuration runs the Claude Code CLI with the five
file tools and a 40-turn cap, printing its session as JSON events:

```json
"harness": {
  "command": ["claude", "--bare", "-p",
              "--permission-mode", "dontAsk",
              "--model", "claude-opus-5",
              "--allowedTools", "Read,Edit,Write,Grep,Glob",
              "--max-turns", "40",
              "--output-format", "stream-json", "--verbose"],
  "output": "claude-stream-json"
}
```

What the harness prints on stdout and stderr goes to the run log a line at a
time, while it runs. `harness.output` names the format its stdout is in, and
falconet knows formats, not harnesses. `text` is shown as it arrives.
`claude-stream-json` is shown as one line for each thing the agent did: its
words, each tool call with the path or pattern it named, each tool call that
failed, and a last line with the turns, the time and the cost, which says
when the session stopped at the turn cap. Long text is cut with a marker. A
line that is not JSON is shown as text. No line from the harness can act as
a workflow command: every `##[` in a line is shown as `##\[`, and a line that
starts with `::` is shown behind `> `. The format changes only what the log
shows. The contract above is the same for every format.

The default pins the model it runs, and a `harness.command` of your own
should pin its model too: the same issue produces the same kind of pull
request across runs. To run something else, name it here and install it with
the caller workflow's `harness-setup` input. A harness with a shell is
still inside the job boundary; what it can reach that the default cannot is
the model key.

## Install it in your repository

Eight steps, by hand, and each ends with **Check:** — how to see that it
worked before you go on. Every `gh` command here runs from inside the
repository you are installing into; `gh` and `jq` are your tools, on your
machine, not things falconet needs.

1. [Check the repository qualifies](#1-check-the-repository-qualifies)
2. [Ignore the handoff directory](#2-ignore-the-handoff-directory)
3. [Create the GitHub App and store its two secrets](#3-create-the-github-app-and-store-its-two-secrets)
4. [Store the model API key](#4-store-the-model-api-key)
5. [Create the four labels](#5-create-the-four-labels)
6. [Write `.github/falconet.json`](#6-write-githubfalconetjson)
7. [Add the caller workflow](#7-add-the-caller-workflow)
8. [File the canary](#8-file-the-canary)

Nothing is vendored and nothing of falconet's is checked out into your
repository: the caller workflow names a tag of this repository, and every job
installs the binary that tag vouches for. Upgrading is changing the tag.

### 1. Check the repository qualifies

- **Checks on pull requests.** Whatever posts evidence on a pull request
  when a person opens one (tests, a linter, a plan bot in an infrastructure
  repository) must run on pull requests opened by the App from step 3 too.
  falconet produces no evidence of its own, and a pull request nothing
  checks is a pull request nobody can review. Nothing can verify this for
  you, which is why step 8 ends by reading what the checks posted.
- **Issues enabled.** `gh api repos/{owner}/{repo} --jq .has_issues` → `true`.
- **Who can start a run.** A run starts only from an event whose sender has
  write access: the person who applied the queue label, reopened the issue,
  or commented on an issue paused `needs-info`. On a public repository anyone
  can file an issue, and their request waits for someone with write to queue
  it. A public repository's Actions logs are public too, and the implement
  job's log shows what the agent read and wrote.
- **Actions may run workflows from outside the repository.**
  `gh api repos/{owner}/{repo}/actions/permissions --jq .allowed_actions`
  must be `all`, or `selected` with `zetlen/falconet` and `actions/*` in
  the list.
  A repository restricted to local actions stops before any of this runs.
- **Linux x64 runners.** The action installs a pinned `linux_x64` release
  asset of gitleaks and checks its digest, so macOS or ARM fails the
  checksum. falconet itself is a release asset for Linux x64.
- **A clean tree on a fresh checkout.** Three verbs read `git status`. If a
  hook or generator leaves untracked files behind on checkout, gitignore them.

If `gh api repos/{owner}/{repo}/actions/permissions/workflow` says
`default_workflow_permissions` is `read` — the default for new repositories —
that is fine: step 7's caller workflow grants what it needs explicitly.

**Check:** the `has_issues` and `allowed_actions` calls above answer `true`
and `all`, or `selected` with the two names in `selected-actions`'s
`patterns_allowed`.

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

**By script.** `install/setup-github.sh` does this whole step on your
machine: it registers the App by manifest (one browser click), puts its ID
and private key straight into the repository's secrets, and waits until you
have installed the App. The PEM is piped from GitHub's answer into
`gh secret set` and is never a file. Run it from a clone, or fetch it at
the tag you are installing, the same `vX.Y.Z` step 7 pins on the `uses:`
line:

```sh
bash install/setup-github.sh
# or, with no clone:
curl -fsSL https://raw.githubusercontent.com/zetlen/falconet/vX.Y.Z/install/setup-github.sh | bash
```

Besides `gh` and `jq` it needs `curl`, `openssl` and `python3`. It does
nothing but the App: labels, config and the workflow file stay steps of
yours. Its first line is `setup-github.sh: registering the GitHub App …`.
A run that prints nothing fetched nothing: `curl -f` is silent on a 404
and `bash` exits 0 on empty input, so check that the tag has the file.

**By hand.** On **github.com → Settings → Developer settings → GitHub
Apps → New GitHub App** (under the organisation's settings if the
repository belongs to one):

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

### 4. Store the model API key

```sh
gh secret set ANTHROPIC_API_KEY
```

The default harness is the Claude Code CLI, which reads an **API key** from
the Anthropic console, not a Claude Code subscription token. A dedicated key
keeps falconet's spend a separate number; set a budget alert on it. Each
agent pass runs the model the default `harness.command` pins and is capped
at 40 turns, a run makes at most `max-attempts` passes, and the agent's job
is capped at 60 minutes.

A different harness reads a different key. Store it under whatever name the
harness expects, and name that variable in step 7's `model-api-key-env`.

**Check:** `gh secret list` shows the key.

### 5. Create the four labels

```sh
for l in falconet needs-info ready-for-human falconet-pr; do
  gh label create "$l" 2>/dev/null || echo "$l already exists"
done
```

| Label | Applied by | Config key |
| --- | --- | --- |
| `falconet` | a person with write access, to queue a request | `issue.queue_label` |
| `needs-info` | falconet, pausing a question back to the requester. Put on a queued issue by hand, it parks the issue the same way: the next comment from someone with write access runs it. | `labels.needs_info` |
| `ready-for-human` | falconet, pausing a run a person has to take over | `labels.human` |
| `falconet-pr` | falconet, on the pull request it opens | `labels.pr` |

All four before the first run: `pause` says `failure` and fails its step
when the label it was asked for cannot be put on the issue, which is at
precisely the moment falconet is trying to tell somebody something.

An issue form with `labels: ["falconet"]` in its front matter puts the queue
label on every issue filed through it. A run still starts only for a sender
with write access. Which event GitHub sends for a form's label, and with
whom as its sender, is unverified; step 8's second canary shows what your
repository does. On a repository where anyone can open an issue, leave the
queue label out of the form and apply it to the requests you have read. To
queue a request a form did label, remove the label and add it again. If that
starts no run, close the issue and reopen it. A checkbox whose text is `Not
eligible for AI agents` (`issue.opt_out_text`) lets a requester keep a
request away from the agent.

**Check:** `gh label list --json name --jq '.[].name' | grep -cxE 'falconet|needs-info|ready-for-human|falconet-pr'` → `4`.

### 6. Write `.github/falconet.json`

One key is required, `paths.allow`: the paths the agent may change. It has
no default, because an allowlist you did not write is a choice made for
you, and `commit` refuses to run until it names something. The smallest
useful file is that key and, if the repository has one, the check that
decides whether the agent's change is right:

```json
{
  "paths": { "allow": ["docs/*.md", "config/**"] },
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
| `paths.deny_content` | `[]` | Strings refused anywhere in a changed file, in this order. The shipped prompt tells the agent this list, at `{deny}`; empty, and the prompt says nothing about refused content. In an OpenTofu repository this is where `data "external"`, `provisioner`, `templatefile(` and `file(` go: the constructs that run a command or read a file during a plan. For a repository whose program is code, a string list is a tripwire and not a wall; the honest shape there is an allowlist over a data surface the program reads, and no denylist. |
| `check.command` | `[]` | The repository's own check — tests, a linter, a build — as an argv, run from the repository root with no shell: `["make", "test"]`, `["npm", "test"]`, `["go", "test", "./..."]`. Several commands is a script or a Makefile target. Empty, and `falconet check` says `skipped`. Its output goes to the run log, and on a failure the last 64 KiB of it to `check-failure.txt` in the handoff directory, which the next agent pass reads. |
| `harness.command` | the Claude Code CLI, as shown under [the implement contract](#the-implement-contract) | The agent, as an argv run with no shell from the repository root, with the rendered prompt on its stdin. Any program meeting the contract. Empty is refused. The default pins the model; a `harness.command` of your own should pin its model too, so the same issue produces the same kind of pull request across runs. |
| `harness.output` | `claude-stream-json` when the file sets no `harness.command`; `text` when it sets one | The format the harness prints on stdout, which is how the run log shows it: `text` or `claude-stream-json`. A file that names a command of its own gets `text` unless it names a format too, so a command you chose is never read as the default's JSON. Any other value is refused. The run log's `implement: running` line names the format in use. |
| `issue.queue_label` | `falconet` | The label that queues an issue, applied by a person with write access. A label event for any other label is not a way in. |
| `issue.blocking_labels` | `needs-info`, `ready-for-human`, `do-not-apply`, `wontfix` | Any of these present and the issue is ineligible. Need not exist. |
| `issue.opt_out_text` | `Not eligible for AI agents` | A ticked checkbox with this text makes the issue ineligible. |
| `issue.branch_prefix` | `issue-` | Branches are `<prefix><number>-<slug>`. |
| `issue.in_flight_prefixes` | `["issue-", "claude/issue-"]` | An open PR from a branch with any of these prefixes and this number means "already in flight". |
| `labels.needs_info` / `labels.human` / `labels.pr` | `needs-info` / `ready-for-human` / `falconet-pr` | Step 5's labels, if you named them differently. |
| `prompts.implement` | the shipped [`prompts/implement.md`](prompts/implement.md), embedded in the binary | Path, relative to your repository root, of a prompt of your own for the agent. Absent, the shipped one is used. Either is rendered by `falconet prompt implement`: `{handoff}`, `{workspace}`, `{allow}` and `{deny}` are substituted from this file. |
| `prompts.pause_needs_info` | the shipped [`prompts/pause-needs-info.md`](prompts/pause-needs-info.md), embedded in the binary | Likewise, for the question posted back to a requester. |
| `handoff_dir` | `.falconet` | Where the verbs leave files for each other. Gitignore it if you move it. |
| `forge` | `github` | `github` or `gitea`: which forge `prepare` and `pause` talk to, and how they read its events. A verb on a Gitea Actions runner with `github`, or on a GitHub Actions runner with `gitea`, refuses to start. See [the verbs against Gitea](#using-falconets-verbs-against-gitea). |

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
run time, which is why the output of `falconet prompt implement` is not the
copy to commit: it has already put this machine's paths and this file's
lists where the placeholders were.

**Check:** `jq -e '.paths.allow | length > 0' .github/falconet.json` → `true`;
every `prompts.*` path names a file under the repository root:
`test -f "$(jq -r .prompts.implement .github/falconet.json)"`; and, from a
clean checkout, `falconet check` prints `pass` (or `skipped`, with no
`check.command`) — it runs the command exactly as the agent job will.

### 7. Add the caller workflow

One file, `.github/workflows/falconet.yml`, and this is the whole of it:

<!-- caller-workflow-template -->
```yaml
name: falconet

on:
  issues:
    types: [labeled, reopened]
  issue_comment:
    types: [created]

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
    # The `if:` saves a gate job on what `prepare` refuses from the event alone:
    # a bot's event, which includes falconet's own comments and labels, a
    # comment on a pull request, and a label other than the queue label.
    # `falconet` here is `queue_label` in the config. Who may start a run is
    # `prepare`'s to decide, from the event's sender.
    if: >-
      github.event.sender.type != 'Bot' &&
      !github.event.issue.pull_request &&
      (github.event.action != 'labeled' || github.event.label.name == 'falconet')
    # One run per issue, so two events on one request never race to open two
    # pull requests. `queue: max` keeps every waiting run. With the default, a
    # newer event cancels the run already waiting, and that run can be a
    # person's reply.
    concurrency:
      group: falconet-${{ github.event.issue.number }}
      queue: max
    uses: zetlen/falconet/.github/workflows/falconet.yml@main
    with:
      issue: ${{ github.event.issue.number }}
    secrets:
      app-id: ${{ secrets.FALCONET_APP_ID }}
      app-private-key: ${{ secrets.FALCONET_APP_PRIVATE_KEY }}
      model-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```
<!-- /caller-workflow-template -->

| Input | Required | Default | What it is |
| --- | --- | --- | --- |
| `issue` | yes | — | The issue number to work. |
| `config` | no | `.github/falconet.json` | Path to the config file. |
| `runs-on` | no | `ubuntu-latest` | Must stay Linux x64. A self-hosted runner must be Actions Runner 2.327.1 or newer, which the pinned actions require. |
| `max-attempts` | no | `3` | How many agent passes a run may spend getting `check.command` to pass. Each pass is a fresh agent context with the check's failure in front of it; at the cap the work is committed, pushed and handed off. With no `check.command` the first pass is the only one. The agent job as a whole is capped at 60 minutes. |
| `harness-setup` | no | `npm install -g @anthropic-ai/claude-code` | Shell that puts the harness on `PATH`, run in the agent job before the first pass. Change it together with `harness.command`. |
| `model-api-key-env` | no | `ANTHROPIC_API_KEY` | The environment variable the harness reads its model credential from. The `model-api-key` secret is exported under this name, in the agent job only. |

Three things about this file that are not obvious:

- **Its `if:` saves a gate job, and `prepare` decides.** The `if:` drops an
  event from a bot, which includes falconet's own comments and labels, a
  comment on a pull request, and a label other than the queue label; if you
  set `queue_label`, set the same name in the `if:`. `prepare` refuses all
  of those from the event too, and decides the rest, because a job-level
  `if:` evaluates before checkout and can never read
  `.github/falconet.json`. Eligible means: the event's **sender has write
  access** to the repository; the issue is **open**, carries the **queue
  label**, carries none of the blocking labels, has no ticked opt-out box,
  and has no open pull request already on a branch for that number. Anyone
  else's event is ineligible, silently: nothing on the issue, the reason in
  the run log. A comment from someone with write on an issue paused
  `needs-info` is the way back in, so to decline a paused request, remove
  the queue label before you comment. A comment on any other issue starts
  nothing: to run a request again after closing its pull request, remove
  the queue label and apply it again. A re-run replays the event and who
  sent it, so re-running a stranger's event queues nothing.
- **The ref in `uses:` must be a literal** — GitHub does not expand
  expressions there — and it is the one coordinate: the workflow at that ref
  installs falconet, in every job, from this repository's release at that
  ref.
  `main` is where the template starts and it moves; put a tag there —
  `@v1.0.0` — as step 8 says.
- **It coexists with a stock `claude.yml`.** If you already run
  `anthropics/claude-code-action` on issue events, that one starts on an
  `@claude` mention and this one on the queue label. Don't write `@claude` in
  a request unless you want both.

**Check:** before pushing, read the file against the template above: it
uses the reusable workflow, and its `permissions:` block grants `contents:
write`, `issues: write` and `pull-requests: write`, which is what the widest
job inside declares. After pushing, `gh workflow list` shows `falconet`.

### 8. File the canary

Pick the smallest change your repository can carry, a line in a document or
one entry in a config file, and file it the way a requester would, via the
form or:

```sh
gh issue create --label falconet \
  --title "Canary: add a line to the README" \
  --body "Please add the line \"falconet canary\" to the end of README.md."
```

Then watch. `gh run watch` follows it, or the Actions tab:

| When | What you should see |
| --- | --- |
| within a minute | A comment on the issue: *Thanks — this request has been picked up and is being worked on automatically.* That is **gate** saying `ready`: eligibility passed, the issue is assigned and the branch exists. |
| next | **implement**: one agent pass, then `falconet check`, your `check.command` or `skipped`, and, if it failed, another pass with the failure in front of it, up to `max-attempts`. Then every guard, once, and the commit. The agent's only output that outlives the run is its commit message. |
| next | **publish**: the push first — `issue-<n>-canary-add-a-txt-record-for-falconet` appears on the remote before anything else happens — then the pull request. |
| within ~15 minutes, or ~45 with three passes | One of exactly three endings on the issue, below. |
| always | **contain** runs whatever happened above, and if the issue is still open with neither a pause label nor an open PR, it pauses it `ready-for-human` with a link to the run. |
| at the end | The run's page carries one panel: how the run ended (pull request, question, hand-off, the guard that refused, not started and why, failed in which job, or stopped in which job, cancelled or out of time), the issue, the branch, the agent passes used of `max-attempts`, and the check's last word. **gate** writes it when the run ends there, and **contain** on every other run. |

The three endings:

| Ending | What it looks like | What to do |
| --- | --- | --- |
| **A pull request**, labelled `falconet-pr` | Title is the agent's commit subject. Body is its explanation, and nothing else; your repository's own checks post on it. | Read the diff and what the checks posted. It should be the canary's change and nothing else. Then **close the PR without merging** unless you mean to keep it; in a repository that deploys on merge, the merge *is* the deploy. Delete the branch, close the issue. |
| **A question**, labelled `needs-info` | A comment asking the requester something. | Answer it in a comment. From someone with write access, that comment re-enters the pipeline: the label is cleared and the same issue is worked again with the answer in hand. From anyone else it waits for a comment on the issue from someone with write access. |
| **A hand-off**, labelled `ready-for-human` | A comment saying why a person is needed, linking the branch if one was pushed and the run. | Read the reason. It is one of the guards refusing, and the text names which; or the check still failing at the cap, in which case the branch is pushed, the check's output is folded under the comment, and the change is yours to finish or discard; or the harness itself failing, in which case the run log says how. |

The ending that is *not* on that list — a red run and an issue with only the
acknowledgment, or nothing at all — is a failed gate, and it is silent. See
[Troubleshooting](#troubleshooting).

**A second canary, where people without write access file requests.** File
another canary from an account with no write access, through the form if
you have one. Its **Prepare** step says `<login> does not hold write on the
repository`, or no run starts, and the issue gets no comment. Then, from
your own account, remove the queue label and add it again. That run's
**Prepare** step should say `ready`. If the re-add starts no run, close the
issue and reopen it, and that run should say `ready`.

**Pin a tag.** The ref in `uses:` is the one coordinate: the workflow at
`@v1.0.0` installs, in every job, the binary from this repository's `v1.0.0`
release. Put the tag there, never `main`, which moves:

```yaml
    uses: zetlen/falconet/.github/workflows/falconet.yml@v1.0.0
```

**Check:** one of the three endings on the issue, and on a pull request,
your repository's checks posted on it. Nothing posted means the checks do
not run on the App's pull requests; that is their configuration, and it has
to be fixed before the next request.

### Troubleshooting

| What you see | Why | Do |
| --- | --- | --- |
| The run is `startup_failure`: no jobs, no logs, and nothing on the issue at all | The caller grants less than a job inside declares, or passes an input or secret the workflow does not declare. GitHub checks both when the workflow file is loaded, so nothing runs and nobody is told, including the requester. | Step 7's `permissions:` block, verbatim, and only the inputs and secrets the table lists. |
| **gate** is red and the issue has no comment | `prepare` hard-failed before the acknowledgment — the one failure the requester never hears about, because `contain` is conditioned on the gate having said `ready`. | Open the run; the last lines of **Prepare** name the cause. The usual one is the next row. |
| A pull request with nothing posted on it | Your checks do not run on pull requests the App opens: a workflow that only runs for members, or a path filter falconet's branch does not match. | Their configuration. Nothing in falconet decides this. |
| `prepare: working tree is dirty before the agent ran:`, listing paths | Something in your repository creates untracked files on checkout. | Gitignore them. |
| `Could not find installation` at `create-github-app-token` | The App exists but is not installed on this repository, or the App ID is wrong. | Step 3: the App's **Install App** page with this repository selected, and `FALCONET_APP_ID` against the App ID on its page. |
| `Resource not accessible by integration` | The caller's `permissions:` block is missing, or the App lacks one of its three permissions. | Steps 3 and 7. |
| `sha256sum: WARNING: 1 computed checksum did NOT match` in the gitleaks install step | The runner is not Linux x64 — gitleaks' pinned asset is the Linux x86-64 one, and the digest is checked before anything is installed — or the asset was replaced, which is what the digest exists to catch. | `runs-on: ubuntu-latest`. A replaced asset is not yours to fix; do not run it. |
| `curl: (22) The requested URL returned error: 404` in the **Install falconet** step | The ref on the workflow's `uses:` line names a tag with no published release: typed by hand, or a release still a draft. | Pin a tag from [the releases page](https://github.com/zetlen/falconet/releases). |
| Paused `ready-for-human`: *The agent changed files it is not allowed to change … Refused paths: .falconet/…* | A run by hand with the handoff directory not ignored. | Step 2. |
| `paths.allow is empty — set it in .github/falconet.json` in the Commit step, and the run ends in **contain**'s hand-off | The config names no allowlist, and `commit` refuses to guess one. | Step 6: `paths.allow`. |
| Paused `ready-for-human`: *The agent changed .github/falconet.json, which is where the rules for what it may change are read from* | The request talked the agent into editing the config — widening the allowlist, say — which is refused before the new contents are consulted. | Nothing, unless the config should change, in which case a person changes it. Read the request for what it was trying to get past the guard. |
| `harness.output is …; it must be one of text, claude-stream-json` in a verb's step | `harness.output` names a format falconet does not know. | Step 6: `text`, `claude-stream-json`, or no key. |
| `check: could not run [...]` in the agent job's loop step, and the run ends in **contain**'s hand-off | `check.command` names a program the runner does not have, or its first element is not on `PATH`. A check that could not run is neither a pass nor a failure the agent can act on, so the job stops. | Step 6: an argv the runner can start, or install it in `harness-setup`. Test it with `falconet check` from a clean checkout. |
| `implement: could not run [...]` or `implement: the harness failed` in the loop step, and the run ends in **contain**'s hand-off | `harness.command` names a program the agent job does not have, or the harness exited non-zero: a bad model key, a model outage, a crash. The harness's own output is above the line. | `harness-setup` installs what `harness.command` names; the key is stored under the name `model-api-key-env` says. Test it with `falconet implement` from a clean checkout, with the key in your environment. |
| Paused `ready-for-human`: *the repository's own check fails on it and I could not get it passing* | The agent's change failed `check.command` on every pass it was allowed. The branch is pushed and the check's output is in the comment. | Read the output. A check that fails on the base tree too fails every run; fix that first. |
| `could not add label <name> to #N: …` in a pause step, and the word `failure` | The label could not be put on the issue: one of step 5's labels is missing, or the App lacks Issues: write. The comment was still posted — saying the label could not be applied and asking the requester to contact the repository administrator — and `contain` tries again. | Step 5; then step 3's permissions. |
| Two runs, two PRs, one issue | The caller lacks the `concurrency` block. | Step 7. |
| Labelling a request starts no run, or only a skipped one | The label named in the caller's `if:` is not the config's `queue_label`. | Step 7: the same name in both. |
| A request gets no run and no comment, and **Prepare** says `<login> does not hold write on the repository` | The person who labelled, reopened or commented has less than write access: a requester on a public repository, a triage-only labeller, or a form's label when GitHub sends it as its author's event. | Apply the queue label yourself, removing it first if it is there, or comment on an issue paused `needs-info`. |
| **gate** is red: `could not read <login>'s permission on <owner>/<repo>` | The permission read failed: a 404 when the App's token cannot see the repository, a 404 when the sender's account was deleted or renamed after the event, or the forge's outage. | Step 3: the App is installed on this repository. Then re-run. A re-run asks about the same sender, so for a deleted or renamed account, remove the queue label and add it again yourself. |

### Known limits

- **The change is checked only as well as `check.command` checks it.** With
  no command configured, nothing validates or formats the change before the
  pull request, and the pull request's own checks are what says so. The
  guards, the path allowlist, the content denylist and the secret scan,
  decide whether a change may ship, never whether it is right.
- **A check that fails on the base tree fails every run.** The check runs
  after the agent's first pass, not before it, and does not know which
  failures the agent caused. Keep the default branch green, or the loop
  spends its passes on a failure nobody asked the agent to fix.
- **The pull request's checks are yours to run.** falconet cannot tell
  whether any are configured, or whether they run on the App's pull
  requests; the canary is the check.
- **A harness with a shell can read the model key.** The job boundary keeps
  it from publishing anything; it cannot keep it from spending. The shipped
  harness grants no shell.
- **A failed gate is silent to the requester.** See the first troubleshooting
  row. Watch the first run.
- **`@main` moves.** Pin a tag.
- **Nothing checks that the App is installed.** The repository's Settings →
  GitHub Apps says, and so does the first run's `create-github-app-token`
  step.
- **A queued request is its writers' text.** A run starts only for a sender
  with write, and it reads the issue as it is when the gate reads it and
  every comment on it, whoever wrote them. Read a stranger's request before
  you queue it; its author can edit it until the run starts. Every event
  the caller's `if:` lets through still starts a gate job, and on a public
  repository a self-hosted runner is exposed to that; the forge's interaction limits are what slow a
  flood.
- **The run's panel is falconet's only under gate and contain.** Code the
  agent job runs, a harness with a shell or the repository's check, can
  write to the implement job's own step summary. falconet writes its panel
  from gate or contain, and only a panel under one of those two jobs is
  falconet's.
- **A run whose gate never started has no panel.** A caller's `if:` that
  skips the gate, or a `startup_failure`, leaves the run page empty.
- **Never put issue text in `args`.** If you call `action.yml` directly, its
  `args` input is split on whitespace and reaches a shell. Issue titles,
  bodies and comments are attacker-controlled, and the reason every verb
  takes files rather than strings is so that text never travels that way.

## The binary on your machine

Nothing in the install needs it: every job of the caller workflow installs
its own at the tag the workflow names, and the eight steps are `gh` and a
browser. On a laptop the binary runs the verbs by hand (the same `prepare`,
`implement`, `check`, `commit`, `push` and `pause` the workflow runs, from a
checkout, with the loop as a shell loop around `falconet implement` and
`falconet check`) and the test suite runs through it.

Every release on [the releases page](https://github.com/zetlen/falconet/releases)
carries a binary for macOS on Apple silicon, Linux ARM64 and Linux x64. With
[mise](https://mise.jdx.dev):

```sh
mise use github:zetlen/falconet
```

mise picks the asset for your machine by the os and arch in its name,
`falconet_<version>_<os>_<arch>.tar.gz`, and puts `falconet` on your `PATH`.
Each archive holds `falconet` and `LICENSE`. `checksums.txt` in the same
release lists the SHA-256 digest of every archive.

On any other machine, or at a commit rather than a release, compile it:

```sh
go install github.com/zetlen/falconet/cmd/falconet@v1.0.0
```

Name the newest tag from the releases page. The `go` command fetches the
module at that tag through Go's module proxy, checks it against the
checksum database, compiles it for the machine you are on, and leaves it at
`$(go env GOPATH)/bin/falconet`. Put that directory on your `PATH` if it is
not there already. It needs a Go at least as new as the `go` line in this
repository's `go.mod`; `GOTOOLCHAIN=auto`, the default, fetches one if yours
is older.

**Check:** `falconet version` prints the tag and the Go it was built with; a
v1.0.0 build on an Apple-silicon Mac says:

```
falconet v1.0.0 (go1.26.7 darwin/arm64)
```

A `go install` of a commit rather than a tag reports the pseudo-version the
`go` command recorded instead of a tag, and a build from a checkout says
`dev`; either runs.

To work on falconet itself, `make hooks` installs the git hooks in
`lefthook.yml`: a commit subject must be a Conventional Commits subject,
and `make test` runs before a push.

## Using falconet's verbs against Gitea

No workflow for Gitea ships, because Gitea gives every job Actions
credentials of its own, so no job there holds nothing for the agent
([register](docs/decisions.md#github-and-gitea-are-the-forges)). The verbs
run against a Gitea repository from a workstation, or from a workflow of
your own, with `"forge": "gitea"` in `.github/falconet.json` and these in
the environment:

| Variable | What it is |
| --- | --- |
| `GH_TOKEN` | A personal access token of a dedicated bot user who is an Administrator of the repository, with `read:user`, `read:repository` and `write:issue` ([register](docs/decisions.md#the-gitea-adapter-speaks-rest-through-nethttp)). |
| `FALCONET_BOT_LOGIN` | That bot user's login. It is how falconet tells its own comments and labels from a person's, and the client checks it against the token before its first request. |
| `GITHUB_API_URL` | The instance's API, `https://<instance>/api/v1`. Required: unset, it means api.github.com, and a verb refuses to start rather than send the bot's token there. |
| `GITHUB_REPOSITORY` | `owner/name`. |
| `GITHUB_SERVER_URL` | `https://<instance>`, for the branch link `pause` posts. |

`prepare --event` reads the payload Gitea Actions writes for an `issues` or
`issue_comment` event. A label change there is `label_updated` with the
labels the request named under `changes.added_labels` and
`changes.removed_labels`. A label named in both, as the replace route names
every label the issue keeps, was not added. An add request that names the
queue label adds it as far as `prepare` can tell, even when the issue
already carries it. A webhook delivery's payload names no change, and
`prepare` reads it as no way in.

**Check:** `curl -fsS -H "Authorization: token $GH_TOKEN" "$GITHUB_API_URL/user" | jq -r .login`
prints `$FALCONET_BOT_LOGIN`.

## Running the tests

```sh
make test                    # build, go test ./..., then the suite through dist/falconet
bash tests/run.sh            # the suite alone (make build first)
bash tests/run.sh prepare    # just the files whose name contains "prepare"
make check                   # go vet, staticcheck, errcheck, govulncheck at ci.yml's pins
```

`go test ./...` holds the logic: unit and property tests beside each guard,
the config merge, the prompt rendering, the dispatcher's lists in step with
what it implements. `go vet`, `staticcheck`, `errcheck` and `govulncheck`
run in CI beside it, and `make check` runs the same four at the same pinned
versions on a laptop: an ignored error is a red build.

The suite under `tests/` holds what can only be seen from outside the
process: exit codes, the one word on stdout, files in the handoff directory,
git state, the calls a verb makes to GitHub, and what the harness and the
scanner are handed. Every case spawns `$FALCONET <verb>`, `dist/falconet` or
another build of the same contract. GitHub is
[`tests/fixtures/fake-github.py`](tests/fixtures/fake-github.py), a loopback
server the verbs reach through the real `gh`, whose requests follow
`GITHUB_API_URL`; `gitleaks` and the harness are bash stubs; pushes land
only in bare repositories under a temp directory. Nothing touches the
network, GitHub or any credential. `tests/contract.test.sh` reads
`action.yml`, the workflow, the Makefile and this README's caller template
and holds their shape.

## Support

None promised. This is built for one operator's repositories and made public
because someone else may find the shape useful. Issues and pull
requests may go unanswered; fork freely.

## License

MIT. See [LICENSE](LICENSE).
