#!/usr/bin/env bash
#
# ci-push-branch.sh — get the working branch onto the remote the moment there
# is a commit on it, so no terminal path in the pipeline can throw the work
# away.
#
# ---------------------------------------------------------------------------
# The incident
# ---------------------------------------------------------------------------
# Until run 32093607680 (issue #36), the only `git push` in
# .github/workflows/infra-issues.yml lived inside the `Open the pull request`
# stage, behind `REVIEW == 'approved'`. Every other way out of the pipeline —
# validation failed twice, the post-review amend broke validation, the review
# did not approve — left the implementing agent's commit on the runner's disk
# and nowhere else, and the runner is destroyed minutes later. That run parked
# issue #36 with:
#
#     I prepared this change, but the automated review stage did not return a
#     usable verdict, so I have not opened a pull request. This one needs a
#     person.
#
# `git ls-remote --heads origin 'issue-36*'` returned nothing. The comment
# handed a human a pointer to work that no longer existed anywhere. A promise
# of a prepared change with no prepared change behind it is worse than
# silence, because a person acts on it.
#
# So this script runs the moment there is a commit to push — directly after
# scripts/ci-commit-change.sh, before validation, before the review, and
# before any of the branches that decide what to do with the change. There is
# no second push and nothing to amend: the repair loops are gone, and each run
# makes exactly one commit and pushes it once. Pushing is unconditional on the
# verdict: the remote is where work lives, and a branch with no pull request
# costs nothing (the in-flight check in stage 1 and the terminal-state check
# at the bottom of the workflow both key on OPEN PULL REQUESTS, not on
# branches, so an abandoned branch never suppresses a later run).
#
# ---------------------------------------------------------------------------
# Why --force-with-lease
# ---------------------------------------------------------------------------
# Not for an amend. Nothing in this pipeline rewrites history any more: no
# agent holds git at all, and ci-commit-change.sh appends one commit and
# stops. The flag is here for the one thing this push cannot see — a branch of
# this name that was already on the remote before the run started, and was
# never fetched.
#
# --force-with-lease says yes to exactly the pushes that are ours: creating
# the branch, fast-forwarding it, and replacing a tip we ourselves put there
# (git remembers it as the remote-tracking ref). It says no to a tip that
# arrived from anywhere else, and it says no to a branch we hold no lease on
# at all — that one is refused as "stale info" rather than clobbered. The
# claim stage already renames the branch when `git ls-remote` finds a
# collision, so this should be unreachable; this is what happens when it
# becomes reachable anyway, and a refused push naming a lease we do not hold
# is the right answer there — better than `--force`, which would silently
# overwrite whatever that other run left, and better than a plain push, whose
# refusal depends on the geometry of two histories nobody in this job has
# looked at.
#
# Modes:
#   ci-push-branch.sh --branch NAME [--base-sha SHA]
#
#     --branch    the branch to push; it must be the checked-out one
#     --base-sha  the commit the run started from. If HEAD still equals it,
#                 nothing was committed and there is nothing to push — the
#                 needs-info path, where the agent asked a question instead of
#                 making a change, and which must not fail because of it.
#
# ---------------------------------------------------------------------------
# The credential, and where it must not end up
# ---------------------------------------------------------------------------
# The remote URL is rewritten first, and rewritten TOKENLESS.
#
# It has to be rewritten at all because claude-code-action unsets the
# credential actions/checkout left in .git/config and points `origin` at its
# own GitHub App token, which it revokes when its step ends; every push in
# this pipeline now happens directly after an agent step, so every one of them
# meets a dead token unless it fixes the remote first. It has to be rewritten
# WITHOUT a credential in it because a credential embedded in a remote URL
# takes precedence over any credential helper: leave the revoked one in the
# URL and the helper below is never consulted, and the push meets that dead
# token anyway.
#
# The token reaches git through a one-shot credential helper passed with `-c`
# on the command line, and so lands in neither of the two places the old
# `https://x-access-token:$GH_TOKEN@...` URL put it (issue #41):
#
#   - NOT in .git/config, where it used to sit for the rest of the job. That
#     is the part that mattered: Validate runs `tofu plan` over .tf files an
#     agent just wrote, and Review then hands a second agent Read over the
#     same workspace. A `file("${path.module}/.git/config")` in agent-authored
#     HCL, or a plain Read by the reviewer, found the token sitting there.
#   - NOT in argv, because the helper string is SINGLE-quoted: `$GH_TOKEN` is
#     expanded by the shell git runs for the helper, not by this script, so
#     the value never appears in this process's command line, where
#     /proc/<pid>/cmdline would show it to anything running on the runner.
#
# The empty `-c credential.helper=` in front clears any helper the environment
# already configured, so ours is the only one asked.
#
# Be exact about what that leaves open, because "the token is unreachable" is
# a stronger claim than the truth. GH_TOKEN is still in the job environment —
# the scripted steps genuinely need it. Both agent steps blank it in their own
# `env:` blocks, which is a best-effort tightening rather than a closed door
# (see the comment there), and /proc/self/environ remains an untested read
# path for an agent whose Read tool may accept absolute paths outside the
# workspace.
#
# Requires GH_TOKEN, GITHUB_SERVER_URL and GITHUB_REPOSITORY for all of that;
# with GH_TOKEN unset the remote is left exactly as it is and the push is made
# with no helper at all, which is what a local run wants.
#
# On a successful push, PUSHED_BRANCH=<name> is appended to $GITHUB_ENV when
# that variable is set. The hand-over steps in the workflow name their branch
# from THAT and never from their own $BRANCH, so a comment can only ever link
# a branch a push actually landed.
#
# Exit codes: 0 = the branch is on the remote (or there was nothing to push),
#             1 = the push failed, 2 = usage error.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
BRANCH=""
BASE_SHA=""

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --branch)   BRANCH="${2:?--branch needs a name}"; shift 2 ;;
    --base-sha) BASE_SHA="${2:?--base-sha needs a commit sha}"; shift 2 ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "$BRANCH" ]] || { usage >&2; exit 2; }

if [[ -n "$BASE_SHA" ]]; then
  head_sha="$(git -C "$REPO_ROOT" rev-parse HEAD)"
  if [[ "$head_sha" == "$BASE_SHA" ]]; then
    echo "no commit on $BRANCH yet (HEAD is still ${BASE_SHA:0:7}) — nothing to push"
    exit 0
  fi
fi

# Restore a working credential without writing one down. See "The credential,
# and where it must not end up" above: the URL is set TOKENLESS so that the
# helper is consulted at all, and the helper is built here so that git — not
# this script — expands $GH_TOKEN.
GIT_AUTH=()
if [[ -n "${GH_TOKEN:-}" && -n "${GITHUB_REPOSITORY:-}" ]]; then
  server="${GITHUB_SERVER_URL:-https://github.com}"
  git -C "$REPO_ROOT" remote set-url origin \
    "${server}/${GITHUB_REPOSITORY}.git" \
    || { echo "could not rewrite the origin URL" >&2; exit 1; }
  # shellcheck disable=SC2016  # $GH_TOKEN must NOT expand here. It is expanded
  # by the shell git runs for the helper, which is the whole point: expanded
  # now, the token would be in this process's argv.
  GIT_AUTH=(-c credential.helper=
            -c 'credential.helper=!f(){ echo username=x-access-token; echo "password=$GH_TOKEN"; };f')
else
  echo "GH_TOKEN or GITHUB_REPOSITORY unset — pushing with the remote as configured"
fi

# The push, written twice, because macOS ships bash 3.2, where expanding an
# EMPTY array under `set -u` is an "unbound variable" error rather than
# nothing — and GIT_AUTH is legitimately empty on a local run, which is the
# path most of tests/ci-push-branch.test.sh takes.
push_branch() {
  if [[ "${#GIT_AUTH[@]}" -gt 0 ]]; then
    git -C "$REPO_ROOT" "${GIT_AUTH[@]}" \
      push --force-with-lease --set-upstream origin "$BRANCH"
  else
    git -C "$REPO_ROOT" push --force-with-lease --set-upstream origin "$BRANCH"
  fi
}

if push_branch; then
  echo "pushed $BRANCH ($(git -C "$REPO_ROOT" rev-parse --short HEAD))"
  # The hand-over comments read this, and they read it INSTEAD of $BRANCH:
  # written here and only here, it is a statement that the branch is on the
  # remote right now, not that the workflow intended to put it there. Every
  # `--branch` argument in infra-issues.yml is this variable.
  [[ -z "${GITHUB_ENV:-}" ]] || echo "PUSHED_BRANCH=$BRANCH" >>"$GITHUB_ENV"
  exit 0
fi

# Loud, and fatal to the step that called it. A run that cannot put its work
# on the remote has nothing to hand anybody, and the workflow's terminal-state
# check will park the issue with this log attached.
echo "::error::could not push $BRANCH — the work for this run exists only on this runner" >&2
exit 1
