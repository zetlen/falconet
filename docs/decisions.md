# The decision register

Every live decision in falconet: what is true, the
[principle](../README.md#the-invariant-principles) it serves, the observation
that should retire it, and — under the table — the shortest honest account of
why, with what was rejected. This is the only document that holds the *why*.
It describes the tree as it is; it does not narrate how it got here.
[`history/`](history/) does that, and is not for reading before changing
something.

A decision is not a rule — it is a choice with a shelf life, and the **Reopen
when** column is the shelf life, written by whoever made the choice, before
they had a stake in defending it. If you can point at a row's trigger in the
present, that row is open. Say so, and change the row.

The Serves column cites the README's five principles by position: `I1` is
the first, `I5` the fifth. `docslint` holds that every row cites a
principle the README declares and links a section of this file that exists.

Decisions absent from this table are absent because nobody made them. That is
a finding, not a formatting error.

| Decision | Serves | Reopen when | Record |
| --- | --- | --- | --- |
| The pipeline is falconet's own code, not `gh-aw` | I2 | this repository acquires the threat model gh-aw is sized for: strangers triggering workflows | [below](#the-pipeline-is-falconets-own-code) |
| A `check` verb and a caller-owned loop | I2, I3 | a check the verb can run requires something the agent job cannot provide (a credential, a service, network access) and cannot be moved out of the critical path | [below](#a-check-verb-and-a-caller-owned-loop) |
| No second, reviewing agent | I5 | a review harness clears the bar the first one failed: an independent, uncontaminated read of diff and message, worth more than it costs — and its verdict is never in the pull request where a reviewer could mistake it for evidence | [below](#no-second-reviewing-agent) |
| GitHub and Claude Code are the platform | I2 | an adopter exists on another forge or harness | [below](#github-and-claude-code-are-the-platform) |
| No default for the path allowlist or the content denylist | I3 | an adopter cannot set the allowlist before the first run, and the cost of one required field outweighs the cost of a default the operator did not choose | [below](#no-default-for-the-path-allowlist-or-the-content-denylist) |
| The shipped prompt says what the config says | I1, I3 | a placeholder the prompt needs has no config key behind it | [below](#the-shipped-prompt-says-what-the-config-says) |
| Stage-level verbs, one JSON config file | I1, I3 | a caller needs an operation no verb exposes, or config needs a type JSON cannot carry | [below](#stage-level-verbs-one-json-config-file) |
| Packaged as a reusable workflow plus a composite action | I2 | the credentials or setup it demands outgrow the README's eight steps | [below](#a-reusable-workflow-and-a-composite-action) |
| Verbs never call each other; they leave files in `.falconet/` | I1, I4 | the pipeline stops being a job graph | [below](#verbs-never-call-each-other) |
| Every assertion crosses a process boundary | I2, I3 | a property cannot be observed from outside a process — and then it is a Go unit test beside the guard | [below](#every-assertion-crosses-a-process-boundary) |
| The language is Go | I2, I3 | a guard cannot be expressed safely in it, or the operator stops being able to read the guards | [below](#the-language-is-go) |
| The verbs talk to GitHub through a `Client` adapter backed by `gh` | I1, I4 | `gh` cannot be installed, or a verb needs a call `gh api` cannot express | [below](#the-github-adapter-backed-by-gh) |
| A GitHub App, registered purely as a credential | I4, I5 | GitHub offers an identity that needs no App | [below](#a-github-app-purely-as-a-credential) |
| The binary is `go install`ed at the caller's ref | I2, I3 | a job's compile time, or the module proxy's availability, starts costing more than a prebuilt asset would | [below](#the-binary-is-go-installed-at-the-callers-ref) |
| falconet does not plan; the repository's plan bot does | I5 | an adopter has no plan bot and cannot run one, or the plan bot cannot be made to plan on falconet's pull requests | [below](#falconet-does-not-plan) |

## The pipeline is falconet's own code

falconet was extracted from `zetlen/wayfinders-infra`, where ~4,700 lines of
pipeline sat on ~1,000 lines of OpenTofu. Adopting `github/gh-aw` instead was
spiked and measured (2026-08): its detection job cost ~44% of the agent's own
tokens on a small task, and its role checks, integrity filtering and
threat-detection stage are sized for a public repository where strangers
trigger workflows. Here there is one operator, their collaborators, and a
human merge at the end of everything — the README's non-goal, stated as a
threat model. So the pipeline became its own project rather than a rented
approximation.

## A `check` verb and a caller-owned loop

The `implement` job runs with `permissions: {}` and no secret but the model
key. It is handed its own `GITHUB_TOKEN` only so the action does not mint one
— in a job with no permissions that token has no scopes at all and cannot
even read the repository. The consumer's checkout arrives from `gate` as an
artifact (`source.tgz`) with its remote and credential stripped, and
`implement` refuses it unless `HEAD` is the base `gate` recorded and no
remote survives. That is what keeps the boundary literal for a private
repository, which answers a tokenless clone with *not found*; the first live
run (2026-08-21) found that, and the answer was to ship the tree rather than
grant the job `contents: read`. falconet itself is compiled from the public
module, no token needed.

The agent's grant is exactly `Read,Edit,Write,Grep,Glob`, capped at 40 turns.
It edits files, writes `commit-msg.txt` (or `needs-info.md`), and stops.

`falconet check` runs the operator's own check — `check.command` in
`falconet.json`, an argv with no shell (the repository's tests, its linter,
whatever decides whether a change is right) — from the repository root, on
the tree as the agent left it, and prints one word: `pass`, `fail`, or
`skipped` when no command is configured. On `fail` it writes
`check-failure.txt` into the handoff directory: the command, how it ended,
and the last 64 KiB of its output, cut on a line boundary with a note saying
so. On `pass` it removes that file, so its presence means the last check
failed. A check that could not run at all — the command not found — is a
mechanical failure, exit 1 with no word, because a check that did not happen
is neither a pass nor something the agent can act on. The verb does not
loop, does not run the agent, and does not decide what happens next.

The iteration is the caller's. In `falconet.yml` it is three attempts,
unrolled as steps because a `uses:` step cannot be repeated by a `run:`
block: after each agent pass a check step, and each further pass and its
check conditioned on the check before it having said `fail` and on the
caller's `max-attempts` input (1, 2 or 3; default 3). On a workstation it is
a shell loop around `claude -p` and `falconet check`. The verb is
CI-agnostic; the iteration policy is the caller's idiom, and
`contract.test.sh` holds the workflow's: a check after every pass, the
retries conditioned on the check's word and on nothing else, the commit once
and after the last check.

What feeds back is `check-failure.txt` and nothing else. A guard refusal
(path allowlist, content denylist, rename, secret scan, the config file
itself) is terminal — a guard the agent can iterate against is an oracle,
not a guard (principle 3) — and is decided once, in the commit step, after
the last check, where nothing before it reads the answer. Each attempt is a
fresh agent context on the same prompt: the agent sees the tree with its
earlier edits, the failure file, and the request, and not its own earlier
conversation; the shipped prompt tells it to look for the file and to
rewrite its commit message. At the cap, `commit` runs regardless of the
check's word — the guard-clean work is committed and pushed, so nothing is
lost — and `publish` hands the issue off `ready-for-human` naming the
branch, with the check's output folded under the comment, instead of
opening a pull request nothing passes (principle 4).

The cap is a workflow input rather than a config key because the loop is
the caller's, and a key in the binary's config bounding a count the binary
never takes would be a config that describes someone else's YAML. Three
slots because that is what one operator wanted to pay for; the input
describes the ceiling, and a caller wanting four edits the workflow.

Rejected: a workflow-level loop across jobs (push, wait for the pull
request's real checks, re-enter `implement` with the failure) — it uses
the repository's actual CI, but each iteration waits for the whole of it,
and the orchestration crosses the job boundary that is the security model.
The check verb is the faster path; the pull request's own checks still run
after the final push, as they always did, and a failure there is a
person's. Also rejected: running the check before the first agent pass, so
a base tree that already fails is known — one extra run of the operator's
suite on every issue, for a failure the operator can see on their default
branch; the README says so under known limits.

## No second, reviewing agent

The origin ran a reviewing agent after the implementing one. Its second cold
context cost more than it caught. The reference verdict protocol shipped
unwired, was never called, and has been deleted; git has it. Any replacement
must clear the bar the original set: an independent, uncontaminated read of
diff and commit message before a person is asked to look — and its verdict
stays out of the pull request, where a reviewer could mistake an agent's
opinion for the repository's own evidence (principle 5).

## GitHub and Claude Code are the platform

The agent pass runs on `anthropics/claude-code-action`; the workstation
equivalent is `claude -p --allowedTools Read,Edit,Write,Grep,Glob`, a CLI the
binary could spawn like any other. The GitHub client speaks GitHub only.
Forge- and harness-agnosticism are non-goals: adapters are code that pays
off only when someone writes the second one. What makes a second harness
cheap when one arrives is principle 2 as it is built — the job boundary,
not the harness's allowlist, is what holds the agent — so a harness is a
tool grant and a way to run it, and nothing else in the tree knows its name.

Until 2026-08-29 this row also said *an OpenTofu repository is the shape*.
The pilot repository retired that day and is restarting on Pulumi, so that
half's trigger fired; the shipped prompt no longer names an IaC tool (the
prompt row), and what remains of the shape is the config defaults, which
have a row of their own, open.

## No default for the path allowlist or the content denylist

Both default to empty. `commit` refuses to run when `paths.allow` has no
entries — an allowlist the operator did not write is a choice made for them,
and a default that silently admits `*.tf` in a Pulumi repository is exactly
that.

Before 2026-08-29 both defaulted to the origin repository's values:
`["*.tf"]` and the seven HCL constructs that make a plan execute code or read
a file. Those were kept because the origin was the only adopter, and a
default that names nothing is a default an adopter must set before the first
run. The origin retired, and the next repository is a Pulumi one, where a
string denylist over a program is a tripwire and not a wall — `pulumi
preview` executes whatever the file says. So the operator names both, or
names nothing and runs without a denylist, which is the honest position for
a repository whose program is code. The recommended shape for such a
repository is that the agent edits a data surface the program reads — YAML
or JSON under an allowlist of its own — and the program stays a person's;
pure data has no denylist to get wrong.

Rejected: keeping the `*.tf` default with documentation saying whose it is
— a default an adopter must understand to override is a default that will
not be overridden.

## The shipped prompt says what the config says

The prompt embedded in the binary names nothing of any repository's. It
tells the agent the allowlist and the denylist by interpolating `{allow}`
and `{deny}` from the same config the `commit` verb enforces — so what the
agent is told it may touch is what the guard refuses (principles 1 and 3),
and the two cannot drift — and it binds the agent to the consumer
repository's own `AGENTS.md` and README for everything else. A paragraph
whose placeholder renders empty is dropped whole, so an empty denylist does
not read `refused: `. Standing facts an operator wants the agent to take as
given live in that repository's `AGENTS.md`, where they bind a person too;
a prompt of the operator's own (`prompts.implement`) is for when the wording
itself should differ. Until 2026-08-29 the shipped prompt carried the
origin's standing facts — its registrar sandbox, its scratch tenant, its
file layout — and every adopter got them.

## Stage-level verbs, one JSON config file

A thing is a public verb if and only if a caller invokes it directly — the
five pipeline verbs (`prepare`, `check`, `commit`, `push`, `pause`) and
`version`.
`prompt`, `config` and `scan` exist unlisted: public in that they work, not
vocabulary. Rejected: mirroring the origin's script names one-to-one (leaves
orchestration in YAML — two code paths) and grouping by domain (`issue
park`, `git prepare` — a taxonomy at five verbs).

Exit codes are uniform — **0** outcome determined, **1** refused
mechanically, **2** usage — and a verb that decides something prints exactly
one word on stdout. A check that ran and failed is an outcome, the word
`fail` with exit 0, so a caller can tell it from a check that could not run,
which is exit 1 and no word. Eligibility (queue label present, no blocking label, opt-out
unchecked) is decided by `prepare`, not by a job-level `if:`: a job `if:`
runs before checkout and cannot read the config, and gating there would fork
eligibility into YAML-in-CI and nothing-locally. That is principle 1 at the
front door: what the agent will read is decided by one verb from one file.
Ineligible events spend a few runner-seconds; paid willingly.

The config is one JSON file at `.github/falconet.json` (`--config`,
`FALCONET_CONFIG`). Every key is optional. JSON over YAML because it is
strict and needs no `yq`; JSONC was weighed and brings nothing JSON lacks
here. Prompt overrides are paths relative to the repository root; absent, the
prompt embedded in the binary is used. The schema lives in `internal/config`,
and the README's config table is its prose.

## A reusable workflow and a composite action

`.github/workflows/falconet.yml` (`on: workflow_call`) is the job graph:
**gate → implement → publish**, with **contain** running whatever happened.
The boundaries between jobs are the security model: the agent's job holds no
token, the scripted jobs never run the agent, and App installation tokens are
minted per step in the jobs that need them. `action.yml` is setup plus
pass-through — it installs gitleaks by version and digest and falconet by
`go install` at its own ref, then runs one verb — for a caller that wants a
verb inside a workflow of its own. Nothing of falconet's is vendored into
the adopter's tree; upgrading is moving a tag.

This was the old charter's worked example of a mechanism generating work no
principle asked for: chosen in passing, it grew a secret-management problem
— a setup verb, a token for it, an App registered from a manifest, a
sealed-box client for the secrets API — that was read as work to do rather
than as a mechanism reporting a fault. On 2026-08-29 all of that went; the
install is eight steps a person does with `gh` and a browser, and the
reopen trigger is those steps outgrowing what a person can check.

## Verbs never call each other

They leave files for each other in `handoff_dir` (default `.falconet/`),
written *inside* the consumer's checkout and untracked: `request.md`,
`base-sha.txt`, `branch.txt` from `prepare`; `commit-msg.txt` or
`needs-info.md` from the agent; `check-failure.txt` from `check`, present
exactly when the last check failed; `commit-subject.txt`, `commit-body.md`
or `failure-reason.txt` from `commit`; `pr.md` from the workflow's own body
step. Every job that runs a verb writes `.falconet/` into
`.git/info/exclude` first, because `prepare` refuses a dirty tree and
`commit` refuses any changed path outside the allowlist, and the consumer's
`.gitignore` is not to be relied on. The same verb sequence therefore runs on
a workstation with no GitHub context; CI-facing exports go to `$GITHUB_ENV`
only when it exists. The handoff directory is how principle 1 is literal:
the agent's input is a file a previous step wrote.

`push` runs the moment a commit exists and before any routing, so every run
leaves its branch on the remote (principle 4 — run 32093607680 lost a
prepared change to a torn-down runner). `PUSHED_BRANCH` is exported only
when the push lands, and every later `--branch` reads it, never `BRANCH`.

## Every assertion crosses a process boundary

`tests/run.sh` spawns `$FALCONET <verb>` and reads stdout, the exit code and
files on disk; GitHub is a loopback fake behind `GITHUB_API_URL`; `gitleaks`
is a stub whose argv is part of the contract; pushes land in bare
repositories under a temp directory. No test reaches inside its subject — it
is what let the suite adjudicate the port from bash to Go unchanged, and it
keeps the guards behind principles 2 and 3 adjudicable from outside. What
cannot be seen from outside a process — truncation never splitting a line,
the fence outrunning every backtick run, the denylist matching in config
order, a prompt paragraph dropped whole, the check's tail never over budget
and never resuming mid-line — is a Go unit or property test beside the
guard. `contract.test.sh` holds the wiring's shape the same way:
no checkout in the agent job, the install before the first verb, every
`uses:` ref one tag, `commit` run once.

## The language is Go

One module, one static binary (`CGO_ENABLED=0 -trimpath`, toolchain pinned in
`go.mod`), standard library only: `os/exec` with argv slices — no shell, no
quoting — `encoding/json`, `regexp` (RE2, linear-time over
attacker-controlled issue text), `net/http`, `embed`. `go.sum` is empty; a
dependency is a change to this row, with a reason. `go vet`, `staticcheck`,
`errcheck` and `govulncheck` are part of green: an ignored error is a red
build.

The bash it replaced (~1,100 executable lines) ran one live issue and was
deleted in the cutover, not kept beside the Go: two implementations agreeing
by convention is the disease. Rejected: **Bun** (a ~90 MB compiled artifact
or a second setup action; backtracking regexes over attacker text; an npm
tree to audit) and **Rust** (stricter, and it would have caught the
fail-open class of bug at compile time — but the operator must be able to
read a guard cold, and the guards are the product). The incident comment
above each guard moved into Go verbatim; `git log` on the porting commits
names every departure from the bash.

## The GitHub adapter backed by gh

`internal/github` defines a `Client` interface — the eleven methods
`prepare` and `pause` need — and `GH`, the one implementation, shells out
to `gh api -i` with full URLs built from `GITHUB_API_URL`. The token
(`GH_TOKEN` then `GITHUB_TOKEN`) is passed explicitly via `-H` so that
non-github.com hosts — the test server, GitHub Enterprise Server — are
authenticated the same way github.com is. The verbs depend on the interface;
nothing in a verb knows the implementation is `gh`.

Before 2026-08-29 the binary had its own `net/http` client (about 400 lines).
The reason it was bespoke — no `gh` dependency on a workstation — served
the retired adoption principle, and the install's own steps used `gh` on the
operator's machine anyway. Two HTTP paths for one forge was one too many: the
workflow's `run:` steps already shelled out to `gh` for the pull request and
contain's check, and the binary now does the same, through a clean adapter
boundary. What a run needs in CI is git, gitleaks, `gh` and the binary; on
a workstation, the same. Rejected: keeping the `net/http` client as a second
implementation behind the same interface — two implementations agreeing by
convention is the disease, and there is no second consumer to justify
the cost.

## A GitHub App, purely as a credential

No webhooks, nothing hosted. The workflow mints installation tokens per step;
output is authored by `falconet[bot]`; App-token pushes fire `pull_request`
events normally, which is what an Actions-token push does not do — and a
pull request no workflow runs on is one the plan bot never sees (principle
5). The operator registers it by hand from the README's step and puts its ID
and private key into the repository's secrets by hand; installing it is a
click in a browser.

## The binary is `go install`ed at the caller's ref

Every job that runs a verb installs falconet with
`go install github.com/zetlen/falconet/cmd/falconet@<ref>`, where `<ref>` is
the one on the `uses: zetlen/falconet@…` line that reached the composite
action — `github.action_ref`, read through the step's `env:`, because inside
a composite action it is empty by the time a `run:` block is evaluated. Go
is `actions/setup-go`, pinned by SHA, reading the `toolchain` line of the
action's own `go.mod`; its cache is off, because it keys on a `go.sum` under
the workspace and the workspace is the consumer's repository. gitleaks is
still a release asset pinned by version and digest. A workstation runs the
same command at a tag. A version is a git tag and nothing else: the workflow
at a tag names that tag on its four `uses:` lines, written by hand as the
last commit before the tag ([operating.md](operating.md)), and
`contract.test.sh` refuses four lines that disagree or a ref that is not a
tag. Between tags the lines name the last one.

The integrity story is Go's. The module proxy serves the source for the ref,
the checksum database — a transparency log — vouches for the bytes of every
version it has ever served, and the runner compiles them: the same channel
and the same trust as `go install` on a laptop. That is the argument for
principle 3: the guards a job runs are the guards in the tree at the ref the
caller named, with no second artifact between the two whose provenance has
to be argued on its own. For principle 2 it is that the install holds
nothing — no token, no release, the same step in the tokenless agent job as
in every other — so the boundary between jobs does not rest on a download
step.

Rejected, and gone from the tree: one release asset per target, with the
linux_amd64 digest committed under `release/` before the tag, a release
workflow that rebuilt the bytes and refused to publish on a mismatch, four
assets and a checksums file, and Makefile targets that wrote the version,
the digest and the workflow's refs in one second. It worked, and it was some
three hundred lines of build discipline — reproducible-build flags each
measured to change the bytes, a toolchain pinned twice — to let a tag vouch
for bytes that did not exist when it was made, which the checksum database
does for every Go module already. What it bought was a one-second install
where this is a compile, and that cost is the Reopen-when. Also weighed:
installing at `job.workflow_sha`, so the reusable workflow would build the
binary at its own commit with no literal to bump — rejected because the
composite action's `uses:` line would still be a literal, and two
coordinates that can disagree is the drift the four-equal-lines rule
exists to prevent. No Homebrew tap, no `curl | sh`: the level of commitment
[operating.md](operating.md) declines is still declined.

## falconet does not plan

falconet opens the pull request and stops. The plan a reviewer reads is
posted on it by the plan bot the repository already runs on every pull
request — Atlantis, dflook's `terraform-plan`, `pulumi/actions` with
`comment-on-pr`, whatever plans a person's pull requests — from credentials
falconet never holds; the pull-request body is the agent's account of the
change and a `Closes` line, and the prompt tells the agent not to describe
the plan. Branch protection on the bot's status is what stands between the
pull request and an apply.

Until 2026-08-26 the binary did this itself: per-stack `init`, `validate`
and `plan` with a config that classified stacks, a `plan-env` secret and a
verb that masked it into the environment, a baseline plan in `prepare`, a
body assembler that refused to abridge, and `tofu fmt` in `commit` — about
2,400 lines, a fifth of the tree, plus three rows of this register. All of
it reimplemented what the plan bots do, for one adopter that needs a plan
bot on its human pull requests anyway. It was removed so that the successor
maintainer inherits one bespoke thing (issue → sandboxed agent → guarded
commit → pull request) and one standard thing, instead of a half of each.
Git has the three retired rows: `plan-env is one secret of static strings`,
`Every planned stack is planned`, `Never narrow a plan with -target`.

What moved with it — the whole plan in front of the reviewer, and the plan
being of what the change touches — is the bot's to deliver, and principle 5
says so: the evidence is the repository's own. What falconet still owes is
that the pull request is of the right change, opened where the bot will see
it, with no account of the plan in it that a reviewer could mistake for the
evidence. The cost, stated plainly: nothing validates or formats the change
before the pull request, and nothing can see whether a plan bot is
configured — the canary is the check.
