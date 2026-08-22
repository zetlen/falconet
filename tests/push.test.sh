#!/usr/bin/env bash
#
# What must be true of the push that run 32093607680 did not make.
#
# Everything here runs against a bare repository on disk, so the tests say
# nothing about GitHub and never touch the network. What they do pin down is
# the git behaviour the pipeline now depends on: append-only pushes succeed,
# an amended commit still lands, and a branch this run has never seen is
# refused rather than overwritten.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

# No token: the script then leaves the `origin` URL alone, which is what lets
# these cases point at a bare repo in $WORK. The rewrite has its own case at
# the bottom.
export GH_TOKEN=""
export GITHUB_REPOSITORY=""
export GIT_TERMINAL_PROMPT=0
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null

# A checkout that looks like the pipeline's: a bare remote, a clone with one
# base commit on main, and the work branch checked out. The verb finds the
# repository from the working directory, so every case cds into it.
new_checkout() { # name -> echoes the checkout path
  local base="$WORK/$1"
  mkdir -p "$base"
  git init --bare -q "$base/remote.git"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  echo base >"$base/repo/base.txt"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" remote add origin "$base/remote.git"
  git -C "$base/repo" push -q origin main
  git -C "$base/repo" switch -qc issue-1-thing
  printf '%s' "$base"
}

commit_in() { # checkout message
  echo "$2" >>"$1/repo/work.txt"
  git -C "$1/repo" add -A
  git -C "$1/repo" commit -qm "$2"
}

push_in() { # checkout [extra args...] -> runs the script, output on stdout
  local c="$1"; shift
  ( cd "$c/repo" && GITHUB_ENV="$c/github_env" "$FALCONET" push "$@" 2>&1 )
}

push_stdout_in() { # checkout [extra args...] -> stdout ONLY, stderr discarded
  local c="$1"; shift
  ( cd "$c/repo" && GITHUB_ENV="$c/github_env" "$FALCONET" push "$@" 2>/dev/null )
}

remote_tip() { # checkout -> subject of the remote branch tip, or empty
  git -C "$1/remote.git" log -1 --format=%s issue-1-thing 2>/dev/null
}

# --- nothing committed ------------------------------------------------------

c="$(new_checkout nothing)"
base_sha="$(git -C "$c/repo" rev-parse HEAD)"
: >"$c/github_env"

it "no commit: the script succeeds and says so"
out="$(push_in "$c" --branch issue-1-thing --base-sha "$base_sha")"
assert_eq 0 "$?" "exit code"

it "no commit: nothing is pushed"
assert_eq "" "$(remote_tip "$c")" "remote tip"

it "no commit: PUSHED_BRANCH is not set, so no hand-over can link a branch"
assert_eq "" "$(cat "$c/github_env")" "GITHUB_ENV"

it "no commit: the log explains itself"
assert_contains "$out" "nothing to push" "output"

# --- the ordinary path ------------------------------------------------------

c="$(new_checkout ordinary)"
base_sha="$(git -C "$c/repo" rev-parse HEAD)"
: >"$c/github_env"
commit_in "$c" "add the thing"

it "first commit: the branch reaches the remote"
push_in "$c" --branch issue-1-thing --base-sha "$base_sha" >/dev/null
assert_eq "add the thing" "$(remote_tip "$c")" "remote tip"

it "first commit: PUSHED_BRANCH is recorded for the hand-over comments"
assert_eq "PUSHED_BRANCH=issue-1-thing" "$(cat "$c/github_env")" "GITHUB_ENV"

it "a second commit fast-forwards"
commit_in "$c" "fix the thing"
push_in "$c" --branch issue-1-thing --base-sha "$base_sha" >/dev/null
assert_eq "fix the thing" "$(remote_tip "$c")" "remote tip"

# --- the amend the tool grant permits ---------------------------------------
#
# The amend prompts say `git commit -m`, but Bash(git commit:*) also matches
# `git commit --amend`. A plain push would fail non-fast-forward here and
# strand the work for a different reason than the one this fix is about.

it "an amended commit still lands (--force-with-lease, not a plain push)"
echo more >>"$c/repo/work.txt"
git -C "$c/repo" add -A
git -C "$c/repo" commit -q --amend -m "fix the thing, amended"
push_in "$c" --branch issue-1-thing --base-sha "$base_sha" >/dev/null
assert_eq "fix the thing, amended" "$(remote_tip "$c")" "remote tip"

it "a re-push with nothing new is a no-op, not a failure"
out="$(push_in "$c" --branch issue-1-thing --base-sha "$base_sha")"
assert_eq 0 "$?" "exit code"

# --- someone else's branch under the same name ------------------------------
#
# Stage 1 renames the branch when it finds one on the remote, so this should
# be unreachable. If it ever becomes reachable, refusing the push is the right
# answer: a previous run's commits are worth more than this one's.

c="$(new_checkout collision)"
base_sha="$(git -C "$c/repo" rev-parse HEAD)"
: >"$c/github_env"
# The previous run, in its own checkout — which is the only way to get a
# branch onto the remote that THIS checkout has no remote-tracking ref for.
# (Pushing it from here would create that ref, the lease would match, and the
# case would prove nothing.)
git clone -q "$c/remote.git" "$c/previous"
git -C "$c/previous" config user.email old@example.invalid
git -C "$c/previous" config user.name old
git -C "$c/previous" switch -qc issue-1-thing
echo stale >"$c/previous/stale.txt"
git -C "$c/previous" add -A
git -C "$c/previous" commit -qm "a previous run's work"
git -C "$c/previous" push -q origin issue-1-thing
commit_in "$c" "this run's work"

it "an unfetched remote branch is refused, not clobbered"
push_in "$c" --branch issue-1-thing --base-sha "$base_sha" >/dev/null 2>&1
assert_eq 1 "$?" "exit code"

it "the previous run's commit is still the remote tip"
assert_eq "a previous run's work" "$(remote_tip "$c")" "remote tip"

it "a failed push records no PUSHED_BRANCH"
assert_eq "" "$(cat "$c/github_env")" "GITHUB_ENV"

# --- the credential rewrite -------------------------------------------------
#
# claude-code-action leaves `origin` pointing at a revoked token of its own,
# so every push has to restore a working URL first. It now restores a
# TOKENLESS one and hands git the credential through a `-c` helper instead,
# because the URL it used to write stayed in .git/config for the rest of the
# job — through the `tofu plan` over agent-authored .tf files, and through the
# reviewing agent's Read (issue #41).
#
# The push itself is expected to fail here — the point is what the remote and
# .git/config look like afterwards, and that a failure is loud and non-zero.

c="$(new_checkout rewrite)"
base_sha="$(git -C "$c/repo" rev-parse HEAD)"
: >"$c/github_env"
commit_in "$c" "work to push"
git -C "$c/repo" remote set-url origin "https://x-access-token:revoked@127.0.0.1:1/o/r.git"

it "the origin URL is rebuilt without any credential in it"
( cd "$c/repo" && GH_TOKEN=fresh-token \
    GITHUB_SERVER_URL=https://127.0.0.1:1 GITHUB_REPOSITORY=o/r \
    GITHUB_ENV="$c/github_env" "$FALCONET" push \
      --branch issue-1-thing --base-sha "$base_sha" >/dev/null 2>&1 )
assert_eq "https://127.0.0.1:1/o/r.git" \
  "$(git -C "$c/repo" remote get-url origin)" "origin URL"

it "and .git/config carries neither the username nor the token"
config="$(cat "$c/repo/.git/config")"
assert_not_contains "$config" "x-access-token" ".git/config"

it "and not the token value either, under any other key"
assert_not_contains "$config" "fresh-token" ".git/config"

it "an unreachable remote fails the step rather than passing quietly"
out="$( cd "$c/repo" && GH_TOKEN=fresh-token \
    GITHUB_SERVER_URL=https://127.0.0.1:1 GITHUB_REPOSITORY=o/r \
    GITHUB_ENV="$c/github_env" "$FALCONET" push \
      --branch issue-1-thing --base-sha "$base_sha" 2>&1 )"
assert_contains "$out" "::error::could not push" "output"

# --- a SUCCESSFUL push down the credential path ------------------------------
#
# The case above proves what is left behind after a failure; this one proves
# the credential path can still land a push at all, and leaves nothing behind
# either. $GITHUB_SERVER_URL is a directory, so the URL the script builds —
# "${server}/${GITHUB_REPOSITORY}.git" — resolves to the bare repo in $WORK.
#
# Be honest about the limit: a local path needs no authentication, so git
# never asks the helper anything. What this covers is that the `-c` pair does
# not break an otherwise ordinary push, that the tokenless URL is what git is
# left pointing at, and that nothing wrote the token into the config on the
# way. The helper actually ANSWERING a challenge needs an https remote and a
# real token, which these tests cannot have; the case below exercises the
# helper's shell instead, and the end-to-end path needs a live run.

c="$(new_checkout credential_path)"
base_sha="$(git -C "$c/repo" rev-parse HEAD)"
: >"$c/github_env"
commit_in "$c" "work to push"
git -C "$c/repo" remote set-url origin "https://x-access-token:revoked@127.0.0.1:1/o/r.git"
# A git that writes its argv down and then gets out of the way. What the verb
# hands git on the command line is the evidence for the helper cases below,
# and a recording stub is the only way to see it from outside the process —
# the suite is not allowed to read the verb's source instead.
REAL_GIT="$(command -v git)"
mkdir -p "$c/stubbin"
cat >"$c/stubbin/git" <<STUB
#!/usr/bin/env bash
printf '%s\n' "\$@" >>"$c/git-argv.txt"
printf -- '--\n' >>"$c/git-argv.txt"
exec "$REAL_GIT" "\$@"
STUB
chmod +x "$c/stubbin/git"
out="$( cd "$c/repo" && PATH="$c/stubbin:$PATH" GH_TOKEN=fake-token-value \
    GITHUB_SERVER_URL="$c" GITHUB_REPOSITORY=remote \
    GITHUB_ENV="$c/github_env" "$FALCONET" push \
      --branch issue-1-thing --base-sha "$base_sha" 2>&1 )"

it "the branch lands with the helper flags in place"
assert_eq "work to push" "$(remote_tip "$c")" "remote tip"

it "and PUSHED_BRANCH is recorded, so a hand-over can link it"
assert_eq "PUSHED_BRANCH=issue-1-thing" "$(cat "$c/github_env")" "GITHUB_ENV"

it "and the token is nowhere in .git/config afterwards"
config="$(cat "$c/repo/.git/config")"
assert_not_contains "$config" "fake-token-value" ".git/config"

it "and neither is the username half of the old URL form"
assert_not_contains "$config" "x-access-token" ".git/config"

it "and nothing echoed the token into the script's own output"
assert_not_contains "$out" "fake-token-value" "output"

# --- the helper string itself ------------------------------------------------
#
# Taken out of git's recorded argv rather than copied here, so a change to the
# helper is a failing test and not a stale duplicate. git runs a `!`-prefixed
# helper through a shell with the operation appended, which is what the
# `"$@"` reproduces.
#
# The property under test is WHEN $GH_TOKEN expands: single-quoted in the
# verb, it is still the literal string `$GH_TOKEN` in git's argv, and only
# the shell git spawns for the helper turns it into the value. Double quotes
# there would put the token on the command line, where /proc/<pid>/cmdline
# shows it to anything else on the runner. Until ADR-0006 D3 step 0 these
# were the suite's two cases that read their subject's SOURCE; the recording
# git above is what let them be rewritten rather than dropped.

argv="$(cat "$c/git-argv.txt")"

it "the helper hands git a password out of the environment, not out of argv"
helper="$(grep '^credential\.helper=!' "$c/git-argv.txt" | head -1)"
body="${helper#credential.helper=!}"
out="$(GH_TOKEN=fake-token-value sh -c "$body \"\$@\"" helper get 2>&1)"
assert_contains "$out" "password=fake-token-value" "helper output"

it "and identifies itself as x-access-token"
assert_contains "$out" "username=x-access-token" "helper output"

it "and the token reached git unexpanded: \$GH_TOKEN is in argv, its value is not"
assert_contains "$argv" 'password=$GH_TOKEN' "git argv"
assert_not_contains "$argv" "fake-token-value" "git argv"

# --- usage ------------------------------------------------------------------

it "-h/--help is a usage error, not a successful push"
( cd "$REPO_ROOT" && "$FALCONET" push --help >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "and an unknown argument is too"
( cd "$REPO_ROOT" && "$FALCONET" push --bogus >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "a missing --branch is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" push >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

# --- stdout belongs to the verbs that decide something ----------------------
#
# push decides nothing, so it prints nothing. The wrapper captures a verb's
# stdout and writes it to $GITHUB_OUTPUT; when push printed "pushed <branch>
# (<sha>)" the write was "Invalid format", and a `publish` job that had
# already pushed the branch failed on the way out — no validate, no pull
# request, and an issue parked for a human over a log line.

c="$(new_checkout quiet)"
base_sha="$(git -C "$c/repo" rev-parse HEAD)"
: >"$c/github_env"

it "nothing to push: stdout is empty"
assert_eq "" "$(push_stdout_in "$c" --branch issue-1-thing --base-sha "$base_sha")" "stdout"

commit_in "$c" "a change"

it "pushed: stdout is still empty, and the sentence went to stderr"
assert_eq "" "$(push_stdout_in "$c" --branch issue-1-thing --base-sha "$base_sha")" "stdout"

it "and the branch really was pushed, so the silence is not a no-op"
assert_eq "a change" "$(remote_tip "$c")" "remote tip"

summary
