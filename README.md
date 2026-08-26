# falconet

A falconet is a small cannon: one precise shot, aimed by hand, at a target you
picked on purpose. This one turns a plain-language infrastructure request into
a reviewed pull request carrying a real `tofu plan` — and then stops, because
applying is a human's job.

**Status: it works, and it is one binary.** One static Go binary runs in CI,
where every job installs it from a release and checks its digest, and on a
laptop, where `falconet init` does the install and `falconet doctor` checks
it. It has run live on a real consumer since 2026-08-21 and reached pull
requests carrying whole plans. Each live run has also found a wiring bug that
no unit test could see, and each is now a case in the suite. See
[Where this stands](#where-this-stands).

## What it does

Someone files an issue that says, in ordinary words, what they want changed.
falconet:

1. assigns itself the issue, opens a branch, and captures a baseline plan
2. runs **one** agent pass with a deliberately narrow toolset — it edits
   config and writes a commit message, and holds no shell and no push token
3. commits through deterministic guards that an agent cannot talk its way past
4. validates every stack, and plans **the stacks the change actually
   reaches** — a change in a directory nothing here plans gets a person, not
   a pull request carrying somebody else's plan
5. opens a pull request whose body carries the **entire** plan, under a
   heading naming the stack it is a plan of, labelled for human review

Every exit is a terminal state: a pull request, a question for the requester,
or a hand-off to a human. A request never disappears into a green run that
produced nothing, and no pull request ever carries a plan of somewhere the
change does not touch.

What it will **not** do is apply anything. The gate at the end is a person.

## Install it in your repository

Four steps, and each ends with **Check:** — how to see that it worked before
you go on.

1. [Install the binary](#1-install-the-binary)
2. [Mint `FALCONET_SETUP_TOKEN`](#2-mint-falconet_setup_token)
3. [Run `falconet init`](#3-run-falconet-init)
4. [File the canary](#4-file-the-canary)

Nothing is vendored and nothing of falconet's is checked out into your
repository: the caller workflow names a tag of this repository, and every job
installs the binary that tag vouches for. Upgrading is changing the tag. The
nine things `init` does and `doctor` checks are each a command in
[the appendix](#appendix-the-manual-path) — the manual path, and the
numbering `init` and `doctor` use when they print a line like
`MISSING      6. label needs-info`.

### 1. Install the binary

Two ways, and one snag on macOS.

**From the release page.**

| Your machine | Asset |
| --- | --- |
| Apple silicon Mac | `falconet_darwin_arm64` |
| Intel Mac | `falconet_darwin_amd64` |
| Linux x86-64 | `falconet_linux_amd64` |
| Linux arm64 | `falconet_linux_arm64` |

Pick the tag from [the releases page](https://github.com/zetlen/falconet/releases),
then:

```sh
tag=v0.2.0
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

Verify the checksum rather than trusting the download. A release tag is a
mutable pointer and an asset can be replaced — the same reason `action.yml`
pins gitleaks by digest as well as by version. falconet's own `linux_amd64`
digest is committed in this tree at
[`release/falconet_linux_amd64.sha256`](release/falconet_linux_amd64.sha256),
written before the tag exists; the release workflow rebuilds those bytes on a
runner and publishes nothing at all if they differ.

**The macOS quarantine snag.** A file a **browser** downloads gets a
`com.apple.quarantine` attribute, and Gatekeeper will not run an unsigned,
un-notarised binary that carries one. Observed on macOS 26: it does not fail
with a message — the process simply hangs. `curl` does not set the attribute,
so the recipe above never trips over this; if a browser fetched the file,
clear it **before** the first run:

```sh
xattr -d com.apple.quarantine ~/.local/bin/falconet
```

Clearing it after a denial did not reliably help in testing — Gatekeeper had
already made up its mind about that file. Clear it first, or re-download with
`curl`. This is documented rather than solved: signing and notarising means
an Apple Developer account and a signing identity in CI, the level of
commitment [docs/operating.md](docs/operating.md) declines everywhere else —
as it declines a Homebrew tap and a `curl … | sh` install script.

**With Go.**

```sh
go install github.com/zetlen/falconet/cmd/falconet@v0.2.0
```

Nothing is quarantined this way: the file is compiled locally rather than
arriving through a browser. It builds with **your** Go, not the pinned one —
`GOTOOLCHAIN=auto`, the default, is a floor and not a pin — so
`falconet version` may report a Go newer than the release assets do. That is
fine here, where nothing is compared against a digest; it is exactly what the
release workflow must not do, and does not.

**Check:** `falconet version` prints the tag and the Go it was built with — a
v0.2.0 build on an Apple-silicon Mac says:

```
falconet v0.2.0 (go1.26.7 darwin/arm64)
```

A `go install` of a commit rather than a tag reports the pseudo-version the
`go` command recorded instead of a tag, and a build from a checkout says
`dev`; either runs, and both matter in step 3, where `init` pins the caller
workflow to the version it reports.

### 2. Mint `FALCONET_SETUP_TOKEN`

`init` writes to your repository through GitHub's API — the labels, the
secrets, the App — and `doctor` reads through it. Both authenticate with one
variable, **`FALCONET_SETUP_TOKEN`**, and nothing else.

On github.com → Settings → Developer settings → Personal access tokens →
**Fine-grained tokens** → Generate new token:

| Field | Set it to |
| --- | --- |
| Repository access | **Only select repositories** → the one you are installing into. |
| Expiration | **7 days.** A setup credential is powerful and short-lived: this one is for steps 2–4 and never again. |
| Repository permissions | The four below, and nothing else. (Metadata: read comes with every fine-grained token.) |

| Permission | Level | For |
| --- | --- | --- |
| Administration | Read | the Actions-policy checks (appendix step 1) |
| Actions | Read | the same |
| Secrets | Read and write | the four secrets (appendix steps 3–5) |
| Issues | Read and write | the four labels (appendix step 6) |

A classic token needs `repo`. Neither kind needs Contents or Workflows:
`init` commits the files it writes locally and never pushes.

Export it in the shell you will run `init` from, without putting it in that
shell's history:

```sh
read -rs FALCONET_SETUP_TOKEN && export FALCONET_SETUP_TOKEN   # paste the token, press Enter; nothing is echoed
```

Deliberately its own name. `GITHUB_TOKEN` and `GH_TOKEN` are **not** read —
in CI they are the Actions token, which cannot do this and must never be
asked to; on a laptop they are whatever you set for something else — and a
credential this powerful should be named for what it is.

**Check:** in a clone of the repository, `falconet doctor`. Without the
token every remote line says `cannot tell … (no FALCONET_SETUP_TOKEN)` and
the permission table above is printed on stderr; with it, those lines
answer — `ok`, or `MISSING` with the command that fixes it on the next line,
which is the expected state before step 3 (`doctor: 6 ok, 10 missing, 0
cannot tell` on a repository with three stacks and nothing else yet). A token
short of a permission says which one:

```
cannot tell  3. secret FALCONET_APP_ID (403 Resource not accessible by personal access token — needs Secrets: read)
```

### 3. Run `falconet init`

From the root of a **clean** clone — untracked files included, because the
one commit `init` makes must carry only what it wrote — with the token
exported:

```sh
falconet init --plan dns --plan-env-file ~/falconet-plan-env.json
```

`--plan` names the stacks a human will apply from the pull request; every
other directory with `.tf` in it is validate-only, the rule appendix step 7
states (at a terminal `init` asks per stack instead). `--plan-env-file` is
the planning environment of appendix step 5: one JSON object of the variables
`tofu init && tofu plan` need in the planned stacks — backend keys, provider
tokens, `TF_VAR_*` — written to a file **outside** the repository. If no
planned stack needs a credential, leave the flag off: step 5 is then
`skipped` and listed, and the workflow's `plan-env` secret is optional.
`falconet init -h` lists the rest.

What it does, in this order. Every read comes before any write, and the
first write is the one that is harmless to repeat, so a token short of a
permission fails before anything hard to undo has happened:

1. **Reads.** The tree is clean (a dirty one is refused, exit 1, before
   anything else); an existing config parses; the stacks are discovered,
   and a repository with none does not qualify, exit 1; the plan-env file
   parses as a JSON object whose values are all strings (anything else is
   `init: validation: …`, exit 1, with the value on no stream) — then,
   through the token, the repository, its issues, its Actions policy, its
   secrets and its labels. Issues disabled, or an Actions policy that
   refuses outside workflows (appendix step 1), is reported `MISSING` and
   left for you: `init` never changes a repository setting.
2. **The labels** (appendix step 6): `infra-request`, `needs-info`,
   `ready-for-human`, `needs-plan-review`, each created unless it exists.
3. **The two secrets that are values** (appendix steps 4 and 5).
   `ANTHROPIC_API_KEY` is read from a no-echo prompt when stdin is a
   terminal, and from stdin otherwise — `falconet init … < key-file`, or
   piped — never from an argument, which would sit in shell history; an
   empty answer skips it. `FALCONET_PLAN_ENV` is the file's bytes. Each is
   sealed to the repository's public key and stored; the value is never
   echoed, and can never be read back. A secret that already exists is left
   alone unless `--replace-secrets`.
4. **The App** (appendix step 3), by manifest. `init` serves a page on
   localhost and opens it in your browser. The page sends the App's
   configuration to GitHub — the three repository permissions, no webhook,
   installable only on this account — and you click **Create GitHub App**
   there. GitHub sends the browser back with a code; `init` exchanges the
   code for the App's ID and private key and seals both straight into
   `FALCONET_APP_ID` and `FALCONET_APP_PRIVATE_KEY`. **The key never touches
   disk**: there is no `.pem` to download and nothing to delete afterwards.
   Then the App's install page opens; click **Install**, then **Only select
   repositories**, and pick this repository, and `init` waits — ten minutes
   by default, `--app-timeout` — until it sees the installation. For an App
   you registered by hand instead: `--app-id N --app-key file.pem`.
   `--no-browser` prints each URL for you to open; `--no-app` leaves step 3
   for you.
5. **The files** (appendix steps 2, 7 and 8), then **one commit**:
   `.falconet/` in `.gitignore`; `.github/falconet.json` naming the stacks;
   `prompts/implement.md`, the shipped prompt copied in so you can edit its
   standing-facts block; and `.github/workflows/infra-requests.yml`, the
   caller, with `uses:` pinned to the version of the binary that wrote it —
   `@v0.2.0` from a release build. (A `dev` build, or a `go install` of an
   untagged commit, has no tag to name and pins `main`, which `doctor` then
   notes as unpinned; put the tag there yourself, step 4.) Committed,
   **never pushed**: pushing a workflow file through the API needs a scope
   the token does not have and should not, pushing over your own git
   credentials needs nothing, and the last step stays in your hands.

Every step is one line on stdout in `doctor`'s format, then a summary, then
**`Left for you:`** — the push first, then anything it skipped, then the
canary, then the check. A run through the manifest flow with a v0.2.0
build, against the test suite's fake GitHub (the App ID and the key id are
the fake's fixtures; yours will differ):

```
ok           1. the working tree is clean
note         7. stack workspace is named in neither --plan nor --validate-only: validate_only, the README's rule for every other directory with .tf in it
ok           1. the repository has issues enabled
ok           1. allowed_actions is all
note         1. default_workflow_permissions is read (fine: the caller workflow grants what it needs)
done         6. label infra-request created
done         6. label needs-info created
done         6. label ready-for-human created
done         6. label needs-plan-review created
done         4. secret ANTHROPIC_API_KEY stored (sealed to key 568250167242549743)
done         5. secret FALCONET_PLAN_ENV stored (sealed to key 568250167242549743)
done         3. secret FALCONET_APP_ID stored (sealed to key 568250167242549743)
done         3. secret FALCONET_APP_PRIVATE_KEY stored (sealed to key 568250167242549743)
done         3. the GitHub App falconet-zetlen-wayfinders-infra (ID 12345) is registered, installed on zetlen/wayfinders-infra, and its two secrets are stored
done         2. .falconet/ added to .gitignore
done         7. .github/falconet.json written (plan: dns; validate_only: workspace)
done         7. prompts.implement names prompts/implement.md, copied from the shipped prompt
done         8. .github/workflows/infra-requests.yml written (uses zetlen/falconet/.github/workflows/falconet.yml@v0.2.0)
done         committed "Install falconet" (4 files)
init: 3 ok, 14 done, 0 skipped, 0 missing, 0 cannot tell

Left for you:
  1. git push origin main
  2. step 7 — edit the standing-facts block in prompts/implement.md: it describes the repository falconet was extracted from (its registrar sandbox, its scratch tenant), and the agent will believe it of this one until it says what is true here
  3. step 9 — file a canary issue: the smallest change the planned stack can carry (one DNS record, one tag), labelled infra-request, then watch the run; once it has reached a pull request, pin the ref in uses: to the SHA or tag you ran
  4. then: falconet doctor
```

Without a token `init` still writes the files and commits them, and lists
steps 3–6 under `Left for you:` in the appendix's words — it degrades to the
manual path, never to nothing. A run that ends early says where: a refused
write is `stopped at step N; what was done before it stands, and a second
run carries on from here`, exit 1; a browser that never came back leaves the
App under `Left for you:` and exits 0. Every step is idempotent, so the
answer to anything unfinished is the same command again.

Do the `Left for you:` list in order. The push is its first item; the edit to
`prompts/implement.md`'s standing-facts block (appendix step 7) is worth
making before it, in the same push.

**Check:** `falconet doctor`, in the same clone, after the push. Every line
`ok` and exit 0. It never writes anything — every call it makes is a read.
This is its output on the clone the `init` run above left, against the same
fake GitHub (which is why nothing is missing):

```
ok           1. stack dns (.stacks.plan) is a directory with .tf files
ok           1. stack workspace (.stacks.validate_only) is a directory with .tf files
ok           1. the repository has issues enabled
ok           1. allowed_actions is all
note         1. default_workflow_permissions is read (fine: the caller workflow grants what it needs)
note         1. runners must be Linux x64 (not checked: runs-on is the caller's input, and ubuntu-latest is the default)
ok           2. .falconet/ is gitignored
ok           3. secret FALCONET_APP_ID exists (a value can never be read back, so the name is the check)
ok           3. secret FALCONET_APP_PRIVATE_KEY exists (a value can never be read back, so the name is the check)
ok           4. secret ANTHROPIC_API_KEY exists (a value can never be read back, so the name is the check)
ok           5. secret FALCONET_PLAN_ENV exists (a value can never be read back, so the name is the check)
ok           6. label infra-request
ok           6. label needs-info
ok           6. label ready-for-human
ok           6. label needs-plan-review
ok           7. .github/falconet.json parses
ok           7. prompts.implement names prompts/implement.md, which exists
ok           8. .github/workflows/infra-requests.yml exists
ok           8. it uses zetlen/falconet/.github/workflows/falconet.yml@v0.2.0
ok           8. permissions grants contents: write, issues: write, pull-requests: write
doctor: 18 ok, 0 missing, 0 cannot tell
```

`note` lines are not checks. A `MISSING` line carries the command that fixes
it on the next line; a `cannot tell` says why — no token, or a permission the
token is short of. Two things `doctor` cannot see. That the App is
**installed**: it holds no key to ask with, so `init`'s `done 3.` line —
which it prints only once it has seen the installation — is the check, and a
run that fails at `actions/create-github-app-token` with *Could not find
installation* is the other way to find out. And that the workflow is
registered on GitHub: `gh workflow list` after the push, or the Actions tab.

### 4. File the canary

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

**Pin a tag.** The ref in `uses:` is the one coordinate: the workflow at
`@v0.2.0` installs, in every job, the binary whose digest the tree at
`v0.2.0` holds. `init` wrote the tag of the binary that ran it. If you wrote
the caller by hand, put the tag there — never `main`, which moves, and which
`doctor` notes as unpinned:

```yaml
    uses: zetlen/falconet/.github/workflows/falconet.yml@v0.2.0
```

If you are upgrading a caller from the bash era, **delete its `falconet-ref:`
input** as well. It no longer exists — there is no checkout left for it to
choose — and a reusable workflow rejects an input it does not declare when
the caller's file is loaded, so the run is a `startup_failure` with nothing
on the issue. `doctor` says so:

```
MISSING      8. falconet-ref is no longer an input; remove it
             the run would be a startup_failure: a reusable workflow rejects an input it does not declare when the caller's file is loaded
```

**Check:** one of the three endings on the issue — and on a pull request, a
plan that shows the canary's resources and nothing else.

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

Three more are for a person at a keyboard, and they are the whole of the
install path above:

| Verb | What it does | Exit |
| --- | --- | --- |
| `doctor` | checks the repository it stands in against the appendix's steps 1–8, read-only, one line per check | 0 every check `ok`; 1 otherwise |
| `init` | does the appendix's steps 2–8, each idempotent and reported one line, then one commit and never a push | 0 everything attempted succeeded (a skipped step is not a failure); 1 a dirty tree, a refused write, a refused plan-env file, a repository that does not qualify or cannot be reached |
| `version` | the tag and the Go it was built with | 0 |

`prompt`, `config`, `scan` and `plan-env` exist unlisted —
public in that they work, not vocabulary, by
[the register](docs/decisions.md#stage-level-verbs-one-json-config-file)'s
criterion that a thing is a verb if and only if a caller invokes it directly. `-h` on any of them says
what it does.

The reusable workflow runs the six as four jobs — **gate**, **implement**,
**publish**, **contain** — and the boundaries between the jobs are the
security model: the agent's job has `permissions: {}` and no secret but the
model key; the scripted jobs hold the token and do the mechanics.
[`.github/workflows/falconet.yml`](.github/workflows/falconet.yml) documents
the trade it makes and why.

What a run needs, in CI: git, tofu, gitleaks and the binary, and nothing
else. [`action.yml`](action.yml) installs all three as the first step of
every job — tofu through `opentofu/setup-opentofu`, gitleaks and falconet by
version and digest, falconet's digest being the one committed in this tree at
[`release/`](release/). On a workstation, the same, plus a browser for
`init`'s App step. The binary needs neither `jq` nor `gh`: the verbs speak to
`GITHUB_API_URL` themselves. (The workflow still uses `gh` in two of its own
`run:` steps, on GitHub's runner, where it already is.)

The same verbs run by hand, against the repository you are standing in — no
workflow. `prepare` and `pause` read `GH_TOKEN` (then `GITHUB_TOKEN`) and
`GITHUB_REPOSITORY=owner/name`; `prepare` falls back to the origin remote,
and `pause` — which operates on an issue rather than a tree — deliberately
does not. The rest need nothing:

```sh
falconet validate --base "$(git rev-parse main)"
```

### Design commitments

- **Deterministic mechanics, agent judgment.** The agent decides *what* the
  change is. The binary does the branching, committing, pushing, planning
  and PR assembly — so those steps cannot be skipped, improvised, or argued
  out of.
- **Guards are incident-shaped.** Every one of them exists because something
  went wrong once. They are documented with the incident that caused them,
  in the comment above the guard, and the port moved those comments into Go
  verbatim: the operator reads Go, and the guards are the product.
- **One agent, one context.** An earlier design ran a second reviewing agent;
  measurements showed the second cold context cost more than it caught.
- **The plan is the evidence.** It goes in the PR body in full, never
  abridged, because a human approving a summary of evidence is not review.
- **Opinionated on purpose.** GitHub and Claude Code are assumed. OpenTofu is
  the shape. Being agnostic across forges is an explicit non-goal.

## Why it exists

It was built inside an OpenTofu repository and worked well enough that the
surrounding repo became mostly pipeline: ~4,700 lines of workflow and shell
against ~1,000 lines of actual infrastructure. Two attempts to escape that —
adopting an off-the-shelf agentic workflow, or trimming in place — both
concluded the same way: the workflow is a good tool wearing a repository as a
costume. So it becomes a tool.
[The founding record](docs/history/0002-extract-the-pipeline-into-falconet.md)
has the measurements that killed the off-the-shelf option.

## Where this stands

| Piece | State |
| --- | --- |
| `cmd/falconet/`, `internal/` — the binary | six verbs, two setup verbs and `version`; the standard library plus `golang.org/x/crypto/nacl/box` for the sealed box the secrets API demands |
| `tests/` | 16 files, 892 cases through the binary (`make test`), with `go test ./...` beside it; the wiring invariants are [`tests/contract.test.sh`](tests/contract.test.sh) |
| `release/` + `.github/workflows/release.yml` | the digest in the tree before the tag, four assets and `checksums.txt` per tag |
| credentials for the jobs that plan | one `plan-env` secret, static values only |
| `prompts/` | embedded in the binary; the standing-facts block is the origin's |
| Live runs | yes, on a real consumer, on the bash (2026-08-21) and on the binary since v0.2.0. Each found a bug that only integration finds — most recently a pull request whose true plan was of a stack the change did not touch ([#23](https://github.com/zetlen/falconet/issues/23), fixed) |

[The charter](docs/charter.md) is what falconet is for, in one page: the six
invariants that hold, the non-goals, and the line between those and everything
that is merely how it is built today. [The decision register](docs/decisions.md)
holds every live decision, with the invariant it serves, the observation that
should retire it, and why. [`docs/history/`](docs/history/) is how those
decisions were reached, kept for its incidents and measurements; it is not a
description of the tree. [operating](docs/operating.md) covers the credentials only the
operator can create; [AGENTS.md](AGENTS.md) is what to read before changing
anything here.

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
subject. It stubs `tofu` and `gitleaks` with bash scripts handed in through
`$TOFU` and `$GITLEAKS`, whose argv is part of the contract. GitHub is
[`tests/fixtures/fake-github.py`](tests/fixtures/fake-github.py), a loopback
server that answers from fixtures and records what it was asked, with
`GITHUB_API_URL` pointing at it. Pushes land only in bare repositories under
a temp directory; nothing touches the network, GitHub, OpenTofu or any
credential. No test stubs `gh` anywhere — the files that once did put a
tripwire on `PATH`, so a verb that shelled out to it would fail loudly before
the real one could carry a test token anywhere.

`go test ./...` covers what the suite cannot see from outside a process: unit
and property tests (`testing/quick`) beside the guard logic — truncation
never splits a line and never exceeds its budget, the fence outruns every
backtick run, the denylist matches in config order, the config merge, the
slug and the in-flight pattern, the sealed box opening with the private key,
the App manifest and its JWT, the dispatcher's lists in step with what it
implements. `go vet`, `staticcheck`, `errcheck` and `govulncheck` run in CI
beside it, and `make check` runs the same four at the same pinned versions
on a laptop: an ignored error is a red build. The suite needs bash, git, jq,
awk and python3 (stdlib only); `go test` needs Go.

## Appendix: the manual path

The nine steps `falconet init` does, by hand, and what `falconet doctor`
checks against — the specification of each write and each check, and the
numbering both verbs use. Every `gh` command here runs from inside the
repository you are installing into; `gh` and `jq` are the manual path's
tools, on your machine, not things falconet needs.

It stays in this file on purpose, at its full length. It is the honest
measure of what installing this thing costs a person, and shortening the
document does not shorten the install — every step here is one `init` has to
do correctly, and one `doctor` has to be able to check. Move it to its own
file and the cost stops being visible; the length is the point.

1. [Check the repository qualifies](#1-check-the-repository-qualifies)
2. [Ignore the handoff directory](#2-ignore-the-handoff-directory)
3. [Create the GitHub App and store its two secrets](#3-create-the-github-app-and-store-its-two-secrets)
4. [Store the Anthropic API key](#4-store-the-anthropic-api-key)
5. [Store the planning environment](#5-store-the-planning-environment)
6. [Create the four labels](#6-create-the-four-labels)
7. [Write `.github/falconet.json`](#7-write-githubfalconetjson)
8. [Add the caller workflow](#8-add-the-caller-workflow)
9. [Run a canary issue](#9-run-a-canary-issue)

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
- **Linux x64 runners.** The action installs pinned `linux_x64` release
  assets of gitleaks and of falconet itself and checks their digests, so
  macOS or ARM fails the checksum.
- **A clean tree on a fresh checkout.** Two verbs read `git status`. If a
  hook or generator leaves untracked files behind on checkout, gitignore them.

If `gh api repos/{owner}/{repo}/actions/permissions/workflow` says
`default_workflow_permissions` is `read` — the default for new repositories —
that is fine: step 8's caller workflow grants what it needs explicitly.

`doctor` checks the first three bullets — each configured stack is a
directory with `.tf` in it, issues are enabled, the Actions policy admits
those four — and reports the policy as `MISSING` when it is wrong; the
runner is a `note` (it is the caller's `runs-on`), and the clean tree on a
fresh checkout is not checked. Neither `doctor` nor `init` changes a
repository setting.

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
`init` registers one by manifest from your browser and the private key never
touches disk (step 3 of the install above). By hand, on **github.com →
Settings → Developer settings → GitHub Apps → New GitHub App** (under the
organisation's settings if the repository belongs to one):

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

The whole PEM, header and footer lines included. Or hand the two to `init`,
which seals them and runs the rest of the steps:
`falconet init --app-id <the App ID> --app-key ~/Downloads/<the .pem>`.

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
and delete it. `init --plan-env-file that-file` does the same, and refuses
the file unless every value is a string and every key is a variable name.

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

All four before the first run: `pause` says `failure` and fails its step
when the label it was asked for cannot be put on the issue, which is at
precisely the moment falconet is trying to tell somebody something. `init`
creates them with a colour and a description each; `doctor` reports each
one that is missing.

An issue form with `labels: ["infra-request"]` in its front matter means
requesters never have to label anything. A checkbox whose text is `Not
eligible for AI agents` (`issue.opt_out_text`) lets them keep a request away
from the agent.

**Check:** `gh label list --json name --jq '.[].name' | grep -cxE 'infra-request|needs-info|ready-for-human|needs-plan-review'` → `4`.

### 7. Write `.github/falconet.json`

Optional — every key has a default, and with no file at all falconet
discovers your stacks (below). Most repositories set `stacks` anyway, because
saying which stacks a human applies is a promise and discovering it is a
guess. The file is merged **over** the defaults: naming one key changes one
thing. Arrays replace wholesale rather than append, because an allowlist that
grows by accident is not an allowlist. A malformed file is a hard failure with
the parse error, never a silent fall back to defaults.

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

**Name every stack, or name none.** Naming neither list means "discover
them": every directory holding `.tf` files, minus the ones another directory
uses as a local module, is a stack, and every one of them is planned. Naming
either list makes the file authoritative, and then a directory holding `.tf`
files that appears in neither is a directory falconet refuses to guess about
— `doctor` reports it as `MISSING`, and a change that lands in it is refused
with a report to the requester rather than answered with some other stack's
plan (#23). Half a config is the one shape that goes wrong.

Every key, with its default:

| Key | Default | What it is |
| --- | --- | --- |
| `stacks.plan` | `[]` — discover | Stacks to init, plan, and put in the PR. Directories. Empty **and** `validate_only` empty means every root module found. |
| `stacks.validate_only` | `[]` — discover | Stacks to validate without a backend. Directories. Setting this alone and leaving `plan` empty is how a repository says it plans nothing. |
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
it, copy [the file](prompts/implement.md) into your repository as
`prompts/implement.md` (which is what `init` does — byte for byte, so the
two placeholders below stay placeholders), replace that block with what is
true of yours, and point `prompts.implement` at the copy. `{handoff}` and
`{workspace}` in it are substituted at run time, by `falconet prompt
implement` — which is why that command's output is not the copy to commit:
it has already put this machine's paths where the placeholders were.

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

One file, `.github/workflows/infra-requests.yml`, and this is the whole of it
— `init` writes exactly this, with `uses:` pinned to its own version:

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
      plan-env: ${{ secrets.FALCONET_PLAN_ENV }}
```
<!-- /caller-workflow-template -->

| Input | Required | Default | What it is |
| --- | --- | --- | --- |
| `issue` | yes | — | The issue number to work. |
| `config` | no | `.github/falconet.json` | Path to the config file. |
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
  expressions there — and it is the one coordinate: the workflow at that ref
  installs, in every job, the binary whose digest that ref's tree holds.
  `main` is where the template starts and it moves; put a tag there —
  `@v0.2.0` — which is what `init` writes, and what step 4 of the install
  says. There is no `falconet-ref` input any more: the bash-era caller
  passed it to choose which falconet the jobs checked out, nothing is
  checked out now, and a caller still passing it is rejected when the file
  is loaded.
- **It coexists with a stock `claude.yml`.** If you already run
  `anthropics/claude-code-action` on issue events, that one starts on an
  `@claude` mention and this one on the queue label. Don't write `@claude` in
  an infra request unless you want both.

**Check:** after pushing, `gh workflow list` shows `infra requests`. Before
pushing, `falconet doctor`'s three `8.` lines: the file exists, it uses the
reusable workflow, and its `permissions:` block grants what the widest job
declares.

### 9. Run a canary issue

Step 4 of the install above, unchanged: file the smallest change the planned
stack can carry, watch the table, expect one of the three endings, and make
sure the ref in `uses:` is a tag.

### Troubleshooting

| What you see | Why | Do |
| --- | --- | --- |
| The run is `startup_failure`: no jobs, no logs, and nothing on the issue at all | The caller grants less than a job inside declares, or passes an input the workflow does not declare — `falconet-ref`, from a bash-era caller. GitHub checks both when the workflow file is loaded, so nothing runs and nobody is told — including the requester. Until 2026-08-21 this README prescribed `contents: read`, which `publish` exceeds. | Step 8's `permissions:` block, verbatim; no `falconet-ref:`. `falconet doctor` reports both. |
| **gate** is red and the issue has no comment | `prepare` hard-failed before the acknowledgment — the one failure the requester never hears about, because `contain` is conditioned on the gate having said `ready`. | Open the run; the last lines of **Prepare** name the cause. The usual ones are the next three rows. |
| `config .stacks.plan names "x", which is not a directory` (or `.stacks.validate_only`) | A name in step 7 is not a directory. | Step 7's check. |
| The issue is paused with **the change is in no stack this repository knows about** | The change landed in a directory holding `.tf` files that `.github/falconet.json` names in neither stack list, so nothing validated it and nothing could plan it. Nothing is wrong with the change (#23). | Add the directory to `.stacks.plan` if a human applies it from a pull request, or to `.stacks.validate_only`; then re-file. `falconet doctor` reports it as `MISSING` before a request ever finds it. |
| The issue is paused with **nothing this change touches is planned** | The change reached only stacks in `.stacks.validate_only`, so there is no plan for a human to approve and nothing to open a pull request about. | Decide whether that stack belongs in `.stacks.plan`. If it is genuinely applied by hand, this is the right ending. |
| `prepare: tofu init failed in dns/ — the stack cannot be planned`, then OpenTofu's own text: *no valid credential sources*, *error configuring S3 Backend* | `FALCONET_PLAN_ENV` is missing, or missing the key the backend needs. | Step 5. |
| `prepare: working tree is dirty before the agent ran:`, listing paths | Something in your repository creates untracked files on checkout. | Gitignore them. |
| `init: the working tree is dirty, and the commit init makes must carry only what it writes; commit or stash these first:` | `init` refuses a dirty clone for the same reason. | Commit or stash, then `init` again. |
| `init: could not create label infra-request: POST …/labels: 403 Resource not accessible by personal access token — the token needs Issues: write`, or `init: could not store secret …: … — the token needs Secrets: write`, then `stopped at step N; what was done before it stands, and a second run carries on from here` | `FALCONET_SETUP_TOKEN` lacks a permission from the table in step 2 of the install. `init` writes the labels first so this happens before anything hard to undo. | Regenerate the token with the permission named, export it, `init` again. |
| `falconet doctor` says `cannot tell … (403 … — needs Secrets: read)`, or `Issues: read`, `Administration: read` | The same token, read side. | The same. |
| `init: state mismatch — refusing the code` on the terminal, *falconet init: state mismatch — refusing the code* in the browser | GitHub's redirect carried a `state` that is not the one this run sent — a stale tab from an earlier `init`, most likely. One is refused and `init` keeps waiting for the right redirect; a second ends step 3 as `skipped 3. … (the App was not registered: two redirects arrived with the wrong state)`, with nothing stored. | Close old tabs and run `init` again; it carries on from where it stopped. |
| `skipped 3. … (the App was not registered: no redirect from GitHub within 10m)` | The browser never came back: the page was not opened, or **Create GitHub App** was not clicked in time. | `init` again. `--no-browser` prints the URL to open by hand; `--app-timeout` lengthens the wait. |
| `cannot tell  3. the App is installed (timed out after 10m — install it at https://github.com/apps/<name>/installations/new, then run falconet doctor)` | The App is registered and both secrets are stored; the install click did not happen in time. | Open that URL → **Install** → **Only select repositories** → this repository. `Left for you:` repeats it. |
| `Could not find installation` at `create-github-app-token` | The App exists but is not installed on this repository, or the App ID is wrong. | The row above; or step 3. |
| `Resource not accessible by integration` | The caller's `permissions:` block is missing, or the App lacks one of its three permissions. | Steps 3 and 8. |
| `sha256sum: WARNING: 1 computed checksum did NOT match` in the install step | The runner is not Linux x64 — both gitleaks' and falconet's pinned assets are the Linux x86-64 ones, and the digest is checked before anything is installed — or a release asset was replaced, which is what the digest in the tree exists to catch. | `runs-on: ubuntu-latest`. A replaced asset is not yours to fix; do not run it. |
| `the installed falconet reports '…', not v0.2.0` in the install step | The asset at that release runs but is not the version the tree pins. | The same as the row above. |
| Paused `ready-for-human`: *The agent changed files it is not allowed to change … Refused paths: .falconet/…* | A run by hand with the handoff directory not ignored. | Step 2. |
| Paused `ready-for-human`: *did not validate*, followed by OpenTofu output | Validation or the plan failed on the agent's change. The fenced output is tofu's own. | Read it. A credential error is step 5; anything else is the change. |
| `could not add label <name> to #N: …` in a pause step, and the word `failure` | The label could not be put on the issue: one of step 6's labels is missing, or the App lacks Issues: write. The comment was still posted if it could be, and `contain` tries again. | Step 6; then step 3's permissions. |
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
- **`@main` moves.** Pin a tag; `init` does.
- **`doctor` cannot see the installation.** It holds no App key to ask with.
  `init` confirms the install when it makes it; afterwards, the repository's
  Settings → GitHub Apps, or the first run.
- **Never put issue text in `args`.** If you call `action.yml` directly, its
  `args` input is split on whitespace and reaches a shell. Issue titles,
  bodies and comments are attacker-controlled, and the reason all six verbs
  take files rather than strings is so that text never travels that way.

## Support

None promised. This is built for one operator's infrastructure repository and
made public because someone else may find the shape useful. Issues and pull
requests may go unanswered; fork freely.

## License

MIT. See [LICENSE](LICENSE).
