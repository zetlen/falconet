#!/usr/bin/env bash
#
# prepare.test.sh — the eligibility gate and the claim.
#
# This is the one verb with no ancestor script: its eligibility half was a
# job-level `if:` expression and its ready half was inline YAML. So none of
# this is a ported test, and every assertion is a decision. Where the two
# origin sources disagreed — the workflow and the human-facing skill that
# worked the same queue — the case name says which one is encoded and why.
#
# GitHub is tests/fixtures/fake-github.py on loopback, with GITHUB_API_URL
# pointing at it: the verb shells out to `gh api` with full URLs built from
# that variable. A case scripts the issue, its comment thread, the open
# pull-request list and any failure in responses.json, and reads back what
# the verb asked for from requests.log. Green means through the binary.
# Nothing here runs tofu: prepare stopped capturing a baseline plan when
# planning left falconet (docs/decisions.md), and the handoff it lays out
# is git and GitHub only.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null

# --- the fake API -------------------------------------------------------------

fake_github
export GH_TOKEN=test-token
export GITHUB_REPOSITORY=zetlen/wayfinders-infra
export GITHUB_SERVER_URL=https://github.com

new_checkout() { # name -> echoes path
  local base="$WORK/$1"
  mkdir -p "$base/repo/.github" "$base/bin" "$base/repo/dns"
  git init --bare -q "$base/origin.git"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  printf 'locals {\n  a = 1\n}\n' >"$base/repo/dns/main.tf"
  printf '.falconet/\n' >"$base/repo/.gitignore"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" remote add origin "$base/origin.git"
  git -C "$base/repo" push -q origin main
  printf '%s' "$base"
}

# The issue as GET /repos/{o}/{r}/issues/42 answers it, and its comment
# thread as GET …/issues/42/comments does.
issue_json() { # path labels-csv body [state]
  local out="$1" labels="$2" body="$3" state="${4:-open}"
  jq -n --arg b "$body" --arg s "$state" --arg l "$labels" \
    '{number:42, title:"Add MX records for papernapkin.tech", body:$b, state:$s,
      labels: ($l | if . == "" then [] else split(",") end | map({name:.}))}' \
    >"$out"
  printf '[{"user":{"login":"zetlen"},"created_at":"2026-08-01T00:00:00Z","body":"bump"}]\n' \
    >"$(dirname "$out")/comments.json"
}

API=/repos/zetlen/wayfinders-infra

# responses.json for one case: the failure knobs first, so they win, then the
# issue, its thread and the pull-request list from the checkout's files.
script_github() { # checkout
  local c="$1"
  [[ -f "$c/comments.json" ]] || printf '[]\n' >"$c/comments.json"
  [[ -f "$c/pr.json" ]] || printf '[]\n' >"$c/pr.json"
  jq -n --slurpfile issue "$c/issue.json" --slurpfile comments "$c/comments.json" \
    --slurpfile pulls "$c/pr.json" --arg b "$API" \
    --arg view "${VIEW_RC:-0}" --arg edit "${EDIT_RC:-0}" --arg remove "${REMOVE_RC:-0}" \
    --arg comment "${COMMENT_RC:-0}" --arg user "${USER_RC:-0}" --arg pullsrc "${PULLS_RC:-0}" \
    --arg issuenull "${ISSUE_NULL:-0}" '
    # A knob of 1 is a 500; any other non-zero knob is the status itself, so
    # a case can script the one answer it is about (a 404 on the label).
    def st: if . == "1" then 500 else tonumber end;
    (if $view != "0" then [{method:"GET", path:($b+"/issues/42"), status:($view|st), body:{message:"boom"}}] else [] end)
    + (if $issuenull != "0" then [{method:"GET", path:($b+"/issues/42"), status:200, body:null}] else [] end)
    + (if $edit != "0" then [{method:"POST", path:($b+"/issues/42/assignees"), status:($edit|st), body:{message:"boom"}}] else [] end)
    + (if $remove != "0" then [{method:"DELETE", path:($b+"/issues/42/labels/needs-info"), status:($remove|st), body:{message:"Label does not exist"}}] else [] end)
    + (if $comment != "0" then [{method:"POST", path:($b+"/issues/42/comments"), status:($comment|st), body:{message:"boom"}}] else [] end)
    + (if $user != "0" then [{method:"GET", path:"/user", status:($user|st), body:{message:"boom"}}] else [] end)
    + (if $pullsrc != "0" then [{method:"GET", path:($b+"/pulls"), status:($pullsrc|st), body:{message:"boom"}}] else [] end)
    + [{method:"GET", path:($b+"/issues/42"), body:$issue[0]},
       {method:"GET", path:($b+"/issues/42/comments"), body:$comments[0]},
       {method:"GET", path:($b+"/pulls"), body:$pulls[0]}]
  ' >"$FAKE_GITHUB/responses.json"
}

p() { # checkout [args...] -> sets OUT ERR RC
  local c="$1"; shift
  script_github "$c"
  : >"$FAKE_GITHUB/requests.log"
  : >"$FAKE_GITHUB/requests.jsonl"
  OUT="$( cd "$c/repo" \
    && GITHUB_ENV="${GH_ENV:-}" GITHUB_RUN_ID="${RUN_ID:-}" \
       GITHUB_TRIGGERING_ACTOR="${ACTOR:-}" \
       "$FALCONET" prepare --issue 42 "$@" 2>"$c/err" )"
  RC=$?
  ERR="$(cat "$c/err")"
  # What the verb asked GitHub, one `METHOD PATH BODY` line each, and the
  # exact bytes of the comment a requester would have been shown.
  cp "$FAKE_GITHUB/requests.log" "$c/requests.log"
  jq -j 'select(.method == "POST" and (.path | endswith("/comments"))) | .body.body' \
    "$FAKE_GITHUB/requests.jsonl" >"$c/posted.md"
  return 0
}
reset() { VIEW_RC=""; EDIT_RC=""; REMOVE_RC=""; COMMENT_RC=""; USER_RC=""; PULLS_RC=""; ISSUE_NULL=""
          GH_ENV=""; RUN_ID=""; ACTOR=""; }
reset

ghlog() { cat "$1/requests.log" 2>/dev/null; }
mutations() { grep -E '^(POST|DELETE|PUT|PATCH) ' "$1/requests.log" 2>/dev/null; }
hand()  { cat "$1/repo/.falconet/$2" 2>/dev/null; }

# --- the gate ---------------------------------------------------------------

c="$(new_checkout eligible)"; issue_json "$c/issue.json" "infra-request" "Please add MX."
p "$c"
it "a queued, open, unblocked issue with no open PR is ready"
assert_eq "ready" "$OUT" "outcome"
it "and exits 0"
assert_eq 0 "$RC" "exit code"

for lbl in ready-for-human do-not-apply wontfix needs-info; do
  c="$(new_checkout "block_$lbl")"; issue_json "$c/issue.json" "infra-request,$lbl" "x"
  p "$c"
  it "a blocking label ($lbl) makes it ineligible"
  assert_eq "ineligible" "$OUT" "outcome"
  it "and the reason names the label, because 'ineligible' alone is not a diagnostic"
  assert_contains "$ERR" "$lbl" "stderr"
done

c="$(new_checkout notqueued)"; issue_json "$c/issue.json" "bug" "x"
p "$c"
it "an issue without the queue label is ineligible"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout prefixlabel)"; issue_json "$c/issue.json" "infra-request-later" "x"
p "$c"
it "and the queue label is matched exactly, not as a prefix"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout nearblock)"; issue_json "$c/issue.json" "infra-request,needs-information" "x"
p "$c"
it "a label that merely starts like a blocking one does not block"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout closed)"; issue_json "$c/issue.json" "infra-request" "x" "closed"
p "$c"
it "a closed issue is ineligible"
assert_eq "ineligible" "$OUT" "outcome"

# --- the opt-out box --------------------------------------------------------

c="$(new_checkout optout)"; issue_json "$c/issue.json" "infra-request" \
  "- [x] Not eligible for AI agents"
p "$c"
it "a ticked opt-out box is ineligible"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout optout_caps)"; issue_json "$c/issue.json" "infra-request" \
  "- [X] not eligible for ai agents"
p "$c"
it "and the whole line is matched case-insensitively"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout optout_star)"; issue_json "$c/issue.json" "infra-request" \
  "* [x] Not eligible for AI agents"
p "$c"
it "either list marker works"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout optout_indent)"; issue_json "$c/issue.json" "infra-request" \
  "  - [x] Not eligible for AI agents"
p "$c"
it "and an indented checkbox still opts out, because issue forms indent them"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout optout_unticked)"; issue_json "$c/issue.json" "infra-request" \
  "- [ ] Not eligible for AI agents"
p "$c"
it "an UNticked box does not opt out"
assert_eq "ready" "$OUT" "outcome"

# The origin's CI form was an unanchored substring test, so the sentence
# appearing anywhere -- quoted from another issue, say -- opted the issue out.
# The human-facing skill anchored it to a list item. Anchored is encoded here.
c="$(new_checkout optout_prose)"; issue_json "$c/issue.json" "infra-request" \
  "I do not think this is [x] Not eligible for AI agents, really"
p "$c"
it "the opt-out is anchored to a checkbox, not found anywhere in the prose"
assert_eq "ready" "$OUT" "outcome"

# gh spelled the state OPEN; the API spells it open. Both are open.
c="$(new_checkout nullbody)"
jq -n '{number:42,title:"T",body:null,state:"OPEN",labels:[{name:"infra-request"}]}' \
  >"$c/issue.json"
p "$c"
it "a null body is not a crash"
assert_eq "ready" "$OUT" "outcome"

# --- in flight --------------------------------------------------------------

c="$(new_checkout inflight)"; issue_json "$c/issue.json" "infra-request" "x"
printf '[{"number":57,"head":{"ref":"issue-42-add-mx"}}]\n' >"$c/pr.json"
p "$c"
it "an open PR on this issue's branch is in-flight"
assert_eq "in-flight" "$OUT" "outcome"
it "and the reason names the pull request"
assert_contains "$ERR" "#57" "stderr"
it "and in-flight changes nothing either: no mutating call"
assert_eq "" "$(mutations "$c")" "mutating API calls"
it "and the checkout stays on its original branch"
assert_eq "main" "$(git -C "$c/repo" branch --show-current)" "branch"

c="$(new_checkout inflight_claude)"; issue_json "$c/issue.json" "infra-request" "x"
printf '[{"number":57,"head":{"ref":"claude/issue-42-20250101"}}]\n' >"$c/pr.json"
p "$c"
it "the legacy claude/ prefix counts too"
assert_eq "in-flight" "$OUT" "outcome"

c="$(new_checkout inflight_other)"; issue_json "$c/issue.json" "infra-request" "x"
printf '[{"number":57,"head":{"ref":"issue-421-other"}}]\n' >"$c/pr.json"
p "$c"
it "issue 421's PR does not make issue 42 in-flight"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout inflight_unanchored)"; issue_json "$c/issue.json" "infra-request" "x"
printf '[{"number":57,"head":{"ref":"feature/issue-42-x"}}]\n' >"$c/pr.json"
p "$c"
it "and the match is anchored, so a nested name is not this issue's branch"
assert_eq "ready" "$OUT" "outcome"

# In flight means an OPEN PULL REQUEST, never a branch. Every run pushes its
# branch now, so a leftover branch is the ordinary state of a retried issue --
# and keying on branches would let one suppress every later run on the issue.
c="$(new_checkout branch_not_inflight)"; issue_json "$c/issue.json" "infra-request" "x"
git -C "$c/repo" switch -qc issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" push -q origin issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" switch -q main
p "$c"
it "a leftover branch on the remote does not make an issue in-flight"
assert_eq "ready" "$OUT" "outcome"

# --- ineligible and in-flight change nothing --------------------------------

c="$(new_checkout silent)"; issue_json "$c/issue.json" "infra-request,wontfix" "x"
GH_ENV="$c/github_env" p "$c"; reset
it "an ineligible issue leaves no handoff files"
assert_file_missing "$c/repo/.falconet/request.md"
it "and no issue.json, so 'ineligible' is not distinguishable from 'never ran'"
assert_file_missing "$c/repo/.falconet/issue.json"
it "and makes no mutating API call"
assert_eq "" "$(mutations "$c")" "mutating API calls"
it "and posts no comment"
assert_not_contains "$(ghlog "$c")" "POST $API/issues/42/comments" "API calls"
it "and exports nothing"
assert_eq "" "$(cat "$c/github_env" 2>/dev/null)" "GITHUB_ENV"
it "and leaves the checkout on its original branch"
assert_eq "main" "$(git -C "$c/repo" branch --show-current)" "branch"

# --- the ready path ---------------------------------------------------------

c="$(new_checkout ready_full)"; issue_json "$c/issue.json" "infra-request" "Please add MX."
GH_ENV="$c/github_env" p "$c"; reset

it "ready writes the branch name"
assert_eq "issue-42-add-mx-records-for-papernapkin-tech" "$(hand "$c" branch.txt)" "branch.txt"
it "and really is on that branch"
assert_eq "$(hand "$c" branch.txt)" "$(git -C "$c/repo" branch --show-current)" "branch"
it "and records the commit the run started from"
assert_eq "$(git -C "$c/repo" rev-parse main)" "$(hand "$c" base-sha.txt)" "base-sha.txt"
it "and exports BRANCH for the wrappers"
assert_contains "$(cat "$c/github_env")" "BRANCH=issue-42-add-mx" "GITHUB_ENV"
it "and BASE_SHA"
assert_contains "$(cat "$c/github_env")" "BASE_SHA=" "GITHUB_ENV"
it "request.md leads with the issue"
assert_contains "$(hand "$c" request.md)" "# Issue #42: Add MX records" "request.md"
it "and carries the requester's words"
assert_contains "$(hand "$c" request.md)" "Please add MX." "request.md"
it "and the comment thread, oldest first"
assert_contains "$(hand "$c" request.md)" "### zetlen — 2026-08-01" "request.md"
it "but never this pipeline's own acknowledgment"
assert_not_contains "$(hand "$c" request.md)" "picked up and is being worked on" "request.md"
it "and no plan is captured: planning is the plan bot's, on the pull request"
assert_file_missing "$c/repo/.falconet/plan-baseline.txt"
it "the claim is recorded"
assert_contains "$(ghlog "$c")" "POST $API/issues/42/assignees" "API calls"
it "and the requester is thanked, in the words that promise only what is true"
assert_contains "$(cat "$c/posted.md" 2>/dev/null)" \
  "Thanks — this request has been picked up" "posted comment"
it "including that a person still decides"
assert_contains "$(cat "$c/posted.md" 2>/dev/null)" \
  "Nothing takes effect until a person has reviewed it." "posted comment"
it "issue.json is the API's issue object"
assert_eq "42 Add MX records for papernapkin.tech" \
  "$(jq -r '"\(.number) \(.title)"' "$c/repo/.falconet/issue.json")" "issue.json"
it "with the comment thread merged in"
assert_eq "zetlen bump" \
  "$(jq -r '.comments[0] | "\(.user.login) \(.body)"' "$c/repo/.falconet/issue.json")" "issue.json comments"

c="$(new_checkout nocomments)"
jq -n '{number:42,title:"T",body:"b",state:"open",labels:[{name:"infra-request"}]}' \
  >"$c/issue.json"
p "$c"
it "an issue with no comments gets no comment-thread heading"
assert_not_contains "$(hand "$c" request.md)" "## Comment thread" "request.md"

# The issue body is attacker-controlled text AND the agent's instructions. It
# travels through files, never through a template expression, and nothing here
# executes any of it.
c="$(new_checkout hostile)"; issue_json "$c/issue.json" "infra-request" \
  'Add `$(touch /tmp/pwned)` and ```a fence``` please'
p "$c"
it "shell-shaped text in the body reaches request.md verbatim"
assert_contains "$(hand "$c" request.md)" '$(touch /tmp/pwned)' "request.md"
it "and is not executed on the way"
assert_file_missing "/tmp/pwned"

# stdout is the outcome and nothing else.
it "nothing else leaks into the outcome word"
assert_eq "ready" "$OUT" "outcome"

# --- the slug ---------------------------------------------------------------

slug_of() { # title -> echoes branch.txt
  local c; c="$(new_checkout "slug$RANDOM")"
  jq -n --arg t "$1" \
    '{number:42,title:$t,body:"b",state:"open",labels:[{name:"infra-request"}]}' \
    >"$c/issue.json"
  p "$c" >/dev/null
  hand "$c" branch.txt
}

it "a long title is cut to 40 slug characters"
b="$(slug_of "An extremely long issue title that goes well past the limit")"
assert_eq 39 "$(( ${#b} - 9 ))" "slug length (branch minus the issue-42- prefix)"

it "and the cut lands where the 40th character was, not at a word boundary"
assert_eq "issue-42-an-extremely-long-issue-title-that-goes" "$b" "branch"

it "and never ends in a dash, even when the cut lands mid-separator"
b="$(slug_of "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa - trailing")"
case "$b" in *-) assert_eq "no trailing dash" "trailing dash" "branch: $b" ;;
              *) assert_eq "ok" "ok" "branch: $b" ;; esac

it "a title with nothing sluggable falls back to 'request'"
assert_eq "issue-42-request" "$(slug_of '!!! ???')" "branch"

it "a non-ASCII title still produces a usable ref"
b="$(slug_of "Zoë's café")"
case "$b" in issue-42-[a-z0-9-]*) assert_eq ok ok "branch: $b" ;;
             *) assert_eq "[a-z0-9-] only" "$b" "branch" ;; esac

# --- the collision rename ---------------------------------------------------

c="$(new_checkout collide)"; issue_json "$c/issue.json" "infra-request" "x"
git -C "$c/repo" switch -qc issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" push -q origin issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" switch -q main
RUN_ID=99 p "$c"; reset
it "a branch already on the remote is renamed rather than pushed onto"
assert_eq "issue-42-add-mx-records-for-papernapkin-tech-99" "$(hand "$c" branch.txt)" "branch.txt"
it "and says so"
assert_contains "$ERR" "already exists on the remote" "stderr"

c="$(new_checkout collide_local)"; issue_json "$c/issue.json" "infra-request" "x"
git -C "$c/repo" switch -qc issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" push -q origin issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" switch -q main
p "$c"
it "and with no GITHUB_RUN_ID it still renames instead of dying under set -u"
assert_eq "ready" "$OUT" "outcome"
it "which is a bug that would only ever have appeared on a retry"
assert_not_contains "$ERR" "unbound variable" "stderr"

# --- the clean-tree assertion, and where it sits ----------------------------

c="$(new_checkout dirty)"; issue_json "$c/issue.json" "infra-request" "x"
printf 'locals {\n  a = 99\n}\n' >"$c/repo/dns/main.tf"
p "$c"
it "a dirty tree is a mechanical refusal, not an outcome"
assert_eq 1 "$RC" "exit code"
it "and prints no outcome word"
assert_eq "" "$OUT" "stdout"
it "and names what is dirty"
assert_contains "$ERR" "dns/main.tf" "stderr"

# The origin asserted this AFTER the claim, the ack and the branch, so a dirty
# tree thanked the requester, assigned the issue, cut a branch and then died.
# The human-facing skill put it in preflight. Preflight is encoded here.
it "and the requester is not thanked for a run that was never going to happen"
assert_not_contains "$(ghlog "$c")" "POST $API/issues/42/comments" "API calls"
it "nor is the issue claimed"
assert_eq "" "$(mutations "$c")" "mutating API calls"
it "nor is a branch left behind"
assert_eq "main" "$(git -C "$c/repo" branch --show-current)" "branch"

# --- re-entry (issue #25) ---------------------------------------------------
#
# needs-info blocks a first claim and admits a reply, which a flat precedence
# list cannot say. The event decides, or --re-entry does.

ev() { # path action extra-jq
  jq -n --argjson pr "${3:-null}" --arg t "${4:-User}" \
    '{action:$ARGS.named.a, comment:{user:{type:$t}},
      issue:{state:"open", pull_request:$pr,
             labels:[{name:"infra-request"},{name:"needs-info"}], body:"x"}}' \
    --arg a "$2" >"$1"
}

c="$(new_checkout reentry)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
ev "$c/event.json" created
p "$c" --event "$c/event.json"
it "a human's comment on a needs-info issue is a way back in"
assert_eq "ready" "$OUT" "outcome"
it "and the parking label is cleared, because requesters usually cannot"
assert_contains "$(ghlog "$c")" "DELETE $API/issues/42/labels/needs-info" "API calls"
it "but the requester is not thanked twice"
assert_not_contains "$(ghlog "$c")" "POST $API/issues/42/comments" "API calls"
it "and no ack.md is written"
assert_file_missing "$c/repo/.falconet/ack.md"

c="$(new_checkout reentry_bot)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
ev "$c/event.json" created null Bot
p "$c" --event "$c/event.json"
it "a bot's comment is not, or the pipeline would answer itself"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout reentry_pr)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
ev "$c/event.json" created '{"url":"x"}'
p "$c" --event "$c/event.json"
it "and neither is a comment on a pull request"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout reentry_flag)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
p "$c" --re-entry
it "--re-entry is how a workstation says it, with no event to read"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout reentry_blocked)"; issue_json "$c/issue.json" \
  "infra-request,needs-info,do-not-apply" "x"
p "$c" --re-entry
it "and re-entry admits needs-info only — the other blocking labels still block"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout reentry_failclear)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
REMOVE_RC=1 p "$c" --re-entry; reset
it "a label that cannot be cleared is fatal, unlike the claim and the ack"
assert_eq 1 "$RC" "exit code"
it "because an issue left parked while a run proceeds is a contradiction"
assert_eq "" "$OUT" "stdout"

# GitHub answers 404 when the label is not on the issue; gh removed nothing and
# said nothing. A retry of a re-entry run that had already cleared it is that.
c="$(new_checkout reentry_alreadyclear)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
REMOVE_RC=404 p "$c" --re-entry; reset
it "a label that is already gone is not a failure to clear it: the run is ready"
assert_eq "ready" "$OUT" "outcome"
it "and says so"
assert_contains "$ERR" "was already clear" "stderr"

# The bash captured `gh pr list` with no check and fell through to ready on an
# empty answer. A gate must not say ready on an unknown.
c="$(new_checkout pullsfail)"; issue_json "$c/issue.json" "infra-request" "x"
PULLS_RC=1 p "$c"; reset
it "an open-pull-request list that cannot be fetched is a mechanical failure, not ready"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
it "and nothing was changed on the way"
assert_eq "" "$(mutations "$c")" "mutating calls"
assert_eq "main" "$(git -C "$c/repo" branch --show-current)" "branch"

c="$(new_checkout issuenull)"; issue_json "$c/issue.json" "infra-request" "x"
jq -n '{action:"labeled", issue:{state:"open", labels:[{name:"infra-request"}], body:"x"}}' >"$c/event.json"
ISSUE_NULL=1 p "$c" --event "$c/event.json"; reset
it "an issue that comes back as null is a sentence and exit 1, not a stack trace"
assert_eq 1 "$RC" "exit code"
assert_not_contains "$ERR" "panic" "stderr"
assert_contains "$ERR" "not a JSON object" "stderr"

# --- the event file itself --------------------------------------------------

c="$(new_checkout noevent)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c" --event "$c/nowhere.json"
it "an --event that names no file is a mechanical failure, not an outcome"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

c="$(new_checkout badevent)"; issue_json "$c/issue.json" "infra-request" "x"
printf '{not json\n' >"$c/event.json"
p "$c" --event "$c/event.json"
it "and so is one that is not JSON"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

# --- best-effort calls really are best-effort -------------------------------

c="$(new_checkout claimfails)"; issue_json "$c/issue.json" "infra-request" "x"
EDIT_RC=1 p "$c"; reset
it "a failed claim does not fail the run"
assert_eq "ready" "$OUT" "outcome"
it "but it is said out loud"
assert_contains "$ERR" "could not assign" "stderr"

c="$(new_checkout ackfails)"; issue_json "$c/issue.json" "infra-request" "x"
COMMENT_RC=1 p "$c"; reset
it "nor does a failed acknowledgment"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout noack)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c" --no-ack
it "--no-ack skips the greeting for a caller that only wants the branch"
assert_not_contains "$(ghlog "$c")" "POST $API/issues/42/comments" "API calls"

c="$(new_checkout assignee)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c" --assignee bob
it "--assignee names who the claim is recorded against"
assert_contains "$(ghlog "$c")" "POST $API/issues/42/assignees {\"assignees\":[\"bob\"]}" "API calls"

# --- who is assigned, when nobody said -------------------------------------
#
# gh resolved `@me` on its own; the port asks GET /user, which an App token
# cannot answer. In CI the triggering actor is named instead, so the login
# lookup is the workstation's path.

c="$(new_checkout whoami)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c"
it "with neither --assignee nor GITHUB_TRIGGERING_ACTOR the token's own login is asked for"
assert_contains "$(ghlog "$c")" "GET /user" "API calls"
it "and the issue is assigned to it"
assert_contains "$(ghlog "$c")" "POST $API/issues/42/assignees {\"assignees\":[\"fake-user\"]}" "API calls"

c="$(new_checkout actor)"; issue_json "$c/issue.json" "infra-request" "x"
ACTOR=alice p "$c"; reset
it "GITHUB_TRIGGERING_ACTOR names the assignee in CI"
assert_contains "$(ghlog "$c")" "POST $API/issues/42/assignees {\"assignees\":[\"alice\"]}" "API calls"
it "and the token's login is not asked for"
assert_not_contains "$(ghlog "$c")" "GET /user" "API calls"

c="$(new_checkout whoami_fails)"; issue_json "$c/issue.json" "infra-request" "x"
USER_RC=1 p "$c"; reset
it "a token that cannot say whose it is — an App token — is a warning, not a failure"
assert_contains "$ERR" "could not assign" "stderr"
it "and the run is still ready"
assert_eq "ready" "$OUT" "outcome"

# --- hard failures ----------------------------------------------------------

c="$(new_checkout viewfails)"; issue_json "$c/issue.json" "infra-request" "x"
VIEW_RC=1 p "$c"; reset
it "an issue that cannot be read is a mechanical failure, not an outcome"
assert_eq 1 "$RC" "exit code"

# --- the token and the repository, resolved when first needed --------------

c="$(new_checkout notoken)"; issue_json "$c/issue.json" "infra-request" "x"
( unset GH_TOKEN GITHUB_TOKEN; p "$c"; printf '%s\n%s\n' "$RC" "$OUT" >"$c/result" )
it "with no GH_TOKEN the ready path is a mechanical failure"
assert_eq 1 "$(sed -n 1p "$c/result")" "exit code"
it "and prints no outcome word"
assert_eq "" "$(sed -n 2p "$c/result")" "stdout"
it "and nothing reaches GitHub"
assert_eq "" "$(ghlog "$c")" "API calls"

c="$(new_checkout notoken_event)"; issue_json "$c/issue.json" "infra-request,wontfix" "x"
jq -n '{action:"labeled", issue:{state:"open", labels:[{name:"infra-request"},{name:"wontfix"}], body:"x"}}' \
  >"$c/event.json"
( unset GH_TOKEN GITHUB_TOKEN; p "$c" --event "$c/event.json"; printf '%s\n' "$OUT" >"$c/result" )
it "an event that says ineligible needs no token at all"
assert_eq "ineligible" "$(cat "$c/result")" "outcome"
it "and makes no request, not even to read"
assert_eq "" "$(ghlog "$c")" "API calls"

# The fixture's origin is a bare repository on disk, not github.com, so
# without GITHUB_REPOSITORY there is no repository to ask about.
c="$(new_checkout norepo)"; issue_json "$c/issue.json" "infra-request" "x"
( unset GITHUB_REPOSITORY; p "$c"; printf '%s\n%s\n' "$RC" "$OUT" >"$c/result"; cp "$c/err" "$c/err.saved" )
it "with no GITHUB_REPOSITORY and an origin that is not on github.com there is nowhere to ask: exit 1"
assert_eq 1 "$(sed -n 1p "$c/result")" "exit code"
assert_eq "" "$(sed -n 2p "$c/result")" "stdout"
it "and the message names both sources"
assert_contains "$(cat "$c/err.saved")" "GITHUB_REPOSITORY" "stderr"
assert_contains "$(cat "$c/err.saved")" "origin" "stderr"
it "before any mutation"
assert_eq "" "$(mutations "$c")" "mutating API calls"
assert_eq "main" "$(git -C "$c/repo" branch --show-current)" "branch"

# --- $GITHUB_ENV is optional ------------------------------------------------

c="$(new_checkout noghenv)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c"
it "with no GITHUB_ENV the files are still written and the run still succeeds"
assert_eq "ready" "$OUT" "outcome"
it "and branch.txt is there, because the files are the contract"
assert_contains "$(hand "$c" branch.txt)" "issue-42-" "branch.txt"

# --- config -----------------------------------------------------------------

c="$(new_checkout cfg)"
printf '{"issue":{"queue_label":"ops","branch_prefix":"req-"}}\n' \
  >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A; git -C "$c/repo" commit -qm cfg
git -C "$c/repo" push -q origin main
issue_json "$c/issue.json" "ops" "x"
p "$c"
it "the queue label comes from config"
assert_eq "ready" "$OUT" "outcome"
it "and so does the branch prefix"
assert_contains "$(hand "$c" branch.txt)" "req-42-" "branch.txt"

# --- usage ------------------------------------------------------------------

it "a missing --issue is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" prepare >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "an --issue that is not a number is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" prepare --issue abc >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "an unknown argument is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" prepare --bogus >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "-h/--help is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" prepare --help >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

summary
