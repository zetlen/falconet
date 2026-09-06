#!/usr/bin/env bash
#
# The hand-over contract: when this pipeline stops without opening a pull
# request, the comment it leaves must point at work that exists.
#
# Run 32093607680 posted "I prepared this change ... This one needs a person"
# on issue #36 while `git ls-remote --heads origin 'issue-36*'` returned
# nothing. These cases are the two halves of that being fixed — the branch is
# on the remote, and the comment says where — tested together, because either
# one alone is still a broken promise.
#
# GitHub is tests/fixtures/fake-github.py: a loopback server that answers
# from fixtures and writes down what it was asked, with GITHUB_API_URL
# pointing at it. Nothing here reaches GitHub or the network.

# The expected strings below are markdown: single-quoted backticks are code
# spans in a GitHub comment, not command substitution.
# shellcheck disable=SC2016

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"


# --- the fake API, and no gh ------------------------------------------------

fake_github
export GH_TOKEN=test-token
export GITHUB_SERVER_URL=https://github.com
export GITHUB_REPOSITORY=zetlen/wayfinders-infra


pause() { # out-name -- args...
  # Runs pause, then leaves three files: $name.out, its stdout; $name.log,
  # one line per API call as `METHOD PATH BODY`; and $name.comment, the
  # exact bytes of the comment a requester would have been shown — the body
  # field of the one POST to .../comments, unescaped. Returns the exit code.
  local name="$1"; shift
  [ "$1" = "--" ] && shift
  : >"$FAKE_GITHUB/requests.log"
  : >"$FAKE_GITHUB/requests.jsonl"
  "$FALCONET" pause "$@" >"$WORK/$name.out" 2>/dev/null
  local rc=$?
  cp "$FAKE_GITHUB/requests.log" "$WORK/$name.log"
  jq -j 'select(.method == "POST" and (.path | endswith("/comments"))) | .body.body' \
    "$FAKE_GITHUB/requests.jsonl" >"$WORK/$name.comment"
  return "$rc"
}

calls() { # out-name -> "METHOD PATH" per line, in order
  awk '{ print $1, $2 }' "$WORK/$1.log"
}

# --- a hand-over with work behind it ----------------------------------------

pause review -- \
  --issue 36 \
  --label ready-for-human \
  --unassign zetlen \
  --run-url https://example.invalid/run/32093607680 \
  --branch issue-36-onboard-ozamataz-buckshank-as-a-full-tim \
  --preamble "I prepared this change, but the automated review stage did not return a usable verdict, so I have not opened a pull request. This one needs a person."
rc=$?
comment="$(cat "$WORK/review.comment")"

it "a hand-over is exit 0"
assert_eq 0 "$rc" "exit code"

it "and the one word on stdout is success"
assert_eq "success" "$(cat "$WORK/review.out")" "stdout"

it "the hand-over comment still leads with its preamble"
case "$comment" in
  "I prepared this change,"*) _pass ;;
  *) _fail "comment should open with the preamble" "got: [${comment:0:120}]" ;;
esac

it "the hand-over comment names the branch"
assert_contains "$comment" \
  'branch `issue-36-onboard-ozamataz-buckshank-as-a-full-tim`' "comment"

it "the hand-over comment links the branch"
assert_contains "$comment" \
  "https://github.com/zetlen/wayfinders-infra/tree/issue-36-onboard-ozamataz-buckshank-as-a-full-tim" \
  "comment"

it "the hand-over comment says no pull request is open"
assert_contains "$comment" "No pull request is open for it." "comment"

it "the branch pointer comes before any collapsed detail block"
before="${comment%%<details>*}"
assert_contains "$before" "tree/issue-36-onboard" "text above <details>"

it "the issue is labelled, on the repository Actions named"
log="$(cat "$WORK/review.log")"
assert_contains "$log" \
  'POST /repos/zetlen/wayfinders-infra/issues/36/labels {"labels":["ready-for-human"]}' "API calls"

it "and the claim is released"
assert_contains "$log" \
  'DELETE /repos/zetlen/wayfinders-infra/issues/36/assignees {"assignees":["zetlen"]}' "API calls"

it "the comment is posted first, then the label, then the claim is released"
assert_eq "POST /repos/zetlen/wayfinders-infra/issues/36/comments
POST /repos/zetlen/wayfinders-infra/issues/36/labels
DELETE /repos/zetlen/wayfinders-infra/issues/36/assignees" "$(calls review)" "call order"

it "the run URL is still cited"
assert_contains "$comment" "https://example.invalid/run/32093607680" "comment"

it "the token travels as a bearer header, on every call"
assert_eq "Bearer test-token
Bearer test-token
Bearer test-token" \
  "$(jq -r '.headers.authorization' "$FAKE_GITHUB/requests.jsonl")" "Authorization"

# --- the old name ------------------------------------------------------------
#
# `park` was the verb until #5's rename. It is not an alias: there are no
# users yet, and two words for one verb is the drift #5 was filed to prevent.

it "park is gone, not aliased"
: >"$FAKE_GITHUB/requests.log"; : >"$FAKE_GITHUB/requests.jsonl"
out="$("$FALCONET" park --issue 36 --label ready-for-human --preamble "Parked." 2>"$WORK/park.err")"
assert_eq 2 "$?" "exit code"
assert_eq "" "$out" "stdout"
assert_contains "$(cat "$WORK/park.err")" "unknown verb 'park'" "stderr"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

# --- a hand-over with nothing behind it -------------------------------------
#
# The commonest way to reach a hand-over is an agent that committed nothing.
# An empty --branch must produce no pointer at all rather than a link to a
# branch that was never pushed — the failure this whole change is about,
# reintroduced from the other direction.

pause empty -- \
  --issue 36 \
  --label ready-for-human \
  --run-url https://example.invalid/run/1 \
  --branch "" \
  --preamble "I tried to prepare this change automatically, twice, and the configuration checks rejected it both times."
comment="$(cat "$WORK/empty.comment")"

it "an empty --branch mentions no branch"
assert_not_contains "$comment" "branch" "comment"

it "an empty --branch links nothing"
assert_not_contains "$comment" "/tree/" "comment"

it "an empty --branch is not a usage error"
assert_contains "$(cat "$WORK/empty.log")" "issues/36/labels" "API calls"

it "and no --unassign releases nothing"
assert_not_contains "$(cat "$WORK/empty.log")" "assignees" "API calls"

# --- where to post, and with what -------------------------------------------
#
# GITHUB_REPOSITORY is the one source for the repository — the variable
# Actions sets in every run, and which a local run exports. Guessing it from
# a git remote is how a comment lands on the wrong repository. The token is
# GH_TOKEN or GITHUB_TOKEN, the two names the workflow hands the verbs.

( unset GITHUB_REPOSITORY
  pause norepo -- --issue 36 --label ready-for-human --preamble "Parked." )
rc=$?

it "with no GITHUB_REPOSITORY there is nowhere to post: exit 1, and the word is failure"
assert_eq 1 "$rc" "exit code"
assert_eq "failure" "$(cat "$WORK/norepo.out")" "stdout"

it "and nothing was posted anywhere"
assert_eq "" "$(cat "$WORK/norepo.log")" "API calls"

( unset GH_TOKEN GITHUB_TOKEN
  pause notoken -- --issue 36 --label ready-for-human --preamble "Parked." )
rc=$?

it "with neither GH_TOKEN nor GITHUB_TOKEN it is failure, before any call"
assert_eq 1 "$rc" "exit code"
assert_eq "failure" "$(cat "$WORK/notoken.out")" "stdout"
assert_eq "" "$(cat "$WORK/notoken.log")" "API calls"

( unset GH_TOKEN; export GITHUB_TOKEN=actions-token
  pause ghtoken -- --issue 36 --label ready-for-human --preamble "Parked." )
rc=$?

it "GITHUB_TOKEN is accepted when GH_TOKEN is unset"
assert_eq 0 "$rc" "exit code"
assert_eq "Bearer actions-token" \
  "$(jq -r '.headers.authorization' "$FAKE_GITHUB/requests.jsonl" | head -1)" "Authorization"

# --- a GitHub call fails ----------------------------------------------------
#
# `failure`, exit 1: the caller must treat the issue as still un-paused. It
# is exit 1 and not commit's 0 because nothing downstream routes on the word
# — the step must fail so the containment job runs. Each of the three calls
# is attempted regardless of the one before it: an issue that got its label
# and not its comment is still better paused than not.

printf '[{"method":"POST","path":"/repos/zetlen/wayfinders-infra/issues/36/comments","status":500,"body":{"message":"boom"}}]\n' \
  >"$FAKE_GITHUB/responses.json"
pause nocomment -- --issue 36 --label ready-for-human --unassign zetlen --preamble "Parked."
rc=$?

it "a comment GitHub refuses is failure, exit 1"
assert_eq 1 "$rc" "exit code"
assert_eq "failure" "$(cat "$WORK/nocomment.out")" "stdout"

it "and the label and the un-assign are still tried"
assert_eq "POST /repos/zetlen/wayfinders-infra/issues/36/comments
POST /repos/zetlen/wayfinders-infra/issues/36/labels
DELETE /repos/zetlen/wayfinders-infra/issues/36/assignees" "$(calls nocomment)" "call order"

printf '[{"method":"POST","path":"/repos/zetlen/wayfinders-infra/issues/36/labels","status":404,"body":{"message":"Not Found"}}]\n' \
  >"$FAKE_GITHUB/responses.json"
pause nolabel -- --issue 36 --label ready-for-human --preamble "Parked."
rc=$?

it "a label GitHub refuses is failure too"
assert_eq 1 "$rc" "exit code"
assert_eq "failure" "$(cat "$WORK/nolabel.out")" "stdout"

# Releasing the claim is best-effort: an issue that keeps a stale assignee is
# still paused.
printf '[{"method":"DELETE","path":"/repos/zetlen/wayfinders-infra/issues/36/assignees","status":422,"body":{"message":"Validation Failed"}}]\n' \
  >"$FAKE_GITHUB/responses.json"
: >"$FAKE_GITHUB/requests.log"
out="$("$FALCONET" pause --issue 36 --label ready-for-human --unassign zetlen --preamble "Parked." 2>"$WORK/unassign.err")"
rc=$?

it "a failed un-assign is a warning, not a failure"
assert_eq 0 "$rc" "exit code"
assert_eq "success" "$out" "stdout"
assert_contains "$(cat "$WORK/unassign.err")" "::warning::could not un-assign zetlen from #36" "stderr"

rm -f "$FAKE_GITHUB/responses.json"

# --- push, then hand over: the invariant run 32093607680 broke --------------

export GIT_TERMINAL_PROMPT=0
checkout="$WORK/pipeline"
mkdir -p "$checkout/repo/scripts"
git init --bare -q "$checkout/remote.git"
git init -q -b main "$checkout/repo"
git -C "$checkout/repo" config user.email ci@example.invalid
git -C "$checkout/repo" config user.name ci
echo base >"$checkout/repo/base.txt"
git -C "$checkout/repo" add -A
git -C "$checkout/repo" commit -qm base
git -C "$checkout/repo" remote add origin "$checkout/remote.git"
git -C "$checkout/repo" push -q origin main
BASE_SHA="$(git -C "$checkout/repo" rev-parse HEAD)"
git -C "$checkout/repo" switch -qc issue-36-onboard

# The implementing agent commits.
echo "ozamataz" >>"$checkout/repo/records-papernapkin-tech.tf"
git -C "$checkout/repo" add -A
git -C "$checkout/repo" commit -qm "Add Ozamataz Buckshank to the employees list"

# The step right after it pushes, and records the branch it pushed. No token:
# push then leaves the origin URL alone, which is what lets it point at the
# bare repository above.
: >"$checkout/github_env"
( cd "$checkout/repo" && GH_TOKEN="" GITHUB_ENV="$checkout/github_env" \
    "$FALCONET" push --branch issue-36-onboard --base-sha "$BASE_SHA" ) >/dev/null 2>&1

# The review verdict comes back unusable, exactly as it did on #36, and the
# hand-over step reads PUSHED_BRANCH out of the environment the push wrote.
# shellcheck source=/dev/null
. "$checkout/github_env"
pause pipeline -- \
  --issue 36 \
  --label ready-for-human \
  --run-url https://example.invalid/run/32093607680 \
  --branch "${PUSHED_BRANCH:-}" \
  --preamble "I prepared this change, but the automated review stage did not return a usable verdict, so I have not opened a pull request. This one needs a person."
comment="$(cat "$WORK/pipeline.comment")"

it "the pipeline's own hand-over names a branch"
assert_contains "$comment" 'branch `issue-36-onboard`' "comment"

it "and that branch really is on the remote"
assert_eq "Add Ozamataz Buckshank to the employees list" \
  "$(git -C "$checkout/remote.git" log -1 --format=%s issue-36-onboard)" "remote tip"

it "and the commit on it is the work the comment promises"
assert_contains \
  "$(git -C "$checkout/remote.git" show --name-only --format= issue-36-onboard)" \
  "records-papernapkin-tech.tf" "files on the remote branch"

# --- the body: a file that is not there is no body ----------------------------
#
# --body is extra detail under the preamble, read from a file. How it is
# rendered — prose pasted as it is, machine output fenced and collapsed, and
# the cap — is internal/pause's business and is tested there. What is seen
# from outside is which files count as "no body" and which are a refusal.

it "a --body that names no file is no body, not an error"
pause nobody -- \
  --issue 36 --label ready-for-human --branch "" \
  --body "$WORK/does-not-exist.txt" --preamble "Parked."
assert_eq "Parked." "$(cat "$WORK/nobody.comment")" "comment"

it "and so is an empty one"
: >"$WORK/empty.txt"
pause emptybody -- \
  --issue 36 --label ready-for-human --branch "" \
  --body "$WORK/empty.txt" --preamble "Parked."
assert_eq "Parked." "$(cat "$WORK/emptybody.comment")" "comment"

it "but a --body that names a directory is failure, not a comment with a hole in it"
mkdir -p "$WORK/adir"
pause dirbody -- \
  --issue 36 --label ready-for-human --branch "" \
  --body "$WORK/adir" --preamble "Parked."
assert_eq 1 "$?" "exit code"
assert_eq "failure" "$(cat "$WORK/dirbody.out")" "stdout"
assert_eq "" "$(cat "$WORK/dirbody.log")" "API calls"

# --- the pause labels come from config --------------------------------------
#
# The allowlist survives the move; only its contents are configurable now. It
# stays an allowlist because every route in is one of two terminal states, and
# a typo that invented a third would park an issue under a label nothing
# queries and nobody is watching -- the silent disappearance this verb exists
# to prevent.

cfgdir="$WORK/parkcfg"; mkdir -p "$cfgdir/.github"
printf '{"labels":{"needs_info":"awaiting-reply","human":"escalated"}}\n' \
  >"$cfgdir/.github/falconet.json"

it "a label the config names is accepted"
( cd "$cfgdir" && "$FALCONET" pause --issue 7 --label escalated --preamble "This needs a person." \
    >/dev/null 2>&1 )
assert_eq 0 "$?" "exit code"

it "and the default label is refused once the config has replaced it"
out=$( cd "$cfgdir" && "$FALCONET" pause --issue 7 --label ready-for-human --preamble x 2>&1 )
rc=$?
assert_eq 2 "$rc" "exit code"

it "and the message names the labels that would have worked"
assert_contains "$out" "awaiting-reply" "usage message"

it "an invented label is a usage error, not a silent third terminal state"
( cd "$WORK" && "$FALCONET" pause --issue 7 --label parked-somewhere --preamble x >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

# --- usage ------------------------------------------------------------------

it "-h/--help is a usage error"
( cd "$WORK" && "$FALCONET" pause --help >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "a missing --preamble is a usage error"
( cd "$WORK" && "$FALCONET" pause --issue 7 --label needs-info >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "an --issue that is not a number is a usage error"
( cd "$WORK" && "$FALCONET" pause --issue seven --label needs-info --preamble x >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "a usage error posts nothing, and puts no word on stdout"
: >"$FAKE_GITHUB/requests.log"
out="$( cd "$WORK" && "$FALCONET" pause --issue 7 --label needs-info --preamble x --bogus 2>/dev/null )"
assert_eq 2 "$?" "exit code"
assert_eq "" "$out" "stdout"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

summary
