#!/usr/bin/env bash
#
# dispatcher.test.sh — bin/falconet resolves a verb and gets out of the way.
#
# The dispatcher's whole contract is exit discipline and silence: usage errors
# are 2, an unimplemented verb is 1, and a verb that runs owns its own stdout.
# Everything here is asserted across a process boundary, which is the only way
# these tests will still mean something after the Bun port.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

FALCONET="$REPO_ROOT/bin/falconet"

# stdout and stderr are captured separately throughout. "Prints usage" is not
# the assertion — "prints usage WHERE A HUMAN SEES IT AND NOT INTO THE
# OUTCOME" is, because five of the six verbs put a single word on stdout and
# a dispatcher that chattered there would corrupt every one of them.
run() { # args... -> sets OUT ERR RC
  OUT="$("$FALCONET" "$@" 2>"$WORK/err")"; RC=$?
  ERR="$(cat "$WORK/err")"
  return 0
}

# --- usage ------------------------------------------------------------------

it "no verb at all is a usage error"
run
assert_eq "2" "$RC" "exit code"

it "and it says what it wanted"
assert_contains "$ERR" "no verb given"

it "an unknown verb is a usage error"
run nosuch
assert_eq "2" "$RC" "exit code"

it "and it names the verb it did not recognize"
assert_contains "$ERR" "unknown verb 'nosuch'"

it "--help is 2, not 0 — 0 would mean a verb ran and was happy"
run --help
assert_eq "2" "$RC" "exit code"

it "usage goes to stderr"
assert_contains "$ERR" "Usage: falconet <verb>"

it "and never to stdout, which belongs to the outcome word"
assert_eq "" "$OUT" "stdout"

# --- the verb list ----------------------------------------------------------

it "usage lists all six verbs"
run --help
for v in prepare commit push validate park assemble; do
  case "$ERR" in *"  $v "*) ;; *) echo "  FAIL  usage omits $v" ;; esac
done
assert_contains "$ERR" "assemble"

it "prompt is deliberately unlisted"
assert_not_contains "$ERR" "prompt "

# --- mid-port: a verb with no file behind it --------------------------------
#
# Asserted against an EMPTY install rather than against whichever verb happens
# to be unbuilt today: bin/falconet finds libexec/ relative to itself, so a
# copy of it beside an empty libexec/falconet/ is the mid-port state, frozen.
# The obvious version of this test named a real verb and went green the moment
# that verb landed, which is a test that stops asking its question exactly
# when the answer starts changing.

EMPTY="$WORK/empty-install"
mkdir -p "$EMPTY/bin" "$EMPTY/libexec/falconet"
cp "$FALCONET" "$EMPTY/bin/falconet"

bare() { # args... -> sets OUT ERR RC
  OUT="$("$EMPTY/bin/falconet" "$@" 2>"$WORK/err")"; RC=$?
  ERR="$(cat "$WORK/err")"
  return 0
}

it "a known verb with no file behind it exits 1, not 2 and not 127"
bare commit
assert_eq "1" "$RC" "exit code"

it "and names the verb"
assert_contains "$ERR" "verb 'commit' is not implemented yet"

it "and names the path it looked for"
assert_contains "$ERR" "libexec/falconet/commit.sh"

it "prompt is unlisted but still dispatched, not refused as unknown"
bare prompt implement
assert_eq "1" "$RC" "exit code: not-implemented, not usage"

it "and a file that exists but is not executable is also 1, with a different reason"
: >"$EMPTY/libexec/falconet/park.sh"
bare park --issue 1
assert_eq "1" "$RC" "exit code"
assert_contains "$ERR" "is not executable"

# --- a verb that exists really is exec'd ------------------------------------
#
# The other half of the contract: dispatch hands over, and what comes back is
# the verb's answer rather than the dispatcher's. A verb's own usage is 2, the
# same number an unknown verb gets, so the distinguishing evidence is that the
# text is the VERB's.

it "a built verb is exec'd, and answers for itself"
run commit --bogus-flag-no-verb-would-accept
assert_eq "2" "$RC" "exit code"
assert_contains "$ERR" "unknown argument"

# --- the tool and the repository it works on are different places -----------
#
# The origin's scripts lived INSIDE the repository they operated on, so "one
# directory above scripts/" answered both questions at once. falconet is a
# separate tool: in CI the composite action checks it out somewhere of its
# own, and in the strangler's endgame it is a binary on $PATH. A verb that
# still used its own location to find the working tree would operate on
# falconet — silently, reporting an outcome about the wrong repository.
#
# Every other test in this suite now runs the verb from here and the fixture
# from a temp directory, so they all cover this incidentally. This one says it
# out loud, because it is the property and not a side effect.

PROJ="$WORK/elsewhere"
mkdir -p "$PROJ/dns"
git init -q -b main "$PROJ"
git -C "$PROJ" config user.email ci@example.invalid
git -C "$PROJ" config user.name ci
printf 'locals {\n  a = 1\n}\n' >"$PROJ/dns/main.tf"
printf '.falconet/\n' >"$PROJ/.gitignore"
git -C "$PROJ" add -A
git -C "$PROJ" commit -qm "base commit"

# A scanner that finds nothing, so these cases are about which repository the
# verb chose and not about whether gitleaks is installed on this machine.
mkdir -p "$WORK/bin"
printf '#!/usr/bin/env bash\nexit 0\n' >"$WORK/bin/gitleaks"
chmod +x "$WORK/bin/gitleaks"
export GITLEAKS="$WORK/bin/gitleaks"

it "a verb run from another repository operates on THAT repository"
out="$( cd "$PROJ" && "$FALCONET" commit 2>&1 )"
assert_eq "failure" "$out" "outcome"

it "and its reason names that repository's state, not falconet's"
assert_contains "$(cat "$PROJ/.falconet/failure-reason.txt" 2>/dev/null)" \
  "left the repository unchanged" "failure reason"

it "and falconet's own tree is not where the handoff landed"
assert_file_missing "$REPO_ROOT/.falconet/failure-reason.txt"

it "\$FALCONET_REPO names the working repository explicitly"
out="$( cd "$WORK" && FALCONET_REPO="$PROJ" "$FALCONET" commit 2>&1 )"
assert_eq "failure" "$out" "outcome"

it "and a \$FALCONET_REPO that names nothing is a legible failure"
out="$( cd "$WORK" && FALCONET_REPO="$WORK/nope" "$FALCONET" commit 2>&1 )"; rc=$?
assert_eq 1 "$rc" "exit code"

# --- the handoff directory is ignored ---------------------------------------
#
# First line of the defence, exactly as .ci-handoff/ was: a `git add -A`
# cannot pick up an ignored path, and validate refuses a commit that force-adds
# it anyway.

it "the handoff directory is gitignored"
assert_contains "$(cat "$REPO_ROOT/.gitignore")" ".falconet/"

summary
