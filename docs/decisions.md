# The decision register

Every live decision in falconet: what is true, the
[principle](../README.md#the-invariant-principles) it serves, the observation
that should retire it, and, under the table, the mechanism it rests on. This
is the only document that holds the *why*, and it describes the tree as it
is. How a decision was reached is in git.

A decision is not a rule. It is a choice with a shelf life, and the **Reopen
when** column is the shelf life, written by whoever made the choice, before
they had a stake in defending it. If you can point at a row's trigger in the
present, that row is open. Say so, and change the row.

The Serves column cites the README's five principles by position: `I1` is
the first, `I5` the fifth.

Decisions absent from this table are absent because nobody made them. That is
a finding, not a formatting error.

| Decision | Serves | Reopen when | Record |
| --- | --- | --- | --- |
| The pipeline is falconet's own code, not `gh-aw` | I2 | a change steered by the text of an admitted request, whoever wrote it, gets past the guards and a person's review | [below](#the-pipeline-is-falconets-own-code) |
| A run starts only from a sender with write | I3 | an adopter needs a person without write to start runs, or a forge's adapter cannot answer a login's permission on the repository with the token the gate job holds | [below](#a-run-starts-only-from-a-sender-with-write) |
| The harness is a configured command, and the default is the Claude Code CLI; falconet knows output formats, not harnesses | I1, I2 | a harness the default cannot be, with the same file-only grant, is what most adopters run | [below](#the-harness-is-a-configured-command) |
| A `check` verb and a caller-owned loop | I2, I3 | a check the verb can run requires something the agent job cannot provide (a credential, a service, network access) and cannot be moved out of the critical path | [below](#a-check-verb-and-a-caller-owned-loop) |
| No second, reviewing agent | I5 | a review harness clears the bar: an independent, uncontaminated read of diff and message, worth more than it costs, whose verdict is never in the pull request where a reviewer could mistake it for evidence | [below](#no-second-reviewing-agent) |
| GitHub and Gitea are the forges, chosen by `forge` in the config | I2, I4 | an adopter exists on a third forge, a verb has to branch on which forge it talks to, or Gitea runs a job that holds no Actions credentials | [below](#github-and-gitea-are-the-forges) |
| No default for the path allowlist or the content denylist | I3 | an adopter cannot set the allowlist before the first run, and the cost of one required field outweighs the cost of a default the operator did not choose | [below](#no-default-for-the-path-allowlist-or-the-content-denylist) |
| CI and automation configuration is refused by default, and exempted one path at a time | I3, I5 | a forge or CI system reads its configuration from somewhere the list does not name, or adopters' ordinary requests need those files changed often enough that the hand-offs cost more than the exposure | [below](#ci-and-automation-configuration-is-refused-by-default) |
| The shipped prompt says what the config says | I1, I3 | a placeholder the prompt needs has no config key behind it | [below](#the-shipped-prompt-says-what-the-config-says) |
| Stage-level verbs, one JSON config file | I1, I3 | a caller needs an operation no verb exposes, or config needs a type JSON cannot carry | [below](#stage-level-verbs-one-json-config-file) |
| One panel on the run's page, written by gate or contain from job outputs | I4 | a run whose gate ran ends with no panel or with two, or the panel needs a fact no job output or handoff word can carry | [below](#one-panel-on-the-runs-page) |
| Packaged as a reusable workflow plus a composite action; the guards run in a job the agent's code never ran in | I2, I3 | the credentials or setup it demands outgrow the README's eight steps, or a CI system offers a way for code in one step to leave the environment of later steps alone | [below](#a-reusable-workflow-and-a-composite-action) |
| Verbs never call each other; they leave files in `.falconet/` | I1, I4 | the pipeline stops being a job graph | [below](#verbs-never-call-each-other) |
| The suite holds what only a process shows; Go tests hold the logic | I2, I3 | a property is asserted in both places, or the suite needs a tool the runner lacks | [below](#the-suite-holds-what-only-a-process-shows) |
| The language is Go | I2, I3 | a guard cannot be expressed safely in it, or the operator stops being able to read the guards | [below](#the-language-is-go) |
| The GitHub adapter is backed by `gh` | I1, I4 | `gh` cannot be installed, or a verb needs a call `gh api` cannot express | [below](#the-github-adapter-is-backed-by-gh) |
| The Gitea adapter speaks REST through `net/http`, with a repository administrator's token | I3, I5 | a verb needs a Gitea route outside the adapter's allowed list, Gitea answers a login's permission on a repository to a token whose user is not an administrator of it, or Gitea's permission route counts a team's per-unit grants | [below](#the-gitea-adapter-speaks-rest-through-nethttp) |
| A GitHub App, registered purely as a credential | I4, I5 | GitHub offers an identity that needs no App | [below](#a-github-app-purely-as-a-credential) |
| App registration is a workstation script, not a verb | I2, I3 | the script needs something the binary's provenance story gives better (versioning against the guards, in-tree tests), or the App stops being the identity that pushes | [below](#app-registration-is-a-workstation-script) |
| Release binaries at a tag; `go install` at any other ref | I2, I3 | a published release's assets can be changed, or the runners consumers use have no asset and compile in every job | [below](#release-binaries-at-a-tag) |
| falconet produces no evidence for the reviewer; the repository's checks do | I5 | an adopter has no checks on pull requests and cannot run any | [below](#falconet-produces-no-evidence) |

## The pipeline is falconet's own code

`github/gh-aw` and its kind carry three things: a role check on who
triggers a workflow, integrity filtering that keeps untrusted text away
from the agent, and a threat-detection stage that costs a large share of
the agent's own tokens on a small task. falconet has the first, as
[its own rule](#a-run-starts-only-from-a-sender-with-write), and not the
other two. An admitted request's body and thread reach the agent whole,
because they are the request (principle 1), and what stands after them is
the job boundary (principle 2), the guards (principle 3) and a person's
merge (principle 5). So the pipeline is its own code.

## A run starts only from a sender with write

Every event names its sender, the account that labelled, opened, reopened
or commented. An event starts a run only when that account holds `write`
or `admin` on the repository, as the forge answers when `prepare` asks. A
run spends the model key and runner minutes, and a guard anyone can run
against as often as they can file an issue is an oracle (principle 3).
Write is the threshold, with no key, because it is the account that could
have pushed the branch itself, and on a public repository every account
holds `read`.

The question is the `Client`'s `RepoPermission(owner, name, login)`, in
four words, `none`, `read`, `write` and `admin`, which each adapter maps
its forge's answer onto. On GitHub it is `GET
/repos/{owner}/{repo}/collaborators/{login}/permission` with the gate
job's App installation token, under Metadata: read, which every App holds.
Its `permission` is the base role over every grant, repository, team,
organization and enterprise, with maintain answering write and triage
answering read. Whether an installation token's answer carries the same
`permission` field is unverified; README step 8's canary is the check. An
answer in none of the four words, and any failed read, a 404 included, is
exit 1 and no word: the forge answers 404 both for a login that is no user
and for a token that cannot see the repository.

The rule reads the sender and nothing about the issue's author or labels.
A form puts labels on an issue for whoever files it, `author_association`
describes the author rather than the labeller and says nothing about
access, and a forge other than GitHub has no such field. From the event
alone, before any network, `prepare` refuses an event with no sender, a
bot's event of any kind, a comment on a pull request, a comment on an
issue that is not parked `needs-info`, any action but opened, reopened,
labeled and created, and a label event that did not add the queue label.
It asks the permission after the rules that need no network and before the
open pull requests are listed, so an event on an issue that is not queued
costs no request and a refused sender costs one. A refused sender is
`ineligible` and nothing more: no comment, no label, the login and the
threshold on stderr.

A person with write queues a stranger's request by applying the queue
label, removing it first if a form put it there, or by reopening the issue
while it carries the queue label. A comment is a way in only on an issue
parked `needs-info`: a requester without write answers the question, and a
comment from someone with write moves the parked issue on; the author gets
no exemption, because a request's own text decides whether the agent asks
again. A comment on any other issue starts nothing, so a remark on a
queued request that is not parked cannot start a run on it, and running a
request again after its pull request is closed is applying the queue label
again. A re-run replays its event and sender, so re-running a stranger's
event queues nothing. A run with no event has no sender and asks nothing,
because the person holding the token is the one acting; inside GitHub
Actions `prepare` refuses to run without an event.

What this does not cover: the text of an admitted request, its thread, and
edits made before the gate reads it; comments anyone adds to an admitted
issue; a `needs-info` label put on by hand, by a triager or a form, which
parks the issue the same way `pause` does, so that the next comment from
someone with write runs it; automation of the operator's own that labels
or comments as a person; the implement job's public log on a public
repository; and the gate job every event the caller's `if:` lets through
still starts.

## The harness is a configured command

`falconet implement` runs `harness.command` from the config: an argv, no
shell, from the repository root, with the rendered prompt on its stdin and
at `prompt.md` in the handoff directory. What the harness finds and what it
must leave is the README's implement contract, and nothing else in the
tree knows the harness's name. The caller workflow installs it through the
`harness-setup` input and hands it one secret, the model key, under the
name `model-api-key-env` says.

The default is the Claude Code CLI with the five file tools, no shell,
`--permission-mode dontAsk`, a pinned model and a 40-turn cap, and the default
`harness-setup` installs it. The grant is deliberately the smallest that
can edit files, because the job boundary is what holds the agent
(principle 2) and the one thing a shell inside that boundary can reach is
the model key. An operator who runs a harness with a shell accepts that
exposure knowingly, in their own config.

The command is read from the working tree after the agent has had its turn
at it, so a tree that changed the config file is refused before anything
runs, as the check verb refuses it.

falconet knows output formats, not harnesses. `harness.output` names the
format the command prints on stdout: `text`, shown a line at a time, or
`claude-stream-json`, one JSON event per line, shown as one readable line
for each thing the agent did. The default command prints
`claude-stream-json`, and that is `harness.output`'s default when the file
sets no `harness.command`. A file that sets one gets `text` unless it names
a format as well, because a command the operator chose is not read as the
default's JSON. A harness that prints another shape prints it under `text`;
a new format is code in `internal/runlog` and a word in this list. The
format changes what the log shows and nothing `implement` decides: the word
is the harness's exit status.

The harness reads the issue, so what it prints is steered by the issue's
text. No line of it reaches the log as a workflow command: `implement`
splits its output on the line breaks the runner splits on, prints every
`##[` in a line as `##\[`, because the runner finds that form anywhere in a
line, and prints a line whose first non-space characters are `::` behind
`> `. `check` prints the check command's output the same way. The output reaches
the log through a pipe, so once the harness exits, `implement` reads for
five more seconds and stops, whatever the harness left running.

## A `check` verb and a caller-owned loop

The `implement` job runs with `permissions: {}` and no secret but the model
key. The consumer's checkout arrives from `gate` as an artifact
(`source.tgz`) with its remote and credential stripped, and `implement`
refuses it unless `HEAD` is the base `gate` recorded and no remote
survives. That keeps the boundary literal for a private repository, which
answers a tokenless clone with *not found*. falconet itself is downloaded
from a public release, no token needed.

`falconet check` runs the operator's own check, `check.command` in
`falconet.json`, an argv with no shell, from the repository root, on the
tree as the agent left it, and prints one word: `pass`, `fail`, or
`skipped` when no command is configured. On `fail` it writes
`check-failure.txt` into the handoff directory: the command, how it ended,
and the last 64 KiB of its output, cut on a line boundary with a note
saying so. On `pass` it removes that file, so its presence means the last
check failed. A check that could not run at all is a mechanical failure,
exit 1 with no word, because a check that did not happen is neither a pass
nor something the agent can act on. The verb does not loop, does not run
the agent, and does not decide what happens next.

The iteration is the caller's. In `falconet.yml` it is one shell loop:
`falconet implement`, then `falconet check`, and a `fail` before
`max-attempts` is another pass. On a workstation it is the same loop typed
by hand. The verbs are CI-agnostic; the iteration policy is the caller's,
and `contract.test.sh` holds the workflow's: a check after every pass, the
loop turning on the check's word and on nothing else, the commit once and
after the loop.

What feeds back is `check-failure.txt` and nothing else. A guard refusal
(path allowlist, content denylist, rename, secret scan, the config file
itself) is terminal, because a guard the agent can iterate against is an
oracle, not a guard (principle 3), and is decided once, in the commit job,
after the loop, where nothing before it reads the answer. Each attempt is a
fresh agent context on the same prompt: the agent sees the tree with its
earlier edits, the failure file, and the request, and not its own earlier
conversation; the shipped prompt tells it to look for the file and to
rewrite its commit message. At the cap, `commit` runs regardless of the
check's word, so the guard-clean work is committed and pushed and nothing
is lost, and `publish` hands the issue off `ready-for-human` naming the
branch, with the check's output folded under the comment, instead of
opening a pull request nothing passes (principle 4).

The cap is a workflow input rather than a config key because the loop is
the caller's, and a key in the binary's config bounding a count the binary
never takes would be a config that describes someone else's YAML. The check
runs after the first agent pass, not before it, so a base tree that already
fails the check fails every run; the README says so under known limits.

## No second, reviewing agent

One agent implements. A second, reviewing agent costs a second cold context,
and any candidate must clear this bar: an independent, uncontaminated read
of diff and commit message before a person is asked to look, with its
verdict kept out of the pull request, where a reviewer could mistake an
agent's opinion for the repository's own evidence (principle 5).

## GitHub and Gitea are the forges

The verbs depend on the `Client` interface in `internal/forge` and on files
in the handoff directory, and nothing in a verb knows which forge is behind
either. `forge` in the config names the forge, `github` when the key is
absent or `gitea`, and any other value fails the config's load.
`cmd/falconet/forge.go` is the one place that turns it into a client and a
reader of the event file. `prepare` and `pause` ask it for both right after
they read the config, before any file or network, and it refuses a job on a
Gitea Actions runner with `forge` set to github, and a job on a GitHub
Actions runner with `forge` set to gitea, because GitHub's reader refuses a
Gitea payload and Gitea's misreads a GitHub one. Who may start a run is a
`Client` question too, a login's permission on the repository in four
words, so the rule in `prepare` names no forge's field.

GitHub's reader, `github.DecodeEvent`, tells falconet's own events by
`sender.type`, and refuses a payload whose sender has a login and no type,
which is no GitHub payload: a Gitea payload read as GitHub's would call
falconet's own comment a person's, however `forge` came to be github.
Gitea's users carry no type, so `gitea.DecodeEvent` marks an event
falconet's own when its sender's login is `FALCONET_BOT_LOGIN`, compared
without case, and the Gitea client checks that login against its token
before its first request. The login is read from the environment, beside
the token, and not from the config: it is a fact about the credential, the
config is committed, and the token is whichever one the caller holds.

Gitea sends one action, `label_updated`, for every change to an issue's
labels, with the labels the request named as added and as removed. The
replace route names the whole new set as added and the whole old set as
removed, so the reader takes as added only a label named as added and not
as removed, and calls the event `labeled` when there is one, as Gitea
matches a workflow's `labeled` type. An add request that re-sends a label
the issue already carries names it as added, and nothing in the payload
tells that from adding it: a person with write who re-sends the queue label
that way starts a run. A comment on a pull request carries `is_pull`.

Everything else a verb reads from the environment is named as on GitHub.
The token is `GH_TOKEN` or `GITHUB_TOKEN`, the API is `GITHUB_API_URL`, and
the repository is `GITHUB_REPOSITORY`. On Gitea the API is the instance's
`/api/v1`, and a verb refuses to start without it or without
`FALCONET_BOT_LOGIN`, before it reads an event file: every other reader
takes an unset `GITHUB_API_URL` to mean api.github.com, and the client's
first request would send the bot's token there. `prepare` assigns the issue
to `--assignee`, then to `GITHUB_TRIGGERING_ACTOR`, then to the event's
sender, because act, the runner behind Gitea Actions, sets no triggering
actor. With none of them it asks for the token's own login, which on Gitea
is the bot's.

No workflow for Gitea ships. Gitea gives every job Actions credentials of
its own, so a job graph on Gitea has no job that holds nothing for the
agent to run in (principle 2). The verbs run against Gitea from a
workstation, or from a workflow the operator writes and answers for.

## No default for the path allowlist or the content denylist

Both default to empty. `commit` refuses to run when `paths.allow` has no
entries: an allowlist the operator did not write is a choice made for them.

The operator names both, or names an allowlist and no denylist, which is the
honest position for a repository whose program is code, where a string
denylist over a program is a tripwire and not a wall. The recommended shape
for such a repository is that the agent edits a data surface the program
reads, YAML or JSON under an allowlist of its own, and the program stays a
person's; pure data has no denylist to get wrong.

## CI and automation configuration is refused by default

A person merges on the strength of the checks posted on the pull request
(principle 5). For a branch in the same repository, GitHub runs those checks
from the workflow files on that branch, and other forges and CI systems do
the same with their own files. A change to one of them can switch off the
check that would judge it, and no guard can tell a harmless edit to such a
file from one that does that.

So `commit` refuses a change to any path on `ProtectedPaths` in
`internal/commit`, whatever `paths.allow` says: workflows, other CI systems'
files, `CODEOWNERS`, dependency bots, scanner configuration and hook
managers. The operator lifts one entry at a time with
`paths.allow_dangerous_access_to`, and an exempted path must still match
`paths.allow`. The refusal of `.github/falconet.json` runs first, so no
exemption reaches it.

## The shipped prompt says what the config says

The prompt embedded in the binary names nothing of any repository's. It
tells the agent the allowlist and the denylist by interpolating `{allow}`
and `{deny}` from the same config the `commit` verb enforces, so what the
agent is told it may touch is what the guard refuses (principles 1 and 3),
and the two cannot drift. It binds the agent to the consumer repository's
own `AGENTS.md` and README for everything else. A paragraph whose
placeholder renders empty is dropped whole, so an empty denylist does not
read `refused: `. Standing facts an operator wants the agent to take as
given live in that repository's `AGENTS.md`, where they bind a person too;
a prompt of the operator's own (`prompts.implement`) is for when the wording
itself should differ.

## Stage-level verbs, one JSON config file

A thing is a public verb if and only if a caller invokes it directly: the
six pipeline verbs (`prepare`, `implement`, `check`, `commit`, `push`,
`pause`) and `version`. `prompt`, `config`, `scan` and `summary` exist
unlisted: public in that they work, not vocabulary.

Exit codes are uniform: **0** outcome determined, **1** refused
mechanically, **2** usage. A verb that decides something prints exactly one
word on stdout. A check that ran and failed is an outcome, the word `fail`
with exit 0, so a caller can tell it from a check that could not run, which
is exit 1 and no word. `summary` exits 0 always, usage errors included,
because it reports a run and a report must not change the run.
Eligibility (queue label present, no blocking label, opt-out unchecked, a
sender with write) is decided by `prepare`, not by a job-level `if:`: a job
`if:` runs before checkout and cannot read the config, and gating there
would fork eligibility into YAML-in-CI and nothing-locally. That is
principle 1 at the front door: what the agent will read is decided by one
verb from one file. A person's ineligible event spends a few runner-seconds.

The rules a job `if:` does carry need nothing but the event and the queue
label's name: an event whose sender is a bot, a comment on a pull request,
and a label event for any other label are never a way in. `prepare`
refuses all three from the event too, a bot's event for any action, so a
run by hand reaches the same answer. The `if:` is there because falconet's
own comments and labels arrive as bot events, and each would otherwise
spend a gate job.

The config is one JSON file at `.github/falconet.json` (`--config`,
`FALCONET_CONFIG`). Every key is optional but `paths.allow`. JSON because
it is strict and needs no `yq`. Prompt overrides are paths relative to the
repository root; absent, the prompt embedded in the binary is used. The
schema lives in `internal/config`, and the README's config table is its
prose.

## One panel on the run's page

`falconet summary` appends one markdown panel to `$GITHUB_STEP_SUMMARY`:
how the run ended, the issue, the branch, the agent passes used of
`max-attempts`, and the check's last word. gate runs it when `prepare` did
not say `ready`. contain runs it on every run where `prepare` did, which is
every run contain runs on. The two conditions turn on the same output, so a
run whose gate ran has one panel, and `contract.test.sh` holds the pair.

The panel reads job results and outputs and nothing else: `toJSON(needs)`
in contain, `toJSON(steps.prepare)` in gate, the writing job's own
`job.status`, and the outcomes of contain's check and pause steps. Which
guard refused is `failure-kind.txt`'s word, which the commit job reads
into an output; why `prepare` said no is its `reason` output. Neither is
read out of prose or out of the log.

A job whose result is `cancelled` is shown as stopped, cancelled or out of
time, and the panel does not say which. The runner stops a job past its
`timeout-minutes` with the same cancel message a person's cancel sends, so
`cancelled()` and `job.status` inside the job, and its result as the jobs
after it read it, are the same for both.

Every value that is not a fixed word of the panel's is shown in a code span
on one line with no backtick, and the only links are to the repository's own
issue, pull request and branch, built from a validated server, repository,
number and branch name. The step continues on error, and where falconet
was not installed the step writes one fixed line instead.

Code in the agent job can write that job's step summary. The panel is
falconet's only under gate or contain.

## A reusable workflow and a composite action

`.github/workflows/falconet.yml` (`on: workflow_call`) is the job graph:
**gate → implement → commit → publish**, with **contain** running whatever
happened. The boundaries between jobs are the security model: the agent's
job holds no token, the scripted jobs never run the agent, the guards run in
a job the agent's code never ran in, and App installation tokens are minted
per step in the jobs that need them.

The guards need their own job because a step can write `$GITHUB_ENV` and
`$GITHUB_PATH`, and every later step in its job runs with what it wrote.
The repository's check runs code the agent wrote. So the agent's job hands
its change on as a patch in the handoff directory, and the commit job
applies it to gate's checkout and judges it with its own git, gitleaks and
environment. Whatever the agent's job uploads is shaped by the agent; the
commit job verifies it. The loop's `check` output is the agent job's too,
and code the check runs can forge it. That decides only whether the run
opens a pull request or hands off, and the pull request's own checks show
the real result. `action.yml` is setup plus
pass-through: it installs gitleaks by version and digest and falconet at
its own ref, then runs one verb, for a caller that wants a
verb inside a workflow of its own. Nothing of falconet's is vendored into
the adopter's tree; upgrading is moving a tag.

The install is eight steps a person does with `gh` and a browser. An
automated install would need a secret-management apparatus larger than the
install itself, and the reopen trigger is the steps outgrowing what a
person can check.

## Verbs never call each other

They leave files for each other in `handoff_dir` (default `.falconet/`),
written *inside* the consumer's checkout and untracked: `request.md`,
`base-sha.txt`, `branch.txt` from `prepare`; `prompt.md` from `implement`;
`commit-msg.txt` or `needs-info.md` from the agent; `check-failure.txt`
from `check`, present exactly when the last check failed;
`commit-subject.txt`, `commit-body.md`, or `failure-reason.txt` and
`failure-kind.txt` from `commit`, the second one word naming which refusal
it was; `pr.md` from the workflow's own body step. Every job that runs a
verb writes `.falconet/` into `.git/info/exclude` first, because `prepare`
refuses a dirty tree and `commit` refuses any changed path outside the
allowlist, and the consumer's `.gitignore` is not to be relied on. The same
verb sequence therefore runs on a workstation with no GitHub context;
CI-facing exports go to `$GITHUB_ENV` only when it exists, and so does the
one step output a verb sets: `prepare`'s `reason`, the sentence behind a
word that is not `ready` or behind an exit 1, in `$GITHUB_OUTPUT`. The handoff
directory is how principle 1 is literal: the agent's input is a file a
previous step wrote.

`push` runs the moment a commit exists and before any routing, so every run
leaves its branch on the remote (principle 4). `PUSHED_BRANCH` is exported
only when the push lands, and every later `--branch` reads it, never
`BRANCH`.

## The suite holds what only a process shows

`go test ./...` holds the logic: unit and property tests beside each guard
in `internal/`, the config merge, the prompt rendering, the dispatcher's
lists in step with what it implements. `tests/run.sh` holds what cannot be
seen from inside: it spawns `$FALCONET <verb>` and reads the exit code, the
one word on stdout, files in the handoff directory, git state, the calls a
verb makes to its forge through a loopback fake behind `GITHUB_API_URL`,
`fake-github.py` or `fake-gitea.py`, and what the harness and the scanner
are handed by bash stubs. `contract.test.sh` holds the wiring's shape the
same way: no checkout in the agent job, the install before the first verb,
every `uses:` ref one tag, `commit` run once, the loop turning on the
check's word.

A property lives in one of the two places, never both. Where a shell case
and a Go test assert the same thing, the shell case goes, because two
suites agreeing by convention drift.

## The language is Go

One module, one static binary (`CGO_ENABLED=0 -trimpath`, toolchain pinned in
`go.mod`), standard library only: `os/exec` with argv slices, so no shell
and no quoting; `encoding/json`; `regexp`, which is RE2 and linear-time
over attacker-controlled issue text; `embed`. `go.sum` is empty; a
dependency is a change to this row, with a reason. `go vet`, `staticcheck`,
`errcheck` and `govulncheck` are part of green: an ignored error is a red
build. The operator must be able to read a guard cold, and the guards are
the product.

## The GitHub adapter is backed by gh

`internal/forge` defines the `Client` interface, the methods `prepare` and
`pause` need, a login's permission on the repository among them, and the
shapes it answers in. `GH` in `internal/github` shells out to `gh api -i`
with full URLs built from `GITHUB_API_URL`. The
token (`GH_TOKEN` then `GITHUB_TOKEN`) is passed explicitly via `-H` so that
non-github.com hosts, the test server and GitHub Enterprise Server, are
authenticated the same way github.com is. The verbs depend on the interface; nothing in a verb
knows the implementation is `gh`.

What a run needs in CI is git, gitleaks, `gh` and the binary; on a
workstation, the same. `gh` is already there on both, for the install's own
steps and for the two workflow `run:` steps that use it (the pull request,
and contain's check).

## The Gitea adapter speaks REST through net/http

`Client` in `internal/gitea` implements `forge.Client` against Gitea's REST
API, `/api/v1`, with `net/http` from the standard library. `forge` set to
gitea in the config selects it
([GitHub and Gitea are the forges](#github-and-gitea-are-the-forges)).

Its token belongs to a bot user who is an Administrator of the repository.
The sender rule reads another account's permission, and Gitea answers
`GET /repos/{owner}/{repo}/collaborators/{login}/permission` only to a site
administrator, a repository administrator, or the login asking about
itself (`routers/api/v1/repo/collaborators.go` in Gitea). Any other token
gets 403, and the adapter's error for that 403 names the administrator
requirement. Gitea's `owner` reads as `admin`; a word outside Gitea's five
is an error.

That route's `permission` counts the repository's owner, a site
administrator, a direct collaborator's grant, and membership of an
organization team with administrator access. It does not count a team
without administrator access, whose read or write Gitea keeps in per-unit
grants. On a repository an organization owns, a person whose write comes
only from such a team reads `read` or `none`, and the sender rule refuses
them. That person starts a run once they are a direct collaborator with
write, or a member of an administrator team.

The token is a personal access token with three scopes, because Gitea checks
a token's scope on every route: `read:user` for `GET /user`,
`read:repository` for the pull requests and the permission, and
`write:issue` for the issue, its comments, labels and assignees
(`routers/api/v1/api.go` in Gitea). A missing scope answers 403, and a
missing `read:user` fails the self-check before any other request.

A repository administrator's token can merge, change branch protection,
collaborators and secrets, and delete the repository. falconet stops at
the pull request (principle 5), so `allowed` in `internal/gitea/client.go`
lists the nine routes a client sends, and a request that matches none of
them is refused before it is built. A test holds that every listed route is
one a method reaches. The token travels only in an `Authorization: token`
header, never in a URL, and an error's text carries neither the token nor
more than 512 bytes of the server's message. The base URL must be https,
or http to a literal loopback address. The client follows no redirect: a
3xx is an error like any other status outside 2xx. A request times out
after 30 seconds, and an answer larger than 8 MiB is an error. A 2xx with an
empty or `null` body, where a document is read, is an error. Nothing
retries.

Gitea's users carry no type, so the bot's login is what tells falconet's own
comments and labels from a person's. The client is built with that login,
and before its first request it reads `GET /user` once. A failed read, or a
token whose user is another login, compared without case, is an error, and
that client sends nothing further.

Five of Gitea's answers differ from GitHub's, and the adapter answers as the
`Client` does on GitHub:

- **Labels.** Gitea's add looks each name up in the repository's labels and
  its organization's, drops a name it finds in neither, and still answers
  200. The adapter posts the names and requires every one in the answer,
  the issue's labels after the change. Gitea removes a label by id, so the
  adapter reads the issue and deletes the id of every label on it with that
  name. A name not on the issue is a 404, and nothing is deleted.
- **Assignees.** Gitea has no route that adds or removes one assignee. The
  adapter reads the issue and sends the whole set with `PATCH`, so a change
  made between the read and the write is lost. Both callers treat
  assignment as best effort.
- **Issues.** Gitea answers 200 with an empty issue when it fails to load
  part of one. An issue whose number is not the number asked for is an
  error, so no assignee `PATCH` is built from an empty set.
- **Lists.** Gitea caps a page at the instance's `MAX_RESPONSE_ITEMS`,
  which can be lower than the `limit` asked for. Open pull requests are read
  page by page until a page comes back empty, and a list longer than 20
  pages is an error. Gitea pages by offset, so a pull request that closes
  during the read can hide one open pull request from the list.
- **Comments.** The comments route takes no paging parameters and answers
  the whole thread at once.

## A GitHub App, purely as a credential

No webhooks, nothing hosted. The workflow mints installation tokens per step;
output is authored by `falconet[bot]`; App-token pushes fire `pull_request`
events normally, which an Actions-token push does not do, and a pull
request no workflow runs on is one the repository's checks never see
(principle 5). The operator registers it by hand from the README's step, or
by the script below, and puts its ID and private key into the repository's
secrets; installing it is a click in a browser.

## App registration is a workstation script

`install/setup-github.sh` is README step 3 done by the manifest flow, as
bash, not a falconet verb. It does only the GitHub-specific machinery that
cannot sensibly stay manual: the manifest round trip, the code conversion,
the two secrets, the install poll. Labels, config and the workflow file are
not its job; a script that grows them is an installer, and the binary the
agent can reach is not to grow for setup either. It runs once, on the
maintainer's own workstation, where `curl … | sh` is a read-and-run choice
rather than an install vector for consumers. The PEM goes from the
conversion response into `gh secret set` on a pipe and is never a file.

## Release binaries at a tag

Every job that runs a verb installs falconet through the composite action,
at the ref on the `uses: zetlen/falconet@…` line that reached it:
`github.action_ref`, read through each step's `env:`, because inside a
composite action it is empty by the time a `run:` block is evaluated.

At a `vX.Y.Z` ref, on a Linux x64, Linux ARM64 or macOS ARM64 runner, the
action downloads `falconet_X.Y.Z_<os>_<arch>.tar.gz` from that tag's
release. It checks the archive against the release's `checksums.txt`
before unpacking it, and requires `falconet version` to report the tag. At
any other ref, or on any other runner, it runs
`go install github.com/zetlen/falconet/cmd/falconet@<ref>`, with Go from
`actions/setup-go`, pinned by SHA, reading the `toolchain` line of the
action's own `go.mod`. setup-go's cache is off, because it keys on a
`go.sum` under the workspace and the workspace is the consumer's
repository. gitleaks is a release asset pinned by version and digest. A
workstation installs a release with mise's github backend, or compiles one
with `go install`.

A version is a release, cut by release-please from the conventional commit
subjects on `main` ([operating.md](operating.md)). The release pull request
sets every `uses:` line in the workflow to the new tag, through the
`# x-release-please-version` marker each line carries. `contract.test.sh`
refuses lines that disagree, a ref that is not a tag, a line without the
marker, and a manifest version that is not the pinned tag. The release is a
draft, with its tag, until the release workflow has run the suite at the
tag, built the assets with `make assets`, and uploaded them. Only then is it
published.

The integrity story is the release's. The repository has immutable releases
turned on, so publishing locks the release: no asset can be added, replaced
or deleted, and the tag cannot be moved or deleted. `checksums.txt` is
locked with the archives, so the digest check proves the bytes a job
unpacks are the bytes that were published. That is the argument for
principle 3: the guards a job runs were built from the tree at the tag the
caller named, and the release that carries them cannot change once
published. For principle 2 the install holds nothing: no token, the same
step in the tokenless agent job as in every other, so the boundary between
jobs does not rest on a credential. The `go install` path keeps a branch or
a commit of falconet runnable in a consuming repository, where there is no
release to download.

## falconet produces no evidence

falconet opens the pull request and stops. What a reviewer reads about the
change is posted on it by the repository's own checks, tests, a linter, a
plan bot in an infrastructure repository, from credentials falconet never
holds. The pull-request body is the agent's account of the change and a
`Closes` line, and the prompt tells the agent not to guess at what the
checks will say. Branch protection on those checks is what stands between
the pull request and the default branch.

What falconet owes is that the pull request is of the right change, opened
where the checks will see it, with no account in it that a reviewer could
mistake for the evidence. The cost, stated plainly: nothing validates or
formats the change before the pull request beyond `check.command`, and
nothing can see whether the repository's checks run on the App's pull
requests. The canary is the check.
