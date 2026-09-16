#!/usr/bin/env bash
#
# conventional-subject.sh FILE — exit 0 when the first line of the message in
# FILE is a Conventional Commits subject, exit 1 with the reason on stderr
# when it is not.
#
# release-please decides the next version and writes CHANGELOG.md from the
# subjects on main, and it skips any subject that does not parse. Pull
# requests are squash-merged with the title as the subject, so
# .github/workflows/pr-title.yml runs this on the title, and lefthook's
# commit-msg hook runs it on every local commit.
#
# The subject is the first line that is neither blank nor a comment, because
# git hands the commit-msg hook the message with its `#` lines still in it.
# The subjects git writes itself (a merge, a revert, a fixup!, squash! or
# amend!) pass: a person rewrites those before they reach main.
set -euo pipefail

types='feat|fix|perf|refactor|docs|test|build|ci|chore|revert|style'
pattern="^($types)(\([a-z0-9][a-z0-9._/-]*\))?!?: [^ ]"

subject="$(grep -v -e '^#' -e '^[[:space:]]*$' "$1" | head -n 1 || true)"

case "$subject" in
  'Merge '* | 'Revert "'* | 'fixup! '* | 'squash! '* | 'amend! '*) exit 0 ;;
esac

if [[ "$subject" =~ $pattern ]]; then
  exit 0
fi

{
  if [ -z "$subject" ]; then
    echo "the message has no subject line"
  else
    echo "not a conventional commit subject: $subject"
  fi
  echo
  echo "write it as <type>[(scope)][!]: <description>, for example"
  echo "  feat: publish release binaries"
  echo "  fix(action): verify the checksum before unpacking"
  echo "where <type> is one of: ${types//|/, }"
  echo "and ! marks a breaking change."
} >&2
exit 1
