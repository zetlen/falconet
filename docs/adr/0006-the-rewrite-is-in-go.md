# ADR-0006 — The rewrite is in Go, and it begins with setup

**Status:** Accepted · 2026-08-22
**Supersedes:** [ADR-0002](0002-extract-the-pipeline-into-falconet.md) D1 —
the language, and the strangler's sequencing;
[ADR-0004](0004-the-strangler-reaffirmed.md) — its reopening condition is
met, and this is the reconsideration it asked for
**Amends:** [ADR-0003](0003-the-cli-surface.md) — the wrappers, the tests'
`gh` stub, and two verbs added under its own criterion;
[ADR-0005](0005-the-agent-job-is-handed-its-source.md) — the agent job's
one checkout becomes none
**Serves:** I2, I5 (the operator can read the guards that hold both), I6
(one binary, one install step, nothing vendored)
**Reopen when:** a guard cannot be expressed safely in Go, or the operator
stops being able to read the guards. Separately, for D6: the build stops
reproducing, or an adopter needs a target the four assets miss.

## Context

Three things have happened since ADR-0004 was written on 2026-08-20.

**The live run.** On 2026-08-21, `zetlen/wayfinders-infra` issue #106 became
pull request #108: acknowledgment inside a minute, one agent pass, every
guard, a real plan, and a pull request carrying it in full. ADR-0004 named
two conditions for reopening the language question, and the second — "the
integration proving stable enough that a rewrite is the only remaining
unknown" — is met in the terms it was written.

**The consumer wants a binary.** `wayfinders-infra#110` measures the cost of
the interim: 3,240 lines of extracted scripts and tests still living there,
agreeing with falconet "by convention", because ADR-0002 Phase 3 promised
that its skill would become "a shell around the falconet CLI" and there is
no CLI to shell to. falconet exists as a checkout: in CI at
`.falconet-tool/` inside the consumer's workspace, and on a workstation as a
clone plus bash, git, jq, awk, sed and python3 — a dependency set the README
never lists, because it is the shape of the tool rather than a choice anyone
made.

**Setup is nine steps of `gh`, typed by hand.** Every check and every write
in README steps 1–9 is a `gh` command, and each is an API call a program
could make; together they are the part of adopting falconet that costs an
afternoon and produces the wiring bugs the README catalogues. And `gh` is
itself a dependency many prospective adopters do not have. It is on GitHub's
runners and on the laptops of people who already live in GitHub's CLI, which
is not the same population as people who maintain an OpenTofu repository.

And the thing ADR-0004's measurement did not carry: the bash has bugs (#3 —
the shipped prompts are unreachable, because the default config is itself an
override), and the tests, black-box in what they assert, are awkward to
write and extend, with coverage counted in assertions rather than in
properties.

### The measurement, repeated

Same method as ADR-0004. Executable lines, comments and blanks stripped:

| file | total | code |
|---|---|---|
| `libexec/falconet/prepare.sh` | 438 | 201 |
| `libexec/falconet/commit.sh` | 479 | 199 |
| `libexec/falconet/validate.sh` | 413 | 181 |
| `libexec/falconet/assemble.sh` | 189 | 100 |
| `libexec/falconet/park.sh` | 184 | 96 |
| `libexec/falconet/review-verdict.sh` | 219 | 68 |
| `libexec/falconet/push.sh` | 208 | 53 |
| `libexec/falconet/prompt.sh` | 90 | 43 |
| `lib/config.sh` | 146 | 82 |
| `lib/scan.sh` | 211 | 71 |
| `lib/handoff.sh` | 56 | 24 |
| `lib/repo.sh` | 39 | 15 |

**1,133 executable lines.** They invoke `git` 78 times, `tofu` 53, `jq` 24,
`gh` 20 and `gitleaks` 13: subprocess orchestration and file plumbing, with
roughly 300 lines of guard logic in the middle — `commit`'s allowlist and
ordered denylist, `assemble`'s whole-line truncation and fence sizing,
`scan`'s sentinel matching, `park`'s cap.

**The suite is 12 files and 484 assertions**, every one of which crosses a
process boundary. But the suite *addresses* its subject by bash path: ten of
the twelve files name a `libexec/falconet/*.sh` or `lib/*.sh` directly, and
only `dispatcher.test.sh` and `prompt.test.sh` go through `bin/falconet`.
`config.test.sh` drives `lib/config.sh` through a bash probe fixture.
`scan.test.sh` runs `lib/scan.sh` as a process, though it is not a verb. One
assertion (`contract.test.sh:416`) greps `push.sh`'s source for an `echo`
without `>&2`. None of this is what ADR-0004 forbade — nothing sources its
subject — but it is exactly what that record said to watch, and it is the
first thing the port has to fix, in bash, before any Go is written.

### Why not Bun, now that it is reconsidered

ADR-0002 D1 chose Bun for one reason: the Claude Agent SDK is TypeScript, and
D1's endgame was falconet running the agent loop itself. ADR-0004 then split
the agent invocation off as "a separate bet that should not be folded into a
language change", and the bet has not been placed: the agent pass runs on
`anthropics/claude-code-action` and stays there (#1 pins its model; nothing
moves it). With the reason gone, what Bun costs is visible:

- `bun build --compile` embeds the runtime. The artifact is on the order of
  90 MB, against ADR-0003's "pins exactly one binary", which meant a small
  one; `setup-bun` is the alternative, and it is a second moving part.
- JavaScript regular expressions backtrack. The denylist and the sentinel
  matching run over issue text, and issue text is attacker-controlled.
- Writing a repository secret requires a libsodium sealed box, and neither
  Node's nor Bun's `crypto` has the primitives. It is a WASM package from npm.
- The dependency tree of an npm GitHub client is not one this repository's
  pin discipline would enjoy auditing.

### Why not Rust

Rust was weighed seriously and would be the stricter choice: `Result` with
`#[must_use]` makes an unchecked failure a compile error, and an exhaustive
`match` makes a forgotten terminal state one too — which is exactly the
fail-open class three of the eight wiring bugs belonged to. It loses on one
thing that outweighs the rest here: **the operator must be able to read a
guard cold.** The guards are the product, the comment above each is the
incident record, and a reviewer who trusts an agent's Rust instead of
reading it is in a worse position than one reading bash. Go is the language
the operator reads. Go's `regexp` is RE2 and linear-time, so the
attacker-controlled-text point is not lost; the fail-closed discipline is
recovered below with tooling rather than with the type system.

## Decision

### D1 — Go. One module, one binary, standard library first.

`falconet` becomes one Go module producing one static binary:
`CGO_ENABLED=0`, `-trimpath`, the toolchain pinned by the `toolchain`
directive in `go.mod` (1.26 today). The standard library covers the program:
`os/exec` with argv slices — no shell, no quoting, no word-splitting;
AGENTS.md's "shell traps" describe a language this tool no longer speaks —
`encoding/json` for the config, `regexp` for the denylist, `net/http` for
GitHub, `embed` for the shipped prompts, `crypto/rsa` and `crypto/sha256`
for the App JWT.

**One dependency outside the standard library is named here**, because the
program cannot avoid it: `golang.org/x/crypto/nacl/box`, for
`SealAnonymous`, the libsodium-compatible sealed box the secrets API
demands. It is maintained by the Go team and it is what `gh` uses. Anything
further is an amendment to this record, with a reason.

Three of falconet's neighbours — `gh`, `gitleaks`, `tofu` — are Go. That is
not a reason on its own, but it means the reference implementation of every
GitHub interaction falconet needs, sealed box included, is readable in the
language the operator reads.

Fail-closed is a discipline, not a type: `go vet`, `staticcheck` and
`errcheck` run in CI, and a commit is not green without them. An ignored
error is a red build.

### D2 — The GitHub client is falconet's own. `gh` and `jq` stop being dependencies.

`net/http` against `GITHUB_API_URL` — the variable Actions sets;
`https://api.github.com` otherwise. The verbs authenticate with `GH_TOKEN`
or `GITHUB_TOKEN`, which is what the workflow already hands them. Setup
authenticates differently (D4). The runtime dependency set in CI becomes
git, tofu, gitleaks and the binary. On a workstation, the same.

The tests lose the `gh` stub on `PATH` and gain a fake API: `GITHUB_API_URL`
pointed at a local server. python3's `http.server` is already in the
test-dependency set; the fake records what it was asked and answers from
fixtures. Still a process boundary; still stdout, exit code, and files.

### D3 — The whole surface moves, behind the carried suite, in this order

ADR-0004 said the tests permit moving the whole surface at once, and that
D1's subcommand-by-subcommand sequencing was risk management for a first
live run. The first live run has happened. So:

0. **The suite first, in bash, before any Go.** Every test addresses its
   subject as `$FALCONET <verb>`, where `FALCONET` defaults to
   `bin/falconet` and is overridable. `scan` and `config` become unlisted
   subcommands — `prompt`'s precedent: public in that they work, not
   vocabulary — so `scan.test.sh` and `config.test.sh` keep every assertion.
   `contract.test.sh:416` is replaced by its behavioural form. The suite is
   green against bash before step 1 starts, and that is the suite Go has to
   pass.
1. **The release path, proven on something small.** `falconet version`,
   `doctor` and `init` (D4, D5) have no bash predecessor and are consumed
   by people on laptops, so they ship before any verb moves and prove
   building, releasing and installing (D6) while nothing in CI depends on
   it.
2. **The verbs, one commit each, on a branch**, each green against the full
   suite — the sequence ADR-0002's plan used, for the same reason: a port
   that touches one verb is reviewable. `assemble` first, because its
   truncation seams are where PR #28 lived and where property tests earn
   their keep. Then `commit`, where the guards are; then `push`, `park`,
   `validate`, `prepare`, `prompt`, and `review-verdict` — unwired, as
   before.
3. **One merge to `main`.** The bash under `bin/`, `lib/` and `libexec/` is
   deleted in that merge, not kept: two implementations agreeing by
   convention is the disease #110 names, and this repository will not host
   it. Consumers pinned to a SHA, as the README tells them to be, are
   untouched until they move the pin.

The verbs' vocabulary, exit discipline (0 / 1 / 2), one-word stdout contract,
handoff protocol, config schema and defaults are unchanged by the port. If
#5's renames are taken, they are taken in step 2, where a rename is a
search-and-replace in one language rather than in two.

Unit and property tests live in Go beside the code they cover (`testing`,
`testing/quick`), for the guard logic ADR-0004 identified as the place bash
was worst: truncation never splits a line and never exceeds its budget; the
fence is always longer than the longest backtick run in the body; the
denylist matches in config order. The bash suite remains the acceptance
suite and the incident record. It is not rewritten.

### D4 — Setup is two verbs, and its credential is a token the operator mints

`falconet doctor` runs README step 1's checks and every "Check:" line from
steps 2–8, read-only, and reports each. `falconet init` does steps 2 through
8 — the `.gitignore` line, the App, the four secrets, the labels,
`.github/falconet.json`, the caller workflow — and files step 9's canary on
request. Both meet ADR-0003's criterion for vocabulary: a caller invokes
them directly. The caller is a person.

Authentication is **`FALCONET_SETUP_TOKEN`**, and nothing else.

- A fine-grained personal access token, scoped to the one repository:

  | Permission | Level | For |
  |---|---|---|
  | Administration | read — write only if `init` is to *change* the Actions policy rather than say how | step 1's `actions/permissions` checks |
  | Actions | read | the same |
  | Secrets | write | steps 3–5 |
  | Issues | write | labels, the canary |

  A classic token needs `repo`. Neither needs Contents or Workflows (D5).
- Deliberately its own name. `GITHUB_TOKEN` and `GH_TOKEN` are **not**
  fallbacks: in CI those are the Actions token, which cannot do this and
  must never be asked to; on a laptop they are whatever someone set for
  something else. A setup credential is powerful and short-lived — the
  README tells the operator to mint it with a seven-day expiry — and its
  name should say which kind it is.
- Classic tokens report their scopes in `X-OAuth-Scopes`; fine-grained ones
  report nothing. So `init` performs every read before any write, and its
  first write is the idempotent one — the labels — so a token short of
  `Secrets: write` fails before anything hard to undo has happened, with the
  missing permission named.
- The device flow — `gh auth login`'s mechanism — was weighed and deferred.
  It needs an OAuth App registered and owned by the maintainer, a client ID
  in the binary, and an explanation of organisation OAuth restrictions. It
  fits behind the same token lookup later, if a token turns out to be the
  step adopters stall on.

Without a token, `init` still does the local steps — the `.gitignore` line,
the config, the workflow file — and prints what is left. It degrades to the
README, never to nothing.

### D5 — What `init` does that the README could not

- **The App, by manifest.** `init` opens `https://github.com/settings/apps/new`
  (or the organisation's) with the manifest — the name, the three
  permissions, no webhook — and listens on localhost for the redirect.
  `POST /app-manifests/{code}/conversions` returns the App ID and the
  private key. The key goes from that response into a sealed box and into
  the repository's secrets; it is never written to disk, and the README's
  `rm ~/Downloads/…pem` step has nothing left to remove. Installing the App
  is still a click in a browser: `init` opens the page and polls
  `GET /repos/{owner}/{repo}/installation`, with a JWT it can now sign,
  until the installation exists.
- **Secrets, sealed.** `GET …/actions/secrets/public-key`, `box.SealAnonymous`,
  base64, `PUT`. The `plan-env` secret is read from a file the operator
  names, or from stdin, and validated as a JSON object before it is sealed —
  the README's `jq -e 'type == "object"'`, done by the tool.
- **Files committed, never pushed.** The `.gitignore` line,
  `.github/falconet.json` and `.github/workflows/infra-requests.yml` are
  written and committed locally. `init` prints the push and does not run it.
  Pushing a workflow file through the API needs the `workflow` scope, which
  the token does not have and should not; pushing over the operator's own
  git credentials needs nothing; and the last step staying in a person's
  hands is this project's shape.

### D6 — Install: a release asset, a digest in-tree, a reproducible build

`action.yml` installs falconet the way it installs gitleaks: a pinned
version, a URL, a SHA-256 for `linux_x64` in the tree, and `falconet version`
as proof it runs. For that digest to be in the tree at the commit a consumer
pins, the build must be reproducible: `CGO_ENABLED=0 go build -trimpath`
with the pinned toolchain produces the same bytes on a laptop and on the
release runner, so the digest is computed, committed and tagged, and the
release workflow rebuilds and refuses to publish if the bytes differ. That
is the discipline; the first release is where it is proven.

A workstation installs from the release page, or with
`go install github.com/zetlen/falconet@<tag>`. No Homebrew tap and no
install script: the level of commitment [operating.md](../operating.md)
declines is still declined. One snag is documented rather than solved:
macOS quarantines a downloaded binary, `go install` does not trigger that,
and the README says so.

In CI, the reusable workflow installs the binary in each job that runs a
verb instead of checking falconet out into the consumer's tree. Prompts are
embedded, so `.falconet-tool/` has no remaining job, and the invariant
AGENTS.md calls "the tool lives inside the tree it works on" — the
`.git/info/exclude` lines, the tar exclusions, the contract assertions that
hold their order — retires with it. ADR-0005's "one checkout in the agent
job, and it is falconet's" becomes **no checkout in the agent job**: a
public release asset is fetched with no token, which is a stronger form of
the sentence that record was written to keep true. `action.yml` keeps its
setup-plus-pass-through shape for a caller that wants one verb in a workflow
of its own.

### Unchanged, on purpose

- **The agent pass runs on `anthropics/claude-code-action`.** ADR-0004's
  split stands. Go closes the Agent SDK door — it is TypeScript and Python —
  and this record accepts that with eyes open: if falconet ever runs the
  pass from a workstation, `claude -p --allowedTools Read,Edit,Write,Grep,Glob`
  is the surface, and it is a CLI the binary spawns like any other.
- **The security model and the job graph.** The agent holds no shell and no
  token; the scripted jobs never run the agent; ADR-0005 stands, stronger.
- **Forge-agnosticism is still a non-goal.** The client speaks GitHub.

## Consequences

- **AGENTS.md changes shape.** "Do not port to Bun yet" becomes "the
  language is Go, and this is why". "Shell traps" retires, except for the
  two facts about tofu that are not about shell: never end a plan early,
  and always `-no-color`. "Two roots" loses `FALCONET_HOME` — 19 references,
  all of them about finding files the binary now embeds — and keeps
  `REPO_ROOT`. "The tool lives inside the tree it works on" retires with
  D6.
- **The README's install section becomes four steps:** install the binary,
  mint the token, run `falconet init`, file the canary. The nine steps move
  to an appendix as the manual path and as the specification `doctor`
  checks against.
- **Unverified until done**, recorded so the first run knows what to check:
  the manifest conversion endpoint's authentication — the docs list no
  token type, which is consistent with a bootstrap flow, but no run has
  confirmed it; the reproducible-digest discipline; the fine-grained
  permission table above, which the reference lists and `doctor` confirms by
  probing; and sealed-box interoperability end to end — `SealAnonymous`'s
  documentation says libsodium-compatible, and the first `init` is the test.
- **Issues.** #3 closes by construction (`embed`). #5's rename is cheapest
  during D3 step 2. #1 and #2 are untouched. #4 is not addressed here, though
  a binary that logs less is a smaller haystack.
- **Owned from now on:** a release pipeline, and falconet's own copy of
  eight GitHub endpoints' shape — in exchange for `gh` and `jq` as
  dependencies, a README that is `init` plus `doctor`, and a consumer whose
  skill can finally be the shell around the CLI that ADR-0002 promised.
- **The operator reads Go.** The comment above every guard moves verbatim.
  They are prose, and they were always the durable part.

## Status, 2026-08-23

- **The port landed.** D3 steps 0–3 are the integration branch from 49edcfd
  (the suite spawns its subject through `$FALCONET`) through 11057cc (the
  review of the cutover), one commit per verb, the bash deleted in e311ab6,
  and the documents in the commit that closes #19. Every verb is native;
  `make test` is 867 suite cases across 16 files through `dist/falconet`
  and `go test ./...` over 19 packages.
- **The reproducible-digest discipline: verified.** v0.1.0 (#8): the digest
  prepared on a darwin/arm64 laptop reproduced on the ubuntu linux/amd64
  release runner, run 32600784604, and the release published.
- **The fine-grained permission table and `doctor`'s probes: verified
  against the fake, not yet against GitHub.** doctor.test.sh and
  init.test.sh hold that each refusal names the permission the reference
  lists (a 403 on the secrets list says `needs Secrets: read`; on a label
  POST, `the token needs Issues: write`) and that `init`'s first write is
  the labels; whether a fine-grained token with exactly D4's four
  permissions satisfies every endpoint awaits the first live `init`.
- **Sealed-box interoperability and the manifest endpoint's authentication:
  still unverified.** `SealAnonymous` is held to a property (the private key
  opens what was sealed), and the manifest conversion is asked first without
  a token and once more with `FALCONET_SETUP_TOKEN` on a 401 or 403, saying
  on stderr which worked; both are proven by the first live `init` and the
  canary that uses the secrets it stored.
- **The first live run on the binary awaits the canary after v0.2.0.** The
  one live run (2026-08-21, `wayfinders-infra` #106 → PR #108) was on the
  bash.
