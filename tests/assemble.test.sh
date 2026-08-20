#!/usr/bin/env bash
#
# What ci-pr-body.sh must do to a description and a plan, with and without
# --plan-url (issue #46).
#
# PR #43 added ONE TXT record and the assembled body came to 36,556 bytes —
# 56% of GitHub's 65536-byte limit, 36,156 of it the plan. namecheap_domain_
# records holds a whole zone's records in one resource, so one changed record
# re-renders the entire zone as a diff; that overflow is the case this file
# exists to cover, with a synthetic plan built here rather than a real `tofu`
# run. Nothing here touches the network, git, or tofu.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

SCRIPT="$REPO_ROOT/libexec/falconet/assemble.sh"

# A plan of exactly N fixed-width lines, each independently identifiable and
# byte-comparable, so truncation can be checked for whole-line boundaries
# without re-implementing the script's own arithmetic. Every line is 51
# bytes including its newline: "LINE nnnn " (10 bytes) + 40 "x"s + "\n".
synthetic_plan() { # n -> path
  local n="$1"
  local out="$WORK/plan-$n.txt"
  local suffix
  suffix="$(printf 'x%.0s' $(seq 1 40))"
  : >"$out"
  for ((i = 1; i <= n; i++)); do
    printf 'LINE %04d %s\n' "$i" "$suffix" >>"$out"
  done
  printf '%s' "$out"
}

run_body() { # args... -> stdout of the script; sets RC
  local out
  out="$("$SCRIPT" "$@" 2>"$WORK/stderr.txt")"
  RC=$?
  printf '%s' "$out"
}

# =============================================================================
# a plan that fits
# =============================================================================

c="$WORK/fits"; mkdir -p "$c"
printf 'This is the human-authored description.\n\nIt spans two paragraphs.\n' \
  >"$c/body.md"
printf 'resource line one\nresource line two\nresource line three\n' >"$c/plan.txt"

run_body --body "$c/body.md" --plan "$c/plan.txt" --issue 43 \
  --run-url "https://example.invalid/runs/1" \
  --plan-url "https://example.invalid/artifacts/plan-43" \
  --out "$c/out.md" >/dev/null
out="$(cat "$c/out.md")"

it "a fitting plan: exit code is 0"
assert_eq 0 "$RC" "exit code"

it "a fitting plan: the description survives verbatim"
assert_contains "$out" "This is the human-authored description." "assembled body"

it "a fitting plan: the Closes line is present"
assert_contains "$out" "Closes #43" "assembled body"

it "a fitting plan: the whole plan is inline"
assert_contains "$out" "resource line one" "assembled body"
assert_contains "$out" "resource line three" "assembled body"

it "a fitting plan: the artifact link is present even though nothing was truncated"
assert_contains "$out" "https://example.invalid/artifacts/plan-43" "assembled body"

it "a fitting plan: the plan is not truncated"
assert_not_contains "$out" "are omitted HERE" "assembled body"

# =============================================================================
# --plan-url omitted: byte-identical to the unflagged construction
# =============================================================================
#
# ci-pr-body.sh builds the body as: description, "Closes #N", an (empty,
# without --plan-url) artifact-link line, then the <details> plan block. That
# is exactly the pre-#46 assembly, reproduced here by hand rather than by
# diffing against a saved copy of the script, so this case keeps testing the
# real contract even if the script is rewritten later.

c="$WORK/omitted"; mkdir -p "$c"
printf 'Add an SPF record.\n\nRequested by the domain owner.\n' >"$c/body.md"
printf 'resource line one\nresource line two\n' >"$c/plan.txt"

run_body --body "$c/body.md" --plan "$c/plan.txt" --issue 7 --out "$c/out.md" >/dev/null

{
  cat "$c/body.md"
  printf '\nCloses #%s\n\n<details><summary>tofu plan output</summary>\n\n%s\n' 7 '```'
  cat "$c/plan.txt"
  printf '%s\n\n</details>\n' '```'
} >"$c/expected.md"

it "--plan-url omitted: exit code is 0"
assert_eq 0 "$RC" "exit code"

it "--plan-url omitted: output is byte-identical to today's construction"
assert_eq "$(cat "$c/expected.md")" "$(cat "$c/out.md")" "assembled body"

it "--plan-url omitted: no artifact-link line is introduced"
assert_not_contains "$(cat "$c/out.md")" "workflow artifact" "assembled body"

# =============================================================================
# a plan that overflows
# =============================================================================

c="$WORK/overflow"; mkdir -p "$c"
printf 'This description must survive intact.\n\nSecond paragraph, also intact.\n' \
  >"$c/body.md"
plan="$(synthetic_plan 2000)"
cp "$plan" "$c/plan.txt"

run_body --body "$c/body.md" --plan "$c/plan.txt" --issue 46 --limit 5000 \
  --run-url "https://example.invalid/runs/99" \
  --plan-url "https://example.invalid/artifacts/plan-46" \
  --out "$c/out.md" >/dev/null
out="$(cat "$c/out.md")"

it "an overflowing plan: exit code is 0"
assert_eq 0 "$RC" "exit code"

it "an overflowing plan: the description survives intact"
assert_contains "$out" "This description must survive intact." "assembled body"
assert_contains "$out" "Second paragraph, also intact." "assembled body"

it "an overflowing plan: the Closes line still lands"
assert_contains "$out" "Closes #46" "assembled body"

it "an overflowing plan: the note names how many lines were dropped, of how many total"
assert_contains "$out" "of 2000 lines of plan output are omitted" "assembled body"

it "an overflowing plan: the note cites the artifact URL, not the run log"
assert_contains "$out" "https://example.invalid/artifacts/plan-46" "assembled body"
assert_not_contains "$out" "https://example.invalid/runs/99" "assembled body"
assert_not_contains "$out" "Validate" "assembled body"

it "an overflowing plan: only whole lines are kept, none of them a partial fragment"
# Every kept plan line matches the fixed-width pattern exactly; a line cut
# mid-way would either be missing its trailing x-suffix or be glued to
# neighbouring text and would fail this pattern.
suffix="$(printf 'x%.0s' $(seq 1 40))"
bad="$(grep -E '^LINE ' "$c/out.md" | grep -vE "^LINE [0-9]{4} ${suffix}\$" || true)"
assert_eq "" "$bad" "malformed plan lines in output"

it "an overflowing plan: the kept lines are a run from the start and a run to the end"
first_kept="$(grep -E '^LINE ' "$c/out.md" | head -1)"
last_kept="$(grep -E '^LINE ' "$c/out.md" | tail -1)"
assert_eq "LINE 0001 $suffix" "$first_kept" "first kept line"
assert_eq "LINE 2000 $suffix" "$last_kept" "last kept line"

it "an overflowing plan: dropped-plus-kept accounts for every line"
dropped="$(grep -oE '[0-9]+ of 2000 lines of plan output are omitted' "$c/out.md" \
  | grep -oE '^[0-9]+')"
kept="$(grep -cE '^LINE ' "$c/out.md")"
assert_eq 2000 "$((dropped + kept))" "dropped + kept"

it "an overflowing plan: something was actually dropped"
assert_eq "yes" "$([[ "$dropped" -gt 0 ]] && echo yes || echo no)" "dropped > 0"

# --- overflow, no --plan-url: falls back to the run-url note, unchanged -----

c="$WORK/overflow_run_url_only"; mkdir -p "$c"
printf 'Description.\n' >"$c/body.md"
plan="$(synthetic_plan 2000)"
cp "$plan" "$c/plan.txt"

run_body --body "$c/body.md" --plan "$c/plan.txt" --issue 46 --limit 5000 \
  --run-url "https://example.invalid/runs/99" \
  --out "$c/out.md" >/dev/null
out="$(cat "$c/out.md")"

it "overflow without --plan-url: the note falls back to the run-url, as before"
assert_contains "$out" 'printed in the "Validate" step of:' "assembled body"
assert_contains "$out" "https://example.invalid/runs/99" "assembled body"

# --- overflow, no --plan-url and no --run-url: falls back to the generic note

c="$WORK/overflow_no_urls"; mkdir -p "$c"
printf 'Description.\n' >"$c/body.md"
plan="$(synthetic_plan 2000)"
cp "$plan" "$c/plan.txt"

run_body --body "$c/body.md" --plan "$c/plan.txt" --issue 46 --limit 5000 \
  --out "$c/out.md" >/dev/null
out="$(cat "$c/out.md")"

it "overflow with neither URL: the note names the run log generically"
assert_contains "$out" "the workflow run log for this pull request" "assembled body"

# =============================================================================
# exit codes
# =============================================================================

c="$WORK/exit1"; mkdir -p "$c"
printf 'A description.\n' >"$c/body.md"
printf 'A plan.\n' >"$c/plan.txt"
run_body --body "$c/body.md" --plan "$c/plan.txt" --issue 1 --limit 10 \
  --out "$c/out.md" >/dev/null

it "the description alone over --limit exits 1"
assert_eq 1 "$RC" "exit code"

it "and nothing is written"
assert_file_missing "$c/out.md"

it "and stderr explains why"
assert_contains "$(cat "$WORK/stderr.txt")" "over the" "stderr"

it "an unknown argument exits 2"
"$SCRIPT" --bogus >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

it "a missing required argument exits 2"
"$SCRIPT" --body "$c/body.md" --plan "$c/plan.txt" --out "$c/out2.md" >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

it "-h/--help is a usage error, not a written body"
"$SCRIPT" --help >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

summary
