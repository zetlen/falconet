#!/usr/bin/env bash
#
# review-verdict.sh — turn the review agent's final message into the files a
# workflow reads.
#
# UNWIRED, ON PURPOSE. There is no `falconet review-verdict` verb, no entry in
# bin/falconet's verb list, and no caller anywhere in this repository. It
# ships as the reference implementation of the verdict protocol and nothing
# else.
#
# ADR-0002 dropped the independent review agent on measurements: the watchdog
# cost ~44% of the worker on a small task, and two cold contexts cost more
# than one warm one. ADR-0001 risk 9 stands, including its bar for any
# future replacement — which this file is the record of, not an invitation.
# Anything that wires it up must first clear that bar: an independent,
# uncontaminated read of the diff, the commit message and the plan, before a
# human is asked to look. A review that reads the implementing agent's
# reasoning, or that shares its context, is not that.
#
# The invariant is asserted, not merely written down: the contract test
# requires this file be referenced zero times by the reusable workflow.
#
# The reviewing agent is granted exactly Read, Grep and Glob. No Bash, no
# Edit, no Write: it cannot run a command, cannot touch the working tree, and
# cannot reach GitHub — not by design accident but on purpose, because its
# only job is to look at the artifacts with fresh eyes and say yes or no.
# That also means it cannot put its own verdict on disk. So it ends its run
# with a sentinel line and this script does the filing.
#
# Modes:
#   ci-review-verdict.sh [--execution-file FILE] [--out-dir DIR]
#       Read the JSON message log claude-code-action writes (its
#       `execution_file` step output; default $RUNNER_TEMP/
#       claude-execution-output.json), take the text of the final `result`
#       message, and route it by the first SENTINEL LINE anywhere in it:
#
#           APPROVED           -> the rest is written to DIR/pr-body.md
#           CHANGES REQUESTED  -> the rest is written to DIR/rejection.md
#
#       On the APPROVED path "the rest" is normally EMPTY, and that is the
#       intended shape: the prompt tells the reviewer to say nothing after
#       the sentinel, because the pull-request description is the
#       implementing agent's commit message (DIR/commit-body.md), not
#       anything the reviewer writes. pr-body.md is kept as a record for
#       whoever is debugging a run; no stage reads it. rejection.md, by
#       contrast, is read and posted to the requester's issue.
#
#       DIR defaults to .ci-handoff/ at the root of this repository — the
#       pipeline's stage-to-stage handoff directory, gitignored, and where
#       ci-validate.sh has already put the plan and the commits. The execution
#       file is the one thing that does NOT live there: claude-code-action
#       chooses where to write its own log, and it writes it under
#       $RUNNER_TEMP. This script only ever reads it.
#
#       Surrounding markdown emphasis (#, *, `, _) and trailing punctuation
#       are stripped before matching, and the match is case-insensitive:
#       be liberal about the formatting, strict about the words.
#
# ---------------------------------------------------------------------------
# Why the whole message is scanned, and why the sentinel must stand alone
# ---------------------------------------------------------------------------
# This used to route on the FIRST NON-BLANK LINE only. The first live run of
# the staged pipeline (run 32093607680, issue #36) died on that. The reviewer
# approved — correctly, thoroughly, having read the entire patch — but opened
# with a line of preamble:
#
#     Confirmed there's only one commit in the patch (already read in full
#     above) touching only `people-employees.tf`.
#
#     APPROVED
#     This adds Ozamataz Buckshank, a new full-time hire, ...
#
# First non-blank line = the preamble, so: "unrecognized verdict sentinel" ->
# missing -> the issue was parked ready-for-human with a comment promising a
# prepared change, and the change was thrown away with the runner. A clean
# approval became a dead end because of one sentence of throat-clearing. The
# prompt already told that agent to put the sentinel first; it did it anyway,
# which is the whole argument for fixing the PARSER and not just the prompt.
#
# So the scan runs over every line and stops at the first one that IS a
# sentinel. "Is", not "starts with": the line must consist of nothing but the
# sentinel once emphasis and trailing punctuation are stripped. That
# strictness is what makes scanning the whole message safe — a reviewer who
# writes "I would have approved this if ..." in the middle of a rejection must
# not be read as approving, and a PR description that discusses approval must
# not hijack the verdict from three paragraphs up. The pre-#36 code matched a
# PREFIX (APPROVED*), which was tolerable while only line one was examined and
# is not tolerable across a whole document; a sentinel with commentary glued
# onto the same line is now unrecognized rather than guessed at.
#
# If both sentinels appear on their own lines, the first one wins. It is the
# reviewer's own document order and there is no better tie-break available;
# the alternative — refusing to route a message that contains both — turns a
# stated verdict into another "missing", which is the failure this section
# exists to record.
#
# Prints exactly one word on stdout — approved | rejected | missing.
# "missing" means no usable verdict was found (no execution file, no result
# message, or no recognizable sentinel). The caller MUST treat that as "not
# approved" and park the issue; guessing on a reviewer's behalf is the one
# thing this whole stage exists to prevent.
#
# Exit codes: 0 = a verdict word was printed (including "missing"),
#             2 = usage error.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)/.falconet"
EXEC_FILE=""

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --execution-file) EXEC_FILE="${2:?--execution-file needs a path}"; shift 2 ;;
    --out-dir)        OUT_DIR="${2:?--out-dir needs a directory}"; shift 2 ;;
    -h|--help)        usage >&2; exit 2 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

mkdir -p "$OUT_DIR" || exit 2
APPROVAL="$OUT_DIR/pr-body.md"
REJECTION="$OUT_DIR/rejection.md"
# A verdict from a previous review round must never be mistaken for this one.
rm -f "$APPROVAL" "$REJECTION"

# Not $OUT_DIR: the action's log is the action's business, and it puts it in
# $RUNNER_TEMP. The caller normally passes the step's execution_file output
# and this default never fires.
[[ -n "$EXEC_FILE" ]] || EXEC_FILE="${RUNNER_TEMP:-/tmp}/claude-execution-output.json"

if [[ ! -s "$EXEC_FILE" ]]; then
  echo "no execution log at $EXEC_FILE — the reviewer produced nothing" >&2
  echo missing
  exit 0
fi

final="$(jq -r '
  (if type == "array" then . else [.] end)
  | map(select(.type? == "result"))
  | last
  | if . == null then "" else (.result // "") end
' "$EXEC_FILE" 2>/dev/null)" || final=""

if [[ -z "${final//[[:space:]]/}" ]]; then
  echo "no final result message in $EXEC_FILE" >&2
  echo missing
  exit 0
fi

# Normalize EVERY line in one pass, then look for the sentinel. Be liberal
# about formatting: **APPROVED**, `APPROVED`, "## APPROVED", "Approved." all
# normalize to APPROVED. sed works a line at a time, so line numbering
# survives the transformation and a hit here indexes straight back into the
# untouched message. (`s/[[:space:]]+/ /g` on one line is what the old
# `tr -s '[:space:]' ' '` did to one line: same collapse, same result.)
normalized_all="$(printf '%s\n' "$final" \
  | tr -d '#*`_\r' \
  | tr '[:lower:]' '[:upper:]' \
  | sed -E 's/[[:space:]]+/ /g; s/^ +//; s/ +$//; s/[[:punct:]]+$//')"

# -x is load-bearing, not tidiness: it is the "alone on its own line" rule
# from the header, and it is the only thing standing between a whole-document
# scan and prose that merely talks about approving. One spelling of the
# alternation, used to find the line and never re-derived to classify it.
#
# -a because a reviewer's message is whatever the model emitted: one stray
# control byte and grep would decide the whole thing is binary and answer
# "Binary file (standard input) matches" instead of a line number.
SENTINELS='APPROVED|CHANGES REQUESTED|CHANGES-REQUESTED|CHANGES REQUIRED|REJECTED'
hit="$(grep -a -n -m1 -x -E "$SENTINELS" <<<"$normalized_all")"

if [[ -z "$hit" ]]; then
  # Quote the opening line: when a reviewer buries or mangles its verdict,
  # that line is what a human debugging the run needs to see.
  opener="$(grep -a -m1 -v '^[[:space:]]*$' <<<"$final")"
  echo "no verdict sentinel on a line of its own: ${opener:0:120}" >&2
  echo missing
  exit 0
fi
sentinel_no="${hit%%:*}"
normalized="${hit#*:}"

body="$(printf '%s\n' "$final" | tail -n +"$((sentinel_no + 1))" \
  | sed -E '/./,$!d')"   # drop leading blank lines, keep everything else

# No catch-all: grep -x above already refused everything that is not one of
# the sentinels, so the two arms below are exhaustive by construction and the
# `*)` arm exists only to make a future edit to $SENTINELS fail loudly here
# instead of silently filing a new verdict word as a rejection.
case "$normalized" in
  APPROVED)
    # An empty body is the EXPECTED shape of an approval now, not a defect:
    # the prompt tells the reviewer to say nothing after APPROVED, and the
    # pull-request description is the implementing agent's commit message,
    # assembled from .ci-handoff/commit-body.md. Nothing reads this file any
    # more. There was a stand-in paragraph and a `::warning::` here; both
    # would have fired on every successful run from now on, which is warning
    # fatigue on the happy path. The file is still written — an approval that
    # did carry prose is worth keeping in the handoff directory for whoever
    # is debugging a run.
    printf '%s\n' "$body" >"$APPROVAL"
    echo approved
    ;;
  "CHANGES REQUESTED"|CHANGES-REQUESTED|"CHANGES REQUIRED"|REJECTED)
    if [[ -z "${body//[[:space:]]/}" ]]; then
      body="The reviewing agent rejected this change but gave no reasons."
    fi
    printf '%s\n' "$body" >"$REJECTION"
    echo rejected
    ;;
  *)
    echo "sentinel \"$normalized\" matched the scan but no arm of the case — \$SENTINELS and the case below have drifted apart" >&2
    echo missing
    ;;
esac
exit 0
