#!/usr/bin/env bash
#
# run.sh — every tests/*.test.sh, in one go.
#
#   tests/run.sh              all of them
#   tests/run.sh handover     only the files whose name contains "handover"
#
# These cover the falconet verbs, the wiring of action.yml and
# .github/workflows/falconet.yml, and install/setup-github.sh. Every verb
# test spawns its subject through $FALCONET, which defaults to the binary
# built out of tree at dist/falconet (`make build`; lib.sh refuses to start
# without it):
#
#   FALCONET=/path/to/another/falconet tests/run.sh
#
# runs the same suite against another build of the same contract. The suite
# holds what can only be seen from outside the process: exit codes, the word
# on stdout, the files left on disk, the state of the git repository, the
# calls that reached the fake GitHub, and the argv the gitleaks stub was
# handed. `go test ./...` holds the logic, and `make test` runs both. The
# suite needs bash, git, gh, jq, awk, curl, openssl and python3 (stdlib
# only). GitHub is
# tests/fixtures/fake-github.py, served on loopback: the verbs reach it
# through the real gh, whose requests follow GITHUB_API_URL, so a test token
# goes nowhere but loopback. gitleaks and the harness are bash stubs, the
# scanner handed in through $GITLEAKS, pushes land only in bare
# repositories under a temp directory, and nothing touches the network,
# GitHub or any credential — which is why .github/workflows/ci.yml can run
# the whole suite on every pull request from a GitHub-hosted runner. Run it
# locally too, before changing anything under cmd/ or internal/.

set -uo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
filter="${1:-}"

failed=0
ran=0
for t in "$TEST_DIR"/*.test.sh; do
  # The file's name, not its path: a checkout whose directory is named after
  # a verb (a worktree called prepare/) would otherwise match every file.
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
