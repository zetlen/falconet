#!/usr/bin/env bash
#
# pause-gitea.test.sh — the hand-over, with "forge": "gitea" in the config.
#
# Gitea is tests/fixtures/fake-gitea.py on loopback, behind GITHUB_API_URL's
# /api/v1. The comment is pause.test.sh's and internal/pause's; these cases
# hold what only the process shows on Gitea: the client the config hands the
# verb, the requests it sends in order, and the refusals made before anything
# is sent.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

unset GITHUB_ACTIONS GITEA_ACTIONS

fake_gitea
export GH_TOKEN=test-token
export GITHUB_SERVER_URL=https://gitea.invalid
export GITHUB_REPOSITORY=zetlen/wayfinders-infra

API=/repos/zetlen/wayfinders-infra

printf '{"forge": "gitea"}\n' >"$WORK/gitea.json"
printf '{}\n' >"$WORK/github.json"

# Issue 36 as Gitea answers it: maint is assigned.
jq -n --arg b "$API" '[{method:"GET", path:($b+"/issues/36"),
  body:{number:36, title:"x", body:"", state:"open", labels:[], assignees:[{id:9, login:"maint"}]}}]' \
  >"$FAKE_GITEA/responses.json"

pause() { # out-name -- args...
  # Runs pause, then leaves $name.out, $name.err, $name.log (one line per
  # request, `METHOD PATH BODY`) and $name.comment (the body posted to
  # …/comments). Returns the exit code.
  local name="$1"; shift
  [ "$1" = "--" ] && shift
  : >"$FAKE_GITEA/requests.log"
  : >"$FAKE_GITEA/requests.jsonl"
  "$FALCONET" pause "$@" >"$WORK/$name.out" 2>"$WORK/$name.err"
  local rc=$?
  cp "$FAKE_GITEA/requests.log" "$WORK/$name.log"
  jq -j 'select(.method == "POST" and (.path | endswith("/comments"))) | .body.body' \
    "$FAKE_GITEA/requests.jsonl" >"$WORK/$name.comment"
  return "$rc"
}
calls() { awk '{ print $1, $2 }' "$WORK/$1.log"; }

# --- a pause on Gitea -------------------------------------------------------------

pause parked -- --config "$WORK/gitea.json" \
  --issue 36 --label needs-info --unassign maint --branch issue-36-docs \
  --preamble "I need one more detail before I can start."
rc=$?
it "a pause on Gitea is success, exit 0"
assert_eq 0 "$rc" "exit code"
assert_eq "success" "$(cat "$WORK/parked.out")" "stdout"
it "and the requests are the self-check, the label, the comment, then the claim released by rewriting the assignees"
assert_eq "GET /user
POST $API/issues/36/labels
POST $API/issues/36/comments
GET $API/issues/36
PATCH $API/issues/36" "$(calls parked)" "requests"
it "and the branch links to the instance"
assert_contains "$(cat "$WORK/parked.comment")" "https://gitea.invalid/zetlen/wayfinders-infra/tree/issue-36-docs" "comment"

# --- the runner is not the configured forge ------------------------------------------

GITEA_ACTIONS=true GITHUB_ACTIONS=true pause mismatch -- --config "$WORK/github.json" \
  --issue 36 --label needs-info --branch "" --preamble "x"
rc=$?
it "forge github under Gitea Actions is failure, exit 1"
assert_eq 1 "$rc" "exit code"
assert_eq "failure" "$(cat "$WORK/mismatch.out")" "stdout"
assert_contains "$(cat "$WORK/mismatch.err")" "set forge to gitea" "stderr"
it "and nothing is sent"
assert_eq "" "$(cat "$WORK/mismatch.log")" "requests"

# --- no API URL ---------------------------------------------------------------------
#
# An unset GITHUB_API_URL is api.github.com to every other reader of it. The
# proxy is a closed loopback port, so a verb that sent the token anyway
# reaches nothing.

( unset GITHUB_API_URL; export HTTPS_PROXY=http://127.0.0.1:9 HTTP_PROXY=http://127.0.0.1:9 NO_PROXY=
  pause nourl -- --config "$WORK/gitea.json" \
    --issue 36 --label needs-info --branch "" --preamble "x"; echo "$?" >"$WORK/nourl.rc" )
it "forge gitea with no GITHUB_API_URL is failure, exit 1"
assert_eq 1 "$(cat "$WORK/nourl.rc")" "exit code"
assert_eq "failure" "$(cat "$WORK/nourl.out")" "stdout"
assert_contains "$(cat "$WORK/nourl.err")" "GITHUB_API_URL" "stderr"

summary
