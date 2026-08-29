#!/usr/bin/env bash
#
# check.test.sh — the repository's own check, run on the tree the agent left.
#
# The verb runs one command, once, and prints one word. Everything here is
# what the caller — the workflow's unrolled attempts, or a shell loop on a
# workstation — can see from outside: the word, the exit code, and
# check-failure.txt in the handoff directory. The command is a bash one-liner
# in every case, because what the check IS is the operator's business and
# what the verb does with its answer is this file's.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null

# A checkout with one base commit, a committed config naming the check the
# case wants, and an empty handoff directory.
new_checkout() { # name command-json -> echoes the checkout path
  local base="$WORK/$1"
  mkdir -p "$base/repo/.falconet" "$base/repo/.github"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  printf 'locals {\n  a = 1\n}\n' >"$base/repo/records-example-tech.tf"
  printf '.falconet/\n' >"$base/repo/.gitignore"
  printf '{"paths":{"allow":["*.tf"]},"check":{"command":%s}}\n' "$2" \
    >"$base/repo/.github/falconet.json"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" switch -qc issue-1-thing
  printf '%s' "$base"
}

run_in() { # checkout [args...] -> sets OUT ERR RC
  local c="$1"; shift
  OUT="$( cd "$c/repo" && "$FALCONET" check --out-dir "$c/repo/.falconet" "$@" 2>"$c/err" )"; RC=$?
  ERR="$(cat "$c/err")"
  return 0
}

# --- no check configured ----------------------------------------------------

c="$(new_checkout none '[]')"
printf 'stale\n' >"$c/repo/.falconet/check-failure.txt"
run_in "$c"

it "with no check.command the word is skipped, and the exit code is 0"
assert_eq "skipped" "$OUT" "outcome"
assert_eq 0 "$RC" "exit code"

it "and the run log says so, so a misspelled key is a line in every run rather than a check that never happens"
assert_contains "$ERR" "no check configured" "stderr"

it "and a failure file a previous check left is removed: nothing failed"
assert_file_missing "$c/repo/.falconet/check-failure.txt"

# --- pass ---------------------------------------------------------------------

c="$(new_checkout pass '["bash","-c","echo this is the check talking; exit 0"]')"
printf 'stale\n' >"$c/repo/.falconet/check-failure.txt"
run_in "$c"

it "a command that exits 0 is pass, exit 0"
assert_eq "pass" "$OUT" "outcome"
assert_eq 0 "$RC" "exit code"

it "and its output goes to stderr — the run log — never to stdout, which is one word"
assert_contains "$ERR" "this is the check talking" "stderr"

it "and the failure file from the last check goes: its presence means the LAST check failed"
assert_file_missing "$c/repo/.falconet/check-failure.txt"

# --- fail ---------------------------------------------------------------------

c="$(new_checkout fail '["bash","-c","echo on stdout; echo on stderr >&2; exit 3"]')"
run_in "$c"

it "a command that exits non-zero is fail — and exit 0, because a word was printed and the caller routes on it"
assert_eq "fail" "$OUT" "outcome"
assert_eq 0 "$RC" "exit code"

it "and check-failure.txt names the command and how it ended"
report="$(cat "$c/repo/.falconet/check-failure.txt" 2>/dev/null)"
assert_contains "$report" "command: bash -c echo on stdout; echo on stderr >&2; exit 3" "report"
assert_contains "$report" "ended:   exit status 3" "report"

it "and carries both of the command's streams, in one place, as the agent will read them"
assert_contains "$report" "on stdout" "report"
assert_contains "$report" "on stderr" "report"

it "while the run log got them too"
assert_contains "$ERR" "on stdout" "stderr"
assert_contains "$ERR" "on stderr" "stderr"

# --- the tail is bounded, and says what it dropped ----------------------------
#
# The file is read by an agent in a fresh context. A megabyte of passing
# tests followed by the one that failed is worse than the last of them, so
# the end is kept, up to a budget, and the note says how much of the
# beginning is not there and where it is.

c="$(new_checkout chatty '["bash","-c","for i in $(seq 1 20000); do echo line number $i of a long log; done; echo THE VERDICT; exit 1"]')"
run_in "$c"

it "a check that prints more than the budget still says fail"
assert_eq "fail" "$OUT" "outcome"

it "and the file keeps the end, where the verdict is"
report="$(cat "$c/repo/.falconet/check-failure.txt")"
assert_contains "$report" "THE VERDICT" "report"

it "and not the beginning"
assert_not_contains "$report" "line number 1 of" "report"

it "and says how much is not there, and that the run log has all of it"
assert_contains "$report" "bytes are not
here; the run log has all of it" "report"

it "and stays inside the budget: 64 KiB of output plus a short header"
size="$(wc -c <"$c/repo/.falconet/check-failure.txt")"
assert_eq "true" "$([[ "$size" -le $((64 * 1024 + 512)) ]] && echo true || echo false)" "size $size within 64 KiB + header"

it "and resumes on a line boundary, never mid-line"
first_output_line="$(awk '/run log has all of it:/ { getline; getline; print; exit }' "$c/repo/.falconet/check-failure.txt")"
assert_contains "$first_output_line" "line number " "first kept line"

# --- the command runs from the repository root, whatever the caller's cwd ----

c="$(new_checkout root '["bash","-c","test -f records-example-tech.tf && test -f .github/falconet.json"]')"
mkdir -p "$c/repo/deep/er"
OUT="$( cd "$c/repo/deep/er" && "$FALCONET" check 2>/dev/null )"; RC=$?

it "the check runs from the repository root, not from where the verb was invoked"
assert_eq "pass" "$OUT" "outcome"

it "and with no --out-dir the file goes to the configured handoff directory"
c="$(new_checkout root_fail '["false"]')"
OUT="$( cd "$c/repo" && "$FALCONET" check 2>/dev/null )"
assert_eq "fail" "$OUT" "outcome"
assert_contains "$(cat "$c/repo/.falconet/check-failure.txt" 2>/dev/null)" "command: false" "report"

# --- mechanical failures: no word, exit 1 -------------------------------------
#
# A check that did not happen is not a pass and not a failure the agent can
# do anything about. Nothing on stdout, so a caller reading the word reads
# nothing, and the exit code fails the step.

c="$(new_checkout notfound '["no-such-command-anywhere-on-this-machine","--flag"]')"
run_in "$c"

it "a command that cannot be started is exit 1"
assert_eq 1 "$RC" "exit code"

it "with no word on stdout"
assert_eq "" "$OUT" "stdout"

it "and stderr names the command"
assert_contains "$ERR" "could not run" "stderr"
assert_contains "$ERR" "no-such-command-anywhere-on-this-machine" "stderr"

it "and no failure file is written: the agent has nothing to iterate on"
assert_file_missing "$c/repo/.falconet/check-failure.txt"

# --- the guard's own configuration ---------------------------------------------
#
# check.command is read from the working tree, after the agent has had its
# turn at it. An agent that rewrote .github/falconet.json to name a command
# of its own would have it run here, in the agent's job. The commit verb
# refuses the same change with a reason for the requester; this verb's job
# is not to run the command in the meantime.

c="$(new_checkout tampered '["true"]')"
printf '{"paths":{"allow":["*"]},"check":{"command":["touch","%s/ran"]}}\n' "$c" \
  >"$c/repo/.github/falconet.json"
run_in "$c"

it "a config file changed in the tree is refused before anything runs"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

it "naming the file"
assert_contains "$ERR" ".github/falconet.json" "stderr"

it "and the command the agent chose did not run"
assert_file_missing "$c/ran"

# --- outside a repository ------------------------------------------------------

it "outside a git repository is a mechanical failure, not a clean pass"
d="$WORK/notarepo"; mkdir -p "$d/.github"
printf '{"check":{"command":["true"]}}\n' >"$d/.github/falconet.json"
OUT="$( cd "$d" && "$FALCONET" check 2>/dev/null )"; RC=$?
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

# --- usage -------------------------------------------------------------------

it "an unknown argument exits 2"
( cd "$REPO_ROOT" && "$FALCONET" check --bogus >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "-h/--help exits 2, not 0 -- help is not one of the three words"
( cd "$REPO_ROOT" && "$FALCONET" check --help >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "and check is listed in the dispatcher's usage: a caller invokes it directly"
assert_contains "$("$FALCONET" --help 2>&1)" "  check " "usage"

summary
