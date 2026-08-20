#!/usr/bin/env bash
#
# ci-pr-body.sh — assemble a pull-request body that always ships the WHOLE
# plan.
#
# PR #28 shipped a plan the agent had abridged by hand: literal
# "# ... omitted here for length" comments inside the code fence. AGENTS.md
# requires every PR to carry pasted `tofu plan` output, and the human who
# approved that one was reading the agent's summary of the evidence instead
# of the evidence. No prompt fixes that reliably. Assembling the body
# mechanically from the plan file removes the opportunity: the agent writes
# prose, this script attaches the plan, and the two never mix.
#
# Modes:
#   ci-pr-body.sh --body FILE --plan FILE --issue N --out FILE
#                 [--run-url URL] [--plan-url URL] [--limit N]
#
#     --body     PR description with NO plan output in it — the body of the
#                implementing agent's commit message, whose prompt tells it
#                not to quote, summarize or abridge the plan
#     --plan     full `tofu plan -no-color` output
#     --issue    issue number; a "Closes #N" line is appended after the body
#     --out      destination for the assembled markdown
#     --run-url  workflow run URL, cited by the truncation note when
#                --plan-url is absent
#     --plan-url download URL for the plan uploaded as a workflow artifact
#                (see infra-issues.yml's "Upload the plan artifact" step,
#                added for issue #46). Optional: every caller that omits it
#                gets exactly today's output, byte for byte. When given, its
#                link is always printed next to the plan block — even when
#                the plan fit inline — so a reviewer never has to fall back
#                to the run log to get the untruncated file. On overflow, the
#                truncation note cites THIS url instead of the run log: a
#                direct download beats sending a human hunting through a
#                step's log output for the same text.
#     --limit    maximum body size (default 65536, GitHub's hard limit)
#
# The plan is wrapped in <details><summary>tofu plan output</summary> and a
# fence long enough to survive any backticks the plan itself contains.
#
# If the assembled body would exceed --limit, the PLAN is truncated — never
# the description, never the "Closes" line. The truncation is deterministic:
# whole lines only, keeping the first 70% and last 30% of the remaining
# budget, with the elision replaced by a note that states how many lines were
# dropped and where the untruncated plan can be read (--plan-url if given,
# else the run log, where ci-validate.sh echoes it in full). Nothing is ever
# dropped silently and nothing is ever summarized.
#
# Sizes are counted in bytes, which is conservative: GitHub's limit is in
# characters, and a byte count can only ever make us truncate sooner.
#
# Exit codes: 0 = written, 1 = the description alone exceeds --limit
#             (nothing written), 2 = usage error.

set -euo pipefail

BODY=""
PLAN=""
ISSUE=""
OUT=""
RUN_URL=""
PLAN_URL=""
LIMIT=65536

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --body)    BODY="${2:?--body needs a file}"; shift 2 ;;
    --plan)    PLAN="${2:?--plan needs a file}"; shift 2 ;;
    --issue)   ISSUE="${2:?--issue needs a number}"; shift 2 ;;
    --out)     OUT="${2:?--out needs a file}"; shift 2 ;;
    --run-url) RUN_URL="${2:?--run-url needs a URL}"; shift 2 ;;
    --plan-url) PLAN_URL="${2:?--plan-url needs a URL}"; shift 2 ;;
    --limit)   LIMIT="${2:?--limit needs a number}"; shift 2 ;;
    -h|--help) usage >&2; exit 2 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for required in BODY PLAN ISSUE OUT; do
  [[ -n "${!required}" ]] || { usage >&2; exit 2; }
done
[[ -f "$BODY" ]] || { echo "no such body file: $BODY" >&2; exit 2; }
[[ -f "$PLAN" ]] || { echo "no such plan file: $PLAN" >&2; exit 2; }
[[ "$ISSUE" =~ ^[0-9]+$ ]] || { echo "--issue must be a number" >&2; exit 2; }
[[ "$LIMIT" =~ ^[0-9]+$ ]] || { echo "--limit must be a number" >&2; exit 2; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Normalize the plan: guarantee a trailing newline so the closing fence never
# ends up glued to the last plan line.
cp "$PLAN" "$work/plan.txt"
if [[ -s "$work/plan.txt" ]] && [[ "$(tail -c1 "$work/plan.txt" | wc -l)" -eq 0 ]]; then
  printf '\n' >>"$work/plan.txt"
fi

# A fence one backtick longer than the longest run of backticks in the plan.
# (`|| true`: grep exits 1 on no match, and pipefail would take that as fatal.)
longest="$( { grep -oE '`+' "$work/plan.txt" || true; } \
  | awk '{ if (length($0) > n) n = length($0) } END { print n + 0 }')"
fence_len=3
if [[ "$longest" -ge 3 ]]; then fence_len=$((longest + 1)); fi
fence="$(printf '%*s' "$fence_len" '' | tr ' ' '`')"

# Printed whether or not the plan below needs truncating: a body that fits
# still benefits from a one-click download of the exact same file, and a
# reviewer should never have to notice truncation before finding the link.
plan_link=""
if [[ -n "$PLAN_URL" ]]; then
  plan_link="$(printf 'Full plan output (workflow artifact, 30-day retention): %s\n\n' "$PLAN_URL")"
fi

{
  cat "$BODY"
  printf '\nCloses #%s\n\n%s<details><summary>tofu plan output</summary>\n\n%s\n' \
    "$ISSUE" "$plan_link" "$fence"
} >"$work/head.md"

printf '%s\n\n</details>\n' "$fence" >"$work/foot.md"

overhead=$(( $(wc -c <"$work/head.md") + $(wc -c <"$work/foot.md") ))
plan_bytes="$(wc -c <"$work/plan.txt")"
budget=$(( LIMIT - overhead ))

if [[ "$budget" -le 0 ]]; then
  echo "the description alone is $overhead bytes, over the $LIMIT limit — refusing to truncate a human-facing description" >&2
  exit 1
fi

if [[ "$plan_bytes" -le "$budget" ]]; then
  cat "$work/head.md" "$work/plan.txt" "$work/foot.md" >"$OUT"
  echo "PR body: $(wc -c <"$OUT" | tr -d " ") bytes, full plan attached ($(wc -l <"$work/plan.txt" | tr -d " ") lines)"
  exit 0
fi

# Truncating. Reserve a fixed slice of the budget for the note so its own
# length never has to be solved for; coming in under the limit is fine,
# going over is not.
NOTE_RESERVE=640
avail=$(( budget - NOTE_RESERVE ))
[[ "$avail" -ge 0 ]] || avail=0
head_budget=$(( avail * 70 / 100 ))
tail_budget=$(( avail - head_budget ))

# head -c / tail -c can land mid-line; drop the partial line at the seam so
# the kept text is always whole lines.
head -c "$head_budget" "$work/plan.txt" | sed '$d' >"$work/plan-head.txt"
tail -c "$tail_budget" "$work/plan.txt" | sed '1d' >"$work/plan-tail.txt"

total_lines="$(wc -l <"$work/plan.txt" | tr -d " ")"
kept_lines=$(( $(wc -l <"$work/plan-head.txt") + $(wc -l <"$work/plan-tail.txt") ))
dropped=$(( total_lines - kept_lines ))
[[ "$dropped" -ge 0 ]] || dropped=0

# A direct download beats a pointer into a log a human has to search through
# — so the artifact URL wins whenever one was given, and the run log is only
# a fallback for callers that never got one.
if [[ -n "$PLAN_URL" ]]; then
  how="downloadable in full, unredacted, as a workflow artifact (30-day retention) at:"
  where="$PLAN_URL"
else
  how="printed in the \"Validate\" step of:"
  where="the workflow run log for this pull request"
  [[ -z "$RUN_URL" ]] || where="$RUN_URL"
fi

{
  echo
  echo "[ ---------------------------------------------------------------- ]"
  echo "[ $dropped of $total_lines lines of plan output are omitted HERE so that this"
  echo "[ pull-request body fits GitHub's $LIMIT-character limit. They were"
  echo "[ neither summarized nor rewritten. The complete, untruncated plan is"
  echo "[ $how"
  echo "[   $where"
  echo "[ ---------------------------------------------------------------- ]"
  echo
} >"$work/note.txt"

cat "$work/head.md" "$work/plan-head.txt" "$work/note.txt" \
    "$work/plan-tail.txt" "$work/foot.md" >"$OUT"

final_bytes="$(wc -c <"$OUT" | tr -d " ")"
echo "PR body: $final_bytes bytes, plan truncated ($dropped of $total_lines lines elided, note points at $where)"
if [[ "$final_bytes" -gt "$LIMIT" ]]; then
  echo "assembled body is still $final_bytes bytes, over the $LIMIT limit" >&2
  exit 1
fi
