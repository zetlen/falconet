#!/usr/bin/env bash
#
# summary.test.sh — the panel on the run's page, from outside the process.
#
# What the panel says, and that nothing from outside renders in it, is
# internal/summary's to hold. What only a process shows is here: the panel
# lands in $GITHUB_STEP_SUMMARY, appended, and the verb exits 0 whatever it
# was handed, because a report that failed its step would change the run it
# reports.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

needs='{"gate":{"result":"success","outputs":{"outcome":"ready","branch":"issue-42-add-a-line"}},
"implement":{"result":"success","outputs":{"outcome":"failure","check":"pass","passes":"1","kind":"paths"}},
"publish":{"result":"success","outputs":{}}}'

run_summary() { # file [args...] -> sets OUT ERR RC
  local file="$1"; shift
  OUT="$( env -i PATH="$PATH" GITHUB_STEP_SUMMARY="$file" GITHUB_SERVER_URL=https://github.com GITHUB_REPOSITORY=acme/infra \
    FALCONET_ISSUE=42 FALCONET_MAX_ATTEMPTS=3 FALCONET_NEEDS="$needs" \
    "$FALCONET" summary "$@" 2>"$WORK/err" )"; RC=$?
  ERR="$(cat "$WORK/err")"
}

printf 'written earlier in the job\n' >"$WORK/panel.md"
run_summary "$WORK/panel.md" --job contain

it "the panel is appended to \$GITHUB_STEP_SUMMARY, after what the job wrote before"
assert_eq 0 "$RC" "exit code"
assert_eq "written earlier in the job" "$(head -1 "$WORK/panel.md")" "the first line"
assert_contains "$(cat "$WORK/panel.md")" "### falconet: refused by a guard, the path allowlist" "the panel"
assert_eq "" "$OUT" "stdout"

run_summary "" --job contain
it "with no \$GITHUB_STEP_SUMMARY the panel is on stdout"
assert_eq 0 "$RC" "exit code"
assert_contains "$OUT" "- **Issue:** [#42](https://github.com/acme/infra/issues/42)" "stdout"

run_summary "$WORK/no-such-dir/panel.md" --job contain
it "a summary file that cannot be written is still exit 0, and says so"
assert_eq 0 "$RC" "exit code"
assert_contains "$ERR" "cannot open \$GITHUB_STEP_SUMMARY" "stderr"

needs='{"gate": not json'
run_summary "$WORK/garbage.md" --job contain
it "contexts that are not JSON are still a panel, exit 0"
assert_eq 0 "$RC" "exit code"
assert_contains "$(cat "$WORK/garbage.md")" "### falconet: " "the panel"

run_summary "$WORK/nojob.md" --job nightly
it "a job it does not know is the fallback panel, exit 0"
assert_eq 0 "$RC" "exit code"
assert_contains "$(cat "$WORK/nojob.md")" "### falconet: no summary" "the panel"

run_summary "$WORK/bogus.md" --bogus
it "an unknown argument is usage on stderr, exit 0, and no panel"
assert_eq 0 "$RC" "exit code"
assert_contains "$ERR" "unknown argument: --bogus" "stderr"
assert_file_missing "$WORK/bogus.md"

summary
