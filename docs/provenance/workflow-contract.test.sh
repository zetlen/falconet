#!/usr/bin/env bash
#
# Structural invariants of .github/workflows/ that a unit test of any single
# script cannot see. Mostly infra-issues.yml; the last section covers the
# Workspace credential in the one workflow that still needs it.
#
# Both bugs from run 32093607680 were wiring, not logic: the pieces were all
# correct and one of them was in the wrong place. These cases guard the wiring
# — a new hand-over path that forgets to name its branch, or a push that
# creeps back into the pull-request stage, fails here.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

WF="$REPO_ROOT/.github/workflows/infra-issues.yml"

# Every ci-park-issue.sh call, with its backslash continuations folded into
# one line, so a check can see the whole argument list. Anchored at the start
# of the command so the `[ -x ./scripts/ci-park-issue.sh ]` guard in the
# containment step is not counted as an invocation of it.
park_calls="$(awk '
  collecting {
    buf = buf " " $0
    if ($0 !~ /\\[ \t]*$/) { collecting = 0; print buf }
    next
  }
  /^[ \t]*\.\/scripts\/ci-park-issue\.sh/ {
    buf = $0
    if ($0 ~ /\\[ \t]*$/) { collecting = 1 } else { print buf }
  }
' "$WF")"

it "every hand-over path calls ci-park-issue.sh"
assert_eq 5 "$(printf '%s\n' "$park_calls" | grep -c 'ci-park-issue\.sh')" \
  "ci-park-issue.sh invocations"

it "every ci-park-issue.sh call passes --branch, so no comment can omit it"
missing="$(printf '%s\n' "$park_calls" | grep -v -- '--branch' || true)"
assert_eq "" "$missing" "calls without --branch"

it "every --branch argument is PUSHED_BRANCH, never the intended branch name"
# $BRANCH is what this run means to push; $PUSHED_BRANCH is what it actually
# got onto the remote. A comment may only ever cite the second.
assert_eq "" \
  "$(printf '%s\n' "$park_calls" | grep -o -- '--branch "[^"]*"' | grep -v 'PUSHED_BRANCH' || true)" \
  "--branch arguments"

it "nothing else in the workflow pushes"
# Command position only: `git push` written in a comment or an agent prompt
# is prose, not a push. The one real push goes through
# scripts/ci-push-branch.sh, which a case further down counts.
assert_eq 0 "$(grep -cE '^[[:space:]]*git push' "$WF")" "raw git push commands"

it "the pull-request stage no longer pushes anything"
open_stage="$(awk '/^      - name: Open the pull request/ { p = 1 }
                   p && /^      - name: / && !/Open the pull request/ { p = 0 }
                   p' "$WF")"
assert_not_contains "$open_stage" "git push" "the Open the pull request step"

it "the in-flight check keys on open pull requests, not on branches"
# A branch pushed by a run that then handed the issue to a human must not
# make the next run skip the issue. `gh pr list --state open` is what makes
# that true.
inflight="$(awk '/^      - name: Stage — stop if a PR for this issue/ { p = 1 }
                 p && /^      - name: / && !/stop if a PR/ { p = 0 }
                 p' "$WF")"
assert_contains "$inflight" "gh pr list --state open" "the in-flight step"

it "the terminal-state check keys on open pull requests too"
terminal="$(awk '/^      - name: Ensure a terminal state/ { p = 1 } p' "$WF")"
assert_contains "$terminal" "gh pr list --state open" "the terminal-state step"

it "neither check consults git for branches"
assert_not_contains "$inflight$terminal" "ls-remote" "the two PR checks"

# --- stage 3: the agent holds no shell ---------------------------------------

implement="$(awk '/^      - name: Implement the change/ { p = 1 }
                  p && /^      - name: / && !/Implement the change/ { p = 0 }
                  p' "$WF")"

it "the implementing agent is granted no Bash at all"
assert_not_contains "$implement" "Bash(" "the implement step's allowedTools"

it "the implementing agent's grant is exactly the five read/write file tools"
assert_contains "$implement" '--allowedTools "Read,Edit,Write,Grep,Glob"' \
  "the implement step"

it "the implementing agent is told where to put its commit message"
assert_contains "$implement" "commit-msg.txt" "the implement prompt"

it "the implementing agent's environment carries no push token"
# Neither agent holds Bash or gh, so nothing in either grant can spend
# GH_TOKEN — this takes it out of the process environment as well. Best
# effort, like the AWS_* blanks beside it: whether a `uses:` step's env:
# reaches a composite action's context is unconfirmed on a live run, and
# /proc/self/environ is an untested read path either way.
assert_contains "$implement" 'GH_TOKEN: ""' "the implement step env"

it "the workflow configures a commit identity before anything commits"
# Nothing set one before: the agent's commits worked only because
# claude-code-action configures git for its own. A script committing on a
# bare runner fails with "Please tell me who you are".
assert_eq 1 "$(grep -c 'git config user.email' "$WF")" "git identity settings"

it "the tree is asserted clean before the agent runs, so dirt is unambiguous"
# tofu init runs earlier and could in principle touch .terraform.lock.hcl.
# ci-commit-change.sh reads a dirty tree as "the agent changed something", so
# the tree has to be provably clean at the moment the agent starts.
#
# Scoped to the claim step, not scanned over the whole file: "the string
# exists somewhere in this YAML" is not the claim being made. The same check
# sitting AFTER the implementing agent would satisfy a whole-file scan and
# prove nothing at all about the tree the agent was handed.
# Single-quoted needle: double quotes would command-substitute it here.
claim="$(awk '/^      - name: Stage — claim the issue and open the working branch/ { p = 1 }
              p && /^      - name: / && !/claim the issue/ { p = 0 }
              p' "$WF")"
assert_contains "$claim" 'dirt=$(git status --porcelain)' \
  "the claim step"

it "the commit is made by the script, never by the agent"
assert_eq 1 "$(grep -c '\./scripts/ci-commit-change\.sh' "$WF")" \
  "ci-commit-change.sh invocations"

it "nothing in the workflow runs a raw git commit"
assert_eq 0 "$(grep -cE '^[[:space:]]*git commit' "$WF")" "raw git commit commands"

# --- the publish-boundary secret scan (issue #41) -----------------------------
#
# The scan lives inside ci-commit-change.sh, so the only thing the WORKFLOW
# owes it is a binary — but a missing binary is a hard failure there, which
# means an install step that quietly moved below the commit step would turn
# every run into a parked issue. Ordering is the invariant worth pinning.

install_step="$(awk '/^      - name: Install gitleaks/ { p = 1 }
                     p && /^      - name: / && !/Install gitleaks/ { p = 0 }
                     p' "$WF")"

it "the scanner is installed before the step that scans with it"
install_line="$(grep -n '^      - name: Install gitleaks' "$WF" | cut -d: -f1)"
commit_line="$(grep -n "^      - name: Commit the agent's change" "$WF" | cut -d: -f1)"
assert_eq "true" \
  "$([[ -n "$install_line" && -n "$commit_line" && "$install_line" -lt "$commit_line" ]] \
      && echo true || echo false)" \
  "install step precedes commit step"

it "the download is pinned to a version"
assert_contains "$install_step" "GITLEAKS_VERSION:" "the install step"

it "and checked against a hard-coded digest, because a tag is mutable"
assert_contains "$install_step" "sha256sum -c" "the install step"

it "the pinned binary is used, not the licensed action"
# gitleaks/gitleaks-action's licence distinguishes individual users from
# organisations; a binary sidesteps that question entirely. `uses:` position
# only — the install step's comment names the action it is avoiding, and
# prose is not a dependency.
assert_eq 0 "$(grep -cE '^[[:space:]]*uses: gitleaks/gitleaks-action' "$WF")" \
  "gitleaks-action uses"

# --- no agent repair loops ---------------------------------------------------

it "there is exactly one validation step"
assert_eq 1 "$(grep -c '^      - name: Validate' "$WF")" "Validate steps"

it "no step amends anything"
assert_eq 0 "$(grep -c '^      - name: Amend' "$WF")" "Amend steps"

it "there are exactly two agent invocations"
assert_eq 2 "$(grep -c 'uses: anthropics/claude-code-action@v1' "$WF")" \
  "claude-code-action invocations"

it "the branch is pushed exactly once, after the commit"
assert_eq 1 "$(grep -c '^ *run: \./scripts/ci-push-branch\.sh' "$WF")" \
  "ci-push-branch.sh invocations"

# --- stage 5: one review, no second chance -----------------------------------

it "the change is reviewed exactly once"
assert_eq 1 "$(grep -c '^      - name: Review the change' "$WF")" "Review steps"

it "there is no second verdict collection"
assert_eq 1 "$(grep -c '^      - name: Collect the review verdict' "$WF")" \
  "verdict collections"

it "a rejection parks the issue instead of re-invoking an agent"
reject="$(awk '/^      - name: Hand over — review did not approve/ { p = 1 }
               p && /^      - name: / && !/review did not approve/ { p = 0 }
               p' "$WF")"
assert_contains "$reject" "ci-park-issue.sh" "the rejection step"

it "a rejection labels the issue ready-for-human"
assert_contains "$reject" "ready-for-human" "the rejection step"

it "the reviewing agent still holds exactly Read, Grep and Glob"
review="$(awk '/^      - name: Review the change/ { p = 1 }
               p && /^      - name: / && !/Review the change/ { p = 0 }
               p' "$WF")"
assert_contains "$review" '--allowedTools "Read,Grep,Glob"' "the review step"

it "the reviewing agent is no longer asked to write the pull-request body"
assert_not_contains "$review" "used verbatim as" "the review prompt"

it "the reviewing agent's environment carries no push token either"
# This one had the sharper exposure: it holds Read over a workspace that used
# to have the token written into .git/config by the push two steps earlier.
# ci-push-branch.sh no longer writes it there; this is the other half.
assert_contains "$review" 'GH_TOKEN: ""' "the review step env"

# --- stage 6: the PR is the commit message ------------------------------------

it "the pull-request body comes from the commit message, not the reviewer"
assemble="$(awk '/^      - name: Assemble the PR body/ { p = 1 }
                 p && /^      - name: / && !/Assemble the PR body/ { p = 0 }
                 p' "$WF")"
assert_contains "$assemble" "commit-body.md" "the assemble step"

it "the assemble step no longer reads the reviewer's approval text"
assert_not_contains "$assemble" '--body "$HANDOFF/pr-body.md"' "the assemble step"

it "the pull-request title is the commit subject, so a squash merge keeps it"
# The ruleset allows squash merges only, and a squash takes the PR title as
# the subject on main. Titling the PR with the issue title would overwrite the
# implementer's subject with the requester's description of a problem.
open_stage_title="$(awk '/^      - name: Open the pull request/ { p = 1 }
                         p && /^      - name: / && !/Open the pull request/ { p = 0 }
                         p' "$WF")"
assert_contains "$open_stage_title" "commit-subject.txt" "the open step"

it "the issue title is no longer used as a pull-request title"
assert_eq 0 "$(grep -c 'pr-title\.txt' "$WF")" "pr-title.txt references"

# --- the Workspace credential is real now (issue #48) -------------------------
#
# The plan-only demo generated a garbage service-account key INSIDE the
# checkout, which was fine precisely because it authenticated nowhere. The key
# is a real delegated credential for a live tenant now, so two properties that
# used not to matter are the contract: it comes from a secret, and it never
# lands where a `git add -A`, a published diff or an agent's file tools can
# reach it.
#
# Only infra-issues.yml owes this now (#16). Before the split, deploy.yml's
# apply — even though it only ever targeted the Namecheap zone — still had to
# configure every provider block in the single root module, googleworkspace
# included, so it materialized this same key on every DNS-only deploy for a
# provider it never touched. dns/ is its own stack now, with its own provider
# set that names no Google credential at all, so deploy.yml has nothing left
# to materialize — see the "no Google Workspace credentials here any more"
# comment in deploy.yml itself.

DEPLOY="$REPO_ROOT/.github/workflows/deploy.yml"

it "no workflow generates a throwaway key any more"
assert_eq 0 "$(cat "$REPO_ROOT"/.github/workflows/*.yml | grep -c 'gws-dummy')" \
  "dummy-key references across .github/workflows"

it "deploy.yml no longer touches a Google credential — dns/ needs none"
assert_eq 0 "$(grep -cE 'GOOGLEWORKSPACE_(SA_KEY_JSON|CREDENTIALS|CUSTOMER_ID|IMPERSONATED_USER_EMAIL)|GOOGLE_CREDENTIALS' "$DEPLOY")" \
  "Google credential references in deploy.yml"

for wf in "$WF"; do
  name="$(basename "$wf")"
  body="$(cat "$wf")"
  # The materializing step, from its name to the next step at the same indent.
  materialize="$(awk '/^      - name: Materialize the Workspace service-account key/ { p = 1; print; next }
                      p && /^      - name: / { p = 0 }
                      p' "$wf")"

  it "$name materializes the key from a repo secret"
  assert_contains "$materialize" 'secrets.GOOGLEWORKSPACE_SA_KEY_JSON' \
    "$name's materialize step"

  it "$name writes the key under RUNNER_TEMP, outside the checkout"
  # github.workspace is the checkout, and it is what the old dummy key used.
  # A key written there is one `git add -A` from being committed and is on a
  # path the implementing agent's Write/Read tools can name.
  assert_contains "$materialize" '"$RUNNER_TEMP/gws-sa.json"' \
    "$name's materialize step"

  it "$name never puts the key back inside the checkout"
  assert_not_contains "$materialize" "github.workspace" \
    "$name's materialize step"

  it "$name never echoes the key's contents"
  # The ONE permitted mention of the JSON in a command is the redirect that
  # writes it. Anything else — an echo for debugging, a cat to prove the file
  # arrived — publishes a live credential into a run log, where GitHub's
  # line-oriented masking does not reliably cover a multi-line key.
  leaks="$(printf '%s\n' "$materialize" \
             | grep 'GOOGLEWORKSPACE_SA_KEY_JSON' \
             | grep -v 'secrets\.GOOGLEWORKSPACE_SA_KEY_JSON' \
             | grep -v '> "\$key"' || true)"
  assert_eq "" "$leaks" "$name's uses of the key JSON beyond the redirect"

  it "$name points both Google providers at the materialized file"
  # GOOGLE_CREDENTIALS too: the plan-only site stack still needs a parseable
  # key to configure its provider, and there is no second key to give it.
  assert_contains "$materialize" 'echo "GOOGLE_CREDENTIALS=$key"' \
    "$name's materialize step"

  it "$name fails loudly when the secret is unset"
  assert_contains "$materialize" 'test -s "$key"' "$name's materialize step"

  it "$name takes the customer id and impersonated user from secrets"
  assert_contains "$body" 'GOOGLEWORKSPACE_CUSTOMER_ID: ${{ secrets.GOOGLEWORKSPACE_CUSTOMER_ID }}' \
    "$name's job env"
done

# --- pull-request CI runs, and runs with nothing (#68) -----------------------
#
# The three commands in this job all existed and none of them ran anywhere.
# The cases below are less about ci.yml being present than about the shape it
# has to keep, because both of the ways it can go wrong are ways that look
# like progress.
#
# One: `pull_request_target` instead of `pull_request`. It is the trigger
# people reach for when a job "needs the token", and this job runs
# tests/run.sh out of the contributor's own branch. Under
# pull_request_target that is unreviewed code holding a write-scoped token.
#
# Two: reaching for `environment: sandbox` to make a credentialed step work.
# NAMECHEAP_* exist ONLY as `sandbox` environment secrets, so that is the
# move that makes a `tofu plan` step go green — and it hands a job that runs
# unreviewed pull-request code the same credentials the apply job holds. The
# deferred plan step in #68 is exactly the future change that will be
# tempted by it.

CI="$REPO_ROOT/.github/workflows/ci.yml"

it "pull-request CI exists at all"
assert_eq "true" "$([[ -f "$CI" ]] && echo true || echo false)" ".github/workflows/ci.yml"

ci_body="$(cat "$CI" 2>/dev/null || true)"

it "CI runs the formatter check that keeps review diffs about the change"
# -recursive, run from the repo root: that is what makes one command cover
# all three stacks (#16) even though root itself carries no .tf files.
assert_contains "$ci_body" "tofu fmt -check -recursive" "ci.yml"

it "CI validates all three stacks, not one root module"
# #16 split the single root module into dns/, workspace/ and site/, each its
# own root module with its own provider set — one `validate` no longer
# covers the repo, so this has to loop.
assert_contains "$ci_body" "for s in dns workspace site" "ci.yml"

it "CI initialises each stack without a backend, which is what makes it credential-free"
# A plain `tofu init` configures a stack's S3-compatible backend and needs
# AWS_* to reach it. Dropping -backend=false is how this job silently
# acquires a reason to want credentials.
assert_contains "$ci_body" "tofu -chdir=\$s init -backend=false" "ci.yml"

it "CI validates every stack it initialises"
assert_contains "$ci_body" "tofu -chdir=\$s validate" "ci.yml"

it "CI runs the test suite that nothing was running"
assert_contains "$ci_body" "bash tests/run.sh" "ci.yml"

it "CI asks for no write permission"
# The permissions block only — the comment above it uses the word "write"
# several times to explain what this job must never be able to do.
ci_permissions="$(awk '/^permissions:/ { p = 1; next } p && /^[^[:space:]]/ { p = 0 } p' "$CI")"
assert_not_contains "$ci_permissions" "write" "ci.yml's permissions block"

# --- no pull-request-triggered job holds infrastructure credentials ----------
#
# Stated over every workflow rather than over ci.yml, because the invariant is
# about the trigger and not about the file: any job started by a pull request
# runs code the repository has not reviewed yet.

for wf in "$REPO_ROOT"/.github/workflows/*.yml; do
  name="$(basename "$wf")"
  # The `on:` block: from the `on:` line to the next line at column 0.
  on_block="$(awk '/^on:/ { p = 1; next } p && /^[^[:space:]]/ { p = 0 } p' "$wf")"
  # A bare `pull_request:` trigger — `pull_request_target:` is a different
  # trigger with different rules and is checked by its own case below.
  grep -qE '^[[:space:]]+pull_request:[[:space:]]*$' <<<"$on_block" || continue

  body="$(cat "$wf")"

  it "$name is triggered by pull_request, never pull_request_target"
  assert_not_contains "$on_block" "pull_request_target" "$name's triggers"

  it "$name declares no environment, so no environment secret is released to it"
  assert_eq 0 "$(grep -cE '^[[:space:]]+environment:' "$wf")" "environment: declarations in $name"

  it "$name names no infrastructure secret"
  assert_eq "" "$(grep -o 'secrets\.\(NAMECHEAP\|AWS\|GOOGLEWORKSPACE\|TFSTATE\)[A-Z_]*' <<<"$body" || true)" \
    "infrastructure secrets named in $name"
done

it "labeler.yml, which does need the token, still pins its checkout to the base ref"
# The other side of the same rule. pull_request_target is correct there
# precisely because it never runs head code — `actions/checkout` with no
# `ref:` resolves to the base under that trigger, and adding one would be the
# bug.
labeler="$(cat "$REPO_ROOT/.github/workflows/labeler.yml")"
assert_not_contains "$labeler" "ref:" "labeler.yml's checkout"

# --- the `-target`/`moved` coupling is retired, not just passing (#67, #16) -
#
# This used to be trap 1: tofu refuses to plan when a `moved` block has an
# end outside the -target set, so a targeted apply and every `moved` block in
# the config were one coupled decision, and nothing said so until the deploy
# went red for three days after #66. Issue #16 removed `-target` from the
# apply entirely — the check that used to pin the coupling has nothing left
# to enforce, so per #16's own instruction ("delete it, or leave it as the
# vacuous pass — but do not leave a target list behind") it is deleted here
# rather than kept as a permanently-green branch nobody reads again.
#
# What replaced it is worth pinning instead: the apply now covers dns/ in
# full, with no target list to fall out of sync with anything.

# Scoped to the apply step's COMMAND, not to the whole file and not to the
# whole step block. Two different comment blocks in this workflow legitimately
# say `-target` while explaining why it is gone — the file header, and the
# readback step's note distinguishing its narrow `-exclude` from the `-target`
# that #16 deleted. A whole-file grep trips on the first; a step-block grep
# that runs to the next `- name:` swallows the second, because trailing
# comments sit between the two steps. So: drop comment lines and keep the
# command, which is the thing the assertion is actually about.
apply_step="$(awk '/^      - name: Apply dns\/ stack/ { p = 1 }
                   p && /^      - name: / && !/Apply dns\/ stack/ { p = 0 }
                   p' "$DEPLOY" | grep -v '^[[:space:]]*#')"

it "deploy.yml's apply step targets no resource — dns/ applies in full"
assert_not_contains "$apply_step" "-target" "deploy.yml's apply step"

it "deploy.yml applies the dns/ stack directly, not the repo root"
assert_contains "$apply_step" "tofu -chdir=dns apply" "deploy.yml's apply step"

it "there is no separate guard-plan step — the untargeted apply evaluates every precondition itself"
# The old targeted apply pruned terraform_data.guards out of the graph, so a
# guard plan had to run separately first. An untargeted apply evaluates every
# precondition on its own, so that step has nothing left to do.
assert_eq 0 "$(grep -ciE '^      - name: .*guard' "$DEPLOY")" "guard-named steps in deploy.yml"

# --- the receipt job reports on the infrastructure without reaching it -------
#
# Trap 3 in reverse. The apply job holds the Namecheap credentials, the
# read-write state pair and a delegated Workspace key, all released by
# `environment: sandbox`. The job that comments about what happened needs none
# of them, and giving it any would hand a second job the applying job's
# authority for no reason.

receipt_job="$(awk '/^  receipt:/ { p = 1 } p && /^  [a-z_-]+:/ && !/^  receipt:/ { p = 0 } p' "$DEPLOY")"

it "the receipt runs whatever the apply did — that is the entire point of it"
assert_contains "$receipt_job" "if: always()" "deploy.yml's receipt job"

it "the receipt job takes no environment, so no sandbox secret is released to it"
assert_not_contains "$receipt_job" "environment:" "deploy.yml's receipt job"

it "the receipt job reads no infrastructure secret"
assert_eq "" "$(printf '%s\n' "$receipt_job" | grep -o 'secrets\.\(NAMECHEAP\|AWS\|GOOGLEWORKSPACE\)[A-Z_]*' || true)" \
  "infrastructure secrets named in deploy.yml's receipt job"

it "the receipt job runs off the allowlisted runner, so it cannot reach the registry"
# The self-hosted runner's egress IP is the only one the Namecheap sandbox
# accepts. Running the receipt anywhere else is that guarantee, physically.
assert_contains "$receipt_job" "runs-on: ubuntu-latest" "deploy.yml's receipt job"

it "both agent steps blank the credential paths alongside the other secrets"
# Best-effort, like the AWS_* blanks beside them — blanking a path does not
# unlink the file, which is why the file lives outside the checkout in the
# first place. Two agent steps, two blanks each.
assert_eq 2 "$(grep -c 'GOOGLEWORKSPACE_CREDENTIALS: ""' "$WF")" \
  "blanked GOOGLEWORKSPACE_CREDENTIALS in agent steps"

summary
