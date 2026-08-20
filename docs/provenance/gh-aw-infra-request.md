---
# The gh-aw replacement for infra-issues.yml — see
# docs/adr/0001-replace-the-issue-pipeline-with-gh-aw.md. During Phase 1/2
# both pipelines coexist: this one fires on the same infra-request label, but
# unlike infra-issues.yml it does NOT exclude do-not-apply, so a spike issue
# labeled do-not-apply + infra-request exercises this workflow alone.
description: "Work an infra-request issue end to end: implement the OpenTofu change, validate and plan it, and open a pull request for human review."

on:
  issues:
    types: [labeled]
    names: [infra-request]

# One group per issue: dedupes double-fires for the same issue without
# queueing distinct issues behind each other (D4 in the ADR).
concurrency:
  group: infra-issue-${{ github.event.issue.number }}
  cancel-in-progress: false

# Read-only for the agent job; every write goes through safe-outputs (ADR:
# "the read-only-agent split, maintained by someone else").
permissions:
  contents: read
  issues: read

# D2 — hosted runner. The plan needs only the R2 state backend and the
# provider registry; the self-hosted runner stays deploy.yml's alone.
runs-on: ubuntu-latest
timeout-minutes: 30
max-turns: 30

# D5 — Claude engine with an Anthropic API key (repository secret).
engine: claude
model: claude-opus-5

# D3 — firewall assumed to compose on a hosted runner. The two custom
# domains serve the post-step's tofu init/plan: the R2 state backend and
# the OpenTofu provider registry.
network:
  allowed:
    - defaults
    - "<account-id>.r2.cloudflarestorage.com"   # redacted on extraction
    - "registry.opentofu.org"

# issues toolset only: the agent reads the comment thread with it. The
# change itself comes from the checkout, and PRs are safe-outputs' job.
tools:
  github:
    toolsets: [context, issues]
  edit:

# Runs after checkout, before the agent: pin the commit the run started
# from. Every post-step below measures the agent's work against this SHA.
steps:
  - name: Record the base commit
    run: echo "BASE_SHA=$(git rev-parse HEAD)" >> "$GITHUB_ENV"

# The deterministic gate, after the agent and before safe-outputs. D1's
# carve-out lives here: the content denylist is not a path rule and stays
# fail-closed, exactly as scripts/ci-commit-change.sh enforced it.
post-steps:
  - name: Content denylist — agent-authored tf must not execute or read anything at plan time
    run: |
      set -uo pipefail
      if [ "$(git rev-parse HEAD)" = "$BASE_SHA" ]; then
        echo "no commits on top of $BASE_SHA — nothing to check"; exit 0
      fi
      denied=0
      while IFS= read -r f; do
        case "$f" in *.tf) [ -f "$f" ] || continue ;; *) continue ;; esac
        hit=""
        grep -Eq 'data[[:space:]]*"[[:space:]]*external[[:space:]]*"' "$f" && hit='data "external"'
        [ -z "$hit" ] && grep -qF 'provisioner'  "$f" && hit='provisioner'
        [ -z "$hit" ] && grep -qF 'local-exec'   "$f" && hit='local-exec'
        [ -z "$hit" ] && grep -qF 'remote-exec'  "$f" && hit='remote-exec'
        [ -z "$hit" ] && grep -Eq 'templatefile[[:space:]]*\(' "$f" && hit='templatefile()'
        [ -z "$hit" ] && grep -Eq 'filebase64[[:space:]]*\('  "$f" && hit='filebase64()'
        [ -z "$hit" ] && grep -Eq 'file[[:space:]]*\('        "$f" && hit='file()'
        if [ -n "$hit" ]; then echo "::error file=$f::content denylist: $hit"; denied=1; fi
      done < <(git diff --name-only "$BASE_SHA" HEAD)
      exit "$denied"

  - name: Set up OpenTofu
    uses: opentofu/setup-opentofu@v1
    with:
      tofu_wrapper: false

  - name: Validate and plan
    env:
      # Bucket-scoped READ-ONLY state credential; -refresh=false -lock=false
      # inside ci-validate.sh is what makes read-only sufficient.
      AWS_ACCESS_KEY_ID: ${{ secrets.AWS_ACCESS_KEY_ID }}
      AWS_SECRET_ACCESS_KEY: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
      # Placeholders by design: the plan never calls the Namecheap API, but
      # the provider block still wants non-empty configuration.
      NAMECHEAP_USER_NAME: ci-plan-only
      NAMECHEAP_API_USER: ci-plan-only
      NAMECHEAP_API_KEY: ci-plan-only-never-used
      NAMECHEAP_USE_SANDBOX: "true"
    run: |
      set -uo pipefail
      if [ "$(git rev-parse HEAD)" = "$BASE_SHA" ]; then
        echo "no commits on top of $BASE_SHA — nothing to validate or plan"; exit 0
      fi
      ./scripts/ci-validate.sh --base "$BASE_SHA"

  - name: Upload the plan artifact
    if: always()
    uses: actions/upload-artifact@v7
    with:
      name: tofu-plan
      path: |
        .ci-handoff/plan.txt
        .ci-handoff/validation-failure.txt
      if-no-files-found: ignore

safe-outputs:
  create-pull-request:
    labels: [needs-plan-review]
    # Phase 1 gap 1: gh-aw's default is draft — a policy, not agent choice.
    # The old pipeline opened ready-for-review PRs; keep that contract.
    draft: false
    # Phase 1 gap 2: PRs made with GITHUB_TOKEN trigger no workflows, so
    # ci.yml never ran on spike PR #99. This PAT (fine-grained, this repo
    # only, Contents: Read & Write and nothing else) pushes one empty
    # commit after PR creation, whose push fires pull_request events
    # normally. The PR itself stays authored by github-actions[bot].
    github-token-for-extra-empty-commit: ${{ secrets.GH_AW_CI_TRIGGER_TOKEN }}
    # D1 — the path policy inverts to a denylist and fails open; the human
    # apply gate (deploy.yml, plan-approved) is the mitigation.
    excluded-files: ["scripts/**", ".github/**", "tests/**", "docs/**"]
    protected-files: fallback-to-issue
  add-comment:
  add-labels:
    allowed: [needs-info, ready-for-human]
  noop:
---

# Work an infra-request issue

You are the agent for this infrastructure repository's request queue,
configured and authorized by the repository owner. You work exactly one
issue: #${{ github.event.issue.number }}. The sanitized text of that issue
is here:

"${{ steps.sanitized.outputs.text }}"

If the issue has comments, read them with the GitHub tools — this run may
be a reply to questions a previous run asked, so read the newest comments
and continue with the new information.

## Standing facts — already true, do not spend a tool call re-checking

- The repository is checked out clean at the tip of the default branch.
- All DNS work targets the Namecheap SANDBOX; this repository has no
  production registrar access.
- The Google Workspace config targets a REAL scratch tenant. Google has no
  sandbox, so an apply there edits a live directory — which is a human's
  decision, never yours.
- The Google Cloud static-site config (site/site-papernapkin-tech.tf) is
  plan-only against a project that does not exist.
- Nothing here is ever applied by an agent. After you finish, CI runs
  `tofu validate` and `tofu plan` and attaches the plan; a human reviews
  that plan and decides whether to apply it. Do not run tofu yourself.

## Repository rules

AGENTS.md and the README bind you. In particular:

- A DNS record lives in exactly ONE place — the `locals` list in its
  dns/records-*.tf file — and everything else that needs the record list
  reads it from there.
- Never weaken, delete or route around a guard in guards*.tf (dns/ or
  site/); mail-affecting DNS mistakes fail silently, which is why those
  guards exist.
- You may edit `.tf` files. Nothing else — not workflows, not scripts, not
  AGENTS.md, not docs. Commits touching other paths are stripped or
  refused downstream, and the request goes to a human.
- Never write `data "external"`, a `provisioner` block, `local-exec`,
  `remote-exec`, `file()`, `templatefile()` or `filebase64()` in a `.tf`
  file: those execute code or read the runner during `tofu plan`, and this
  pipeline never runs code an agent wrote. A change containing them is
  refused after you finish. If the request seems to require any of that,
  it is not a request you can work.

## Do exactly one of these three things

### (A) WORKABLE — the request maps onto resources this configuration manages today

Make the edit, commit it with git, and call the `create-pull-request`
tool.

The commit message and the pull-request description are the only account
of this change that outlives the run, and the person reading them is
deciding whether to apply the plan. First line: a one-line summary written
the way a commit subject is written. Then two or three plain-language
sentences on what changes and why. Do NOT paste, quote, summarize or
abridge any tofu output: CI attaches the complete plan separately.

Commit even if you are not certain the plan will be clean — validation
runs after you, and a failed validation goes to a human with your branch
intact.

### (B) AMBIGUOUS — you genuinely cannot tell WHAT is being asked for

Edit no files and commit nothing. Call `add-comment` with your questions,
addressed to the requester in plain language: no jargon, no terraform
vocabulary, one question per bullet, and say why you need each answer.
Then call `add-labels` with `needs-info`.

Choose (B) only for real ambiguity about WHAT is wanted. "I am not sure
this is the tidiest implementation" is not ambiguity: make the change and
let review judge. Parking an under-specified request is one of the most
valuable things this system does — reach for it rather than guessing at
what someone meant.

### (C) NOTHING TO DO — the request is already satisfied or asks for nothing

Call `noop` with a one-sentence explanation. Never end the run without
calling at least one safe-output tool.
