#!/usr/bin/env bash
#
# What the verdict parser must do to a reviewing agent's final message.
#
# The first case is the whole reason this file exists: the verbatim message
# from run 32093607680 (issue #36), where a correct, thorough approval was
# read as no verdict at all because the reviewer wrote one line of preamble
# above its sentinel. The issue was parked ready-for-human and the change was
# destroyed with the runner. It is a fixture now, kept byte-for-byte in
# tests/fixtures/, so no future rewrite of the parser can quietly stop
# handling it.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

FIXTURES="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/fixtures"

# Each case gets its own out-dir, so a file left behind by one is never
# mistaken for a file written by the next.
run_verdict() { # message-source out-dir-name  -> prints the verdict word
  local out="$WORK/$2"
  mkdir -p "$out"
  "$FALCONET" review-verdict --execution-file "$1" --out-dir "$out" 2>"$out/stderr.txt"
}

# --- the incident -----------------------------------------------------------

it "run 32093607680: APPROVED under a line of preamble is an approval"
log="$WORK/issue36.json"
execution_log_from "$FIXTURES/review-final-message-run-32093607680.txt" "$log"
verdict="$(run_verdict "$log" issue36)"
assert_eq approved "$verdict" "verdict"

it "run 32093607680: the PR body starts after the sentinel, not before it"
body="$(cat "$WORK/issue36/pr-body.md")"
case "$body" in
  "This adds Ozamataz Buckshank"*) _pass ;;
  *) _fail "pr-body.md should open with the first line under APPROVED" \
           "got: [${body:0:120}]" ;;
esac

it "run 32093607680: the preamble is not in the PR body"
assert_not_contains "$body" "Confirmed there's only one commit" "pr-body.md"

it "run 32093607680: the rest of the description survives verbatim"
assert_contains "$body" "people-count bump from 7 to 8. Nothing else changes." "pr-body.md"

it "run 32093607680: no rejection file is written"
assert_file_missing "$WORK/issue36/rejection.md"

# --- the shape the prompt actually asks for ---------------------------------

it "sentinel on the true first line still approves"
execution_log_of "APPROVED
Adds one employee. Nothing else.

What the plan shows: three new resources." "$WORK/first-line.json"
assert_eq approved "$(run_verdict "$WORK/first-line.json" firstline)" "verdict"

it "sentinel on the true first line: body is everything after it"
assert_eq "Adds one employee. Nothing else.

What the plan shows: three new resources." "$(cat "$WORK/firstline/pr-body.md")" "pr-body.md"

# --- rejection, also under a preamble ---------------------------------------

it "CHANGES REQUESTED under a preamble is a rejection"
execution_log_of "Let me summarize what I checked before giving the verdict.

CHANGES REQUESTED

The record was added to dns/records-papernapkin-tech.tf with a ttl of 30,
which guards.tf refuses. Use a ttl in range." "$WORK/changes.json"
assert_eq rejected "$(run_verdict "$WORK/changes.json" changes)" "verdict"

it "CHANGES REQUESTED: the reasons reach rejection.md, the preamble does not"
rej="$(cat "$WORK/changes/rejection.md")"
assert_contains "$rej" "which guards.tf refuses. Use a ttl in range." "rejection.md"

it "CHANGES REQUESTED: no PR body is written"
assert_file_missing "$WORK/changes/pr-body.md"

# --- both sentinels present -------------------------------------------------

it "both sentinels: the first one on a line of its own wins (rejection first)"
execution_log_of "CHANGES REQUESTED

I would have written APPROVED if the TTL were in range.

APPROVED" "$WORK/both-rej.json"
assert_eq rejected "$(run_verdict "$WORK/both-rej.json" bothrej)" "verdict"

it "both sentinels: the first one on a line of its own wins (approval first)"
execution_log_of "APPROVED

This is fine. Had it touched a guard the verdict would have been

CHANGES REQUESTED" "$WORK/both-app.json"
assert_eq approved "$(run_verdict "$WORK/both-app.json" bothapp)" "verdict"

# --- what must NOT be read as a verdict -------------------------------------
#
# The scan runs over the whole message now, so the "alone on its own line"
# rule is the only thing keeping prose about approving from becoming an
# approval. These cases are that rule.

it "prose mentioning approval with no standalone sentinel is missing"
execution_log_of "I have approved changes like this one in the past, and this change is
approved in spirit, but the TTL is out of range so I cannot say
APPROVED here without qualification. Approved subject to that fix." \
  "$WORK/prose.json"
assert_eq missing "$(run_verdict "$WORK/prose.json" prose)" "verdict"

it "prose mentioning approval writes neither verdict file"
assert_file_missing "$WORK/prose/pr-body.md"

it "prose mentioning approval: stderr says why"
assert_contains "$(cat "$WORK/prose/stderr.txt")" "no verdict sentinel on a line of its own" "stderr"

it "a sentinel sharing its line with commentary is not a verdict"
execution_log_of "APPROVED - looks good to me, ship it." "$WORK/inline.json"
assert_eq missing "$(run_verdict "$WORK/inline.json" inline)" "verdict"

# --- liberal about formatting, strict about the words -----------------------

it "markdown emphasis and trailing punctuation still normalize"
execution_log_of "Checked the patch and the plan.

## **Approved.**

Adds one employee." "$WORK/fancy.json"
assert_eq approved "$(run_verdict "$WORK/fancy.json" fancy)" "verdict"

it "a CRLF message still matches"
printf 'Preamble line.\r\n\r\nAPPROVED\r\nAdds one employee.\r\n' >"$WORK/crlf.txt"
execution_log_from "$WORK/crlf.txt" "$WORK/crlf.json"
assert_eq approved "$(run_verdict "$WORK/crlf.json" crlf)" "verdict"

it "an approval with nothing after it is still an approval"
# This is the shape the prompt now ASKS for — "if APPROVED: say nothing more"
# — because the pull-request description is the implementing agent's commit
# message. It is the happy path, not a defect.
execution_log_of "APPROVED" "$WORK/bare.json"
assert_eq approved "$(run_verdict "$WORK/bare.json" bare)" "verdict"

it "a bare approval invents no stand-in description"
assert_eq "" "$(cat "$WORK/bare/pr-body.md")" "pr-body.md"

it "a bare approval is not warned about, since nothing is wrong with it"
# A ::warning:: here would fire on every successful run forever.
assert_eq "" "$(cat "$WORK/bare/stderr.txt")" "stderr"

# --- nothing to read at all -------------------------------------------------

it "no execution file is missing, not a crash"
assert_eq missing "$(run_verdict "$WORK/does-not-exist.json" noexec)" "verdict"

it "an execution log with no result message is missing"
jq -n '[{type: "system"}]' >"$WORK/noresult.json"
assert_eq missing "$(run_verdict "$WORK/noresult.json" noresult)" "verdict"

it "an empty final message is missing"
execution_log_of "" "$WORK/empty.json"
assert_eq missing "$(run_verdict "$WORK/empty.json" empty)" "verdict"

# --- round two must not inherit round one's verdict -------------------------

it "a stale pr-body.md from a previous round is cleared"
mkdir -p "$WORK/stale"
echo "round one's approval" >"$WORK/stale/pr-body.md"
"$FALCONET" review-verdict --execution-file "$WORK/changes.json" --out-dir "$WORK/stale" >/dev/null 2>&1
assert_file_missing "$WORK/stale/pr-body.md"

it "-h/--help is a usage error"
"$FALCONET" review-verdict --help >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

# The protocol ships unwired (ADR-0002; ADR-0001 risk 9). "Unwired" was never
# about the dispatcher — the script was always runnable by path — it means not
# in usage and never invoked by the workflow. contract.test.sh holds the
# workflow half (zero references); this is the usage half. The dispatcher
# reaches it as an unlisted subcommand, like `prompt`, since ADR-0006 D3
# step 0, and #18 ports it the same way: "ported and unwired".
it "review-verdict is not vocabulary: usage does not mention it"
"$FALCONET" -h >/dev/null 2>"$WORK/usage.txt"
assert_not_contains "$(cat "$WORK/usage.txt")" "review-verdict" "usage"

it "but it is dispatched, not refused as an unknown verb"
out=$( cd "$WORK" && "$FALCONET" review-verdict \
         --execution-file "$WORK/changes.json" --out-dir "$WORK/dispatched" 2>&1 ); rc=$?
assert_not_contains "$out" "unknown verb" "dispatcher output"
[[ "$rc" -eq 2 ]] && reached=refused || reached=dispatched
assert_eq dispatched "$reached" "exit code $rc"

summary
