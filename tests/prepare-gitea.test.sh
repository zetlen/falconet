#!/usr/bin/env bash
#
# prepare-gitea.test.sh — the gate, with "forge": "gitea" in the config.
#
# Gitea is tests/fixtures/fake-gitea.py on loopback, behind GITHUB_API_URL's
# /api/v1. The rules themselves are prepare.test.sh's and internal/prepare's,
# and Gitea's event reader and client are internal/gitea's; these cases hold
# what only the process shows on Gitea: which reader and which client the
# config hands the verb, the requests they send in order, and the refusals
# made before the event file is read.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null

# ci.yml runs this suite inside GitHub Actions. Every case here brings its
# event, and the runner cases set the variables themselves.
unset GITHUB_ACTIONS GITEA_ACTIONS GITHUB_TRIGGERING_ACTOR GITHUB_RUN_ID GITHUB_RUN_ATTEMPT

fake_gitea
export GH_TOKEN=test-token
export GITHUB_REPOSITORY=zetlen/wayfinders-infra
export GITHUB_SERVER_URL=https://gitea.invalid

API=/repos/zetlen/wayfinders-infra

new_checkout() { # name [config-json] -> echoes path; the config is committed
  local base="$WORK/$1" config="${2-}"
  [[ $# -ge 2 ]] || config='{"forge": "gitea"}'
  mkdir -p "$base/repo/.github"
  git init --bare -q "$base/origin.git"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  printf 'hello\n' >"$base/repo/README.md"
  printf '.falconet/\n' >"$base/repo/.gitignore"
  [[ -z "$config" ]] || printf '%s\n' "$config" >"$base/repo/.github/falconet.json"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" remote add origin "$base/origin.git"
  git -C "$base/repo" push -q origin main
  printf '%s' "$base"
}

# responses.json: issue 42 as Gitea answers it, labels carrying their ids,
# and SENDER's permission, PERM.
script_gitea() { # labels-json
  jq -n --argjson labels "$1" --arg b "$API" \
    --arg perm "${PERM:-write}" --arg sender "${SENDER:-maint}" '
    [{method:"GET", path:($b+"/issues/42"),
      body:{number:42, title:"Add a line to the docs", body:"Please.", state:"open",
            labels:$labels, assignees:[], pull_request:null}},
     {method:"GET", path:($b+"/collaborators/"+$sender+"/permission"),
      body:{permission:$perm, role_name:$perm, user:{id:9, login:$sender}}}]
  ' >"$FAKE_GITEA/responses.json"
}

# An event as Gitea's Actions notifier writes it. A label change adds
# `changes.added_labels`; a comment carries `is_pull`. The sender is $SENDER,
# a login and no type.
label_event() { # path
  jq -n --arg who "${SENDER:-maint}" \
    '{action:"label_updated", number:42, sender:{id:9, login:$who},
      changes:{added_labels:[{id:1, name:"falconet"}], removed_labels:null},
      issue:{number:42, state:"open", body:"Please.", pull_request:null,
             labels:[{id:1, name:"falconet"}]}}' >"$1"
}
comment_event() { # path
  jq -n --arg who "${SENDER:-maint}" \
    '{action:"created", is_pull:false, sender:{id:9, login:$who},
      comment:{body:"Here is the detail."},
      issue:{number:42, state:"open", body:"Please.", pull_request:null,
             labels:[{id:1, name:"falconet"}, {id:2, name:"needs-info"}]}}' >"$1"
}

p() { # checkout [args...] -> sets OUT ERR RC
  local c="$1"; shift
  : >"$FAKE_GITEA/requests.log"
  : >"$FAKE_GITEA/requests.jsonl"
  OUT="$(cd "$c/repo" && "$FALCONET" prepare --issue 42 "$@" 2>"$c/err")"
  RC=$?
  ERR="$(cat "$c/err")"
  cp "$FAKE_GITEA/requests.log" "$c/requests.log"
  return 0
}
calls() { awk '{ print $1, $2 }' "$1/requests.log"; }
mutations() { grep -E '^(POST|DELETE|PUT|PATCH) ' "$1/requests.log"; }

# --- a writer's label ---------------------------------------------------------

c="$(new_checkout writer)"
script_gitea '[{"id":1,"name":"falconet"}]'
label_event "$c/event.json"
p "$c" --event "$c/event.json"
it "a writer's label on Gitea is ready"
assert_eq "ready" "$OUT" "outcome"
assert_eq 0 "$RC" "exit code"
it "and the requests are the self-check, the permission, the pulls, the snapshot, the claim and the ack, in that order"
assert_eq "GET /user
GET $API/collaborators/maint/permission
GET $API/pulls
GET $API/issues/42
GET $API/issues/42/comments
GET $API/issues/42
PATCH $API/issues/42
POST $API/issues/42/comments" "$(calls "$c")" "requests"
it "and the claim names the sender, because Gitea names no triggering actor"
assert_contains "$(cat "$c/requests.log")" "PATCH $API/issues/42 {\"assignees\":[\"maint\"]}" "requests"

# --- a stranger's label ---------------------------------------------------------

c="$(new_checkout stranger)"
SENDER=stranger PERM=read script_gitea '[{"id":1,"name":"falconet"}]'
SENDER=stranger label_event "$c/event.json"
p "$c" --event "$c/event.json"
it "a label from someone without write is ineligible"
assert_eq "ineligible" "$OUT" "outcome"
assert_contains "$ERR" "stranger does not hold write" "stderr"
it "and it costs the self-check and the permission, and changes nothing"
assert_eq "GET /user
GET $API/collaborators/stranger/permission" "$(calls "$c")" "requests"
assert_eq "main" "$(git -C "$c/repo" branch --show-current)" "branch"

# --- the bot's own comment ----------------------------------------------------

c="$(new_checkout bot)"
script_gitea '[{"id":1,"name":"falconet"},{"id":2,"name":"needs-info"}]'
SENDER=falconet-bot comment_event "$c/event.json"
p "$c" --event "$c/event.json"
it "the bot's own comment on a parked issue is ineligible, told by its login"
assert_eq "ineligible" "$OUT" "outcome"
assert_contains "$ERR" "bot" "stderr"
it "and it asks nothing"
assert_eq "" "$(cat "$c/requests.log")" "requests"

# --- re-entry ---------------------------------------------------------------------

c="$(new_checkout reentry)"
script_gitea '[{"id":1,"name":"falconet"},{"id":2,"name":"needs-info"}]'
comment_event "$c/event.json"
p "$c" --event "$c/event.json"
it "a writer's reply on a parked issue is ready"
assert_eq "ready" "$OUT" "outcome"
it "and needs-info is cleared by the id the issue carries it under"
assert_contains "$(calls "$c")" "GET $API/issues/42
DELETE $API/issues/42/labels/2" "requests"
it "and the requester is not thanked twice"
assert_not_contains "$(mutations "$c")" "/comments" "mutations"

# --- refused before the event file is read ------------------------------------------

c="$(new_checkout mismatch "")"
( export GITEA_ACTIONS=true GITHUB_ACTIONS=true; p "$c" --event "$c/nowhere.json"; printf '%s\n%s\n' "$RC" "$OUT" >"$c/result"; cp "$c/err" "$c/err.saved" )
it "forge github under Gitea Actions is refused: exit 1, no word"
assert_eq 1 "$(sed -n 1p "$c/result")" "exit code"
assert_eq "" "$(sed -n 2p "$c/result")" "stdout"
it "before the event file is read"
assert_contains "$(cat "$c/err.saved")" "set forge to gitea" "stderr"
assert_not_contains "$(cat "$c/err.saved")" "names no file" "stderr"
it "and it asks nothing"
assert_eq "" "$(cat "$c/requests.log")" "requests"

c="$(new_checkout gitea_noevent)"
script_gitea '[{"id":1,"name":"falconet"}]'
( GITEA_ACTIONS=true p "$c"; printf '%s\n%s\n' "$RC" "$OUT" >"$c/result"; cp "$c/err" "$c/err.saved" )
it "a Gitea Actions runner's run with no event is refused: exit 1, no word"
assert_eq 1 "$(sed -n 1p "$c/result")" "exit code"
assert_eq "" "$(sed -n 2p "$c/result")" "stdout"
it "and it asks nothing"
assert_eq "" "$(cat "$c/requests.log")" "requests"

# An unset GITHUB_API_URL is api.github.com to every other reader of it. The
# proxy is a closed loopback port, so a verb that sent the token anyway
# reaches nothing.
c="$(new_checkout nourl)"
( unset GITHUB_API_URL; export HTTPS_PROXY=http://127.0.0.1:9 HTTP_PROXY=http://127.0.0.1:9 NO_PROXY=
  p "$c" --event "$c/nowhere.json"; printf '%s\n%s\n' "$RC" "$OUT" >"$c/result"; cp "$c/err" "$c/err.saved" )
it "forge gitea with no GITHUB_API_URL is refused: exit 1, no word"
assert_eq 1 "$(sed -n 1p "$c/result")" "exit code"
assert_eq "" "$(sed -n 2p "$c/result")" "stdout"
it "before the event file is read, naming the variable"
assert_contains "$(cat "$c/err.saved")" "GITHUB_API_URL" "stderr"
assert_not_contains "$(cat "$c/err.saved")" "names no file" "stderr"

c="$(new_checkout nobot)"
( unset FALCONET_BOT_LOGIN; p "$c" --event "$c/nowhere.json"; printf '%s\n%s\n' "$RC" "$OUT" >"$c/result"; cp "$c/err" "$c/err.saved" )
it "forge gitea with no FALCONET_BOT_LOGIN is refused: exit 1, no word"
assert_eq 1 "$(sed -n 1p "$c/result")" "exit code"
assert_eq "" "$(sed -n 2p "$c/result")" "stdout"
it "before the event file is read, naming the variable"
assert_contains "$(cat "$c/err.saved")" "FALCONET_BOT_LOGIN" "stderr"
assert_not_contains "$(cat "$c/err.saved")" "names no file" "stderr"
it "and it asks nothing"
assert_eq "" "$(cat "$c/requests.log")" "requests"

summary
