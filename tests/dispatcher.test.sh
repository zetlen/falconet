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

it "but prompt is still dispatched, not refused as unknown"
run prompt implement
assert_eq "1" "$RC" "exit code: not-implemented, not usage"

# --- mid-port: a verb with no file behind it --------------------------------
#
# Every verb hits this branch until its task lands, so its message is read
# more often during the port than any other line in this file. A bare 127
# from exec would be true and useless.

it "a known but unbuilt verb exits 1, not 2 and not 127"
run commit
assert_eq "1" "$RC" "exit code"

it "and names the verb"
assert_contains "$ERR" "verb 'commit' is not implemented yet"

it "and names the path it looked for"
assert_contains "$ERR" "libexec/falconet/commit.sh"

# --- the handoff directory is ignored ---------------------------------------
#
# First line of the defence, exactly as .ci-handoff/ was: a `git add -A`
# cannot pick up an ignored path, and validate refuses a commit that force-adds
# it anyway.

it "the handoff directory is gitignored"
assert_contains "$(cat "$REPO_ROOT/.gitignore")" ".falconet/"

summary
