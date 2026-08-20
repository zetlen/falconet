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
# `gh` is stubbed. Nothing here reaches GitHub or the network.

# The expected strings below are markdown: single-quoted backticks are code
# spans in a GitHub comment, not command substitution.
# shellcheck disable=SC2016

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PARK="$REPO_ROOT/scripts/ci-park-issue.sh"

# --- the gh stub ------------------------------------------------------------

mkdir -p "$WORK/bin"
cat >"$WORK/bin/gh" <<'STUB'
#!/usr/bin/env bash
# Records every call, and keeps a copy of whatever body file was posted, so a
# test can read exactly what a requester would have been shown.
printf '%s\n' "$*" >>"$GH_STUB_LOG"
if [ "${1:-}" = "issue" ] && [ "${2:-}" = "comment" ]; then
  while [ $# -gt 0 ]; do
    [ "$1" = "--body-file" ] && cp "$2" "$GH_STUB_COMMENT"
    shift
  done
fi
exit 0
STUB
chmod +x "$WORK/bin/gh"
PATH="$WORK/bin:$PATH"
export PATH

park() { # out-name -- args...
  local name="$1"; shift
  [ "$1" = "--" ] && shift
  export GH_STUB_LOG="$WORK/$name.log"
  export GH_STUB_COMMENT="$WORK/$name.comment"
  : >"$GH_STUB_LOG"
  : >"$GH_STUB_COMMENT"
  "$PARK" "$@" >/dev/null 2>&1
}

export GITHUB_SERVER_URL=https://github.com
export GITHUB_REPOSITORY=zetlen/wayfinders-infra

# --- a hand-over with work behind it ----------------------------------------

park review -- \
  --issue 36 \
  --label ready-for-human \
  --unassign zetlen \
  --run-url https://example.invalid/run/32093607680 \
  --branch issue-36-onboard-ozamataz-buckshank-as-a-full-tim \
  --preamble "I prepared this change, but the automated review stage did not return a usable verdict, so I have not opened a pull request. This one needs a person."
comment="$(cat "$WORK/review.comment")"

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

it "the issue is still labelled and the claim still released"
log="$(cat "$WORK/review.log")"
assert_contains "$log" "issue edit 36 --add-label ready-for-human" "gh calls"

it "the run URL is still cited"
assert_contains "$comment" "https://example.invalid/run/32093607680" "comment"

# --- a hand-over with nothing behind it -------------------------------------
#
# The commonest way to reach a hand-over is an agent that committed nothing.
# An empty --branch must produce no pointer at all rather than a link to a
# branch that was never pushed — the failure this whole change is about,
# reintroduced from the other direction.

park empty -- \
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
assert_contains "$(cat "$WORK/empty.log")" "issue edit 36 --add-label" "gh calls"

# --- outside Actions, name the branch but invent no URL ---------------------

it "with no GITHUB_REPOSITORY the branch is named but not linked"
( unset GITHUB_REPOSITORY GITHUB_SERVER_URL
  export GH_STUB_LOG="$WORK/local.log" GH_STUB_COMMENT="$WORK/local.comment"
  : >"$GH_STUB_LOG"; : >"$GH_STUB_COMMENT"
  "$PARK" --issue 36 --label ready-for-human --branch issue-36-thing \
    --preamble "Parked." >/dev/null 2>&1 )
comment="$(cat "$WORK/local.comment")"
assert_contains "$comment" 'branch `issue-36-thing`' "comment"

it "with no GITHUB_REPOSITORY no URL is fabricated"
assert_not_contains "$comment" "http" "comment"

# --- push, then hand over: the invariant run 32093607680 broke --------------

export GH_TOKEN=""
export GIT_TERMINAL_PROMPT=0
checkout="$WORK/pipeline"
mkdir -p "$checkout/repo/scripts"
git init --bare -q "$checkout/remote.git"
git init -q -b main "$checkout/repo"
git -C "$checkout/repo" config user.email ci@example.invalid
git -C "$checkout/repo" config user.name ci
cp "$REPO_ROOT/scripts/ci-push-branch.sh" "$checkout/repo/scripts/"
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

# The step right after it pushes, and records the branch it pushed.
: >"$checkout/github_env"
( cd "$checkout/repo" && GITHUB_ENV="$checkout/github_env" \
    ./scripts/ci-push-branch.sh --branch issue-36-onboard --base-sha "$BASE_SHA" ) >/dev/null 2>&1

# The review verdict comes back unusable, exactly as it did on #36, and the
# hand-over step reads PUSHED_BRANCH out of the environment the push wrote.
# shellcheck source=/dev/null
. "$checkout/github_env"
export GITHUB_SERVER_URL=https://github.com
export GITHUB_REPOSITORY=zetlen/wayfinders-infra
park pipeline -- \
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

summary
