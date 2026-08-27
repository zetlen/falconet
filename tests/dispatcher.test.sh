#!/usr/bin/env bash
#
# dispatcher.test.sh — `falconet <verb>` resolves a verb and gets out of the way.
#
# The dispatcher's whole contract is exit discipline and silence: usage errors
# are 2, and a verb that runs owns its own stdout. Everything here is asserted
# across a process boundary, which is what let these tests keep meaning
# something through the port to Go (ADR-0006) and after it.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"


# stdout and stderr are captured separately throughout. "Prints usage" is not
# the assertion — "prints usage WHERE A HUMAN SEES IT AND NOT INTO THE
# OUTCOME" is, because three of the four pipeline verbs put a single word on stdout and
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

it "usage lists all four pipeline verbs"
run --help
for v in prepare commit push pause; do
  case "$ERR" in *"  $v "*) ;; *) echo "  FAIL  usage omits $v" ;; esac
done
assert_contains "$ERR" "pause"

it "and the retired plan-side verbs are gone, not listed"
for v in validate assemble plan-env; do
  assert_not_contains "$ERR" "  $v " "usage"
done

it "prompt is deliberately unlisted"
assert_not_contains "$ERR" "prompt "

it "park is gone, not aliased or listed"
assert_not_contains "$ERR" "  park "
run park --issue 1 --label needs-info --preamble x
assert_eq "2" "$RC" "exit code"
assert_contains "$ERR" "unknown verb 'park'"

it "scan and config are unlisted too, and dispatch rather than being refused"
OUT="$( cd "$WORK" && "$FALCONET" config get .handoff_dir 2>/dev/null )"; RC=$?
assert_eq "0" "$RC" "config exit code"
assert_eq ".falconet" "$OUT" "config get .handoff_dir"
( cd "$WORK" && "$FALCONET" scan --help >/dev/null 2>&1 )
assert_eq "2" "$?" "scan --help exit code"

# --- mid-port: a verb with no file behind it --------------------------------
#
# Gone. The case that lived here copied the bash dispatcher beside an empty
# verb directory — the one test that knew its subject was a shell script, and
# the one ADR-0004 named as the test to watch. ADR-0006 D3 step 0 retired it,
# and #19 deleted the fallback it was about: a verb the binary knows is a
# verb the binary implements, and `go test ./cmd/falconet` holds that.

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
# separate tool — a binary on $PATH, in CI and on a workstation — and a verb
# that used its own location to find the working tree would operate on
# wherever the binary sits, silently, reporting an outcome about the wrong
# repository.
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
# cannot pick up an ignored path, and commit refuses a commit that force-adds
# it anyway.

it "the handoff directory is gitignored"
assert_contains "$(cat "$REPO_ROOT/.gitignore")" ".falconet/"

summary
