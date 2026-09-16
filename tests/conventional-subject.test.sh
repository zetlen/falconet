#!/usr/bin/env bash
#
# conventional-subject.sh — the rule a commit subject and a pull request
# title are held to, spawned the way lefthook's commit-msg hook and the
# pr-title workflow spawn it: with the path of a file holding the message.
set -uo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=tests/lib.sh
. "$REPO_ROOT/tests/lib.sh"

SUBJECT="$REPO_ROOT/scripts/conventional-subject.sh"

check() { # message
  printf '%s' "$1" >"$WORK/msg"
  bash "$SUBJECT" "$WORK/msg" >"$WORK/out" 2>"$WORK/err"
}

for ok in \
  'feat: publish release binaries' \
  'fix(action): verify the checksum before unpacking' \
  'feat!: drop the go install fallback' \
  'refactor(internal/guard)!: one allowlist type' \
  'chore(main): release 1.2.0' \
  'docs: say where the assets come from' \
  'test: make git refuse a missing identity on any hostname'; do
  it "accepts: $ok"
  check "$ok"$'\n\nA body that says anything at all.\n'
  assert_eq 0 "$?" "exit code"
done

for bad in \
  'Publish release binaries' \
  'feature: publish release binaries' \
  'feat:publish release binaries' \
  'feat: ' \
  'Feat: publish release binaries' \
  'feat(): publish release binaries'; do
  it "refuses: $bad"
  check "$bad"$'\n'
  assert_eq 1 "$?" "exit code"
done

it "and the refusal names the subject and the types it accepts"
check $'Publish release binaries\n'
assert_contains "$(cat "$WORK/err")" 'Publish release binaries' "stderr"
assert_contains "$(cat "$WORK/err")" 'feat, fix,' "stderr"

# git writes its own messages for these, and a person rewrites them before
# they reach main, or they never do.
for git_made in \
  'Merge branch '\''main'\'' into release-binaries' \
  'Revert "feat: publish release binaries"' \
  'fixup! feat: publish release binaries' \
  'squash! feat: publish release binaries' \
  'amend! feat: publish release binaries'; do
  it "lets git's own subject through: $git_made"
  check "$git_made"$'\n'
  assert_eq 0 "$?" "exit code"
done

# git hands the commit-msg hook the file with its comment lines still in it.
it "reads the subject past git's comment lines and blank lines"
check $'# Please enter the commit message\n\nfeat: publish release binaries\n# On branch main\n'
assert_eq 0 "$?" "exit code"
check $'# Please enter the commit message\n\nPublish release binaries\n'
assert_eq 1 "$?" "exit code"

it "refuses a message with no subject"
check $'# only comments\n\n'
assert_eq 1 "$?" "exit code"

summary
