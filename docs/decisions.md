# The decision register

Every live decision in falconet: what is true, the [charter](charter.md)
invariant it serves, the observation that should retire it, and — under the
table — the shortest honest account of why, with what was rejected. This is
the only document that holds the *why*. It describes the tree as it is; it
does not narrate how it got here. [`history/`](history/) does that, and is
not for reading before changing something.

A decision is not a rule — it is a choice with a shelf life, and the **Reopen
when** column is the shelf life, written by whoever made the choice, before
they had a stake in defending it. If you can point at a row's trigger in the
present, that row is open. Say so, and change the row.

Decisions absent from this table are absent because nobody made them. That is
a finding, not a formatting error.

| Decision | Serves | Reopen when | Record |
| --- | --- | --- | --- |
| The pipeline is falconet's own code, not `gh-aw` | I5, I6 | this repository acquires the threat model gh-aw is sized for: strangers triggering workflows | [below](#the-pipeline-is-falconets-own-code) |
| One agent pass, holding nothing it could publish with | I5 | never for convenience — a second pass changes I5, and that goes to the operator | [below](#one-agent-pass-holding-nothing) |
| No second, reviewing agent | I6 | a review harness clears the bar the first one failed: an independent, uncontaminated read of diff, message and plan, worth more than it costs | [below](#no-second-reviewing-agent) |
| GitHub, Claude Code and OpenTofu are the platform | I6 | an adopter exists on another forge or harness — and there is one adopter | [below](#github-claude-code-and-opentofu-are-the-platform) |
| Stage-level verbs, one JSON config file | I6 | a caller needs an operation no verb exposes, or config needs a type JSON cannot carry | [below](#stage-level-verbs-one-json-config-file) |
| Packaged as a reusable workflow plus a composite action | I6 | the credentials or setup it demands outgrow what an adopter can check in the README's steps | [below](#a-reusable-workflow-and-a-composite-action) |
| Verbs never call each other; they leave files in `.falconet/` | I4 | the pipeline stops being a job graph | [below](#verbs-never-call-each-other) |
| Every assertion crosses a process boundary | I2, I5 | a property cannot be observed from outside a process — and then it is a Go unit test beside the guard | [below](#every-assertion-crosses-a-process-boundary) |
| The language is Go | I2, I5, I6 | a guard cannot be expressed safely in it, or the operator stops being able to read the guards | [below](#the-language-is-go) |
| falconet's own GitHub client; `gh` and `jq` are not runtime dependencies | I6 | the client's subset stops covering what a verb needs, by more than a dependency would cost | [below](#falconets-own-github-client) |
| Setup is two verbs and a token the operator mints | I6 | `init` cannot do a step, and the manual path becomes the only path | [below](#setup-is-two-verbs-and-a-token) |
| A GitHub App, registered purely as a credential | I4, I6 | GitHub offers an identity that needs no App, or registration stops fitting inside `init` | [below](#a-github-app-purely-as-a-credential) |
| One release asset per target, digest in the tree before the tag | I6 | the build stops reproducing, or an adopter needs a target the four assets miss | [below](#one-release-asset-per-target) |
| `plan-env` is one secret whose values are all static strings | I6 | a stack needs a credential that cannot be a static string — OIDC, workload identity | [below](#plan-env-is-one-secret-of-static-strings) |
| Every planned stack is planned; the diff decides whether there is anything to plan, and whose fault a failure is | I3, I1 | a real repository layout appears that discovery reads wrongly | [below](#every-planned-stack-is-planned) |
| Never narrow a plan with `-target` | I1 | never. This is I1 in mechanism form and it moves only when I1 does | [below](#never-narrow-a-plan-with--target) |

## The pipeline is falconet's own code

falconet was extracted from `zetlen/wayfinders-infra`, where ~4,700 lines of
pipeline sat on ~1,000 lines of OpenTofu. Adopting `github/gh-aw` instead was
spiked and measured (2026-08): its detection job cost ~44% of the agent's own
tokens on a small task, and its role checks, integrity filtering and
threat-detection stage are sized for a public repository where strangers
trigger workflows. Here there is one operator, one collaborator, and a human
apply at the end of everything. So the pipeline became its own project rather
than a rented approximation.

## One agent pass, holding nothing

The `implement` job runs with `permissions: {}` and no secret but the model
key. It is handed its own `GITHUB_TOKEN` only so the action does not mint one
— in a job with no permissions that token has no scopes at all and cannot
even read the repository. The consumer's checkout arrives from `gate` as an
artifact (`source.tgz`) with its remote and credential stripped, and
`implement` refuses it unless `HEAD` is the base `gate` recorded and no
remote survives. That is what keeps the boundary literal for a private
repository, which answers a tokenless clone with *not found*; the first live
run (2026-08-21) found that, and the answer was to ship the tree rather than
grant the job `contents: read`. falconet itself is installed as a public
release asset, no token needed.

The agent's grant is exactly `Read,Edit,Write,Grep,Glob`, capped at 40 turns.
It edits files, writes `commit-msg.txt` (or `needs-info.md`), and stops. There
is exactly one validate step and no repair loop: nothing feeds a failure back
for another turn.

## No second, reviewing agent

The origin ran a reviewing agent after the implementing one. Its second cold
context cost more than it caught. The reference verdict protocol shipped
unwired, was never called, and has been deleted; git has it. Any replacement
must clear the bar the original set: an independent, uncontaminated read of
diff, commit message and plan before a person is asked to look.

## GitHub, Claude Code and OpenTofu are the platform

The agent pass runs on `anthropics/claude-code-action`; the workstation
equivalent is `claude -p --allowedTools Read,Edit,Write,Grep,Glob`, a CLI the
binary could spawn like any other. The GitHub client speaks GitHub only.
`plan.command` is a string, so a terraform binary works, but the repository
layout falconet discovers is an OpenTofu one. Forge- and harness-agnosticism
are non-goals: adapters are code that pays off only when someone writes the
second one.

## Stage-level verbs, one JSON config file

A thing is a public verb if and only if a caller invokes it directly — the six
pipeline verbs (`prepare`, `commit`, `push`, `validate`, `pause`,
`assemble`), three for a person (`doctor`, `init`, `version`). `prompt`,
`config`, `scan` and `plan-env` exist unlisted: public in that they work, not
vocabulary. Rejected: mirroring the origin's script names one-to-one (leaves
orchestration in YAML — two code paths) and grouping by domain (`issue park`,
`git prepare` — a taxonomy at six verbs).

Exit codes are uniform — **0** outcome determined, **1** refused or a check
failed, **2** usage — and a verb that decides something prints exactly one
word on stdout. Eligibility (queue label present, no blocking label, opt-out
unchecked) is decided by `prepare`, not by a job-level `if:`: a job `if:`
runs before checkout and cannot read the config, and gating there would fork
eligibility into YAML-in-CI and nothing-locally. Ineligible events spend a
few runner-seconds; paid willingly.

The config is one JSON file at `.github/falconet.json` (`--config`,
`FALCONET_CONFIG`). Every key is optional, and the defaults name nothing of
any particular repository's: `stacks` are empty, which means discover. JSON
over YAML because it is strict and needs no `yq`; JSONC was weighed and
brings nothing JSON lacks here. Prompt overrides are paths relative to the
repository root; absent, the prompt embedded in the binary is used. The
schema lives in `internal/config`, and the README's config table is its
prose.

## A reusable workflow and a composite action

`.github/workflows/falconet.yml` (`on: workflow_call`) is the job graph:
**gate → implement → publish**, with **contain** running whatever happened.
The boundaries between jobs are the security model: the agent's job holds no
token, the scripted jobs never run the agent, and App installation tokens are
minted per step in the jobs that need them. `action.yml` is setup plus
pass-through — it pins and installs falconet, tofu and gitleaks, then runs one
verb — for a caller that wants a verb inside a workflow of its own. Nothing
of falconet's is vendored into the adopter's tree; upgrading is moving a tag.

This is the charter's worked example: chosen in passing, it grew a
secret-management problem that was read as work to do rather than as a
mechanism reporting a fault. The reopen trigger is that problem's size.

## Verbs never call each other

They leave files for each other in `handoff_dir` (default `.falconet/`),
written *inside* the consumer's checkout and untracked: `request.md`,
`plan-baseline.txt`, `base-sha.txt`, `branch.txt` from `prepare`;
`commit-msg.txt` or `needs-info.md` from the agent; `plan.txt`, `diff.patch`,
`changed-files.txt`, `validation-failure.txt` from `validate`; `pr.md` from
`assemble`. Every job that runs a verb writes `.falconet/` into
`.git/info/exclude` first, because `prepare` refuses a dirty tree and
`commit` refuses any changed path outside the allowlist, and the consumer's
`.gitignore` is not to be relied on. The same verb sequence therefore runs on
a workstation with no GitHub context; CI-facing exports go to `$GITHUB_ENV`
only when it exists.

`push` runs the moment a commit exists and before any routing, so every run
leaves its branch on the remote (I4 — run 32093607680 lost a prepared change
to a torn-down runner). `PUSHED_BRANCH` is exported only when the push lands,
and every later `--branch` reads it, never `BRANCH`.

## Every assertion crosses a process boundary

`tests/run.sh` spawns `$FALCONET <verb>` and reads stdout, the exit code and
files on disk; GitHub is a loopback fake behind `GITHUB_API_URL`; `tofu` and
`gitleaks` are stubs whose argv is part of the contract; pushes land in bare
repositories under a temp directory. No test reaches inside its subject — it
is what let the suite adjudicate the port from bash to Go unchanged, and it
keeps the guards behind I2 and I5 adjudicable from outside. What cannot be
seen from outside a process — truncation never splitting a line, the fence
outrunning every backtick run, the denylist matching in config order — is a
Go unit or property test beside the guard. `contract.test.sh` holds the
wiring's shape the same way: no checkout in the agent job, the install before
the first verb, every `uses:` ref equal to `release/VERSION`.

## The language is Go

One module, one static binary (`CGO_ENABLED=0 -trimpath`, toolchain pinned in
`go.mod`), standard library first: `os/exec` with argv slices — no shell, no
quoting — `encoding/json`, `regexp` (RE2, linear-time over attacker-controlled
issue text), `net/http`, `embed`, `crypto/rsa`. The one dependency outside it
is `golang.org/x/crypto/nacl/box`, for the sealed box the secrets API
demands; anything further is a change to this row, with a reason. `go vet`,
`staticcheck`, `errcheck` and `govulncheck` are part of green: an ignored
error is a red build.

The bash it replaced (~1,100 executable lines) ran one live issue and was
deleted in the cutover, not kept beside the Go: two implementations agreeing
by convention is the disease. Rejected: **Bun** (a ~90 MB compiled artifact
or a second setup action; backtracking regexes over attacker text; a WASM
package for the sealed box; an npm tree to audit) and **Rust** (stricter, and
it would have caught the fail-open class of bug at compile time — but the
operator must be able to read a guard cold, and the guards are the product).
The incident comment above each guard moved into Go verbatim; `git log` on
the porting commits names every departure from the bash.

## falconet's own GitHub client

`net/http` against `GITHUB_API_URL` (the variable Actions sets;
`https://api.github.com` otherwise), authenticating with `GH_TOKEN` then
`GITHUB_TOKEN`. What a run needs in CI is git, tofu, gitleaks and the binary;
on a workstation, the same. The workflow's own `run:` steps still use `gh`
for the pull request and contain's check, on GitHub's runner where it already
is; the binary never does.

## Setup is two verbs and a token

`doctor` checks the repository it stands in against the README appendix's
steps, read-only, one line each. `init` does steps 2–8 — the `.gitignore`
line, the App, the secrets, the labels, the config, the caller workflow —
each idempotent, then one local commit and never a push: pushing a workflow
file through the API would need a `workflow` scope the token should not have,
and the last step staying in a person's hands is this project's shape.

Both authenticate with `FALCONET_SETUP_TOKEN` and nothing else. `GITHUB_TOKEN`
and `GH_TOKEN` are deliberately not fallbacks: in CI they are the Actions
token, which cannot do this and must never be asked to; on a laptop they are
whatever someone set for something else. A fine-grained token reports no
scopes, so `init` performs every read before any write and its first write is
the idempotent one (labels), so a token short of a permission fails before
anything hard to undo has happened, naming the permission. Without a token,
`init` still does the local steps and prints what is left. The device flow
was weighed and deferred: it needs an OAuth App the maintainer owns.

## A GitHub App, purely as a credential

No webhooks, nothing hosted. The workflow mints installation tokens per step;
output is authored by `falconet[bot]`; App-token pushes fire `pull_request`
events normally, which is what an Actions-token push does not do. `init`
registers the App from a manifest, receives the private key over localhost,
and seals it straight into the repository's secrets — it is never written to
disk. Installing the App is still a click in a browser.

## One release asset per target

`release/` holds the version and the linux_amd64 digest, committed before the
tag; `release.yml` rebuilds the bytes and refuses to publish if they differ.
`action.yml` installs falconet by version and digest in every job that runs a
verb, the way it installs gitleaks. A workstation installs from the release
page or with `go install`. No Homebrew tap, no `curl | sh`: the level of
commitment [operating.md](operating.md) declines is still declined.

## plan-env is one secret of static strings

The jobs that plan get their cloud credentials from a single `plan-env`
secret, a JSON object of string values, which `init` validates before sealing
and `plan-env` prints into the environment. It sits as late in the job as it
can and never reaches `implement`. [operating.md](operating.md) is the
account of how the operator provides it.

## Every planned stack is planned

`validate` initialises and validates every stack the repository has, and
plans every stack in `stacks.plan`, each under a `## <stack>` heading —
including the only one. The diff does not narrow which stacks are planned: a
`source =` walk cannot see a `data "terraform_remote_state"` edge, so a
diff-narrowed plan can silently omit a stack the change really moved (that
was the first form of this decision, amended the same day). What the diff
decides is whether there is anything to plan at all, and whose fault a
failure is: a failed init, validate or plan in a stack the change does not
reach is advisory — a line in the log and a named, missing plan in the body,
never a failed run, so a stack whose bucket was unreachable for ten seconds
does not block a request about a different stack — while any failure in a
stack the change does reach stops everything, because that one is about this
change.

A change that reaches nothing plannable gets a person, not a pull request:
**uncovered** (a `.tf` in a directory the config names in neither list, or at
the repository root) and **unplanned** (only `validate_only` stacks reached)
are both reported in words the requester can read, saying nothing is wrong
with the request. `wayfinders-infra#120` changed a database tier and got back
`No changes.` — a true plan of a stack the change did not touch, under no
heading — which is the pull request this row exists to stop opening.

The layout is discovered, and the config classifies it. A directory holding
`.tf` files is a root module unless another directory names it as a local
`source`; the repository root is never a stack; a change in a module reaches
every stack that sources it, transitively, templates and data files included.
Naming neither `stacks` list means discover and plan everything found; naming
either makes the config authoritative, and a `.tf` directory in neither is
`MISSING` in `doctor` and uncovered in a run. Module edges are read with a
regular expression over `source = "./…"` and `"../…"`; `.tf.json` is not
read. `init` writes its config from the same discovery a run uses, so the two
cannot drift. These are the assumptions Atlantis, Terragrunt and Spacelift
make of an ordinary OpenTofu repository, and falconet makes no cleverer ones.

## Never narrow a plan with `-target`

A targeted plan does not show what an apply will do, and the person at the
end of this pipeline approves an untargeted apply. It also makes OpenTofu
print `The -target option is not for routine use` into a log an adopter reads
while deciding whether this tool is serious. The way to plan less is to plan
fewer stacks. `plan.command` defaults to `-refresh=false -lock=false`, from a
read-only state credential, and a consumer who wants a refreshing plan says
so in one key; what the default does not contain, and will not, is `-target`.
