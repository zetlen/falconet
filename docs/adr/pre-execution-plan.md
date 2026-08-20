# Implementation plan — port the corpus onto the CLI surface

**Status:** ready to execute · 2026-08-20
**Implements:** [ADR-0003](0003-the-cli-surface.md), which decided the surface.
**Governed by:** [ADR-0004](0004-the-strangler-reaffirmed.md), [ADR-0002](0002-extract-the-pipeline-into-falconet.md) and `HANDOFF.md` §3 (settled decisions) and §5 (landmines).

## What this is

This is a **port**, not a greenfield build. Seven working scripts (1,815 lines)
and six passing test files (1,421 lines) already live in this repository. The
job is to move them onto the verb surface ADR-0003 settled — `prepare`,
`commit`, `push`, `validate`, `park`, `assemble` — without losing a guard, a
test, or an incident comment.

Only two things here are genuinely new: the `prepare` verb (which existed as
inline YAML in the origin workflow, never as a script) and the first test for
`validate` (which shipped untested). Everything else is `git mv` plus rewiring.

**The most important rule: `bash tests/run.sh` is green before the first task
and green after every commit.** A task that leaves it red is not finished.

---

## Invariants

These hold for every task. An executing agent that finds itself about to
violate one should stop and say so rather than proceed.

1. **Never rewrite `tests/lib.sh` or `tests/run.sh`.** They are committed,
   working, and load-bearing for six test files. `lib.sh` provides `it`,
   `assert_eq expected actual`, `assert_contains haystack needle`,
   `assert_not_contains`, `assert_file_missing`, `summary`, `$REPO_ROOT`,
   a `$WORK` **directory**, and `execution_log_from`/`execution_log_of`
   (which `ci-review-verdict.test.sh` depends on). Add helpers if a task
   needs one; replace nothing.
2. **Move with `git mv`, so history follows the file.** The incident comments
   are the reason the guards are trustworthy — `HANDOFF.md` §5. Preserve them
   verbatim; fix only the paths they name, and only in the task that moves the
   file, never in a sweep.
3. **Port each test alongside its script, in the same commit.** A verb whose
   test did not move with it is an untested verb.
4. **Exit discipline, everywhere:** 0 = outcome determined, 1 = refused
   mechanically or check failed, 2 = usage (including `--help`). Verbs that
   decide something print exactly one word on stdout and nothing else.
5. **No network in tests.** Stub `gh`; push only into bare repos under `$WORK`.
   Tools available: bash, git, jq, awk, python3 stdlib. Add nothing.
6. **Config is read, never assumed.** Precedence: `--config` flag, then
   `$FALCONET_CONFIG`, then `.github/falconet.json`, then the hardcoded
   defaults in ADR-0003 — which reproduce the origin repository's behavior
   exactly. Every key optional.
7. **`$GITHUB_ENV` is optional.** Handoff files are written always; CI exports
   append only when that path is set and writable. The verb sequence must run
   on a workstation with no GitHub context.
8. **Do not pipe `tofu plan` into `head`/`tail`** — SIGPIPE kills tofu before
   it releases its state lock. Redirect to a file, always with `-no-color`.
9. **No test may reach inside its subject.** Every assertion crosses a
   process boundary — spawn, then check stdout, exit code, files. No sourcing
   the verb under test, no asserting on bash internals. This is what keeps the
   deferred Bun port cheap ([ADR-0004](0004-the-strangler-reaffirmed.md)); a
   test that couples to bash spends that option.
10. **Ask before anything that leaves this machine.** No `gh repo create`, no
   push, no credential minting — `HANDOFF.md` §4 and §7. Those are the
   operator's calls.

---

## The shape

```text
before                              after
──────                              ─────
scripts/ci-secret-scan.sh      →    lib/scan.sh                    (invoked by commit)
scripts/ci-commit-change.sh    →    libexec/falconet/commit.sh
scripts/ci-push-branch.sh      →    libexec/falconet/push.sh
scripts/ci-validate.sh         →    libexec/falconet/validate.sh
scripts/ci-park-issue.sh       →    libexec/falconet/park.sh
scripts/ci-pr-body.sh          →    libexec/falconet/assemble.sh
scripts/ci-review-verdict.sh   →    libexec/falconet/review-verdict.sh   (unwired: no verb, no caller)
                       (new)   →    libexec/falconet/prepare.sh
                       (new)   →    libexec/falconet/prompt.sh           (unlisted helper)
                       (new)   →    bin/falconet                          (dispatcher)
                       (new)   →    lib/config.sh, lib/handoff.sh         (sourced)
.ci-handoff/                   →    .falconet/                            (configurable)
```

### The one trap in the move

Four scripts locate the repository root relative to themselves:

```bash
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"          # one level up from scripts/
```

`lib/scan.sh` sits at the same depth, so **it needs no change**. But
`libexec/falconet/` is two levels down, so `commit.sh`, `push.sh`,
`validate.sh` and `review-verdict.sh` each need a second `dirname`. Getting
this wrong is silent — the script finds *a* directory and misbehaves later.

The three tests that copy a script into a scratch repo to exercise its
self-location must copy to the new path too:

| Test | copies to `<scratch>/repo/scripts/` | becomes |
|---|---|---|
| `ci-secret-scan.test.sh:38` | `ci-secret-scan.sh` | `<scratch>/repo/lib/scan.sh` |
| `ci-commit-change.test.sh:34-35` | commit-change + secret-scan | `repo/libexec/falconet/commit.sh` + `repo/lib/scan.sh` |
| `ci-push-branch.test.sh:33`, `handover.test.sh:144` | `ci-push-branch.sh` | `repo/libexec/falconet/push.sh` |

`park.sh` and `assemble.sh` never resolve their own path and are invoked
directly by absolute path in tests — they move without any path surgery.

---

## Tasks

Each task ends with a green `bash tests/run.sh` and one commit. Check the box
when the commit exists.

### Task 0 — commit what is already decided

- [ ] `git add docs/adr/0003-the-cli-surface.md`, `docs/adr/0004-the-strangler-reaffirmed.md` and this plan; commit.
- [ ] Fold `HANDOFF.md` into the repo's own docs (its closing line asks for
      this): §3 decisions are already in ADR-0002/0003 — verify, then keep
      only what is not recorded elsewhere. §4 (operator credentials) and §7
      (publishing) belong in a new `docs/operating.md`. §5 landmines belong
      next to the guards they describe, or in `CONTRIBUTING`-shaped prose that
      is **not** a contributor guide (HANDOFF §3.9 forbids one).
- [ ] Delete `HANDOFF.md` only once nothing in it is lost.

**Verify:** `bash tests/run.sh` green (unchanged, 6 files).

---

### Task 1 — dispatcher, config, handoff plumbing (purely additive)

**New:** `bin/falconet`, `lib/config.sh`, `lib/handoff.sh`,
`tests/dispatcher.test.sh`, `tests/config.test.sh`, `.gitignore` entry.
**Moves:** nothing. The suite must still be green with all six old test files
untouched.

`bin/falconet` routes `falconet <verb> [args]` to
`libexec/falconet/<verb>.sh`. Known verbs: `prepare commit push park validate
assemble` plus the unlisted `prompt`. Unknown verb or no verb → usage on
stderr, **exit 2**. A known verb whose file is missing or not executable →
a named error, **exit 1** — not a bare 127. During the port most verbs are
missing, and that path must be legible.

`lib/config.sh` is **sourced**, not executed. It resolves the config file by
invariant 6 and exposes the ADR-0003 defaults when a key is absent. Reads via
`jq`; a malformed config is exit 1 with the parse error, never a silent
fallback to defaults.

`lib/handoff.sh` is sourced too: resolves `handoff_dir` (default `.falconet`),
creates it, and provides the "append to `$GITHUB_ENV` only if set and
writable" helper that four verbs need.

- [ ] `tests/dispatcher.test.sh`: unknown verb → 2 + usage; no args → 2;
      known-but-unbuilt verb → 1 with the verb named; `--help` → 2.
- [ ] `tests/config.test.sh`: defaults with no file; `.github/falconet.json`
      picked up; `$FALCONET_CONFIG` beats it; `--config` beats that;
      malformed JSON → exit 1; `paths.deny_content` **order preserved**
      (`templatefile(` must be tested before `file(` — see Task 3).
- [ ] `.gitignore`: add `.falconet/` beside the existing `.ci-handoff/` entry,
      carrying the same comment about why the entry is the first line of the
      defence.

**Verify:** `bash tests/run.sh` → 8 files, all pass.
**Commit:** `feat: dispatcher, config resolution, handoff plumbing`

---

### Task 2 — port the secret scan to `lib/scan.sh`

ADR-0003: the scan is **not** a verb. It was only ever called by the commit
stage and it stays there — `lib/` bash with its fail-closed exit discipline
intact (0 clean, 1 scanner broken, 2 usage, 3 finding).

- [ ] `git mv scripts/ci-secret-scan.sh lib/scan.sh`. `REPO_ROOT` resolution
      is already correct at this depth — confirm, do not "fix".
- [ ] Update the header's `.ci-handoff/` references to the configured handoff
      dir, and its self-name.
- [ ] `git mv tests/ci-secret-scan.test.sh tests/scan.test.sh`; retarget the
      copy at line 38 to `<scratch>/repo/lib/scan.sh`.
- [ ] All 241 lines of assertions survive, including the `GITLEAKS=` override
      and the "no targets is a usage error, not a silent pass" case.
- [ ] **Sequencing correction, found in execution.** Moving the scan breaks
      `commit` in the same instant, so this task must also carry the minimal
      rewiring that keeps the suite green: point `SECRET_SCAN` at
      `$REPO_ROOT/lib/scan.sh` and retarget the copy in
      `tests/ci-commit-change.test.sh:35`. Both were originally listed under
      Task 3a, which is one commit too late.

**Verify:** `bash tests/run.sh` green; `scan.test.sh` passes with the same
count it had as `ci-secret-scan.test.sh`.
**Commit:** `refactor: ci-secret-scan.sh -> lib/scan.sh`

---

### Task 3 — port `commit` (two commits, both green)

The largest script (398 lines) and the largest test (524 lines). Split the
work so a failure is attributable.

**3a — move and rewire, behavior identical**

- [ ] `git mv scripts/ci-commit-change.sh libexec/falconet/commit.sh`.
- [ ] Second `dirname` for `REPO_ROOT` (see "the one trap").
- [ ] ~~`SECRET_SCAN` rewiring~~ — done in Task 2, which could not leave it
      undone and stay green.
- [ ] `OUT_DIR` default `.ci-handoff` → `handoff_dir` via `lib/handoff.sh`,
      `--out-dir` still overriding.
- [ ] `git mv tests/ci-commit-change.test.sh tests/commit.test.sh`; retarget
      both copies (lines 34-35) and the invocation at line 74; keep the
      `--bogus` and `--help` usage cases at 497/501.

**Verify:** green, same assertion count.
**Commit:** `refactor: ci-commit-change.sh -> libexec/falconet/commit.sh`

**3b — config-drive the policy**

The allowlist is currently the literal `*.tf` case arm at line 259; the
denylist is the `tf_denylist_hit` function at 270-281. Both become
`paths.allow` and `paths.deny_content` reads, **defaulting to exactly today's
values**.

- [ ] **`deny_content` order is load-bearing.** `templatefile(` must be
      matched before `file(`, or a `templatefile()` call is reported as
      `file()` and the failure message names the wrong construct. The existing
      code encodes this by ordering its `grep` calls; the config array must
      preserve array order and the test must assert it.
- [ ] The `templatefile[[:space:]]*\(` regex handles whitespace before the
      paren. Whatever the config format, that tolerance survives.
- [ ] Add cases to `tests/commit.test.sh`: a custom `paths.allow` admits a
      path the default refuses; a custom `deny_content` entry refuses; the
      default config produces byte-identical behavior to 3a.

**Verify:** green. **Commit:** `feat: commit reads path policy from config`

---

### Task 4 — port `push`

- [ ] `git mv scripts/ci-push-branch.sh libexec/falconet/push.sh`; second
      `dirname`.
- [ ] `PUSHED_BRANCH` appends to `$GITHUB_ENV` **only when the push landed**,
      via `lib/handoff.sh`. It is deliberately not `BRANCH` under another
      name — a run that failed to push must not look pushed.
- [ ] Preserve `--force-with-lease` and the one-shot single-quoted credential
      helper verbatim, with the 40-line incident comment about run
      32093607680.
- [ ] `git mv tests/ci-push-branch.test.sh tests/push.test.sh`; retarget the
      copy at line 33 and the four invocations; keep the "missing `--branch`
      is a usage error" case.
- [ ] `tests/handover.test.sh` also copies push-branch (line 144) — retarget
      it here, in this commit, or the suite goes red.

**Verify:** green, including `handover.test.sh`'s live bare-repo push cases.
**Commit:** `refactor: ci-push-branch.sh -> libexec/falconet/push.sh`

---

### Task 5 — port `park`

- [ ] `git mv scripts/ci-park-issue.sh libexec/falconet/park.sh`. No path
      surgery needed — it never resolves its own location.
- [ ] Labels from `labels.needs_info` / `labels.human`, defaulting to today's.
- [ ] Preserve the 60000-char whole-line truncation with its explicit cut note
      pointing at `--run-url`. Content is dropped loudly or not at all.
- [ ] **Discrepancy to resolve:** the script accepts `--body-title`, which
      folds `--body` into a collapsed fenced `<details>` block for machine
      output. ADR-0003's signature omits it. Keep the flag — the routing table
      needs it for validation logs — and amend ADR-0003's row rather than
      dropping a working option. Note the amendment in the commit message.
- [ ] `git mv tests/handover.test.sh tests/park.test.sh` (it is the park
      test, plus one push integration case); retarget `$PARK`.

**Verify:** green — all 15 `handover` assertions, including "the branch really
is on the remote".
**Commit:** `refactor: ci-park-issue.sh -> libexec/falconet/park.sh`

---

### Task 6 — port `validate`, and write its first test

`ci-validate.sh` is 281 lines and **has no test file**. This is the one place
in the port where real test-writing is owed, not carried over.

- [ ] `git mv scripts/ci-validate.sh libexec/falconet/validate.sh`; second
      `dirname`.
- [ ] Stacks from config: `stacks.plan` gets a **real** `init`,
      `stacks.validate_only` gets `-backend=false`. Defaults reproduce the
      current hardcoded `for s in dns workspace site` loop with `dns` as the
      only planned stack, and the `dns_validate_ok` gate that stops a plan
      from merely repeating a failed validate.
- [ ] Plan command from `plan.command` with `{stack}` substitution, defaulting
      to today's `-no-color -input=false -refresh=false -lock=false`.
      `-refresh=false -lock=false` is mandatory in CI: the job holds a
      read-only state credential and must never call the provider's API.
- [ ] The commit-touches-handoff-dir check follows the rename: it must now
      refuse a commit touching `.falconet/` (and keep refusing `.ci-handoff/`
      until the consuming repo has migrated).
- [ ] **New `tests/validate.test.sh`** with a `$TOFU` stub, covering: failures
      across multiple stacks are **collected, not early-exited**;
      `validation-failure.txt` is prose with no instructions in it;
      `plan.txt`, `diff.patch`, `changed-files.txt` are snapshotted on every
      run including failing ones; a commit touching the handoff dir is
      refused; no commit on top of `--base` is refused; exit 1 on failure.
- [ ] `diff.patch` stays `git log -p`, not `git diff` — reviewers get each
      commit message beside the change it claims to make.

- [ ] **Bugs found by writing the test, all fixed in this task.** Being the
      first reader of an untested 281-line file turns up more than coverage:
      `--base` was string-compared rather than resolved, so `--base main` made
      the no-commit guard silently false and the run could reach `exit 0`
      having snapshotted an empty diff for a reviewing agent that holds no
      Bash; `$HANDOFF_DIR` was interpolated raw into an ERE, so a
      config-supplied name carrying a regex metacharacter made grep exit 2 and
      the `if` read that as "no match" — a guard failing OPEN on exactly the
      commit it exists to refuse; `--help` exited 0, which for this verb means
      "validation passed"; `mkdir -p || exit 2` reported a filesystem failure
      as a usage error; and `dns_validate_ok` was inverted with respect to its
      name (0 = OK), a trap for whoever writes `-eq 1` by intuition and plans
      a stack whose validate just failed.
- [ ] **The report contained an instruction, against its own header.** The
      plan-failure path wrote "The guard is authoritative: quote it, never
      weaken it" into `validation-failure.txt`, which is posted verbatim to
      the requester — while the file's header promises it gives no
      instructions, because there is nobody there to instruct. Moved to
      stderr, where a person debugging a run reads it.
- [ ] **The documented snapshot contract was aspirational.** "Written on every
      run" is not true and should not become true: the two guards stop before
      the snapshot precisely because a snapshot taken past them would be a
      lie. The header now says what holds — every run that gets past the
      guards, including every failing one.
- [ ] `VALIDATED=true` on the success path, which ADR-0003 lists among the CI
      exports and the original never wrote.

**Verify:** green, with `validate.test.sh` present and non-trivial.
**Commit:** `refactor: ci-validate.sh -> libexec/falconet/validate.sh, with tests`

---

### Task 7 — port `assemble`

- [ ] `git mv scripts/ci-pr-body.sh libexec/falconet/assemble.sh`. No path
      surgery.
- [ ] Preserve, with the PR #28 comment intact: fence = longest-backtick-run
      + 1; 70/30 head/tail whole-line truncation; the elision note stating how
      many lines were dropped and where the untruncated plan lives; exit 1
      when the description alone exceeds `--limit` (nothing written);
      `--plan-url` printed next to the plan block **even when the plan fit**.
- [ ] `git mv tests/ci-pr-body.test.sh tests/assemble.test.sh`; retarget
      `$SCRIPT`.

**Verify:** green, all 221 lines of assertions.
**Commit:** `refactor: ci-pr-body.sh -> libexec/falconet/assemble.sh`

---

### Task 8 — `prepare` (the one new verb)

Stage 1 of the origin workflow was inline YAML, at
`docs/provenance/infra-issues.yml:421-620`. Read those 200 lines before
writing anything; they are the specification. Read
`docs/provenance/work-infra-issues-SKILL.md` beside them — where the workflow
and the human-facing skill agree, that is the real interface.

Prints one word: `ready` | `in-flight` | `ineligible`.

**Precedence, in this order** (the first match wins):

1. blocking label present → `ineligible`
2. opt-out checkbox ticked in the body → `ineligible`
3. queue label absent → `ineligible`
4. open PR on a branch matching `^(claude/)?issue-<n>-` → `in-flight`
5. otherwise → `ready`

`ineligible` and `in-flight` write **no handoff files and park nothing** —
duplicate and ineligible events are silent no-ops (ADR-0003's routing table).

On `ready`:

- [ ] Fetch the issue once to `issue.json`; everything downstream reads the
      file. Never `gh ... | grep -q` — `grep -q` exits at the first match and
      can SIGPIPE `gh`, which under `pipefail` turns a *found* match into a
      non-zero pipeline: the opposite of the answer just computed.
- [ ] Render `request.md` from the snapshot (title, body, comment thread
      oldest-first). Both agents read this file; neither has `gh`.
- [ ] Slug the title → `issue-<n>-<slug>`, 40 chars, `request` if empty.
- [ ] If `git ls-remote` finds that branch on the remote, append
      `$GITHUB_RUN_ID`. Since every run now pushes, a leftover branch is the
      *ordinary* state of a retried issue.
- [ ] `git switch -c`, set the bot identity (a fresh runner has none, and
      `commit` dies on "Please tell me who you are"), then **assert the tree
      is clean** — the agent's outcome is read from the tree, so a dirty tree
      makes that reading a lie.
- [ ] Baseline plan → `plan-baseline.txt`. Hard-fails on purpose: if `main`
      cannot plan, no agent time fixes it.
- [ ] Acknowledge on first claim only, not on the needs-info re-entry path;
      clear the parking label on re-entry. Body from a **file**, never an
      inline `--body`: an indented YAML string carries its indentation into
      the comment and four-space-indented markdown renders as a code block.
- [ ] Write `base-sha.txt`, `branch.txt`; export `BRANCH`, `BASE_SHA`.
- [ ] `tests/prepare.test.sh`: the full gate matrix from issue-JSON fixtures
      (labeled / blocked / opt-out / in-flight / re-entry), a `gh` stub,
      ineligible-writes-nothing, the branch-collision rename, and the
      dirty-tree refusal.

**Verify:** green. **Commit:** `feat: prepare — the eligibility gate and claim`

> **Checkpoint.** All six verbs exist. Stop here and check in before the
> wrappers: `HANDOFF.md` §3.11 says development *is* integration, and wiring
> the consuming repo to `falconet@main` needs the operator (§4, §7).

---

### Task 9 — `prompt` helper and the shipped prompts

- [ ] `libexec/falconet/prompt.sh`: print the config's `prompts.<name>` path
      override if set, else the shipped `prompts/<name>.md`. Unlisted in
      usage; it exists so the wrappers stay free of YAML-embedded heredocs.
- [ ] `prompts/implement.md` and `prompts/park-needs-info.md`, extracted from
      `docs/provenance/infra-issues.yml`. The implementing agent's prompt must
      keep telling it: write the commit message to a file, do not quote or
      summarize the plan, do not try to fix baseline drift.
- [ ] `tests/prompt.test.sh`: default resolution; config override wins;
      unknown name → exit 1 naming it.

**Commit:** `feat: prompt helper and shipped prompts`

---

### Task 10 — `review-verdict`, unwired

- [ ] `git mv scripts/ci-review-verdict.sh libexec/falconet/review-verdict.sh`;
      second `dirname`. **No verb, no caller, no dispatcher entry**.
      ADR-0002 dropped the review agent (ADR-0001 risk 9 stands); `HANDOFF.md`
      §3.6 keeps the script as the reference verdict protocol only.
- [ ] `git mv tests/ci-review-verdict.test.sh tests/review-verdict.test.sh`;
      retarget `$VERDICT`. It uses `execution_log_from` from `lib.sh` —
      another reason invariant 1 exists.
- [ ] A header note saying it is unwired and why, so nobody wires it without
      clearing the bar `HANDOFF.md` §3.6 set: an independent, uncontaminated
      read of diff + commit message + plan before a human is asked to look.

**Commit:** `refactor: review-verdict ships unwired in libexec/`

---

### Task 11 — the wrappers

- [ ] `action.yml` — composite: pin and install `gitleaks`, OpenTofu, `jq`,
      then `falconet <verb>`. Setup plus pass-through, nothing else.
- [ ] `.github/workflows/falconet.yml` (`on: workflow_call`) — the documented
      job graph: gate → implement (agent job, blanked env) → validate →
      publish → containment (`if: always()`). **Job-level separation is the
      security model**: the agent job never holds the token, the scripted jobs
      never run the agent. A single flattened composite destroys this and is
      refused by `HANDOFF.md` §3.2.
- [ ] Agent step stays on `anthropics/claude-code-action@v1`, tool grant
      exactly `Read,Edit,Write,Grep,Glob`, no Bash. Issue text is
      attacker-controlled *and* is the agent's instructions; "also edit the
      workflow to grant Bash" is the attack, and the path policy is what
      refuses it.
- [ ] Tokens minted per-step by `actions/create-github-app-token`. **Delete
      the empty-commit-with-a-scoped-PAT workaround** — do not port it
      (`HANDOFF.md` §4).
- [ ] `tests/contract.test.sh`, retargeted from
      `docs/provenance/workflow-contract.test.sh`, asserting what a unit test
      cannot see: agent toolset exactly `Read,Edit,Write,Grep,Glob` with no
      Bash; exactly one validate step; no repair loop; branch pushed exactly
      once and only by `push`; every `park` call passes `--branch
      "$PUSHED_BRANCH"`; pinned binaries installed before first use;
      `review-verdict` referenced zero times; PR title from
      `commit-subject.txt`, never the issue title.
- [ ] `.github/workflows/ci.yml` running `bash tests/run.sh` on every PR —
      `tests/run.sh`'s own header already promises this exists.

**Commit:** `feat: composite action and reusable workflow`

---

### Task 12 — retire the old shape

- [ ] `scripts/` is empty; remove it.
- [ ] `README.md`: the six verbs, the config file, the operator's two setup
      steps. State the non-goals plainly — forge-agnosticism is refused, no
      apply, no review agent, no support promises. No code of conduct, issue
      templates, contributor guide, or marketplace listing (`HANDOFF.md` §3.9).
- [ ] Re-scan before anything is published: one value was already redacted
      during extraction (a Cloudflare account ID) and the origin repository is
      private.
- [ ] `docs/provenance/` stays as-is. Its header comments describe the origin
      pipeline in the present tense on purpose.

**Commit:** `docs: retire scripts/, document the CLI`

> **Checkpoint.** Publishing is the operator's call. Ask before `gh repo
> create` or any push (`HANDOFF.md` §7).

---

## Deferred, deliberately

- **The Bun strangler.** Bash lands first, then subcommands port one at a
  time behind this stable verb interface, each answering to the tests carried
  here. The agent invocation ports **last** (ADR-0002 D1). Reconsidered and
  reaffirmed in [ADR-0004](0004-the-strangler-reaffirmed.md), which adds one
  constraint this plan must honor: **no test may reach inside its subject.**
  The suite's 177 assertions all cross a process boundary today, which is what
  keeps the deferred port cheap. Tasks 2, 3 and 4 retarget the three tests that
  copy a script into a scratch repository — they are the closest the suite
  comes to knowing its subject is a shell script, and they must stay black-box.
- **`prove-guards`** — break each guard on purpose, assert it refuses. Travels
  from the origin repository as a follow-on deliverable.
- **A dedicated fixture repo.** A written decision, not an oversight: the
  origin repository is the integration environment until falconet outgrows
  "personal project."
- **Plan-on-PR, apply, deploy receipts, a review agent.** None in v1.

## What was wrong with the previous version of this file

Recorded so it is not reintroduced. It was written as greenfield TDD against a
repository that already had a working corpus: its Task 1 replaced the
committed test harness with a 13-line sketch that used `mktemp -t` (a file)
where every test needs a directory, replaced the counters with booleans,
dropped the `execution_log_*` helpers, and inverted `assert_contains`'
argument order — turning a green suite red on the first step. Its task
numbering contradicted itself (header "Tasks 1-5" over ten tasks; two
different tasks named as `lib/scan.sh`). Roughly a dozen of its literal code
blocks did not parse. And it specified fresh ~50-line reimplementations of
guards that exist as tested 200-400 line scripts whose comments are the
incident record — which ADR-0003 requires to survive whole.
