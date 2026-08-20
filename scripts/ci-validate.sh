#!/usr/bin/env bash
#
# ci-validate.sh — the deterministic gate between the implementing agent and
# the reviewing agent in .github/workflows/infra-issues.yml.
#
# Everything an agent used to prove by hand — did I commit? does it parse?
# what does the plan say? — happens here, once, in one shell, and lands in
# files. Agents run no tofu in CI.
#
# Modes:
#   ci-validate.sh --base <sha> [--out-dir DIR]
#       Validate HEAD against the commit the run started from:
#         1. assert at least one commit exists on top of <sha>
#         2. assert the commit does not touch .ci-handoff/ — CI's own scratch
#            has no business inside a change (see below)
#         3. snapshot the commits to DIR/diff.patch and the changed paths to
#            DIR/changed-files.txt — the review agent is granted no Bash, so
#            its evidence has to be on disk before it starts
#         4. tofu validate, once per stack (dns/, workspace/, site/ — #16
#            split the single root module into three)
#         5. tofu -chdir=dns plan -no-color -refresh=false -lock=false
#            > DIR/plan.txt — dns/ only; see the comment on step 5 below for
#            why the other two stacks are not planned here.
#
# Nothing checks a record registry between 4 and 5 any more. That step
# cross-checked the dns/records-*.tf locals lists against
# scripts/record-manifest.txt, a hand-copied mirror of the record list that
# existed in order to be checked; #17 deleted both. The live-DNS verification
# the same script did is now the check blocks in dns/checks-live-dns.tf, which
# are inert unless a run names a zone to verify — so this script never
# triggers them and CI never queries public DNS.
#
# diff.patch is `git log -p`, not `git diff`: the reviewing agent gets each
# commit MESSAGE with the change it describes. The message is the implementing
# agent's claim about what it did, and checking a change against its own claim
# — and both against the request — is a real part of reviewing it. It is also
# the one thing that agent says which outlives the run, and it is a public
# artifact of exactly the kind humans review everywhere. Its reasoning
# transcript is not, and the reviewer never sees that.
#
# -no-color is not cosmetic: without it tofu writes ANSI escapes into the
# file and whoever reads it next has to strip them. A run on issue #33 spent
# two of its nineteen shell calls on `sed -r 's/\x1b\[[0-9;]*m//g'`.
#
# -refresh=false -lock=false is mandatory in CI: the job holds a
# bucket-scoped READ-ONLY state credential, so taking a lock is refused, and
# it must never call the Namecheap API.
#
# There is deliberately NO `tofu fmt -check` here, but no longer for the
# reason issue #20 gave — that `tofu fmt` already failed on main. #20 is
# closed and the tree is clean. .github/workflows/ci.yml now runs
# `tofu fmt -check -recursive` as its first step on every pull request, so
# repeating it here would report the same thing twice to the same reader.
#
# ONE PLAN, NOT THREE (#16). Of the three stacks, only dns/ is ever applied
# by this pipeline: deploy.yml runs an untargeted `tofu -chdir=dns apply`
# and nothing else applies anything — workspace/ is applied by hand against
# the real Google Workspace tenant, and site/ is plan-only forever, against
# a GCP project that does not exist. A human approves THIS script's plan and
# that approval becomes exactly one apply. Planning workspace/ or site/
# here would show a reviewer a diff their approval cannot act on, which is
# the dishonest option; showing them only the plan their label will trigger
# is the truthful one. (workspace/ and site/ still get `tofu validate` in
# step 4 below, so a broken stack is still caught — just not planned.)
#
# Outputs, written into DIR (default: .ci-handoff/ at the root of this
# repository, which is where the CI pipeline hands files between its stages
# and is listed in .gitignore):
#   diff.patch              base..HEAD as `git log -p`, oldest commit first
#   changed-files.txt       one changed path per line
#   plan.txt                full plan output — written only on success
#   validation-failure.txt  human-readable summary — written only on failure
#
# Exit codes: 0 = every check passed, 1 = a check failed and
#             validation-failure.txt says which, 2 = usage error.

# NOTE: no `set -e`. This script's job is to collect ALL the failures in one
# pass. There is no amending stage to feed any more — a failed validation goes
# straight to a human — so the one report this writes is the only report
# anybody gets, and it had better be complete.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
# Inside the checkout, not $RUNNER_TEMP: CI's agents are certain to be able to
# write to their own working directory and nothing else is certain. Keyed to
# the script's own location so a manual run lands in the same place a CI run
# does. .gitignore keeps it out of commits; the check below keeps it out of
# the ones that try anyway.
HANDOFF_DIR=".ci-handoff"
OUT_DIR="$REPO_ROOT/$HANDOFF_DIR"
BASE=""

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base)    BASE="${2:?--base needs a commit sha}"; shift 2 ;;
    --out-dir) OUT_DIR="${2:?--out-dir needs a directory}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done
[[ -n "$BASE" ]] || { usage >&2; exit 2; }

mkdir -p "$OUT_DIR" || exit 2
FAILURES="$OUT_DIR/validation-failure.txt"
# Clear artifacts from any earlier pass: a stale plan.txt or diff.patch read
# as this attempt's evidence is exactly the class of bug this pipeline exists
# to kill. Every one of these is rewritten below on the paths that still reach
# them, so an absent file always means "this attempt never got that far".
rm -f "$FAILURES" "$OUT_DIR/plan.txt" "$OUT_DIR/diff.patch" \
      "$OUT_DIR/changed-files.txt"

# --- 1. a commit must exist -------------------------------------------------
#
# Unreachable as the pipeline now stands, and kept anyway. The outcome is
# decided before this script is called: scripts/ci-commit-change.sh makes the
# commit, and the workflow only reaches validation on `success`, which means
# there is one. This guard is what catches that stopping being true.
#
# Note who reads what follows. $FAILURES is posted verbatim as a comment on
# the REQUESTER's issue — no agent ever reads it — so it explains what
# happened to someone who asked for a DNS record and is owed an answer. It
# gives no instructions, because there is nobody here to instruct.
head_sha="$(git -C "$REPO_ROOT" rev-parse HEAD)"
if [[ "$head_sha" == "$BASE" ]]; then
  {
    echo "## No commit on the working branch"
    echo
    echo "HEAD is still $BASE — the commit this run started from — so nothing"
    echo "was recorded for this request. There is nothing to validate, plan"
    echo "or review."
    echo
    echo "Nothing about the request caused this. The step that makes the"
    echo "commit reported that it had made one, and then there was none, so"
    echo "the fault is in the pipeline. The run log linked above has the"
    echo "whole story; someone should read it before this request is tried"
    echo "again."
  } >"$FAILURES"
  cat "$FAILURES" >&2
  exit 1
fi
echo "commit: $(git -C "$REPO_ROOT" rev-parse --short HEAD) on top of ${BASE:0:7}"

# --- 2. the commit must not carry CI's own handoff files --------------------
# .ci-handoff/ is gitignored, and the only thing that stages anything now is
# scripts/ci-commit-change.sh, which passes an explicit vetted pathspec and
# never `-f`. The implementing agent holds no Bash at all, so it cannot
# force-add anything; this arm is unreachable too, for the same kind of reason
# as arm 1. It stays because of what it costs versus what it prevents: one
# grep, against a commit that would put CI's scratch into the pull request and
# hand the reviewing agent its own evidence — the request, the plan, this very
# report — as part of the change under review.
#
# Unlike everything below it, this does not join a collected report and carry
# on: it invalidates the very artifacts the remaining steps produce — step 3
# would snapshot a half-megabyte plan file as an added file and hand that to
# the reviewer — so it stops here, and stops first.
#
# The `"?` in the pattern catches git's quoted form, which it uses for paths
# containing control characters or quotes.
git -C "$REPO_ROOT" diff --name-only "$BASE" HEAD >"$OUT_DIR/changed-files.txt"
if smuggled="$(grep -E "^\"?${HANDOFF_DIR}(/|\"|\$)" "$OUT_DIR/changed-files.txt")"; then
  {
    echo "## The commit contains CI's own handoff files"
    echo
    echo "These committed paths are inside $HANDOFF_DIR/:"
    echo
    printf '%s\n' "$smuggled" | sed 's/^/  /'
    echo
    echo "$HANDOFF_DIR/ is where each stage of this pipeline leaves files for"
    echo "the next one — the request, the plan, the diff, this report. It is"
    echo "listed in .gitignore and it is not part of any change. Committing it"
    echo "would ship CI's internals in the pull request and would hand the"
    echo "reviewing stage its own notes as part of the change to review."
    echo
    echo "Nothing about the request caused this either. These paths are"
    echo "ignored, so only a deliberate force-add can commit them, and the"
    echo "only thing that stages files in this pipeline is a script that"
    echo "names every path it stages. Something upstream is wrong. The"
    echo "branch and the run log linked above have the rest."
  } >"$FAILURES"
  cat "$FAILURES" >&2
  rm -f "$OUT_DIR/changed-files.txt"
  exit 1
fi

# --- 3. snapshot the evidence for the reviewer ------------------------------
# `git log -p`, not `git diff`: the reviewer gets each commit message with the
# change it claims to describe (see the header). --reverse so it reads
# oldest-first, the order the work happened in, rather than git's default
# newest-first. --no-color for the same reason tofu gets -no-color: a
# color.ui=always anywhere in the config would otherwise write escape codes
# into a file the next reader has to strip, which is two of the nineteen shell
# calls the #33 run wasted.
git -C "$REPO_ROOT" log --reverse --no-decorate --no-color -p "$BASE"..HEAD \
  >"$OUT_DIR/diff.patch"
echo "changed files:"
sed 's/^/  /' "$OUT_DIR/changed-files.txt"

status=0
scratch="$(mktemp)"
trap 'rm -f "$scratch"' EXIT

# --- 4. tofu validate, once per stack ---------------------------------------
#
# dns/ gets a REAL init (with the backend) here, not -backend=false: it is
# the one stack step 5 plans, and a real init serves both `validate` and
# `plan` without initializing twice. workspace/ and site/ are validated only
# — never planned by this script, see step 5 — so a `-backend=false` init is
# all they need: enough for `tofu validate` to see provider schemas, without
# touching state or credentials they don't need for that.
#
# dns_validate_ok tracks dns/ alone, because only dns/'s own validate result
# decides whether step 5 attempts a plan; a broken workspace/ or site/ must
# not silently cancel the one plan a reviewer acts on.
dns_validate_ok=0
for s in dns workspace site; do
  if [[ "$s" == "dns" ]]; then
    init_cmd=(tofu -chdir="$REPO_ROOT/$s" init -input=false)
  else
    init_cmd=(tofu -chdir="$REPO_ROOT/$s" init -backend=false -input=false)
  fi
  if "${init_cmd[@]}" >"$scratch" 2>&1 \
       && tofu -chdir="$REPO_ROOT/$s" validate -no-color >>"$scratch" 2>&1; then
    echo "tofu validate ($s/): OK"
  else
    status=1
    [[ "$s" == "dns" ]] && dns_validate_ok=1
    { echo "## tofu validate failed ($s/)"; echo; cat "$scratch"; echo; } >>"$FAILURES"
  fi
done

# --- 5. plan (dns/ only — see the header comment on why) --------------------
if [[ "$dns_validate_ok" -ne 0 ]]; then
  {
    echo "## tofu plan was not attempted"
    echo
    echo "\`tofu validate\` failed on dns/ above, so a plan would only repeat it."
    echo
  } >>"$FAILURES"
else
  # Never pipe tofu plan into another process: a SIGPIPE from a short reader
  # kills tofu before it releases its state lock. Redirect, then read the file.
  if tofu -chdir="$REPO_ROOT/dns" plan -no-color -input=false \
       -refresh=false -lock=false >"$OUT_DIR/plan.txt" 2>"$scratch"; then
    echo "tofu plan (dns/): OK ($(wc -l <"$OUT_DIR/plan.txt") lines)"
    # Echo the whole plan into the run log. When a PR body has to truncate
    # the plan to fit GitHub's 65536-character limit, this is the untruncated
    # copy the truncation note points a reviewer at.
    echo "----- begin tofu plan (dns/) -----"
    cat "$OUT_DIR/plan.txt"
    echo "----- end tofu plan (dns/) -----"
  else
    status=1
    {
      echo "## tofu plan failed (dns/)"
      echo
      echo "A failing guard (dns/guards*.tf) shows up here as a precondition error."
      echo "The guard is authoritative: quote it, never weaken it."
      echo
      cat "$scratch"
      echo
      echo "### plan output before the failure"
      echo
      cat "$OUT_DIR/plan.txt"
      echo
    } >>"$FAILURES"
    # A half-written plan must never reach the PR-body assembler.
    rm -f "$OUT_DIR/plan.txt"
  fi
fi

if [[ "$status" -eq 0 ]]; then
  echo "validation OK"
else
  echo "validation FAILED — see $FAILURES" >&2
  cat "$FAILURES" >&2
fi
exit "$status"
