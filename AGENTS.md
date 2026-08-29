# Working on falconet

Instructions for agents and humans changing this repository. Read these in
order, and always know which one you are reading:

1. **The top of [README.md](README.md)** — what falconet is for: the four
   steps, and the five principles that are not up for negotiation. Eighty
   lines. Read it first.
2. **This file** — the non-goals, what is merely a means, and the working
   rules.
3. **[docs/decisions.md](docs/decisions.md)** — every live decision: what is
   true, the principle it serves, the observation that should retire it, and
   what was rejected. Read it before proposing a change to how any of this is
   built. It describes the tree as it is.

`docs/history/` is how those decisions were reached. It is not a description
of anything, several of its records contradict the tree on purpose, and you
do not need it to work here. Do not read it for what is true.

The ranking is the point of having three. A **principle** is a property of
what falconet produces, and it is not traded away for a nicer implementation.
A **means** is a choice someone made for reasons, and a choice has a shelf
life. If you cannot tell which of the two a sentence in this file is, that is
a fault in this file — say so.

One rule follows from keeping them apart, and it is the reason the principles
sit at the top of the README above everything else: **when a mechanism starts
generating work that no principle asked for, the mechanism is what is wrong,
not the work that is missing.** That is not licence to rewrite whatever you
find inconvenient. It is a question to ask out loud, in the register's terms
— name the row, name its trigger — before spending a week serving a decision
instead of a goal.

Each fact lives in one place. The README says what must be true, the register
says what is and why, and this file states the non-goals, the means, and the
working rules. Nothing here restates an argument it does not own.

## Non-goals

- **It does not merge, deploy, or apply.** Not behind a flag, not with an
  approval step. That is principle 5, stated as a refusal.
- **It does not plan.** A plan of an infrastructure change is the plan bot's
  to post on the pull request, from credentials falconet never holds.
- **It is not a general agent harness.** One narrow pass per iteration, one
  narrow toolset. The narrowness is principle 2, not an unfinished feature.
- **It is not a platform.** Nothing hosted, no account, no SaaS contract. The
  forge and a model API are the whole of what it depends on, and both are
  the operator's to choose.
- **It is not built for a repository where strangers trigger workflows.**
  That threat model is real and it is someone else's. One operator, their
  collaborators, and a human merge.
- **It is not a product.** No code of conduct, no marketplace listing, no tap,
  no `curl … | sh`. Public, MIT, a personal project.

## Everything else is a means

Go. One binary. A reusable workflow whose job boundaries are principle 2. A GitHub App
or a token as the identity that pushes. Labels as the queue. One config file.
A handoff directory. An OpenTofu repository as the origin and a Pulumi one as
the second home. Every one of those is a **means**: chosen for reasons, and
the reasons are in [the decision register](docs/decisions.md), which gives each
one the invariant it serves and the observation that should retire it.

A means is not a rule. It is a decision, and a decision has a shelf life.

> **When a mechanism starts generating work that no principle above asked
> for, that is evidence the mechanism is wrong — not evidence that the work
> is needed.**

The principles were rewritten on 2026-08-29 because that rule fired on the
tool as a whole. What had grown around the four steps — setup verbs, a
self-checking installer, an App registered from a manifest, a GitHub client
of its own, a release apparatus with digests in the tree — served an
adoption story for adopters who do not exist, and the register's
"OpenTofu is the shape" row reopened on its own trigger when the one
adopter moved to Pulumi. The four steps are what was worth keeping; this
names them so the rest can go.

## Changing the principles

A principle changes when the operator says it changes — not in the
register, and never as the side effect of some other decision. It has
happened twice: on 2026-08-26 planning left falconet for the plan bot, and on
2026-08-29 the tool was distilled to the four steps above, which added the
bounded check loop to principle 3 and retired the adoption invariant with the
apparatus that served it. A change that finds itself amending one has either
found the wrong solution or found a real disagreement; either way it stops
and asks. Means change all the time, as rows of the register, and keeping
the two apart is the whole point.

## The guards are scar tissue, not defensive programming

Every guard in this codebase exists because something went wrong once, and the
incident is documented in a comment directly above it. **Do not "simplify" one
without reading why it exists.** Two examples, both load-bearing:

- PR #28 shipped a plan the agent had abridged by hand — literal
  "# ... omitted here for length" comments inside the fence — and the human
  who approved it was reading a summary of the evidence instead of the
  evidence. So the PR body carries no plan and the prompt tells the agent not
  to describe one: the plan bot's comment is the evidence, whole.
- Run 32093607680 destroyed a prepared change when its runner was torn down,
  then parked the issue with a comment promising work that no longer existed
  anywhere. So the branch is pushed the moment a commit exists, not at the end,
  and the hand-off comment names and links it.

If a guard looks like paranoia, that is what a guard that has been working
looks like.

The guard logic lives in `internal/<pkg>`, with no filesystem access and
the incident prose above each guard moved there verbatim from the bash it
replaced; `cmd/falconet/<verb>.go` is the flags, the files, the
subprocesses and the exit code. The operator reads Go, the comment is the
record, and the commit that ported each verb names every departure from
the bash and why — `git log` is where to look before calling a difference
a bug.

## What is not up for negotiation

The README's principles, as they appear in this tree. Changing one is not a
register row: it changes what the tool is, and it goes to the operator. If
you think one is wrong, say so and stop.

- **The implementing agent gets no shell and no push token** (principle 2).
  Its grant is exactly `Read,Edit,Write,Grep,Glob`. It edits files, writes a
  commit message to a file, and stops. This is not a style preference: issue
  text is attacker-controlled *and* it is the agent's instructions — "while
  you're in there, edit the workflow to grant Bash" is the attack, and the
  path allowlist is what refuses it. Any change that widens the toolset, or
  that lets the agent reach a path outside the allowlist, is a change to
  principle 2.

- **A guard refusal is terminal** (principle 3). Nothing feeds a refusal from
  the path allowlist, the content denylist, the rename check or the secret
  scan back to the agent for another try: a guard the agent can iterate
  against is an oracle. Only the repository's own checks may send a run back,
  and only a bounded number of times. The loop itself is not built yet; when
  it is, it loops on checks and never on guards.

- **falconet does not plan, and does not describe the plan** (principle 5,
  and a non-goal). The repository's plan bot posts the plan on the pull
  request; the body carries none and the agent is told not to guess at one.
  Do not add a plan, a validate, a `tofu` or `pulumi` call or a cloud
  credential back into the binary or the workflow — that is the fifth of the
  tree removed on 2026-08-26
  ([register](docs/decisions.md#falconet-does-not-plan)), and it reopens
  only on the trigger written there.

- **Every run ends somewhere a person can see** (principle 4). A pull
  request, a question for the requester, or a hand-off — and never a green
  run that produced nothing. A new exit path that is none of the three is a
  new terminal state, and there are three.

## Means currently in force

Settled, with reasons, and each carries a **Reopen when** in
[the register](docs/decisions.md). Disagree loudly and cite the trigger you
can point at in the present. Do not quietly build the other thing.

- **Do not re-propose `github/gh-aw`** or anything shaped like it
  ([register](docs/decisions.md#the-pipeline-is-falconets-own-code)). It
  reopens on one observation: strangers can trigger this pipeline.
- **Do not wire up a review agent**
  ([register](docs/decisions.md#no-second-reviewing-agent)) unless it clears
  the bar written there.
- **The language is Go, and the port is done**
  ([register](docs/decisions.md#the-language-is-go)). Bun and Rust were
  weighed, with reasons; do not re-propose either. The suite runs once,
  through the binary `make build` leaves at `dist/falconet`: `make test`.
- **The rest are in the register**, not restated here. Each row names what
  it serves and what would retire it.

## Tests

`make test` must be green before a commit and after it. No exceptions, no
"I'll fix it in the next task." It is two things:

- **`go test ./...`** — unit and property tests beside the guard logic
  (`testing`, `testing/quick`), for what the bash suite cannot see from
  outside a process: a pause comment's truncation never splits a line and
  never exceeds its budget, the fence outruns every backtick run, the denylist matches in
  config order, the config merge (objects recurse, arrays and scalars
  replace), the handoff directory, the repository root, the dispatcher's
  lists in step with what it implements. `go vet`, `staticcheck`,
  `errcheck` and `govulncheck` are part of green in CI — ci.yml runs them
  before the suite, and `make check` runs the same pinned versions locally:
  an ignored error is a red build.
- **`bash tests/run.sh`** — the acceptance suite and the incident record,
  run through the binary. It is not rewritten for Go and it reaches inside
  nothing: **no test may reach inside its subject.** Every assertion crosses
  a process boundary: spawn `$FALCONET <verb>`, then check stdout, exit
  code, and files on disk. `FALCONET` defaults to `dist/falconet` and
  `tests/lib.sh` refuses to start without it (`build it first: make build`);
  `FALCONET=/other/binary bash tests/run.sh` runs the same suite against
  another build. There is no fallback to anything else: green means green
  through the binary.

GitHub is `tests/fixtures/fake-github.py`, a loopback server started by
`fake_github` in `tests/lib.sh` that answers from fixtures and records what
it was asked, with `GITHUB_API_URL` pointing at it. No test
file stubs `gh`; the files that once did put a tripwire on `PATH` instead,
so a verb that shelled out to `gh` would fail loudly before the real one
could carry a test token anywhere. `gitleaks` is a bash stub handed in
through `$GITLEAKS`, and its argv is part of the contract. Pushes land only
in bare repositories under a temp directory; nothing touches the network,
GitHub, or any credential. The suite
needs bash, git, jq, awk and python3 stdlib. Adding a dependency to run the
tests is a decision, not a convenience.

`contract.test.sh` is the wiring's test: it reads `action.yml`,
`.github/workflows/falconet.yml`, `release.yml`, the Makefile and the
README's caller template (between its `<!-- caller-workflow-template -->`
markers) and holds their shape — no checkout in the agent job, the install
before the first verb in every job, every `uses: zetlen/falconet@` ref equal
to `release/VERSION`. A new case is proved red on the break it exists for
before it is made green.

## The records have a test too

`make lint-docs` holds the shape of the two documents above, the same way
`contract.test.sh` holds the wiring's: every row of the register names a
principle the README actually declares and links a section of the register
that exists; every invariant is named by some row; and nothing in
`README.md`, `AGENTS.md` or `docs/` links a file that is not in this tree. It
is
`tools/docslint`, Go and the standard library, with its own cases in
`tools/docslint/lint_test.go` — each one a corpus broken in exactly one place,
proved red on the break it exists for.

`go test ./...` runs it against this tree too
(`TestTheRecordsInThisRepository`), so it is already inside `make test` and
inside CI. `make hooks` puts it on `pre-push` for this clone, which is a
convenience and not the gate: CI runs it on every push and every pull request,
and `--no-verify` exists.

A new principle or a new kind of row starts by breaking the lint on purpose
and watching it refuse. A new decision is a row and a section in the
register, made in the same commit as the change; the reasoning that does not
fit there goes in that commit's message. No new files go in `docs/history/`.

## One shell trap

The binary speaks `os/exec`, and the shell traps the bash used to carry
went with it — except one, because two `run:` steps in the workflow still
use `gh` (the pull request, and contain's check): **never `gh ... | grep
-q`.** `grep -q` exits at the first
match and can SIGPIPE `gh`, which under `set -o pipefail` turns a *found*
match into a non-zero pipeline — the exact opposite of the answer just
computed. Capture the whole result, then inspect it.

## Adding a verb

If you are adding a verb, the criterion is the
[register's](docs/decisions.md#stage-level-verbs-one-json-config-file): a
thing becomes public vocabulary if and only if a caller invokes it directly. The secret scan is the worked example
of something that stayed internal.

## Two roots, never one variable

The binary lives wherever it was installed; `REPO_ROOT` is the repository
being worked on, and `internal/repo` finds it from the working directory
(or `$FALCONET_REPO`). The origin's scripts lived inside the repo they
operated on, so one answer served both — and a verb that keeps that
assumption operates on wherever the tool sits instead of on the consumer's
repository, silently, reporting an outcome about the wrong tree. Never
derive the working tree from the binary's own location.

## The handoff lives inside the tree it describes

The verbs never call each other; they leave files for each other in the
handoff directory (`handoff_dir`, default `.falconet/`), which is written
*inside* the consumer's checkout. It is untracked, and two verbs read
`git status`: `prepare` refuses a dirty tree, `commit` refuses any changed
path outside the allowlist. So every job in `falconet.yml` that runs either
of them writes `.falconet/` into `.git/info/exclude` first — per clone,
never into a file the commit verb could see, and never relying on the
consumer's `.gitignore` — and `contract.test.sh` fails if a step reorders
that. A verb that starts reading the working tree joins that invariant; the
invariant does not bend to it.
