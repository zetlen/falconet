#!/usr/bin/env bash
#
# ci-park-issue.sh — put an infra-request issue into a terminal state and say
# so, in plain language, where the requester will see it.
#
# The staged pipeline in .github/workflows/infra-issues.yml has several
# places a request can legitimately stop: the implementing agent needs more
# information, validation failed, the reviewing agent did not approve, or a
# step simply died. Every one of them comes through here,
# so "stopped" always means the same three things happened — a comment, a
# label, and the claim released — and never means "silently nothing". A
# request that vanishes into an empty green run is the failure mode this repo
# cares about most.
#
# Modes:
#   ci-park-issue.sh --issue N --label needs-info|ready-for-human
#                    --preamble TEXT
#                    [--body FILE] [--body-title TEXT]
#                    [--run-url URL] [--unassign LOGIN] [--branch NAME]
#
#     --preamble    the plain-language sentence the requester reads first
#     --body        extra detail appended after the preamble
#     --body-title  if given, --body is folded into a collapsed <details>
#                   block and fenced as code. Use it for machine output
#                   (validation logs, plan errors); omit it when --body is
#                   already prose written for a human.
#     --unassign    release the claim (see the workflow's claim step)
#     --branch      the pushed working branch carrying the commits this run
#                   made, named and linked immediately under the preamble.
#                   Pass it wherever a commit exists; pass nothing (or an
#                   empty string) where none does.
#
# --branch exists because of run 32093607680 (issue #36), which parked an
# issue saying "I prepared this change ... This one needs a person" when the
# only push in the pipeline sat behind an approved review, so the prepared
# change had been destroyed with the runner and the branch had never reached
# the remote. Work is pushed as soon as it exists now (scripts/
# ci-push-branch.sh); this is the other half of that fix — the hand-over
# comment says WHERE it is, in a link a person can click, rather than
# describing work the reader has no way to find.
#
# The comment is capped at 60000 characters; if --body is longer it is cut on
# a line boundary with an explicit note pointing at --run-url. As everywhere
# else in this pipeline, content is dropped loudly or not at all.
#
# Requires GH_TOKEN (or gh's own auth) in the environment.
#
# Exit codes: 0 = parked, 1 = a GitHub call failed (the caller must treat the
#             issue as still un-parked), 2 = usage error.

set -uo pipefail

ISSUE=""
LABEL=""
PREAMBLE=""
BODY=""
BODY_TITLE=""
RUN_URL=""
UNASSIGN=""
BRANCH=""
COMMENT_LIMIT=60000

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --issue)      ISSUE="${2:?--issue needs a number}"; shift 2 ;;
    --label)      LABEL="${2:?--label needs a label}"; shift 2 ;;
    --preamble)   PREAMBLE="${2:?--preamble needs text}"; shift 2 ;;
    --body)       BODY="${2:?--body needs a file}"; shift 2 ;;
    --body-title) BODY_TITLE="${2:?--body-title needs text}"; shift 2 ;;
    --run-url)    RUN_URL="${2:?--run-url needs a URL}"; shift 2 ;;
    --unassign)   UNASSIGN="${2:?--unassign needs a login}"; shift 2 ;;
    # An empty --branch is legal and means "no branch": the caller is a
    # workflow step that may or may not have one, and forcing every one of
    # them to build its argument list conditionally is how a hand-over path
    # gets missed.
    --branch)
      [[ $# -ge 2 ]] || { echo "--branch needs a value (the empty string is fine)" >&2; exit 2; }
      BRANCH="$2"; shift 2 ;;
    -h|--help)    usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "$ISSUE" && -n "$LABEL" && -n "$PREAMBLE" ]] || { usage >&2; exit 2; }
[[ "$ISSUE" =~ ^[0-9]+$ ]] || { echo "--issue must be a number" >&2; exit 2; }
case "$LABEL" in
  needs-info|ready-for-human) ;;
  *) echo "--label must be needs-info or ready-for-human (the two parking labels in docs/agents/triage-labels.md)" >&2; exit 2 ;;
esac

comment="$(mktemp)"
trap 'rm -f "$comment"' EXIT

printf '%s\n' "$PREAMBLE" >"$comment"

# The pointer to the work, directly under the sentence that mentions it, and
# before any collapsed <details> block a reader might not open. One fixed
# wording, written once, here: "no pull request" is true of every path that
# comes through this script, because a run that opened one does not park the
# issue.
if [[ -n "$BRANCH" ]]; then
  {
    echo
    # shellcheck disable=SC2016  # the backticks are a markdown code span, not
    # a subshell: this string is a GitHub comment, and it must not expand.
    printf 'The commits are pushed to the branch `%s`. No pull request is open for it.\n' "$BRANCH"
    # GITHUB_SERVER_URL / GITHUB_REPOSITORY are set in every Actions run. A
    # local invocation has neither, and names the branch without a link
    # rather than printing a fabricated URL.
    if [[ -n "${GITHUB_SERVER_URL:-}" && -n "${GITHUB_REPOSITORY:-}" ]]; then
      echo
      printf '%s/%s/tree/%s\n' \
        "$GITHUB_SERVER_URL" "$GITHUB_REPOSITORY" "$BRANCH"
    fi
  } >>"$comment"
fi

if [[ -n "$BODY" && -s "$BODY" ]]; then
  detail="$(mktemp)"
  if [[ "$(wc -c <"$BODY")" -gt "$COMMENT_LIMIT" ]]; then
    head -c "$COMMENT_LIMIT" "$BODY" | sed '$d' >"$detail"
    where="${RUN_URL:-the Actions tab of this repository}"
    {
      echo
      echo "[ ... cut here: the rest is in the run log,"
      echo "      $where ]"
    } >>"$detail"
  else
    cat "$BODY" >"$detail"
  fi

  {
    echo
    if [[ -n "$BODY_TITLE" ]]; then
      printf '<details><summary>%s</summary>\n\n```\n' "$BODY_TITLE"
      cat "$detail"
      printf '```\n\n</details>\n'
    else
      cat "$detail"
    fi
  } >>"$comment"
  rm -f "$detail"
fi

if [[ -n "$RUN_URL" ]]; then
  printf '\n(Run log: %s)\n' "$RUN_URL" >>"$comment"
fi

status=0
gh issue comment "$ISSUE" --body-file "$comment" || status=1
gh issue edit "$ISSUE" --add-label "$LABEL" || status=1
if [[ -n "$UNASSIGN" ]]; then
  # Releasing the claim is best-effort: the claim itself is (see the
  # workflow) and an issue that keeps a stale assignee is still parked.
  gh issue edit "$ISSUE" --remove-assignee "$UNASSIGN" \
    || echo "::warning::could not un-assign $UNASSIGN from #$ISSUE"
fi

if [[ "$status" -eq 0 ]]; then
  echo "issue #$ISSUE parked $LABEL"
else
  echo "failed to fully park issue #$ISSUE" >&2
fi
exit "$status"
