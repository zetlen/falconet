#!/usr/bin/env bash
#
# What the guard on the publish boundary must do (issue #41).
#
# The two files this scans are the only agent-authored text that leaves the
# runner verbatim — commit-msg.txt becomes the pull-request body,
# needs-info.md becomes a comment on the requester's issue — and the third
# thing it scans, the staged diff, is what `tofu plan` echoes into that same
# pull request. So the cases below are mostly about the two ways this script
# can be worse than useless: reporting a clean scan that never happened, and
# repeating the secret it just found into the very comment that reports it.
#
# gitleaks itself is stubbed. These tests pin down THIS script's contract —
# which channels it scans, what it prints, and what it does with each exit
# code — not gitleaks' rule set, which is gitleaks' business and changes
# between releases.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null

# Assembled at runtime and never written literally into this file: a fixture
# that IS a credential-shaped string would be a finding in any future scan of
# this repository, which is a silly way to make a test suite unrunnable.
fake_token() { printf 'ghp_%s' '0123456789abcdefghijABCDEFGHIJ012345'; }

# A checkout with the script in it, a git repo around it (so --staged has
# something to read), and a `gitleaks` that finds token-shaped strings on its
# stdin and nothing else.
new_checkout() { # name -> echoes the checkout path
  local base="$WORK/$1"
  mkdir -p "$base/repo/scripts" "$base/bin"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  cp "$REPO_ROOT/scripts/ci-secret-scan.sh" "$base/repo/scripts/"
  printf 'locals {\n  a = 1\n}\n' >"$base/repo/records-example-tech.tf"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"

  cat >"$base/bin/gitleaks" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GITLEAKS_CALLS"
code=1
prev=""
for a in "$@"; do [[ "$prev" == "--exit-code" ]] && code="$a"; prev="$a"; done
if grep -qE 'gh[ps]_[A-Za-z0-9]{36}'; then
  echo "leaks found: 1" >&2
  exit "$code"
fi
echo "no leaks found" >&2
exit 0
STUB
  chmod +x "$base/bin/gitleaks"
  printf '%s' "$base"
}

scan_in() { # checkout argument... -> runs the script, stdout only
  local c="$1"; shift
  ( cd "$c/repo" \
    && GITLEAKS="$c/bin/gitleaks" GITLEAKS_CALLS="$c/gitleaks-calls.txt" \
       ./scripts/ci-secret-scan.sh "$@" 2>/dev/null )
}

# --- a clean channel ---------------------------------------------------------

c="$(new_checkout clean)"
printf 'Add a record\n\nBecause the requester asked for one.\n' >"$c/repo/msg.txt"
out="$(scan_in "$c" -- msg.txt)"; rc=$?

it "a clean file exits 0"
assert_eq 0 "$rc" "exit code"

it "and prints nothing at all"
assert_eq "" "$out" "stdout"

it "and the scanner was asked to redact what it prints"
assert_contains "$(cat "$c/gitleaks-calls.txt")" "--redact" "gitleaks arguments"

# --- a channel carrying something token-shaped -------------------------------

c="$(new_checkout dirty)"
{ printf 'Add a record\n\nFor traceability the header was '
  fake_token; printf '\n'; } >"$c/repo/msg.txt"
out="$(scan_in "$c" -- msg.txt)"; rc=$?

it "a match exits 3, which is not gitleaks' own error code"
assert_eq 3 "$rc" "exit code"

it "and names the channel that matched"
assert_eq "msg.txt" "$out" "stdout"

it "and never prints the matched value"
assert_not_contains "$out" "$(fake_token)" "stdout"

# ci-commit-change.sh resolves its handoff paths to absolute ones before it
# calls this, and the name printed here is spliced into a comment on the
# requester's issue. "$HOME/work/repo/repo/.ci-handoff/commit-msg.txt" is a
# runner detail nobody can act on; ".ci-handoff/commit-msg.txt" is the name
# the pipeline's own documentation uses.
out="$(scan_in "$c" -- "$c/repo/msg.txt")"

it "an absolute path inside the repository is named relative to it"
assert_eq "msg.txt" "$out" "stdout"

# --- absent and empty channels are normal states -----------------------------
#
# A run with no questions has no needs-info.md; a run that asked one and
# changed nothing has an empty commit-msg.txt. Neither is a finding, and
# neither is an error — deciding what an empty message MEANS is
# ci-commit-change.sh's job.

c="$(new_checkout absent)"
: >"$c/repo/empty.txt"
out="$(scan_in "$c" -- no-such-file.txt empty.txt)"; rc=$?

it "a missing or empty file is skipped rather than failed"
assert_eq 0 "$rc" "exit code"

it "and nothing is scanned"
assert_file_missing "$c/gitleaks-calls.txt"

# --- several channels, one of them dirty -------------------------------------

c="$(new_checkout mixed)"
printf 'A clean commit message\n' >"$c/repo/msg.txt"
{ printf -- '- Which zone? The token I read was '; fake_token; printf '\n'; } \
  >"$c/repo/questions.md"
out="$(scan_in "$c" -- msg.txt questions.md)"; rc=$?

it "one dirty channel among several exits 3"
assert_eq 3 "$rc" "exit code"

it "and only the dirty one is named"
assert_eq "questions.md" "$out" "stdout"

# --- the staged diff ---------------------------------------------------------

c="$(new_checkout staged)"
{ printf 'locals {\n  a = "'; fake_token; printf '"\n}\n'; } \
  >"$c/repo/records-example-tech.tf"
git -C "$c/repo" add -A
out="$(scan_in "$c" --staged)"; rc=$?

it "a token-shaped string in the staged diff exits 3"
assert_eq 3 "$rc" "exit code"

it "and the staged change is named as the channel"
assert_contains "$out" "staged change" "stdout"

c="$(new_checkout staged_clean)"
printf 'locals {\n  a = 2\n}\n' >"$c/repo/records-example-tech.tf"
git -C "$c/repo" add -A
out="$(scan_in "$c" --staged)"; rc=$?

it "an ordinary staged change exits 0"
assert_eq 0 "$rc" "exit code"

it "and prints nothing"
assert_eq "" "$out" "stdout"

# --- the scanner's own stdout is not this script's stdout --------------------
#
# `gitleaks --verbose` prints its findings to STDOUT, and this script's stdout
# is spliced into a comment on the requester's issue by ci-commit-change.sh.
# A finding line carries the text SURROUNDING the secret, so letting it
# through would publish the agent-authored context this whole guard exists to
# withhold — and would put a second line into a stdout the caller reads as a
# list of channel names.

c="$(new_checkout chatty)"
cat >"$c/bin/gitleaks" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GITLEAKS_CALLS"
cat >/dev/null
printf 'Finding:     the header was REDACTED\n'
printf 'RuleID:      github-pat\n'
printf 'Fingerprint: stdin:github-pat:1\n'
echo "leaks found: 1" >&2
exit 3
STUB
chmod +x "$c/bin/gitleaks"
printf 'anything at all\n' >"$c/repo/msg.txt"
out="$(scan_in "$c" -- msg.txt)"; rc=$?

it "a chatty scanner still exits 3"
assert_eq 3 "$rc" "exit code"

it "and its findings do not reach this script's stdout"
assert_eq "msg.txt" "$out" "stdout"

# --- a scan that could not run is not a clean scan ---------------------------

c="$(new_checkout no_binary)"
printf 'anything at all\n' >"$c/repo/msg.txt"
out="$( cd "$c/repo" && GITLEAKS="$c/bin/nope" ./scripts/ci-secret-scan.sh -- msg.txt 2>/dev/null )"
rc=$?

it "a missing gitleaks exits 1, never 0"
assert_eq 1 "$rc" "exit code"

it "and names no channel, because none was scanned"
assert_eq "" "$out" "stdout"

c="$(new_checkout broken)"
cat >"$c/bin/gitleaks" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GITLEAKS_CALLS"
cat >/dev/null
echo "FTL failed to load config" >&2
exit 1
STUB
chmod +x "$c/bin/gitleaks"
printf 'anything at all\n' >"$c/repo/msg.txt"
out="$(scan_in "$c" -- msg.txt)"; rc=$?

it "gitleaks' own fatal exit (1) is not mistaken for a finding"
assert_eq 1 "$rc" "exit code"

it "and no channel is named"
assert_eq "" "$out" "stdout"

# --- usage -------------------------------------------------------------------

c="$(new_checkout usage)"

it "no targets at all is a usage error, not a silent pass"
scan_in "$c" >/dev/null
assert_eq 2 "$?" "exit code"

it "an unknown argument exits 2"
scan_in "$c" --bogus >/dev/null
assert_eq 2 "$?" "exit code"

it "--help exits 2, because 0 means 'scanned, nothing found'"
scan_in "$c" --help >/dev/null
assert_eq 2 "$?" "exit code"

summary
