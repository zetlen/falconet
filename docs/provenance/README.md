# Provenance

Nothing in this directory runs. These are the artifacts falconet is being
extracted *from*, kept because the design lives in them and would otherwise
have to be reconstructed from memory.

They come from `wayfinders-infra`, a private OpenTofu repository, at commit
`97b5669` — deliberately the last commit where the pipeline was **live and
coherent**, not the later commit that retired it. Header comments here
therefore describe a working system in the present tense. That is the system
falconet is becoming.

| File | What it is | Why it matters |
| --- | --- | --- |
| `infra-issues.yml` | The retired orchestrator: 1,307 lines of GitHub Actions YAML | **The most important file here.** ~270 lines of it are prompt text, and the rest is stage sequencing, per-stage tool grants, and credential blanking. The scripts are limbs; this is the spine. |
| `work-infra-issues-SKILL.md` | The same job, written as instructions for an agent in an interactive session | The human-readable statement of the procedure, and the reason a CLI-first design pays off: one code path can serve both. |
| `gh-aw-infra-request.md` | The off-the-shelf alternative, as actually configured and run | What was measured and rejected, in its final working form. Read it before re-proposing anything like it. |
| `workflow-contract.test.sh` | Structural tests over the orchestrator's wiring | Encodes invariants a unit test cannot see: the agent holds no shell, nothing else pushes, there is exactly one validation step and no repair loop. These become falconet's own contract tests. |

## Reading `infra-issues.yml`

The stage numbering is the workflow's own; stage 3 is skipped on purpose
(validate and amend were collapsed into one gate).

1. **Stage** — in-flight check, claim the issue, open the branch, baseline plan
2. **Implement** — agent #1, granted exactly `Read,Edit,Write,Grep,Glob`; no
   Bash, no push token; writes its commit message to a file and stops
4. **Commit** — guards, push, route the outcome, validate
5. **Review** — agent #2, granted exactly `Read,Grep,Glob`; emits a sentinel
   verdict line because it cannot write
6. **Reject** — any non-approval parks the issue
7. **Assemble** — plan artifact and PR body
8. **Open** — create the PR, label it, comment on the issue
9. **Ensure a terminal state** — the backstop that makes "silently nothing"
   impossible

Stage 5 is **dropped** in falconet v1 (see ADR-0002): the second cold context
cost more than it caught. `scripts/ci-review-verdict.sh` ships unwired as the
reference implementation of its protocol.

## Redactions

One value was redacted during extraction: the Cloudflare account ID in
`gh-aw-infra-request.md`'s network allowlist, now `<account-id>`. Nothing else
was altered — no credentials were present, and the only e-mail addresses in
the corpus are `example.invalid` test fixtures.
