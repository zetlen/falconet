#!/usr/bin/env bash
#
# contract.test.sh — structural invariants of the wrappers that a unit test of
# any single verb cannot see.
#
# Both bugs from run 32093607680 were WIRING, not logic: every piece was
# correct and one of them was in the wrong place. These cases guard the
# wiring. A new hand-over path that forgets to name its branch, a push that
# creeps back behind a condition, a tool grant that quietly widens — each
# fails here, where a unit test would see nothing wrong.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

WF="$REPO_ROOT/.github/workflows/falconet.yml"
ACTION="$REPO_ROOT/action.yml"

wf="$(cat "$WF")"
action="$(cat "$ACTION")"

# Every park invocation, with backslash and YAML folded continuations pulled
# into one line, so a check can see the whole argument list.
park_calls="$(awk '
  /verb: park/ { inpark = 1; buf = ""; next }
  inpark {
    buf = buf " " $0
    if ($0 ~ /^[ \t]*- name:/ || $0 ~ /^[ \t]*- uses:/) { print buf; inpark = 0 }
  }
  END { if (inpark) print buf }
' "$WF")"

# --- the agent holds nothing it could publish with -------------------------

it "the agent's tool grant is exactly the five file tools"
assert_contains "$wf" '--allowedTools "Read,Edit,Write,Grep,Glob"' "workflow"

it "and names no Bash tool, which is the whole boundary"
assert_not_contains "$wf" 'Bash(' "workflow"

it "the agent job holds no permissions at all"
assert_contains "$wf" "permissions: {}" "workflow"

it "and the workflow's default is the same, so a new job must opt in"
# Comment lines excluded: the header explains the rule and would be counted.
assert_eq 2 "$(grep -v '^[[:space:]]*#' "$WF" | grep -c 'permissions: {}')" \
  "permissions: {} declarations"

it "the agent job checks out without persisting credentials"
assert_contains "$wf" "persist-credentials: false" "workflow"

it "and no step in the agent job is handed a token"
implement_job="$(awk '/^  implement:/{f=1} /^  publish:/{f=0} f' "$WF")"
assert_not_contains "$implement_job" "steps.token.outputs.token" "implement job"

# --- exactly one of each thing that must happen once -----------------------

it "there is exactly one validate step"
assert_eq 1 "$(grep -c 'verb: validate' "$WF")" "validate steps"

it "the branch is pushed exactly once"
assert_eq 1 "$(grep -c 'verb: push' "$WF")" "push steps"

it "and only by the push verb — nothing else runs git push"
assert_not_contains "$wf" "git push" "workflow"

it "there is no repair loop: commit happens once"
assert_eq 1 "$(grep -c 'verb: commit' "$WF")" "commit steps"

# --- the push is unconditional and comes first -----------------------------
#
# The one guard run 32093607680 bought. Every other exit used to leave the
# work on a runner that was destroyed minutes later.

it "the push step carries no condition"
push_step="$(awk '/name: Push/{f=1} f && /verb: push/{print; exit} f' "$WF")"
assert_not_contains "$push_step" "if:" "push step"

it "and nothing that publishes runs before it"
push_line="$(grep -n 'verb: push' "$WF" | cut -d: -f1)"
pr_line="$(grep -n 'gh pr create' "$WF" | cut -d: -f1)"
[[ "$push_line" -lt "$pr_line" ]] \
  && assert_eq "before" "before" "push at $push_line, pr create at $pr_line" \
  || assert_eq "push before pr create" "push=$push_line pr=$pr_line" "order"

it "the work is bundled before the agent job ends, so it outlives the runner"
assert_contains "$wf" "git bundle create" "workflow"

# --- every hand-over names its branch --------------------------------------

it "every park call passes --branch"
missing="$(printf '%s\n' "$park_calls" | grep -c -- '--branch' || true)"
total="$(grep -c 'verb: park' "$WF")"
assert_eq "$total" "$missing" "park calls passing --branch"

it "and reads PUSHED_BRANCH rather than the branch prepare intended"
assert_contains "$wf" 'env.PUSHED_BRANCH' "workflow"

it "there is a containment job that runs whatever happened"
assert_contains "$wf" "if: always() && needs.gate.outputs.outcome == 'ready'" "workflow"

# --- the review protocol stays unwired -------------------------------------

it "the workflow names review-verdict zero times"
assert_not_contains "$wf" "review-verdict" "workflow"

it "and there is no second agent"
assert_eq 1 "$(grep -c 'claude-code-action' "$WF")" "agent invocations"

# --- the pull request describes the change, not the request ----------------

it "the PR title comes from the commit subject the agent wrote"
assert_contains "$wf" 'cat .falconet/commit-subject.txt' "workflow"

it "and never from the issue title"
assert_not_contains "$wf" "github.event.issue.title" "workflow"

it "the whole plan goes in the body, assembled rather than quoted"
assert_contains "$wf" "verb: assemble" "workflow"

# --- pinned binaries, installed before anything depends on them ------------

it "gitleaks is pinned by version"
assert_contains "$action" "gitleaks-version" "action"

it "and by digest, because a tag is a mutable pointer"
assert_contains "$action" "sha256sum -c -" "action"

it "and proves it runs before anything depends on it"
assert_contains "$action" "gitleaks version" "action"

it "the digest is checked before the tarball is unpacked"
sha_line="$(grep -n 'sha256sum -c -' "$ACTION" | cut -d: -f1)"
tar_line="$(grep -n 'tar -xzf' "$ACTION" | cut -d: -f1)"
[[ "$sha_line" -lt "$tar_line" ]] \
  && assert_eq "before" "before" "sha at $sha_line, tar at $tar_line" \
  || assert_eq "sha before tar" "sha=$sha_line tar=$tar_line" "order"

# `setup: false` says "an earlier step in THIS job already installed them",
# and a job is a fresh runner, so the claim is about the job and never about
# the workflow. A step that needs the binaries and is the first falconet step
# in its job has nothing behind that claim, and the secret scan fails closed,
# so the run dies on a missing scanner rather than on anything real.
#
# Dependency-shaped rather than positional: push, park and assemble need none
# of the pinned binaries and are free to skip the install.
it "no verb that needs the pinned binaries runs before an install in its job"
unmet="$(awk '
  function flush(   verb, installs) {
    if (buf ~ /uses: \.\/\.falconet-tool/) {
      verb = buf; sub(/.*verb: /, "", verb); sub(/[^a-z].*/, "", verb)
      installs = (buf !~ /setup: .false./)
      if (verb ~ /^(prepare|commit|validate)$/ && !installed[job] && !installs)
        print job "/" verb
      if (installs) installed[job] = 1
    }
    buf = ""
  }
  /^  [a-z][a-z-]*:$/ { flush(); job = $1; sub(/:$/, "", job) }
  /^      - / { flush() }
  { buf = buf " " $0 }
  END { flush() }
' "$WF")"
assert_eq "" "$unmet" "steps needing an install that never got one"

# --- the planning credentials ----------------------------------------------
#
# One secret, loaded in the two jobs that run tofu and in neither of the
# others. Where it is loaded is the invariant; what is in it is the
# consumer's business.

it "the jobs that run tofu load the planning credentials"
assert_eq 2 "$(grep -c 'name: Credentials for the stacks that plan' "$WF")" \
  "credential-loading steps"

it "and the agent's job is not one of them, which is the whole boundary"
assert_not_contains "$implement_job" "plan-env" "implement job"

it "the secret is optional, because a repository may need none"
plan_env_decl="$(awk '/^      plan-env:/{f=1} f && /required:/{print; exit}' "$WF")"
assert_contains "$plan_env_decl" "required: false" "plan-env declaration"

it "the value travels by environment, never by template expression"
assert_eq 2 "$(grep -c 'FALCONET_PLAN_ENV: ${{ secrets.plan-env }}' "$WF")" \
  "env-passed references"

it "and every line of it is masked before it can reach a log"
assert_contains "$wf" "::add-mask::" "workflow"

# --- attacker-controlled text never reaches a shell ------------------------

it "the action passes the verb through the environment, not a template"
assert_contains "$action" 'FALCONET_VERB: ${{ inputs.verb }}' "action"

it "and its arguments the same way"
assert_contains "$action" 'FALCONET_ARGS: ${{ inputs.args }}' "action"

it "no run block interpolates an issue body"
assert_not_contains "$wf" "github.event.issue.body" "workflow"

# --- the credential is an App, not a PAT and not a workaround --------------

it "tokens are minted per job by the App"
assert_contains "$wf" "actions/create-github-app-token" "workflow"

it "and the empty-commit workaround is not ported"
assert_not_contains "$wf" "allow-empty" "workflow"

summary
