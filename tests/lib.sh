#!/usr/bin/env bash
#
# lib.sh — four assertions, a scratch directory, and a fake GitHub. Sourced
# by every tests/*.test.sh.
#
# Deliberately not a framework. The thing under test is one binary that
# reads files and writes files, and the interesting question about each verb
# is "given exactly this input, what came out?" — which needs a temp dir, a
# comparison, and a non-zero exit code, and nothing else.

set -uo pipefail

TESTS_RUN=0
TESTS_FAILED=0
CURRENT_TEST=""

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export REPO_ROOT

# The subject. Every test spawns verbs through this and nothing else, so the
# suite can be pointed at another build of the same contract:
#
#   FALCONET=/path/to/binary bash tests/run.sh
#
# The default is the binary `make build` leaves at dist/falconet, and there
# is no other answer: since #19 nothing in this tree runs a verb but the
# binary, so a suite that started without one would fail every case for one
# reason, slowly. Said once, here, before any file runs anything.
FALCONET="${FALCONET:-$REPO_ROOT/dist/falconet}"
if [[ ! -f "$FALCONET" || ! -x "$FALCONET" ]]; then
  echo "tests: no falconet binary at $FALCONET — build it first: make build" >&2
  exit 1
fi
export FALCONET

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Each test case announces itself first, so a failure names the case rather
# than a line number in a helper.
it() {
  CURRENT_TEST="$1"
  TESTS_RUN=$((TESTS_RUN + 1))
}

_fail() {
  TESTS_FAILED=$((TESTS_FAILED + 1))
  echo "  FAIL  $CURRENT_TEST"
  printf '        %s\n' "$@"
}

_pass() { echo "  ok    $CURRENT_TEST"; }

assert_eq() { # expected actual [what]
  if [[ "$1" == "$2" ]]; then _pass; else
    _fail "${3:-value} expected: [$1]" "${3:-value} actual:   [$2]"
  fi
}

assert_contains() { # haystack needle [what]
  case "$1" in
    *"$2"*) _pass ;;
    *) _fail "${3:-output} does not contain: [$2]" "got: [${1:0:400}]" ;;
  esac
}

assert_not_contains() { # haystack needle [what]
  case "$1" in
    *"$2"*) _fail "${3:-output} unexpectedly contains: [$2]" "got: [${1:0:400}]" ;;
    *) _pass ;;
  esac
}

assert_file_missing() { # path
  if [[ -e "$1" ]]; then _fail "file should not exist: $1"; else _pass; fi
}

summary() {
  echo
  if [[ "$TESTS_FAILED" -eq 0 ]]; then
    echo "$(basename "$0"): $TESTS_RUN passed"
  else
    echo "$(basename "$0"): $TESTS_FAILED of $TESTS_RUN FAILED"
  fi
  return "$TESTS_FAILED"
}

# A GitHub API on loopback, for the verbs that have stopped shelling out to
# `gh` (ADR-0006 D2): tests/fixtures/fake-github.py, which answers from
# fixtures and writes down what it was asked. Exports GITHUB_API_URL pointing
# at it and FAKE_GITHUB as the directory it records into; see the fixture's
# header for the files. Started once per test file, and killed with the
# scratch directory.
fake_github() {
  FAKE_GITHUB="$WORK/fake-github"
  mkdir -p "$FAKE_GITHUB"
  python3 "$REPO_ROOT/tests/fixtures/fake-github.py" --dir "$FAKE_GITHUB" &
  FAKE_GITHUB_PID=$!
  # Out of the job table, so the kill below is not reported as "Terminated".
  disown "$FAKE_GITHUB_PID"
  trap 'kill "$FAKE_GITHUB_PID" 2>/dev/null; rm -rf "$WORK"' EXIT
  local waited=0
  until [ -s "$FAKE_GITHUB/port" ]; do
    waited=$((waited + 1))
    if [ "$waited" -gt 200 ] || ! kill -0 "$FAKE_GITHUB_PID" 2>/dev/null; then
      echo "fake-github.py did not start" >&2
      exit 1
    fi
    sleep 0.05
  done
  GITHUB_API_URL="http://127.0.0.1:$(cat "$FAKE_GITHUB/port")"
  export GITHUB_API_URL FAKE_GITHUB
}

# An execution log shaped like the one claude-code-action writes: a JSON array
# whose last `result` entry carries the agent's final message. Built with jq
# from a file so a fixture can hold the message verbatim, backticks, em dashes
# and all.
execution_log_from() { # message-file destination-file
  jq -n --rawfile r "$1" '[
    {type: "system", subtype: "init"},
    {type: "assistant", message: {content: [{type: "text", text: "..."}]}},
    {type: "result", subtype: "success", result: $r}
  ]' >"$2"
}

# Same, from a literal string.
execution_log_of() { # message destination-file
  printf '%s' "$1" >"$WORK/.msg"
  execution_log_from "$WORK/.msg" "$2"
}
