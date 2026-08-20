#!/usr/bin/env bash
#
# prepare.sh — decide whether an issue is this pipeline's to work, and if it
# is, claim it and lay out everything the implementing agent will need.
#
# Modes:
#   prepare.sh --issue N [--config FILE] [--out-dir DIR] [--event FILE]
#              [--assignee LOGIN] [--re-entry] [--no-ack]
#
#     --event      a GitHub webhook payload (issues / issue_comment). Also
#                  read from $FALCONET_EVENT_PATH. Optional: without one the
#                  gate reads the issue itself.
#     --assignee   who to record the claim against; defaults to
#                  $GITHUB_TRIGGERING_ACTOR, then to @me
#     --re-entry   treat this as a requester's reply to a needs-info question
#                  rather than a first claim. Inferred from the event when
#                  there is one; this is how a workstation says it.
#     --no-ack     skip the acknowledgment comment
#
# Prints exactly one word on stdout — the outcome — and nothing else:
#
#   ready        the issue is ours, the branch exists, the handoff is written
#   in-flight    an open pull request is already carrying this issue
#   ineligible   a blocking label, an opt-out, a closed issue, or not queued
#
# in-flight and ineligible write NOTHING and change NOTHING. They make no gh
# call that mutates, create no branch, leave no file, and park nothing:
# duplicate and ineligible events are silent no-ops. The reason goes to
# stderr, because "ineligible" on its own is not a diagnostic.
#
# Outputs on the ready path, written into the handoff directory:
#   issue.json          the one snapshot every later step reads
#   ack.md              the comment posted to the requester (first claim only)
#   request.md          the request in markdown — both agents read this first
#   plan-baseline.txt   what main already plans, before anyone touches it
#   base-sha.txt        the commit this run started from
#   branch.txt          the working branch
#
# and, when $GITHUB_ENV is writable, BRANCH and BASE_SHA.
#
# ---------------------------------------------------------------------------
# Where this came from, and the one thing it changes
# ---------------------------------------------------------------------------
# This verb is the only one with no ancestor script. It was inline YAML in the
# origin workflow's first stage, and its eligibility half was not even that —
# it was a job-level `if:` expression, evaluated before checkout. That is why
# it moves: an `if:` runs before the repository exists, so it can never read
# the config file, and gating there would fork eligibility into YAML-in-CI and
# nothing-locally for a project whose whole rule is one code path. The cost is
# runner-seconds on ineligible events. Paid willingly (ADR-0003).
#
# ---------------------------------------------------------------------------
# needs-info is both a blocking label and the way back in
# ---------------------------------------------------------------------------
# The origin admitted two kinds of run: an issue gains the queue label while
# carrying no parked state, or a human replies on an issue that is already
# parked needs-info. The second is the re-entry path, and issue #25 is why it
# exists — the requester answered the question, and clearing the label by hand
# is something requesters usually cannot do.
#
# So needs-info blocks a first claim and admits a reply, and a flat precedence
# list cannot say both. Two modes:
#
#   re-entry   the event is an issue_comment on an issue (not a PR), the
#              commenter is not a bot, the queue label is present, and the
#              needs-info label is present. Or --re-entry says so.
#   claim      everything else.
#
# In claim mode every blocking label blocks. In re-entry mode the needs-info
# label is the ticket in, and the other blocking labels still block.
#
# Re-entry is never INFERRED from the comment thread here — "the last comment
# is not mine" is a judgment, and a verb that reaches a different answer on an
# unchanged issue is not a gate. The caller says so, or the event does.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Two levels: libexec/falconet/ sits where scripts/ used to sit one deep.
FALCONET_HOME="$(dirname "$(dirname "$SCRIPT_DIR")")"

. "$FALCONET_HOME/lib/config.sh"
. "$FALCONET_HOME/lib/repo.sh"
. "$FALCONET_HOME/lib/handoff.sh"

TOFU="${TOFU:-tofu}"

repo_root_init

ISSUE=""
CONFIG=""
OUT_DIR=""
EVENT="${FALCONET_EVENT_PATH:-}"
ASSIGNEE=""
RE_ENTRY=""
NO_ACK=""

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --issue)    ISSUE="${2:?--issue needs a number}"; shift 2 ;;
    --config)   CONFIG="${2:?--config needs a file}"; shift 2 ;;
    --out-dir)  OUT_DIR="${2:?--out-dir needs a directory}"; shift 2 ;;
    --event)    EVENT="${2:?--event needs a file}"; shift 2 ;;
    --assignee) ASSIGNEE="${2:?--assignee needs a login}"; shift 2 ;;
    --re-entry) RE_ENTRY=1; shift ;;
    --no-ack)   NO_ACK=1; shift ;;
    -h|--help)  usage >&2; exit 2 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "$ISSUE" ]] || { usage >&2; exit 2; }
# The event schema used to guarantee this was an integer. A CLI caller
# guarantees nothing, and the number goes into a regex and a branch name.
[[ "$ISSUE" =~ ^[0-9]+$ ]] || { echo "--issue must be a number" >&2; exit 2; }

# Resolve caller-relative paths before the cd.
case "$EVENT" in ""|/*) ;; *) EVENT="$PWD/$EVENT" ;; esac
case "$OUT_DIR" in ""|/*) ;; *) OUT_DIR="$PWD/$OUT_DIR" ;; esac

cd "$REPO_ROOT" || exit 1
config_init "$CONFIG"

QUEUE_LABEL="$(config_get '.issue.queue_label')"
OPT_OUT="$(config_get '.issue.opt_out_text')"
NEEDS_INFO="$(config_get '.labels.needs_info')"
BRANCH_PREFIX="$(config_get '.issue.branch_prefix')"

# A configured value is data, and it is about to be part of a regex.
regex_escape() { printf '%s' "$1" | sed -e 's/[][\\.^$*+?{}|()]/\\&/g'; }

say() { printf '%s\n' "$*" >&2; }
die() { printf '%s\n' "$*" >&2; exit 1; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# --- the gate's inputs ------------------------------------------------------
#
# One fetch at most. With an event payload the gate reads it and an ineligible
# issue costs no network at all; without one the issue is fetched once, gated,
# and the same snapshot is reused by the ready path. The issue is not going to
# change during the next twenty lines.

SNAPSHOT=""          # set when a full gh snapshot exists
mode=claim

fetch_issue() {
  gh issue view "$ISSUE" --json number,title,body,labels,comments,state \
    >"$TMP/issue.json" 2>"$TMP/gh.err" \
    || die "prepare: could not read issue #$ISSUE: $(cat "$TMP/gh.err")"
  [[ -s "$TMP/issue.json" ]] || die "prepare: issue #$ISSUE came back empty"
  SNAPSHOT="$TMP/issue.json"
}

if [[ -n "$EVENT" ]]; then
  [[ -f "$EVENT" ]] || die "prepare: --event names no file: $EVENT"
  jq -e . "$EVENT" >/dev/null 2>&1 || die "prepare: $EVENT is not valid JSON"
  labels="$(jq -r '.issue.labels[]?.name // empty' "$EVENT")"
  body="$(jq -r '.issue.body // ""' "$EVENT")"
  state="$(jq -r '.issue.state // "open"' "$EVENT")"
  # The re-entry shape, exactly: a human comment on an issue that is parked
  # needs-info and still queued. `.issue.pull_request` is what distinguishes a
  # PR comment from an issue comment.
  ev_action="$(jq -r '.action // ""' "$EVENT")"
  ev_is_pr="$(jq -r 'if .issue.pull_request then "yes" else "" end' "$EVENT")"
  ev_bot="$(jq -r 'if (.comment.user.type // "") == "Bot" then "yes" else "" end' "$EVENT")"
  if [[ "$ev_action" == "created" && -z "$ev_is_pr" && -z "$ev_bot" ]] \
     && grep -qxF "$QUEUE_LABEL" <<<"$labels" \
     && grep -qxF "$NEEDS_INFO" <<<"$labels"; then
    mode=re-entry
  fi
  # A bot comment, or a comment on a pull request, is not a way in.
  if [[ "$ev_action" == "created" && ( -n "$ev_is_pr" || -n "$ev_bot" ) ]]; then
    say "issue #$ISSUE: comment event is from a bot or on a pull request"
    echo ineligible; exit 0
  fi
else
  fetch_issue
  labels="$(jq -r '.labels[]?.name // empty' "$SNAPSHOT")"
  body="$(jq -r '.body // ""' "$SNAPSHOT")"
  state="$(jq -r '.state // "OPEN"' "$SNAPSHOT")"
fi

[[ -n "$RE_ENTRY" ]] && mode=re-entry

# --- rule 0: the issue is open ----------------------------------------------
#
# The origin checked this on both admission paths. It is the cheapest and most
# obviously terminal fact, and the containment step checked it first too.
# `gh` says OPEN, a webhook says open.
case "$state" in
  open|OPEN|"") ;;
  *) say "issue #$ISSUE is $state"; echo ineligible; exit 0 ;;
esac

# --- rule 1: no blocking label ----------------------------------------------
#
# Exact-line, and fixed-string: a label named needs-info-later must not block,
# and a configured label may contain regex metacharacters.
while IFS= read -r blocking; do
  [[ -n "$blocking" ]] || continue
  if [[ "$mode" == "re-entry" && "$blocking" == "$NEEDS_INFO" ]]; then
    continue
  fi
  if grep -qxF "$blocking" <<<"$labels"; then
    say "issue #$ISSUE carries the blocking label '$blocking'"
    echo ineligible; exit 0
  fi
done < <(config_get_array '.issue.blocking_labels')

# --- rule 2: the opt-out box is not ticked ----------------------------------
#
# A checked markdown checkbox carrying the configured text, matched
# case-insensitively. The origin's CI form was an unanchored substring test,
# which meant the sentence appearing anywhere — quoted from another issue, say
# — opted the issue out; the human-facing skill anchored it to a list item.
# Anchored is right, and the leading-whitespace tolerance is a widening of
# both, because issue forms indent nested checkboxes.
if grep -qiE "^[[:space:]]*[-*] \[[xX]\] $(regex_escape "$OPT_OUT")" <<<"$body"; then
  say "issue #$ISSUE has the opt-out box ticked"
  echo ineligible; exit 0
fi

# --- rule 3: the queue label is present -------------------------------------
if ! grep -qxF "$QUEUE_LABEL" <<<"$labels"; then
  say "issue #$ISSUE is not labelled '$QUEUE_LABEL'"
  echo ineligible; exit 0
fi

# --- rule 4: no open pull request is already carrying it --------------------
#
# In flight means an OPEN PULL REQUEST, never a branch. Since every run pushes
# its branch, a leftover branch is the ordinary state of a retried issue, and
# keying on branches would let one suppress every later run on the issue.
#
# The regex is built from config and passed through the ENVIRONMENT, never
# spliced into the filter text. And the result is captured whole before it is
# inspected: never `gh ... | grep -q`, because grep -q exits at the first match
# and can SIGPIPE gh, which under pipefail turns a FOUND match into a non-zero
# pipeline — the opposite of the answer just computed.
alts=""
while IFS= read -r p; do
  [[ -n "$p" ]] || continue
  alts="${alts:+$alts|}$(regex_escape "$p")"
done < <(config_get_array '.issue.in_flight_prefixes')
[[ -n "$alts" ]] || alts="$(regex_escape "$BRANCH_PREFIX")"
FALCONET_INFLIGHT_RE="^($alts)${ISSUE}-"
export FALCONET_INFLIGHT_RE

heads="$(gh pr list --state open --json number,headRefName \
  --jq '.[] | select(.headRefName | test(env.FALCONET_INFLIGHT_RE)) | "#\(.number) \(.headRefName)"' \
  2>"$TMP/gh.err")"
if [[ -n "$heads" ]]; then
  say "issue #$ISSUE already has an open PR: $(tr '\n' ',' <<<"$heads" | sed 's/,$//') — nothing to do"
  echo in-flight; exit 0
fi

# ===========================================================================
# ready
# ===========================================================================

# The tree must be clean before anything else happens.
#
# The agent's outcome is read from the state of the tree, so the tree has to
# be clean before it starts or the reading is a lie. The origin asserted this
# AFTER the claim, the acknowledgment and the branch — so a dirty tree thanked
# the requester, assigned the issue, cut a branch and then died. The
# human-facing skill put it in preflight. Preflight is right, and this is as
# early as it can go while still being free: after the gate, which touches
# nothing, and before the first mutating call.
git rev-parse --is-inside-work-tree >/dev/null 2>&1 \
  || die "prepare: $REPO_ROOT is not a git repository"
dirt="$(git status --porcelain)" || die "prepare: could not read the working tree"
if [[ -n "$dirt" ]]; then
  say "prepare: working tree is dirty before the agent ran:"
  printf '%s\n' "$dirt" >&2
  exit 1
fi

handoff_init "$OUT_DIR"

[[ -n "$SNAPSHOT" ]] || fetch_issue
cp "$SNAPSHOT" "$HANDOFF/issue.json"

# The requester replied, so clear the parking label rather than spending an
# agent turn on it. Hard-fails, deliberately, while the claim and the
# acknowledgment below are best-effort: an issue left parked while a run
# proceeds against it is a contradiction a human has to untangle later.
if [[ "$mode" == "re-entry" ]]; then
  gh issue edit "$ISSUE" --remove-label "$NEEDS_INFO" >/dev/null 2>"$TMP/gh.err" \
    || die "prepare: could not clear '$NEEDS_INFO' from #$ISSUE: $(cat "$TMP/gh.err")"
  say "cleared '$NEEDS_INFO': this run is a requester reply"
fi

# The claim. Best effort — it buys one thing, which is dropping this issue out
# of the unassigned queue a human's own tooling reads, and that is not worth
# failing a run over. A bot cannot be an assignee, so in CI this records the
# human who triggered the run.
who="${ASSIGNEE:-${GITHUB_TRIGGERING_ACTOR:-@me}}"
gh issue edit "$ISSUE" --add-assignee "$who" >/dev/null 2>&1 \
  || say "warning: could not assign #$ISSUE to $who"

# The acknowledgment, on a first claim only. Someone who has just answered a
# question is already mid-conversation with this system and does not need to
# be greeted again.
#
# It exists because the next thing this pipeline says can be twenty minutes
# away, and silence after filing a request reads as nothing happened. It is
# scripted so it costs no tokens and cannot be rephrased into something that
# overpromises: a machine is doing the work, and a person still decides.
#
# Written to a file rather than passed inline, which is also how park does it:
# the file is the artifact a test can read to assert what a requester saw.
if [[ "$mode" != "re-entry" && -z "$NO_ACK" ]]; then
  {
    printf "Thanks — this request has been picked up and is being worked on automatically.\n\n"
    printf "You'll hear back here when there's a change ready for review, or if we need more detail from you. Nothing takes effect until a person has reviewed it.\n"
  } >"$HANDOFF/ack.md"
  gh issue comment "$ISSUE" --body-file "$HANDOFF/ack.md" >/dev/null 2>&1 \
    || say "warning: could not acknowledge #$ISSUE to its requester"
fi

# The request, in markdown, on disk. Both agents read this file; neither has
# gh. Built from the snapshot taken before the acknowledgment was posted,
# which is why the acknowledgment is not in it: the agents should read the
# requester's words, not this pipeline's.
jq -r '
  "# Issue #\(.number): \(.title)\n\n\(.body // "")\n\n"
  + (if (.comments | length) > 0
     then "## Comment thread (oldest first)\n\n"
          + ([.comments[] | "### \(.author.login // "unknown") — \(.createdAt)\n\n\(.body // "")\n"] | join("\n"))
     else "" end)
' "$HANDOFF/issue.json" >"$HANDOFF/request.md" \
  || die "prepare: could not render request.md from the issue snapshot"
say "wrote request.md ($(wc -l <"$HANDOFF/request.md") lines)"

# The branch name is mechanics, not judgment, and should never cost an agent a
# tool call. The title is used for the slug and nothing else — the
# pull-request title comes from the commit subject the agent writes.
#
# Both trailing-dash strips are load-bearing: the cut can land mid-run and
# leave one behind, and `issue-42-` is a perfectly valid ref name that nothing
# downstream would have caught.
title="$(jq -r '.title // ""' "$HANDOFF/issue.json")"
slug="$(printf '%s' "$title" \
  | tr '[:upper:]' '[:lower:]' \
  | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' \
  | cut -c1-40 \
  | sed -E 's/-+$//')"
[[ -n "$slug" ]] || slug=request
branch="${BRANCH_PREFIX}${ISSUE}-${slug}"

# A previous run can leave this branch on the remote — its PR closed, or never
# opened, so the in-flight check let this run start. Pushing onto it would be
# refused: --force-with-lease says no to a ref it has never seen, which is the
# right answer, because the alternative is silently overwriting the last run's
# work. Disambiguate now instead. The prefix survives, so the in-flight check
# and the containment check still recognize it.
#
# --exit-code with both streams discarded means this is never a failure, only
# an answer: no remote, or no credentials, reads as "no such branch".
#
# $GITHUB_RUN_ID is CI-only, and under `set -u` an unguarded reference would
# kill the run at the moment a collision fires — a bug that appears only on
# retries. The suffix's only job is to disambiguate.
if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
  branch="${branch}-${GITHUB_RUN_ID:-$(date +%s)}"
  say "a branch by the obvious name already exists on the remote; using $branch"
fi

base_sha="$(git rev-parse HEAD)" || die "prepare: could not read HEAD"
git switch -qc "$branch" || die "prepare: could not create branch $branch"

# Nothing on a fresh runner has an identity, and the commit is made by a
# script rather than by an agent's tooling, so without this the commit verb
# dies on "Please tell me who you are". Set only when unset: on a workstation
# this is a real repository with a real author, and overwriting that would be
# a surprise the origin never had to consider.
if ! git config user.email >/dev/null 2>&1; then
  git config user.name "github-actions[bot]"
  git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
fi

printf '%s\n' "$branch" >"$HANDOFF/branch.txt"
printf '%s\n' "$base_sha" >"$HANDOFF/base-sha.txt"
github_env_append "BRANCH=$branch" "BASE_SHA=$base_sha"

# What main already plans, before anyone touches anything. Handing this to the
# implementing agent is what stops it trying to fix pre-existing drift, and it
# is what lets a reviewer tell this change's plan lines from main's.
#
# Hard-fails on purpose: if main itself cannot plan, no amount of agent time
# will fix it, and failing here costs nothing because no agent has run yet.
PLAN_COMMAND="$(config_get '.plan.command')"
: >"$HANDOFF/plan-baseline.txt"
multi=0
[[ "$(config_get_array '.stacks.plan' | wc -l)" -gt 1 ]] && multi=1
while IFS= read -r s; do
  [[ -n "$s" ]] || continue
  cmd_str="${PLAN_COMMAND//\{stack\}/$REPO_ROOT/$s}"
  # shellcheck disable=SC2206
  cmd=($cmd_str)
  [[ "${cmd[0]}" == "tofu" ]] && cmd[0]="$TOFU"
  # Never pipe a plan into another process: a SIGPIPE from a short reader kills
  # tofu before it releases its state lock. Redirect, then read the file.
  if ! "${cmd[@]}" >"$TMP/baseline.txt" 2>"$TMP/plan.err"; then
    say "prepare: the baseline plan failed on $s/ — main does not plan cleanly:"
    cat "$TMP/plan.err" >&2
    exit 1
  fi
  [[ "$multi" -eq 1 ]] && { echo "## $s"; echo; } >>"$HANDOFF/plan-baseline.txt"
  cat "$TMP/baseline.txt" >>"$HANDOFF/plan-baseline.txt"
done < <(config_get_array '.stacks.plan')
say "baseline plan: $(wc -l <"$HANDOFF/plan-baseline.txt") lines"
say "working branch $branch from ${base_sha:0:7}"

echo ready
