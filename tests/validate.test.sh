#!/usr/bin/env bash
#
# validate.test.sh — the deterministic gate, under test for the first time.
#
# ci-validate.sh shipped with no test file. Everything here is new, which
# means none of it is a port and all of it is a decision: where the assertions
# disagree with what the script did before, the disagreement is deliberate and
# the case name says so.
#
# Note what this verb does NOT have: a one-word stdout. Five of the six verbs
# print an outcome; this one prints the whole plan into the run log on
# purpose, because that is the untruncated copy a PR body's truncation note
# points a reviewer at. Its verdict is its exit code.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null

# A tofu that runs nothing, records every invocation, and fails exactly where
# the case says to. Shaped like the real binary: validate and plan talk on
# stdout, errors on stderr, and a failing plan emits the partial output the
# real one does before it gives up.
#
# The [[ -p /dev/stdout ]] line is a tripwire for the SIGPIPE prohibition: a
# plan whose stdout is a pipe is the bug the script's comment forbids, because
# a short reader's SIGPIPE kills tofu before it releases its state lock.
# Recorded rather than fatal, so a failure names the invocation.
make_tofu() { # path
  cat >"$1" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$TOFU_CALLS"
[[ -p /dev/stdout ]] && printf 'STDOUT-IS-A-PIPE: %s\n' "$*" >>"$TOFU_CALLS"
stack="" verb=""
for a in "$@"; do
  case "$a" in
    -chdir=*) stack="$(basename "${a#-chdir=}")" ;;
    -*) ;;
    *) [[ -z "$verb" ]] && verb="$a" ;;
  esac
done
fails() { case " ${!1:-} " in *" $stack "*) return 0 ;; *) return 1 ;; esac; }
case "$verb" in
  init)
    fails TOFU_FAIL_INIT && { echo "Error: backend init failed for $stack" >&2; exit 1; }
    echo "OpenTofu has been successfully initialized!" ;;
  validate)
    fails TOFU_FAIL_VALIDATE && {
      echo "Error: Unsupported argument" >&2
      echo "  on $stack/main.tf line 3" >&2
      exit 1; }
    echo "Success! The configuration is valid." ;;
  plan)
    printf 'OpenTofu will perform the following actions.\n\n'
    printf '  # %s_record.example will be created\n' "$stack"
    fails TOFU_FAIL_PLAN && {
      echo "Error: Resource precondition failed" >&2
      echo "  on $stack/guards-zone.tf line 12" >&2
      exit 1; }
    printf '\nPlan: 1 to add, 0 to change, 0 to destroy.\n' ;;
esac
exit 0
STUB
  chmod +x "$1"
}

# A checkout with the verb inside it (it finds the repository root from its own
# location), a git repo around it, three stacks, and one commit on top of base.
new_checkout() { # name -> echoes the checkout path
  local base="$WORK/$1"
  mkdir -p "$base/repo/.github" "$base/repo/.falconet" "$base/bin"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  local s
  for s in dns workspace site; do
    mkdir -p "$base/repo/$s"
    printf 'locals {\n  a = 1\n}\n' >"$base/repo/$s/main.tf"
  done
  printf '.falconet/\n.ci-handoff/\n' >"$base/repo/.gitignore"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" switch -qc issue-1-thing
  make_tofu "$base/bin/tofu"
  printf '%s' "$base"
}

# The commit the run is measured against, plus one commit on top of it.
with_change() { # checkout [subject]
  local c="$1" subject="${2:-Add a record}"
  printf 'locals {\n  a = 2\n}\n' >"$c/repo/dns/records-example-tech.tf"
  git -C "$c/repo" add -A
  git -C "$c/repo" commit -qm "$subject"
}

base_of() { git -C "$1/repo" rev-parse main; }

# git reports the PHYSICAL path, and on a Mac $WORK is under a symlinked
# /var. Assertions about paths the tool printed have to speak its dialect.
phys() { ( cd "$1/repo" && pwd -P ); }

v() { # checkout base [extra args...] -> sets OUT ERR RC
  local c="$1" b="$2"; shift 2
  OUT="$( cd "$c/repo" \
    && TOFU="$c/bin/tofu" TOFU_CALLS="$c/tofu-calls.txt" \
       TOFU_FAIL_INIT="${FAIL_INIT:-}" \
       TOFU_FAIL_VALIDATE="${FAIL_VALIDATE:-}" \
       TOFU_FAIL_PLAN="${FAIL_PLAN:-}" \
       GITHUB_ENV="${GH_ENV:-}" \
       "$REPO_ROOT/libexec/falconet/validate.sh" --base "$b" "$@" 2>"$c/err" )"
  RC=$?
  ERR="$(cat "$c/err")"
  return 0
}
reset_fail() { FAIL_INIT=""; FAIL_VALIDATE=""; FAIL_PLAN=""; GH_ENV=""; }
reset_fail

calls() { cat "$1/tofu-calls.txt" 2>/dev/null; }
report() { cat "$1/repo/.falconet/validation-failure.txt" 2>/dev/null; }

# --- failures are collected, not raced to the first one ---------------------
#
# The script carries no `set -e` on purpose: a failed validation goes straight
# to a human, so the one report it writes is the only report anybody gets and
# it had better be complete.

c="$(new_checkout collect)"; b="$(base_of "$c")"; with_change "$c"
FAIL_VALIDATE="workspace" v "$c" "$b"; reset_fail

it "a failing stack does not stop the ones after it"
assert_contains "$(calls "$c")" "/site validate" "tofu calls"

it "and the run still fails"
assert_eq 1 "$RC" "exit code"

c="$(new_checkout collect_two)"; b="$(base_of "$c")"; with_change "$c"
FAIL_VALIDATE="workspace site" v "$c" "$b"; reset_fail

it "every failing stack gets its own section in the report"
assert_contains "$(report "$c")" "## tofu validate failed (workspace/)" "report"

it "including the last one"
assert_contains "$(report "$c")" "## tofu validate failed (site/)" "report"

c="$(new_checkout collect_first)"; b="$(base_of "$c")"; with_change "$c"
FAIL_VALIDATE="dns workspace site" v "$c" "$b"; reset_fail

it "a failure in the first stack does not cancel the rest"
assert_contains "$(calls "$c")" "/site validate" "tofu calls"

# --- the plan gate keys on the planned stacks alone -------------------------

c="$(new_checkout gate_other)"; b="$(base_of "$c")"; with_change "$c"
FAIL_VALIDATE="site" v "$c" "$b"; reset_fail

it "a broken validate-only stack does not cancel the one plan a reviewer acts on"
assert_contains "$(calls "$c")" "/dns plan" "tofu calls"

it "and that plan is still snapshotted, on a run that failed"
assert_contains "$(cat "$c/repo/.falconet/plan.txt" 2>/dev/null)" \
  "Plan: 1 to add" "plan.txt"

c="$(new_checkout gate_planned)"; b="$(base_of "$c")"; with_change "$c"
FAIL_VALIDATE="dns" v "$c" "$b"; reset_fail

it "a planned stack whose validate failed is not then planned"
assert_not_contains "$(calls "$c")" "/dns plan" "tofu calls"

it "and the report says so rather than leaving the reader to infer it"
assert_contains "$(report "$c")" "## tofu plan was not attempted" "report"

it "and no plan.txt is left for the assembler"
assert_file_missing "$c/repo/.falconet/plan.txt"

# --- a half-written plan never reaches the assembler ------------------------

c="$(new_checkout plan_fails)"; b="$(base_of "$c")"; with_change "$c"
FAIL_PLAN="dns" v "$c" "$b"; reset_fail

it "a failing plan leaves no plan.txt"
assert_file_missing "$c/repo/.falconet/plan.txt"

it "but its partial output survives in the report"
assert_contains "$(report "$c")" "plan output before the failure" "report"

it "and the failing run exits 1"
assert_eq 1 "$RC" "exit code"

# --- the report is for the requester, and instructs nobody ------------------
#
# validation-failure.txt is posted verbatim as a comment on the REQUESTER's
# issue. It explains what happened to someone who asked for a DNS record. The
# script's header has always promised it gives no instructions, because there
# is nobody there to instruct — and the plan-failure path used to break that
# promise with a sentence addressed to an implementing agent. That sentence
# now goes to stderr, where a person debugging a run reads it.

it "the report carries no instruction addressed to an agent"
assert_not_contains "$(report "$c")" "never weaken it" "report"

it "and that guidance is not lost, only moved to where a debugger reads it"
assert_contains "$ERR" "never weaken it" "stderr"

# --- the evidence is snapshotted on failing runs too ------------------------

c="$(new_checkout evidence)"; b="$(base_of "$c")"; with_change "$c" "Add a record"
FAIL_VALIDATE="site" v "$c" "$b"; reset_fail

it "changed-files.txt is written even when validation fails"
assert_contains "$(cat "$c/repo/.falconet/changed-files.txt")" \
  "dns/records-example-tech.tf" "changed-files.txt"

it "and so is diff.patch"
assert_contains "$(cat "$c/repo/.falconet/diff.patch")" \
  "records-example-tech.tf" "diff.patch"

it "the diff carries each commit message beside the change it claims to make"
assert_contains "$(cat "$c/repo/.falconet/diff.patch")" "Add a record" "diff.patch"

# --- a clean run ------------------------------------------------------------

c="$(new_checkout clean)"; b="$(base_of "$c")"; with_change "$c"
GH_ENV="$c/github_env" v "$c" "$b"; reset_fail

it "a run where everything passes exits 0"
assert_eq 0 "$RC" "exit code"

it "and leaves no failure report"
assert_file_missing "$c/repo/.falconet/validation-failure.txt"

it "and writes the plan"
assert_contains "$(cat "$c/repo/.falconet/plan.txt")" "Plan: 1 to add" "plan.txt"

it "and records VALIDATED for the wrappers"
assert_contains "$(cat "$c/github_env" 2>/dev/null)" "VALIDATED=true" "GITHUB_ENV"

it "the planned stack gets a real init, so validate and plan share one"
assert_contains "$(calls "$c")" "-chdir=$(phys "$c")/dns init -input=false" "tofu calls"

it "and is initialized exactly once for both"
assert_eq 1 "$(grep -c -- "-chdir=$(phys "$c")/dns init" "$c/tofu-calls.txt")" "dns inits"

it "a validate-only stack is initialized without its backend"
assert_contains "$(calls "$c")" "-chdir=$(phys "$c")/workspace init -backend=false" "tofu calls"

it "and the planned stack is not"
assert_not_contains "$(calls "$c")" "-chdir=$(phys "$c")/dns init -backend=false" "tofu calls"

it "the plan is asked for no color, because its next reader is a file"
assert_contains "$(calls "$c")" "plan -no-color" "tofu calls"

it "and never refreshes, because the state credential is read-only"
assert_contains "$(calls "$c")" "-refresh=false" "tofu calls"

it "and never locks, for the same reason"
assert_contains "$(calls "$c")" "-lock=false" "tofu calls"

it "the plan is redirected to a file, never piped"
assert_not_contains "$(calls "$c")" "STDOUT-IS-A-PIPE" "tofu calls"

# --- guards -----------------------------------------------------------------

c="$(new_checkout smuggled)"; b="$(base_of "$c")"; with_change "$c"
printf 'secret\n' >"$c/repo/.falconet/plan.txt"
git -C "$c/repo" add -f .falconet/plan.txt
git -C "$c/repo" commit -qm "smuggle the handoff"
v "$c" "$b"

it "a commit carrying the handoff directory is refused"
assert_eq 1 "$RC" "exit code"

it "and the report names the smuggled path"
assert_contains "$(report "$c")" ".falconet/plan.txt" "report"

it "and it stops before any tofu runs, because the evidence would be a lie"
assert_file_missing "$c/tofu-calls.txt"

c="$(new_checkout nocommit)"; b="$(base_of "$c")"
v "$c" "$b"

it "no commit on top of --base is refused"
assert_eq 1 "$RC" "exit code"

it "and says so in the requester's own terms"
assert_contains "$(report "$c")" "No commit on the working branch" "report"

it "and nothing is snapshotted, because nothing got that far"
assert_file_missing "$c/repo/.falconet/diff.patch"

# --- --base is resolved, not string-compared --------------------------------
#
# This was a comparison against the raw argument. `--base main` made the guard
# above silently false, and then git log "main"..HEAD produced an empty
# diff.patch — a run that could exit 0 having snapshotted nothing, handed to a
# reviewing agent that holds no Bash and would have seen no change.

c="$(new_checkout baseref)"; b="$(base_of "$c")"
v "$c" "main"

it "a --base given as a ref, with no commit on top, is still refused"
assert_eq 1 "$RC" "exit code"

c="$(new_checkout baseshort)"; b="$(base_of "$c")"; with_change "$c"
v "$c" "$(git -C "$c/repo" rev-parse --short main)"

it "and a short sha resolves to the same commit a full one does"
assert_eq 0 "$RC" "exit code"

it "with a diff that is not empty"
assert_contains "$(cat "$c/repo/.falconet/diff.patch")" "records-example-tech.tf" "diff.patch"

c="$(new_checkout basebogus)"; b="$(base_of "$c")"; with_change "$c"
v "$c" "not-a-commit"

it "a --base naming no commit is a usage error, not an empty diff"
assert_eq 2 "$RC" "exit code"

it "and nothing is snapshotted for it"
assert_file_missing "$c/repo/.falconet/diff.patch"

# --- stale artifacts from an earlier attempt --------------------------------

c="$(new_checkout stale)"; b="$(base_of "$c")"; with_change "$c"
printf 'STALE PLAN\n' >"$c/repo/.falconet/plan.txt"
printf 'STALE REPORT\n' >"$c/repo/.falconet/validation-failure.txt"
FAIL_VALIDATE="dns" v "$c" "$b"; reset_fail

it "a stale plan from an earlier attempt is cleared, not inherited"
assert_file_missing "$c/repo/.falconet/plan.txt"

it "and a stale report does not survive into this one"
assert_not_contains "$(report "$c")" "STALE REPORT" "report"

# --- the stacks come from config --------------------------------------------

c="$(new_checkout cfgstacks)"; b="$(base_of "$c")"
mkdir -p "$c/repo/infra"; printf 'locals {\n  a = 1\n}\n' >"$c/repo/infra/main.tf"
printf '{"stacks":{"plan":["infra"],"validate_only":[]}}\n' >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A; git -C "$c/repo" commit -qm "configure falconet"
with_change "$c"
v "$c" "$b"

it "a consumer names its own stacks"
assert_contains "$(calls "$c")" "-chdir=$(phys "$c")/infra init -input=false" "tofu calls"

it "and the defaults stop being consulted"
assert_not_contains "$(calls "$c")" "-chdir=$(phys "$c")/dns" "tofu calls"

# A stack named in config that is not in the repository is a configuration
# error, and this report goes to the requester — who asked for a DNS record
# and cannot act on tofu's failure to change directory.
c="$(new_checkout nostack)"; b="$(base_of "$c")"
printf '{"stacks":{"plan":["nowhere"],"validate_only":["site"]}}\n' \
  >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A; git -C "$c/repo" commit -qm "configure falconet"
with_change "$c"
v "$c" "$b"

it "a configured stack that is not a directory fails the run"
assert_eq 1 "$RC" "exit code"

it "and the report names the config key rather than tofu's error"
assert_contains "$(report "$c")" 'config .stacks.plan names "nowhere"' "report"

it "and tofu is never asked to enter it"
assert_not_contains "$(calls "$c")" "nowhere" "tofu calls"

it "and the stacks that do exist are checked anyway"
assert_contains "$(calls "$c")" "/site validate" "tofu calls"

c="$(new_checkout cfgplan)"; b="$(base_of "$c")"
printf '{"plan":{"command":"tofu -chdir={stack} plan -no-color -compact-warnings"}}\n' \
  >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A; git -C "$c/repo" commit -qm "configure falconet"
with_change "$c"
v "$c" "$b"

it "and the plan command itself is configurable"
assert_contains "$(calls "$c")" "plan -no-color -compact-warnings" "tofu calls"

# --- an unreadable handoff name cannot make the guard fail open -------------
#
# The smuggling check interpolated $HANDOFF_DIR into an ERE. Harmless while
# the name was a constant; not once it comes from config, because a value
# carrying a regex metacharacter produced a broken pattern, grep exited 2, and
# the `if` read that as "no match" — a guard that fails OPEN on exactly the
# commit it exists to refuse.

c="$(new_checkout regexname)"; b="$(base_of "$c")"
printf '{"handoff_dir":"ci(handoff)"}\n' >"$c/repo/.github/falconet.json"
printf '.falconet/\nci(handoff)/\n' >"$c/repo/.gitignore"
git -C "$c/repo" add -A; git -C "$c/repo" commit -qm "configure falconet"
with_change "$c"
mkdir -p "$c/repo/ci(handoff)"
printf 'secret\n' >"$c/repo/ci(handoff)/plan.txt"
git -C "$c/repo" add -f "ci(handoff)/plan.txt"
git -C "$c/repo" commit -qm "smuggle"
v "$c" "$b"

it "a handoff directory whose name is regex-shaped is still refused"
assert_eq 1 "$RC" "exit code"

it "and named in the report"
assert_contains "$(cat "$c/repo/ci(handoff)/validation-failure.txt" 2>/dev/null)" \
  "ci(handoff)/plan.txt" "report"

# --- usage ------------------------------------------------------------------

it "a missing --base is a usage error"
( cd "$REPO_ROOT" && ./libexec/falconet/validate.sh >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "an unknown argument is a usage error"
( cd "$REPO_ROOT" && ./libexec/falconet/validate.sh --bogus >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "-h/--help is 2, because for this verb 0 would mean validation passed"
( cd "$REPO_ROOT" && ./libexec/falconet/validate.sh --help >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

summary
