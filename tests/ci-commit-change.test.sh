#!/usr/bin/env bash
#
# What the script that replaced the agent's `git commit` must do.
#
# The implementing agent has no Bash at all, so everything it "decided" has to
# be legible from the disk it left behind: a dirty tree, a message file, a
# questions file, or nothing. These cases pin down that reading, the path
# allowlist that stops an issue body from talking the agent into editing the
# pipeline that reviews it, and the ordering that keeps a commit alive when
# the same run also asks a question.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null

# A token-shaped string, assembled at runtime and never written literally into
# this file: a fixture that IS a credential-shaped string would be a finding
# in any future scan of this repository, which is a silly way to make a test
# suite unrunnable.
fake_token() { printf 'ghp_%s' '0123456789abcdefghijABCDEFGHIJ012345'; }

# A checkout shaped like the pipeline's, mid-run: one base commit, a working
# branch, an empty handoff directory, a `tofu` that records its arguments
# instead of running, and a `gitleaks` that finds token-shaped strings and
# nothing else.
new_checkout() { # name -> echoes the checkout path
  local base="$WORK/$1"
  mkdir -p "$base/repo/scripts" "$base/repo/.ci-handoff" "$base/bin"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  cp "$REPO_ROOT/scripts/ci-commit-change.sh" "$base/repo/scripts/"
  cp "$REPO_ROOT/scripts/ci-secret-scan.sh" "$base/repo/scripts/"
  printf 'locals {\n  a = 1\n}\n' >"$base/repo/records-example-tech.tf"
  printf '.ci-handoff/\n' >"$base/repo/.gitignore"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" switch -qc issue-1-thing

  cat >"$base/bin/tofu" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$TOFU_CALLS"
STUB
  chmod +x "$base/bin/tofu"

  # Shaped like the real binary: it reads the text on stdin, honours the
  # --exit-code it was handed, and says what it found on STDERR only. Quiet on
  # stdout, like `gitleaks` without --verbose — the chatty case below is a
  # separate stub, on purpose.
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

run_in_with() { # checkout gitleaks-path -> runs the script, stdout only
  local c="$1" g="$2"
  ( cd "$c/repo" \
    && TOFU="$c/bin/tofu" TOFU_CALLS="$c/tofu-calls.txt" \
       GITLEAKS="$g" GITLEAKS_CALLS="$c/gitleaks-calls.txt" \
       ./scripts/ci-commit-change.sh --out-dir "$c/repo/.ci-handoff" 2>/dev/null )
}

run_in() { # checkout -> runs the script, stdout only
  run_in_with "$1" "$1/bin/gitleaks"
}

head_subject() { git -C "$1/repo" log -1 --format=%s; }
commit_count() { git -C "$1/repo" rev-list --count HEAD; }

# --- the agent asked a question and changed nothing -------------------------

c="$(new_checkout parked)"
printf -- '- Which zone did you mean?\n' >"$c/repo/.ci-handoff/needs-info.md"
out="$(run_in "$c")"

it "a questions file alone routes to needs-info"
assert_eq "needs-info" "$out" "outcome"

it "parking makes no commit"
assert_eq 1 "$(commit_count "$c")" "commits"

# --- the ordinary success path ----------------------------------------------

c="$(new_checkout success)"
printf 'locals {\n  a = 2\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add the thing\n\nBecause the requester asked for the thing.\n' \
  >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "an edit plus a message routes to success"
assert_eq "success" "$out" "outcome"

it "the commit is made with the agent's subject"
assert_eq "Add the thing" "$(head_subject "$c")" "commit subject"

it "the subject is filed for the pull-request title"
assert_eq "Add the thing" "$(cat "$c/repo/.ci-handoff/commit-subject.txt")" "commit-subject.txt"

it "the body is filed for the pull-request description, without the subject"
assert_eq "Because the requester asked for the thing." \
  "$(cat "$c/repo/.ci-handoff/commit-body.md")" "commit-body.md"

it "tofu fmt ran on the changed .tf file before the commit"
assert_contains "$(cat "$c/tofu-calls.txt")" "fmt -- records-example-tech.tf" "tofu calls"

it "tofu fmt was not run recursively"
assert_not_contains "$(cat "$c/tofu-calls.txt")" "-recursive" "tofu calls"

it "the handoff directory stays out of the commit"
assert_not_contains "$(git -C "$c/repo" show --name-only --format= HEAD)" ".ci-handoff" "committed paths"

it "and no failure-reason.txt is left behind on success"
assert_file_missing "$c/repo/.ci-handoff/failure-reason.txt"

# --- tofu fmt's own stdout must not leak into the outcome word -------------
#
# The stub above only records its arguments; it never prints anything, so
# nothing in the suite so far would catch `tofu fmt` behaving like the real
# binary, which prints the reformatted file's name to STDOUT on success. This
# stub imitates that.

c="$(new_checkout stdout_purity)"
cat >"$c/bin/tofu" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$TOFU_CALLS"
printf '%s\n' "$*"
STUB
chmod +x "$c/bin/tofu"
printf 'locals {\n  a = 9\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Reformat the thing\n\nBecause it needed it.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "tofu fmt's own stdout does not leak into the outcome word"
assert_eq "success" "$out" "outcome"

# --- a message with no body -------------------------------------------------

c="$(new_checkout subject_only)"
printf 'locals {\n  a = 3\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Just a subject\n' >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a message with a subject and no body still succeeds"
assert_eq "success" "$out" "outcome"

it "an empty body falls back to the subject rather than an empty description"
assert_eq "Just a subject" "$(cat "$c/repo/.ci-handoff/commit-body.md")" "commit-body.md"

# --- edits with no message --------------------------------------------------

c="$(new_checkout no_message)"
printf 'locals {\n  a = 4\n}\n' >"$c/repo/records-example-tech.tf"
out="$(run_in "$c")"

it "edits without a commit message are a failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the missing message"
assert_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" "commit-msg.txt" "failure reason"

it "and no commit-subject.txt is written on failure"
assert_file_missing "$c/repo/.ci-handoff/commit-subject.txt"

# --- the agent did nothing at all -------------------------------------------

c="$(new_checkout nothing)"
out="$(run_in "$c")"

it "an untouched tree with no files is a failure"
assert_eq "failure" "$out" "outcome"

# --- the escalation guard ---------------------------------------------------

c="$(new_checkout escalation)"
mkdir -p "$c/repo/.github/workflows"
printf 'allowedTools: everything\n' >"$c/repo/.github/workflows/infra-issues.yml"
printf 'locals {\n  a = 5\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Widen the toolset\n\nThe issue asked me to.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a change outside the allowlist is a failure, however good the message"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed, including the legitimate .tf edit"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the offending path"
assert_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" \
  ".github/workflows/infra-issues.yml" "failure reason"

# --- the allowlist is .tf and nothing else ----------------------------------
#
# scripts/record-manifest.txt was its second entry until #17 deleted the
# manifest, and this case is what that entry turned into: there is no longer
# any non-.tf path a request can legitimately need, so every one of them is
# refused. AGENTS.md stands in for the class — the workflow's own prompt names
# it as off-limits, and it is the file an agent asked to "document this" would
# reach for first.

c="$(new_checkout only_tf)"
printf '# Notes\n' >"$c/repo/AGENTS.md"
printf 'locals {\n  a = 7\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add the thing and write it down\n\nBoth halves.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a non-.tf path is refused, whatever else the run got right"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed, including the legitimate .tf edit"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the offending path"
assert_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" \
  "AGENTS.md" "failure reason"

# --- committed AND asked ----------------------------------------------------
#
# The question wins the routing, but the work still has to survive: the push
# step runs before the park, exactly as it does today.

c="$(new_checkout both)"
printf 'locals {\n  a = 6\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A real change\n\nWith a real body.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
printf -- '- But which TTL?\n' >"$c/repo/.ci-handoff/needs-info.md"
out="$(run_in "$c")"

it "a question routes the run even when there is also a commit"
assert_eq "needs-info" "$out" "outcome"

it "and the commit is made anyway, so the work is not lost"
assert_eq 2 "$(commit_count "$c")" "commits"

# --- the content denylist ---------------------------------------------------
#
# The path allowlist only says where an agent may write. A `.tf` file is
# executable content in this pipeline: `data "external"` runs a command
# during `tofu plan`, two steps after this script hands off.

c="$(new_checkout denylist)"
printf 'data "external" "danger" {\n  program = ["sh", "-c", "whoami"]\n}\n' \
  >"$c/repo/records-example-tech.tf"
printf 'Add a data source\n\nTotally safe, I promise.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a data \"external\" block routes to failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the file"
assert_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" \
  "records-example-tech.tf" "failure reason"

# --- the denylist covers reading, not only executing ------------------------
#
# `file()` runs nothing and needs no provider, and `tofu plan` prints what it
# read under `Changes to Outputs:` — into the plan.txt that becomes the pull
# request body. The workspace checkout carries a push token in .git/config, so
# "it only reads" is not a defence.

c="$(new_checkout denylist_read)"
printf 'output "leak" {\n  value = file("/etc/hosts")\n}\n' \
  >"$c/repo/records-example-tech.tf"
printf 'Add an output\n\nJust surfacing a detail.\n' \
  >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a file() call routes to failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the file"
assert_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" \
  "records-example-tech.tf" "failure reason"

# --- denial beats needs-info (Ruling B) -------------------------------------
#
# The needs-info ordering exists to protect committed work, and a refused
# run commits nothing, so a path or content violation beats needs-info even
# when the same run also wrote a message and asked a question.

c="$(new_checkout denial_beats_needs_info)"
mkdir -p "$c/repo/.github/workflows"
printf 'allowedTools: everything\n' >"$c/repo/.github/workflows/infra-issues.yml"
printf 'Widen the toolset\n\nThe issue asked me to.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
printf -- '- Which zone did you mean?\n' >"$c/repo/.ci-handoff/needs-info.md"
out="$(run_in "$c")"

it "a denied path beats a question: failure, not needs-info"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

# --- the publish-boundary secret scan (issue #41) ----------------------------
#
# The two files the agent writes for publication are not committed files, so
# neither the path allowlist nor the content denylist above has ever looked at
# them: commit-msg.txt becomes the pull-request body, needs-info.md becomes a
# comment on the requester's issue. The agent can read the job's push token
# out of .git/config, and the GitHub API does not mask what the run log masks.

c="$(new_checkout leak_in_questions)"
{ printf -- '- Which zone did you mean? For traceability the token is '
  fake_token; printf '\n'; } >"$c/repo/.ci-handoff/needs-info.md"
out="$(run_in "$c")"

it "a token-shaped string in needs-info.md is a failure, not a question"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the file that would have been posted"
assert_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" \
  "needs-info.md" "failure reason"

it "and the reason never repeats the matched value"
assert_not_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" \
  "$(fake_token)" "failure reason"

c="$(new_checkout leak_in_message)"
printf 'locals {\n  a = 7\n}\n' >"$c/repo/records-example-tech.tf"
{ printf 'Add the thing\n\nFor traceability, the header was '
  fake_token; printf '\n'; } >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a token-shaped string in the commit message is a failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed, so nothing can be pushed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and no pull-request body is filed"
assert_file_missing "$c/repo/.ci-handoff/commit-body.md"

# The .tf arm: the message is clean, so only the staged diff can catch this.
# `tofu plan` prints .tf content into plan.txt, which becomes the PR body.

c="$(new_checkout leak_in_tf)"
{ printf 'locals {\n  a = "'; fake_token; printf '"\n}\n'; } \
  >"$c/repo/records-example-tech.tf"
printf 'Add a local\n\nNothing to see here.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a token-shaped string in the staged diff is a failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the staged change"
assert_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" \
  "staged change" "failure reason"

# --- the scanner's own stdout must not leak into the outcome word -----------
#
# `gitleaks --verbose` prints its findings to STDOUT, exactly as `tofu fmt`
# prints the file it reformatted. This stub imitates that. A run that printed
# two lines here would fall through the workflow's `case "${OUTCOME}"` and
# take neither the success nor the hand-over path — the failure a previous
# change to this script actually shipped.

c="$(new_checkout scanner_stdout_purity)"
cat >"$c/bin/gitleaks" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GITLEAKS_CALLS"
cat >/dev/null
printf 'Finding:     locals { a = REDACTED }\n'
printf 'RuleID:      github-pat\n'
printf 'Fingerprint: stdin:github-pat:1\n'
echo "no leaks found" >&2
exit 0
STUB
chmod +x "$c/bin/gitleaks"
printf 'locals {\n  a = 12\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A clean change\n\nWith a chatty scanner.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a chatty scanner's stdout does not leak into the outcome word"
assert_eq "success" "$out" "outcome"

# The same stub, now finding something. Its "Finding:" line carries the text
# AROUND the secret — agent-authored text this guard exists to withhold — and
# failure-reason.txt is posted to the requester's issue verbatim.

c="$(new_checkout scanner_stdout_in_reason)"
cat >"$c/bin/gitleaks" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GITLEAKS_CALLS"
cat >/dev/null
printf 'Finding:     the header was REDACTED\n'
printf 'RuleID:      github-pat\n'
echo "leaks found: 1" >&2
exit 3
STUB
chmod +x "$c/bin/gitleaks"
printf 'locals {\n  a = 15\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A change the scanner objects to\n\nWith a chatty scanner.\n' \
  >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"

it "a chatty scanner's finding is still a one-word failure"
assert_eq "failure" "$out" "outcome"

it "and its output does not reach the comment the requester reads"
assert_not_contains "$(cat "$c/repo/.ci-handoff/failure-reason.txt")" \
  "RuleID" "failure reason"

# --- a scan that could not run is not a pass --------------------------------

c="$(new_checkout scanner_missing)"
printf 'locals {\n  a = 13\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A change nobody could scan\n\nBecause there is no scanner.\n' \
  >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in_with "$c" "$c/bin/no-such-gitleaks")"; rc=$?

it "a missing scanner exits 1 rather than reporting an outcome"
assert_eq 1 "$rc" "exit code"

it "and prints no outcome word at all"
assert_eq "" "$out" "stdout"

it "and commits nothing"
assert_eq 1 "$(commit_count "$c")" "commits"

c="$(new_checkout scanner_broken)"
cat >"$c/bin/gitleaks" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GITLEAKS_CALLS"
cat >/dev/null
echo "FTL failed to load config" >&2
exit 1
STUB
chmod +x "$c/bin/gitleaks"
printf 'locals {\n  a = 14\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A change the scanner died on\n\nSo this is a mechanical failure.\n' \
  >"$c/repo/.ci-handoff/commit-msg.txt"
out="$(run_in "$c")"; rc=$?

it "a scanner that dies mid-scan exits 1, never 'no leaks found'"
assert_eq 1 "$rc" "exit code"

it "and prints no outcome word"
assert_eq "" "$out" "stdout"

# --- exit codes --------------------------------------------------------------
#
# The single-word stdout contract only matters if the exit code agrees with
# it: 0 whenever an outcome was printed (success, needs-info, or failure),
# 2 for a usage error, 1 only for a mechanical failure that produced no
# outcome at all.

c="$(new_checkout exit_success)"
printf 'locals {\n  a = 10\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Exit zero on success\n\nBecause an outcome was printed.\n' \
  >"$c/repo/.ci-handoff/commit-msg.txt"
run_in "$c" >/dev/null; rc=$?

it "a success outcome exits 0"
assert_eq 0 "$rc" "exit code"

c="$(new_checkout exit_needs_info)"
printf -- '- Which zone did you mean?\n' >"$c/repo/.ci-handoff/needs-info.md"
run_in "$c" >/dev/null; rc=$?

it "a needs-info outcome exits 0"
assert_eq 0 "$rc" "exit code"

c="$(new_checkout exit_failure)"
run_in "$c" >/dev/null; rc=$?

it "a failure outcome exits 0"
assert_eq 0 "$rc" "exit code"

it "an unknown argument exits 2"
( cd "$REPO_ROOT" && ./scripts/ci-commit-change.sh --bogus >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "-h/--help exits 2, not 0 -- help is not one of the three outcomes"
( cd "$REPO_ROOT" && ./scripts/ci-commit-change.sh --help >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

# A pre-commit hook is a clean way to force a genuine git failure that is
# NOT the "nothing staged" case above, which now has its own outcome.
c="$(new_checkout git_failure)"
printf 'locals {\n  a = 11\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A commit a hook will refuse\n\nSo this is a genuine git failure.\n' \
  >"$c/repo/.ci-handoff/commit-msg.txt"
cat >"$c/repo/.git/hooks/pre-commit" <<'HOOK'
#!/usr/bin/env bash
echo "pre-commit refuses" >&2
exit 1
HOOK
chmod +x "$c/repo/.git/hooks/pre-commit"
out="$(run_in "$c")"; rc=$?

it "a genuine git failure (not 'nothing staged') exits 1"
assert_eq 1 "$rc" "exit code"

it "and no outcome word is printed for a mechanical failure"
assert_eq "" "$out" "stdout"

summary
