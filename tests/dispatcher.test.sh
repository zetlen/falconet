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
# OUTCOME" is, because four of the five pipeline verbs put a single word on stdout and
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

# --- unlisted verbs still dispatch ------------------------------------------
#
# What usage lists, and what it does not, is `go test ./cmd/falconet`'s.

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

# --- the unlisted verbs' exit codes and streams -----------------------------
#
# config, prompt and scan are helpers rather than pipeline steps, and their
# logic lives in `go test ./...`. What only a process boundary can show is
# kept here: which number each answers with, which stream carries the answer,
# what is left on disk, and what the gitleaks stub was handed.

# config: the value on stdout, the complaint on stderr.
CFG="$WORK/cfg"
mkdir -p "$CFG/.github"

it "config reads a value to stdout and exits 0"
OUT="$( cd "$CFG" && "$FALCONET" config get .labels.human 2>"$WORK/err" )"; RC=$?
assert_eq "0" "$RC" "exit code"
assert_eq "ready-for-human" "$OUT" "stdout"

it "malformed JSON is exit 1, with the file named on stderr and nothing on stdout"
printf '{"issue": {"queue_label": }\n' >"$CFG/.github/falconet.json"
OUT="$( cd "$CFG" && "$FALCONET" config get .labels.human 2>"$WORK/err" )"; RC=$?
assert_eq "1" "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$(cat "$WORK/err")" ".github/falconet.json is not valid JSON" "stderr"

it "--config naming no file is exit 1, on stderr"
rm "$CFG/.github/falconet.json"
OUT="$( cd "$CFG" && "$FALCONET" config --config "$CFG/nope.json" get .handoff_dir 2>"$WORK/err" )"; RC=$?
assert_eq "1" "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$(cat "$WORK/err")" "--config names no file" "stderr"

# prompt: a file named by the config is what prints, and printing is a read.
PRM="$WORK/prm"
mkdir -p "$PRM/.github/custom"
printf 'A different prompt entirely.\n' >"$PRM/.github/custom/mine.md"
printf '{"prompts":{"implement":".github/custom/mine.md"}}\n' >"$PRM/.github/falconet.json"

it "a prompts.implement override in .github/falconet.json is what prompt prints"
OUT="$( cd "$PRM" && "$FALCONET" prompt implement 2>"$WORK/err" )"; RC=$?
assert_eq "0" "$RC" "exit code"
assert_contains "$OUT" "A different prompt entirely." "stdout"

it "and printing it left no handoff directory behind"
assert_file_missing "$PRM/.falconet"

it "an override naming a missing file exits 1 and names the file"
printf '{"prompts":{"implement":"custom/gone.md"}}\n' >"$PRM/.github/falconet.json"
OUT="$( cd "$PRM" && "$FALCONET" prompt implement 2>"$WORK/err" )"; RC=$?
assert_eq "1" "$RC" "exit code"
assert_contains "$(cat "$WORK/err")" "custom/gone.md" "stderr"

it "prompt with no name is 2, and with an unknown name is 1"
run prompt
assert_eq "2" "$RC" "exit code"
run prompt nosuch
assert_eq "1" "$RC" "exit code"

# scan: a repository with a staged change, and a gitleaks that finds
# token-shaped strings on its stdin. The token is assembled at runtime so this
# file never holds a credential-shaped string itself. Git's own config is shut
# out so a workstation's external diff driver cannot rewrite the staged diff.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
fake_token() { printf 'ghp_%s' '0123456789abcdefghijABCDEFGHIJ012345'; }
SCN="$WORK/scn"
mkdir -p "$SCN/bin"
git init -q -b main "$SCN/repo"
git -C "$SCN/repo" config user.email ci@example.invalid
git -C "$SCN/repo" config user.name ci
printf 'locals {\n  a = 1\n}\n' >"$SCN/repo/main.tf"
git -C "$SCN/repo" add -A
git -C "$SCN/repo" commit -qm "base commit"
cat >"$SCN/bin/gitleaks" <<'STUB'
#!/usr/bin/env bash
code=1
prev=""
for a in "$@"; do [[ "$prev" == "--exit-code" ]] && code="$a"; prev="$a"; done
if grep -qE 'gh[ps]_[A-Za-z0-9]{36}'; then echo "leaks found: 1" >&2; exit "$code"; fi
echo "no leaks found" >&2
exit 0
STUB
chmod +x "$SCN/bin/gitleaks"
scan() { # args... -> sets OUT RC, stdout only
  OUT="$( cd "$SCN/repo" && GITLEAKS="$SCN/bin/gitleaks" "$FALCONET" scan "$@" 2>/dev/null )"; RC=$?
  return 0
}

it "scan of a clean file exits 0 and prints nothing"
printf 'Add a record\n' >"$SCN/repo/msg.txt"
scan -- msg.txt
assert_eq "0" "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

it "a match exits 3 and names the channel, never the value"
{ printf 'the header was '; fake_token; printf '\n'; } >"$SCN/repo/msg.txt"
scan -- msg.txt
assert_eq "3" "$RC" "exit code"
assert_eq "msg.txt" "$OUT" "stdout"
assert_not_contains "$OUT" "$(fake_token)" "stdout"

it "--staged against a staged token exits 3 and says 'staged change'"
{ printf 'locals {\n  a = "'; fake_token; printf '"\n}\n'; } >"$SCN/repo/main.tf"
git -C "$SCN/repo" add main.tf
scan --staged
assert_eq "3" "$RC" "exit code"
assert_contains "$OUT" "staged change" "stdout"

it "a missing gitleaks exits 1, never 0"
OUT="$( cd "$SCN/repo" && GITLEAKS="$SCN/bin/nope" "$FALCONET" scan -- msg.txt 2>/dev/null )"; RC=$?
assert_eq "1" "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

it "no targets at all is a usage error, not a silent pass"
scan
assert_eq "2" "$RC" "exit code"

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
mkdir -p "$PROJ/dns" "$PROJ/.github"
git init -q -b main "$PROJ"
git -C "$PROJ" config user.email ci@example.invalid
git -C "$PROJ" config user.name ci
printf 'locals {\n  a = 1\n}\n' >"$PROJ/dns/main.tf"
printf '.falconet/\n' >"$PROJ/.gitignore"
printf '{"paths":{"allow":["*.tf"]}}\n' >"$PROJ/.github/falconet.json"
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

# --- the handoff directory is ignored ---------------------------------------
#
# First line of the defence, exactly as .ci-handoff/ was: a `git add -A`
# cannot pick up an ignored path, and commit refuses a commit that force-adds
# it anyway.

it "the handoff directory is gitignored"
assert_contains "$(cat "$REPO_ROOT/.gitignore")" ".falconet/"

summary
