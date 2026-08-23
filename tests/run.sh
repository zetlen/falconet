#!/usr/bin/env bash
#
# run.sh — every tests/*.test.sh, in one go.
#
#   tests/run.sh              all of them
#   tests/run.sh handover     only the files whose name contains "handover"
#
# These cover the falconet verbs and the wiring of action.yml and
# .github/workflows/falconet.yml. Every test spawns its subject through
# $FALCONET, which defaults to bin/falconet:
#
#   FALCONET=/path/to/another/falconet tests/run.sh
#
# runs the same suite against another implementation of the same contract.
# They need bash, git, jq, awk and python3 (stdlib only); they stub `gh` for
# the verbs that still use it and serve a fake GitHub API on loopback for the
# verbs that have moved off it, push only into bare repositories under a temp
# directory, and never touch the network, GitHub, OpenTofu or any credential
# — which is why .github/workflows/ci.yml can run the whole suite on every
# pull request from a GitHub-hosted runner. Run it locally too, before
# changing anything under bin/, lib/, libexec/, cmd/ or internal/: CI
# reports, and only the `protecc main` ruleset decides whether a red report
# can be merged past.

set -uo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
filter="${1:-}"

failed=0
ran=0
for t in "$TEST_DIR"/*.test.sh; do
  # The file's name, not its path: a checkout whose directory is named after
  # a verb (a worktree called doctor/) would otherwise match every file.
  [[ -z "$filter" || "$(basename "$t")" == *"$filter"* ]] || continue
  ran=$((ran + 1))
  echo "=== $(basename "$t")"
  bash "$t" || failed=$((failed + 1))
  echo
done

if [[ "$ran" -eq 0 ]]; then
  echo "no test files matched '$filter'" >&2
  exit 2
fi

if [[ "$failed" -eq 0 ]]; then
  echo "all $ran test files passed"
  exit 0
fi
echo "$failed of $ran test files FAILED" >&2
exit 1
