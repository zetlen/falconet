#!/usr/bin/env bash
#
# validate.sh — the deterministic gate between the implementing agent and
# whatever looks at the change next.
#
# Everything an agent used to prove by hand — did I commit? does it parse?
# what does the plan say? — happens here, once, in one shell, and lands in
# files. Agents run no tofu in CI.
#
# Modes:
#   validate.sh --base <sha> [--out-dir DIR] [--config FILE]
#       Validate HEAD against the commit the run started from:
#         1. assert at least one commit exists on top of <sha>
#         2. assert the commit does not touch the handoff dir — CI's own scratch
#            has no business inside a change (see below)
#         3. snapshot the commits to DIR/diff.patch and the changed paths to
#            DIR/changed-files.txt — the review agent is granted no Bash, so
#            its evidence has to be on disk before it starts
#         4. tofu validate, once per configured stack (dns/, workspace/, site/ by
#            default — #16
#            split the single root module into three)
#         5. tofu -chdir=dns plan -no-color -refresh=false -lock=false
#            > DIR/plan.txt — stacks.plan only; see the comment on step 5 for
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
# Outputs, written into DIR (default: handoff_dir at the root of this
# repository, which is where the CI pipeline hands files between its stages
# and is listed in .gitignore):
#   diff.patch              base..HEAD as `git log -p`, oldest commit first
#   changed-files.txt       one changed path per line
#   plan.txt                full plan output — written only when a plan ran
#                           and succeeded; deleted if one is half-written
#   validation-failure.txt  human-readable summary — written only on failure
#
# The two snapshots are written by every run that gets past the two guards
# below, INCLUDING failing ones — a reviewer needs the evidence most when
# something went wrong. They are not written by a run that stops in a guard,
# because a guard stopping means the evidence would be a lie. Do not read the
# first sentence as "always"; the guards are the exception and they are the
# whole point.
#
# Exit codes: 0 = every check passed, 1 = a check failed and
#             validation-failure.txt says which, 2 = usage error.

# NOTE: no `set -e`. This script's job is to collect ALL the failures in one
# pass. There is no amending stage to feed any more — a failed validation goes
# straight to a human — so the one report this writes is the only report
# anybody gets, and it had better be complete.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Two levels: libexec/falconet/ sits where scripts/ used to sit one deep.
FALCONET_HOME="$(dirname "$(dirname "$SCRIPT_DIR")")"

. "$FALCONET_HOME/lib/config.sh"
. "$FALCONET_HOME/lib/repo.sh"
. "$FALCONET_HOME/lib/handoff.sh"

# The tests stub the planner, as they stub the formatter and the scanner.
TOFU="${TOFU:-tofu}"

repo_root_init

OUT_DIR=""
CONFIG=""
BASE=""

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base)    BASE="${2:?--base needs a commit sha}"; shift 2 ;;
    --out-dir) OUT_DIR="${2:?--out-dir needs a directory}"; shift 2 ;;
    --config)  CONFIG="${2:?--config needs a file}"; shift 2 ;;
    # 2, not 0. This verb's exit code IS the verdict, so a --help that exits 0
    # is a run reporting that validation passed.
    -h|--help) usage >&2; exit 2 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done
[[ -n "$BASE" ]] || { usage >&2; exit 2; }

config_init "$CONFIG"
handoff_init "$OUT_DIR"
OUT_DIR="$HANDOFF"
# The name, for the message and the prefix check below. handoff_init resolved
# the directory; this is what to call it.
HANDOFF_DIR="$(basename "$OUT_DIR")"

# Resolve --base to a full commit sha before anything compares against it.
#
# This was a string comparison against the raw argument, and every guard below
# inherited the assumption that the caller passed a 40-character sha. It
# usually did, because prepare writes one. But `--base main`, or a short sha,
# or `HEAD`, made the "no commit exists" check below silently false — and then
# `git log "$BASE"..HEAD` produced an empty diff.patch, `git diff` an empty
# changed-files.txt, and the run could reach `exit 0` having snapshotted
# nothing at all. The reviewing agent is granted no Bash; it would have read
# an empty diff and seen no change. Resolve, or refuse.
if ! BASE="$(git -C "$REPO_ROOT" rev-parse --verify --quiet "${BASE}^{commit}")"; then
  echo "validate: --base does not name a commit in this repository" >&2
  exit 2
fi

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
# decided before this script is called: the commit verb makes the
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
# The handoff dir is gitignored, and the only thing that stages anything now
# is the commit verb, which passes an explicit vetted pathspec and
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
# Matched as a literal prefix, not as a regex. This was an ERE with
# $HANDOFF_DIR interpolated raw, which was harmless while the name was a
# constant and is not now that it comes from config: a value carrying `(` or
# `[` produces a broken pattern, grep exits 2, and the `if` reads that as "no
# match" -- a guard that fails OPEN. The `"?` still catches git's quoted form,
# which it uses for paths containing control characters or quotes.
#
# The loop is not wrapped in a command substitution and does not use `case`:
# macOS ships bash 3.2, which mis-parses an unbalanced `)` in a case pattern
# inside $( ). [[ ]] with a quoted left side and an unquoted trailing glob
# says the same thing and parses everywhere.
smuggled=""
while IFS= read -r _p; do
  _u="${_p%\"}"; _u="${_u#\"}"
  if [[ "$_u" == "$HANDOFF_DIR" || "$_u" == "$HANDOFF_DIR"/* ]]; then
    smuggled="${smuggled}${_p}"$'\n'
  fi
done <"$OUT_DIR/changed-files.txt"
smuggled="${smuggled%$'\n'}"
if [[ -n "$smuggled" ]]; then
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
# A planned stack gets a REAL init (with the backend), not -backend=false: it
# is a stack step 5 plans, and a real init serves both `validate` and `plan`
# without initializing twice. A validate-only stack is never planned by this
# script, see step 5, so a `-backend=false` init is all it needs: enough for
# `tofu validate` to see provider schemas, without touching state or
# credentials it does not need for that.
#
# The stacks come from config now. stacks.plan get a real init because they
# are the ones step 5 plans and one init serves both verbs; stacks.validate_only
# get -backend=false, which is enough for `validate` to see provider schemas
# without touching state or credentials they do not need for that.
#
# plan_stack_failed tracks the PLANNED stacks alone, because only their own
# validate result decides whether step 5 attempts a plan; a broken
# validate-only stack must not silently cancel the one plan a reviewer acts on.
#
# It was called dns_validate_ok and it was inverted with respect to its name:
# 0 meant OK and 1 meant failed, and it read correctly only because its one
# use said `-ne 0`. Renamed rather than left as a trap for whoever writes
# `-eq 1` by intuition and plans a stack whose validate just failed.
PLAN_STACKS=()
while IFS= read -r _s; do [ -n "$_s" ] && PLAN_STACKS+=("$_s"); done \
  < <(config_get_array '.stacks.plan')
CHECK_STACKS=()
while IFS= read -r _s; do [ -n "$_s" ] && CHECK_STACKS+=("$_s"); done \
  < <(config_get_array '.stacks.validate_only')

plan_stack_failed=0
for s in ${PLAN_STACKS[@]+"${PLAN_STACKS[@]}"} ${CHECK_STACKS[@]+"${CHECK_STACKS[@]}"}; do
  planned=0
  for _p in ${PLAN_STACKS[@]+"${PLAN_STACKS[@]}"}; do
    [[ "$_p" == "$s" ]] && planned=1
  done
  # A configured stack that is not there is a configuration error rather than
  # a validation failure. Reported rather than fatal, so the other stacks are
  # still checked and the report says which key named a directory that is not
  # in the repository.
  if [[ ! -d "$REPO_ROOT/$s" ]]; then
    status=1
    key=validate_only
    [[ "$planned" -eq 1 ]] && { plan_stack_failed=1; key=plan; }
    { echo "## the configured stack $s/ is not in this repository"
      echo
      config_stack_missing "$key" "$s"; echo
      echo
    } >>"$FAILURES"
    continue
  fi

  if [[ "$planned" -eq 1 ]]; then
    init_cmd=("$TOFU" -chdir="$REPO_ROOT/$s" init -input=false)
  else
    init_cmd=("$TOFU" -chdir="$REPO_ROOT/$s" init -backend=false -input=false)
  fi
  if "${init_cmd[@]}" >"$scratch" 2>&1 \
       && "$TOFU" -chdir="$REPO_ROOT/$s" validate -no-color >>"$scratch" 2>&1; then
    echo "tofu validate ($s/): OK"
  else
    status=1
    [[ "$planned" -eq 1 ]] && plan_stack_failed=1
    # The heading says "validate" even when it was `init` that died, because
    # the two are one gate from the requester's side and splitting the wording
    # would mean explaining the difference to someone who did not ask.
    { echo "## tofu validate failed ($s/)"; echo; cat "$scratch"; echo; } >>"$FAILURES"
  fi
done

# --- 5. plan (the planned stacks only — see the header on why) --------------
#
# The command is plan.command from config, with {stack} replaced by the
# stack's directory. It is split on whitespace, so an argument containing a
# space cannot be expressed — the default has none, and a consumer who needs
# one should say so and get a better mechanism rather than a quoting puzzle.
# If the first word is `tofu` it is replaced by $TOFU, which is how the tests
# reach it.
#
# All planned stacks land in one plan.txt, because the handoff protocol names
# one file and assemble attaches one file. With more than one they are
# separated by a `## <stack>` heading; with the default single stack the file
# is exactly what it always was.
if [[ "$plan_stack_failed" -ne 0 ]]; then
  {
    echo "## tofu plan was not attempted"
    echo
    echo "\`tofu validate\` failed above, so a plan would only repeat it."
    echo
  } >>"$FAILURES"
else
  PLAN_COMMAND="$(config_get '.plan.command')"
  multi=0
  [[ "${#PLAN_STACKS[@]}" -gt 1 ]] && multi=1
  : >"$OUT_DIR/plan.txt"
  for s in ${PLAN_STACKS[@]+"${PLAN_STACKS[@]}"}; do
    cmd_str="${PLAN_COMMAND//\{stack\}/$REPO_ROOT/$s}"
    # shellcheck disable=SC2206
    cmd=($cmd_str)
    [[ "${cmd[0]}" == "tofu" ]] && cmd[0]="$TOFU"
    # Never pipe a plan into another process: a SIGPIPE from a short reader
    # kills tofu before it releases its state lock. Redirect, then read the
    # file.
    stack_plan="$scratch.plan"
    if "${cmd[@]}" >"$stack_plan" 2>"$scratch"; then
      [[ "$multi" -eq 1 ]] && { echo "## $s"; echo; } >>"$OUT_DIR/plan.txt"
      cat "$stack_plan" >>"$OUT_DIR/plan.txt"
      echo "tofu plan ($s/): OK ($(wc -l <"$stack_plan") lines)"
      # Echo the whole plan into the run log. When a PR body has to truncate
      # the plan to fit GitHub's 65536-character limit, this is the
      # untruncated copy the truncation note points a reviewer at.
      echo "----- begin tofu plan ($s/) -----"
      cat "$stack_plan"
      echo "----- end tofu plan ($s/) -----"
    else
      status=1
      {
        echo "## tofu plan failed ($s/)"
        echo
        cat "$scratch"
        echo
        echo "### plan output before the failure"
        echo
        cat "$stack_plan"
        echo
      } >>"$FAILURES"
      # A failing guard shows up above as a precondition error, and the guard
      # is authoritative. That sentence used to be in the report; it is here
      # instead, because the report is posted verbatim to the REQUESTER, who
      # asked for a DNS record and is not the person being told not to weaken
      # a guard. The file's own header promises it gives no instructions,
      # because there is nobody there to instruct — and this was the one path
      # that broke that promise.
      echo "a failing guard shows up as a precondition error; the guard is" >&2
      echo "authoritative — quote it, never weaken it" >&2
      # A half-written plan must never reach the PR-body assembler.
      rm -f "$OUT_DIR/plan.txt" "$stack_plan"
      break
    fi
    rm -f "$stack_plan"
  done
fi

if [[ "$status" -eq 0 ]]; then
  echo "validation OK"
  github_env_append "VALIDATED=true"
else
  echo "validation FAILED — see $FAILURES" >&2
  cat "$FAILURES" >&2
fi
exit "$status"
