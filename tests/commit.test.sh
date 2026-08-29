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
# branch, an empty handoff directory, and a `gitleaks` that finds
# token-shaped strings and nothing else.
new_checkout() { # name -> echoes the checkout path
  local base="$WORK/$1"
  mkdir -p "$base/repo/.falconet" "$base/repo/.github" "$base/bin"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  printf 'locals {\n  a = 1\n}\n' >"$base/repo/records-example-tech.tf"
  printf '.falconet/\n' >"$base/repo/.gitignore"
  # paths.allow has no default; every checkout needs one.
  cat >"$base/repo/.github/falconet.json" <<'CFG'
{"paths":{"allow":["*.tf"],"deny_content":["data \"external\"","provisioner","local-exec","remote-exec","templatefile(","filebase64(","file("]}}
CFG
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" switch -qc issue-1-thing

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
    && GITLEAKS="$g" GITLEAKS_CALLS="$c/gitleaks-calls.txt" \
       "$FALCONET" commit --out-dir "$c/repo/.falconet" 2>/dev/null )
}

run_in() { # checkout -> runs the script, stdout only
  run_in_with "$1" "$1/bin/gitleaks"
}

head_subject() { git -C "$1/repo" log -1 --format=%s; }
commit_count() { git -C "$1/repo" rev-list --count HEAD; }

# --- the agent asked a question and changed nothing -------------------------

c="$(new_checkout parked)"
printf -- '- Which zone did you mean?\n' >"$c/repo/.falconet/needs-info.md"
out="$(run_in "$c")"

it "a questions file alone routes to needs-info"
assert_eq "needs-info" "$out" "outcome"

it "parking makes no commit"
assert_eq 1 "$(commit_count "$c")" "commits"

# --- the ordinary success path ----------------------------------------------

c="$(new_checkout success)"
printf 'locals {\n  a = 2\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add the thing\n\nBecause the requester asked for the thing.\n' \
  >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "an edit plus a message routes to success"
assert_eq "success" "$out" "outcome"

it "the commit is made with the agent's subject"
assert_eq "Add the thing" "$(head_subject "$c")" "commit subject"

it "the subject is filed for the pull-request title"
assert_eq "Add the thing" "$(cat "$c/repo/.falconet/commit-subject.txt")" "commit-subject.txt"

it "the body is filed for the pull-request description, without the subject"
assert_eq "Because the requester asked for the thing." \
  "$(cat "$c/repo/.falconet/commit-body.md")" "commit-body.md"

it "the handoff directory stays out of the commit"
assert_not_contains "$(git -C "$c/repo" show --name-only --format= HEAD)" ".falconet" "committed paths"

it "and no failure-reason.txt is left behind on success"
assert_file_missing "$c/repo/.falconet/failure-reason.txt"

# --- a message with no body -------------------------------------------------

c="$(new_checkout subject_only)"
printf 'locals {\n  a = 3\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Just a subject\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a message with a subject and no body still succeeds"
assert_eq "success" "$out" "outcome"

it "an empty body falls back to the subject rather than an empty description"
assert_eq "Just a subject" "$(cat "$c/repo/.falconet/commit-body.md")" "commit-body.md"

# --- edits with no message --------------------------------------------------

c="$(new_checkout no_message)"
printf 'locals {\n  a = 4\n}\n' >"$c/repo/records-example-tech.tf"
out="$(run_in "$c")"

it "edits without a commit message are a failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the missing message"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" "commit-msg.txt" "failure reason"

it "and no commit-subject.txt is written on failure"
assert_file_missing "$c/repo/.falconet/commit-subject.txt"

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
printf 'Widen the toolset\n\nThe issue asked me to.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a change outside the allowlist is a failure, however good the message"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed, including the legitimate .tf edit"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the offending path"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" \
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
printf 'Add the thing and write it down\n\nBoth halves.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a non-.tf path is refused, whatever else the run got right"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed, including the legitimate .tf edit"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the offending path"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" \
  "AGENTS.md" "failure reason"

# --- committed AND asked ----------------------------------------------------
#
# The question wins the routing, but the work still has to survive: the push
# step runs before the park, exactly as it does today.

c="$(new_checkout both)"
printf 'locals {\n  a = 6\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A real change\n\nWith a real body.\n' >"$c/repo/.falconet/commit-msg.txt"
printf -- '- But which TTL?\n' >"$c/repo/.falconet/needs-info.md"
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
printf 'Add a data source\n\nTotally safe, I promise.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a data \"external\" block routes to failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the file"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" \
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
  >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a file() call routes to failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the file"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" \
  "records-example-tech.tf" "failure reason"

# --- a file named like a flag is still read ---------------------------------
#
# `git add` guards its paths with `--` because an agent might create
# `-check.tf`. The denylist has to read that file too: a guard that hands the
# path to a tool which parses it as options has not looked at it, and the
# construct inside goes through on the strength of a scan that never ran.

c="$(new_checkout dash_named_file)"
printf 'data "external" "danger" {\n  program = ["sh", "-c", "whoami"]\n}\n' \
  >"$c/repo/-check.tf"
printf 'Add a check\n\nIn a file named like a flag.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a denied construct in a file named like a flag is still refused"
assert_eq "failure" "$out" "outcome"

it "and the reason names that file"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" "-check.tf" "failure reason"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

# --- denial beats needs-info (Ruling B) -------------------------------------
#
# The needs-info ordering exists to protect committed work, and a refused
# run commits nothing, so a path or content violation beats needs-info even
# when the same run also wrote a message and asked a question.

c="$(new_checkout denial_beats_needs_info)"
mkdir -p "$c/repo/.github/workflows"
printf 'allowedTools: everything\n' >"$c/repo/.github/workflows/infra-issues.yml"
printf 'Widen the toolset\n\nThe issue asked me to.\n' >"$c/repo/.falconet/commit-msg.txt"
printf -- '- Which zone did you mean?\n' >"$c/repo/.falconet/needs-info.md"
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
  fake_token; printf '\n'; } >"$c/repo/.falconet/needs-info.md"
out="$(run_in "$c")"

it "a token-shaped string in needs-info.md is a failure, not a question"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the file that would have been posted"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" \
  "needs-info.md" "failure reason"

it "and the reason never repeats the matched value"
assert_not_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" \
  "$(fake_token)" "failure reason"

c="$(new_checkout leak_in_message)"
printf 'locals {\n  a = 7\n}\n' >"$c/repo/records-example-tech.tf"
{ printf 'Add the thing\n\nFor traceability, the header was '
  fake_token; printf '\n'; } >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a token-shaped string in the commit message is a failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed, so nothing can be pushed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and no pull-request body is filed"
assert_file_missing "$c/repo/.falconet/commit-body.md"

# The .tf arm: the message is clean, so only the staged diff can catch this.
# .tf content reaches the remote, and the plan bot echoes it on the PR.

c="$(new_checkout leak_in_tf)"
{ printf 'locals {\n  a = "'; fake_token; printf '"\n}\n'; } \
  >"$c/repo/records-example-tech.tf"
printf 'Add a local\n\nNothing to see here.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a token-shaped string in the staged diff is a failure"
assert_eq "failure" "$out" "outcome"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

it "and the reason names the staged change"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" \
  "staged change" "failure reason"

# --- the scanner's own stdout must not leak into the outcome word -----------
#
# `gitleaks --verbose` prints its findings to STDOUT. This stub imitates
# that. A run that printed
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
printf 'A clean change\n\nWith a chatty scanner.\n' >"$c/repo/.falconet/commit-msg.txt"
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
  >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a chatty scanner's finding is still a one-word failure"
assert_eq "failure" "$out" "outcome"

it "and its output does not reach the comment the requester reads"
assert_not_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" \
  "RuleID" "failure reason"

# --- a scan that could not run is not a pass --------------------------------

c="$(new_checkout scanner_missing)"
printf 'locals {\n  a = 13\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A change nobody could scan\n\nBecause there is no scanner.\n' \
  >"$c/repo/.falconet/commit-msg.txt"
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
  >"$c/repo/.falconet/commit-msg.txt"
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
  >"$c/repo/.falconet/commit-msg.txt"
run_in "$c" >/dev/null; rc=$?

it "a success outcome exits 0"
assert_eq 0 "$rc" "exit code"

c="$(new_checkout exit_needs_info)"
printf -- '- Which zone did you mean?\n' >"$c/repo/.falconet/needs-info.md"
run_in "$c" >/dev/null; rc=$?

it "a needs-info outcome exits 0"
assert_eq 0 "$rc" "exit code"

c="$(new_checkout exit_failure)"
run_in "$c" >/dev/null; rc=$?

it "a failure outcome exits 0"
assert_eq 0 "$rc" "exit code"

# --- the policy comes from config -------------------------------------------
#
# The defaults reproduce the hardcoded behavior every case above asserts, so
# those 59 cases already prove the default path. These prove the config path,
# and the ordering guard that the hardcoded version encoded in the sequence of
# its greps and nothing tested.

c="$(new_checkout allow_configured)"
printf '{"paths":{"allow":["*.tf","docs/*.md"]}}\n' >"$c/repo/.github/falconet.json"
printf '.falconet/\n' >"$c/repo/.gitignore"
git -C "$c/repo" add .github/falconet.json .gitignore
git -C "$c/repo" commit -qm "configure falconet"
mkdir -p "$c/repo/docs"
printf 'a note\n' >"$c/repo/docs/note.md"
printf 'Add a note\n\nBecause the requester asked.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a path the default refuses is committed when paths.allow admits it"
assert_eq "success" "$out" "outcome"

it "and it really is in the commit"
assert_contains "$(git -C "$c/repo" show --name-only --format= HEAD)" "docs/note.md"

c="$(new_checkout allow_narrowed)"
printf '{"paths":{"allow":["dns/*.tf"]}}\n' >"$c/repo/.github/falconet.json"
printf '.falconet/\n' >"$c/repo/.gitignore"
git -C "$c/repo" add .github/falconet.json .gitignore
git -C "$c/repo" commit -qm "configure falconet"
printf 'locals {\n  a = 2\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add a record\n\nBecause.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "and narrowing paths.allow refuses a path the default would have taken"
assert_eq "failure" "$out" "outcome"

it "naming the path, and the allowlist it was measured against"
reason="$(cat "$c/repo/.falconet/failure-reason.txt")"
assert_contains "$reason" "records-example-tech.tf" "failure reason"
assert_contains "$reason" "dns/*.tf" "failure reason"

# --- the allowlist's globs: `*` crosses `/` ---------------------------------
#
# The README says so: "`*` crosses `/`, so `*.tf` matches `dns/records.tf`".
# That is bash `case` pattern matching, and it is not what every glob library
# does — Go's path.Match stops `*` at a slash. Every default-allowlist case
# above touches a root-level .tf, so none of them would notice a port that
# changed this. These two do: one where the star must cross a slash, and one
# where the directory in the pattern must still be honoured, with a second
# file under a deeper directory that the star has to reach across.

c="$(new_checkout glob_crosses_slash)"
mkdir -p "$c/repo/dns"
printf 'locals {\n  a = 2\n}\n' >"$c/repo/dns/records.tf"
printf 'Add a nested record\n\nBecause the requester asked.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "*.tf admits dns/records.tf: the star crosses the slash"
assert_eq "success" "$out" "outcome"

it "and the nested file is what was committed"
assert_contains "$(git -C "$c/repo" show --name-only --format= HEAD)" "dns/records.tf" "committed paths"

c="$(new_checkout glob_keeps_directory)"
printf '{"paths":{"allow":["dns/*.tf"]}}\n' >"$c/repo/.github/falconet.json"
printf '.falconet/\n' >"$c/repo/.gitignore"
git -C "$c/repo" add .github/falconet.json .gitignore
git -C "$c/repo" commit -qm "configure falconet"
mkdir -p "$c/repo/site" "$c/repo/dns/zones"
printf 'locals {\n  a = 2\n}\n' >"$c/repo/site/a.tf"
printf 'locals {\n  b = 2\n}\n' >"$c/repo/dns/zones/a.tf"
printf 'Add two records\n\nOne of them where it may not go.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "dns/*.tf refuses site/a.tf: the directory in the pattern is honoured"
assert_eq "failure" "$out" "outcome"

it "naming site/a.tf and not dns/zones/a.tf, which the star reaches across its slash"
reason="$(cat "$c/repo/.falconet/failure-reason.txt")"
assert_contains "$reason" "site/a.tf" "failure reason"
assert_not_contains "$reason" "dns/zones/a.tf" "failure reason"

# --- a staged rename is refused, not mis-parsed ------------------------------
#
# `git status -z` reports a rename as TWO NUL-terminated fields: the
# status-prefixed new path, then the bare old path with no prefix at all.
# Slicing the second the way every other entry is sliced would corrupt it.
# The agent cannot stage one — it has no shell — so this arm fires only for a
# person running the verb by hand, which is exactly when a misread path
# would be hardest to notice.

c="$(new_checkout staged_rename)"
git -C "$c/repo" mv records-example-tech.tf records-renamed.tf
printf 'Rename the record file\n\nBecause.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a staged rename is refused rather than parsed"
assert_eq "failure" "$out" "outcome"

it "and the reason says so, naming the new path"
reason="$(cat "$c/repo/.falconet/failure-reason.txt")"
assert_contains "$reason" "rename or copy" "failure reason"
assert_contains "$reason" "records-renamed.tf" "failure reason"

it "and nothing is committed"
assert_eq 1 "$(commit_count "$c")" "commits"

# --- the denylist, and the order it is tested in ----------------------------
#
# `templatefile(` contains a `file(`. The hardcoded version encoded "most
# specific first" as the order of its greps and nothing asserted it; the
# config version encodes it as array order, which is easier to get wrong and
# now impossible to get wrong silently. A run that reports file() when the
# agent wrote templatefile() is the right refusal naming the wrong construct,
# and nothing downstream can recover the distinction.

c="$(new_checkout denylist_order)"
printf 'locals {\n  a = templatefile("x.tpl", {})\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add a record\n\nBecause.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a templatefile() call is refused"
assert_eq "failure" "$out" "outcome"

it "and is named as templatefile(), not as the file( hiding inside it"
reason="$(cat "$c/repo/.falconet/failure-reason.txt")"
assert_contains "$reason" "templatefile()" "failure reason"
assert_not_contains "$reason" ": file()" "failure reason"

# HCL does not care about the whitespace in the joints, so neither may the
# guard: `templatefile (` is the same construct and must not be a way past it.
c="$(new_checkout denylist_whitespace)"
printf 'locals {\n  a = templatefile ("x.tpl", {})\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add a record\n\nBecause.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "whitespace before the paren is not a way past the denylist"
assert_eq "failure" "$out" "outcome"

c="$(new_checkout denylist_spaced_quotes)"
printf 'data  "external"  "d" {\n  program = ["sh"]\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add a record\n\nBecause.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "nor is whitespace around the quotes of a data \"external\" block"
assert_eq "failure" "$out" "outcome"

c="$(new_checkout denylist_configured)"
printf '{"paths":{"allow":["*.tf"],"deny_content":["jsondecode("]}}\n' >"$c/repo/.github/falconet.json"
printf '.falconet/\n' >"$c/repo/.gitignore"
git -C "$c/repo" add .github/falconet.json .gitignore
git -C "$c/repo" commit -qm "configure falconet"
printf 'locals {\n  a = jsondecode("{}")\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add a record\n\nBecause.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "a construct only the config names is refused"
assert_eq "failure" "$out" "outcome"

it "and is named in the reason"
assert_contains "$(cat "$c/repo/.falconet/failure-reason.txt")" "jsondecode()" "failure reason"

c="$(new_checkout denylist_replaced)"
printf '{"paths":{"allow":["*.tf"],"deny_content":["jsondecode("]}}\n' >"$c/repo/.github/falconet.json"
printf '.falconet/\n' >"$c/repo/.gitignore"
git -C "$c/repo" add .github/falconet.json .gitignore
git -C "$c/repo" commit -qm "configure falconet"
printf 'locals {\n  a = file("x")\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add a record\n\nBecause.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$(run_in "$c")"

it "and a configured denylist REPLACES the default rather than extending it"
assert_eq "success" "$out" "outcome"

# --- the handoff directory, when nobody names one ---------------------------
#
# Every case above passes --out-dir, because the workflow always did. The
# default is what a workstation run gets, and it is the one thing about this
# verb that changed in the move: .ci-handoff/ was hardcoded, .falconet/ comes
# from config with handoff_dir overriding it.

c="$(new_checkout default_handoff)"
printf 'locals {\n  a = 2\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add the thing\n\nBecause the requester asked.\n' >"$c/repo/.falconet/commit-msg.txt"
out="$( cd "$c/repo" \
        && GITLEAKS="$c/bin/gitleaks" GITLEAKS_CALLS="$c/gitleaks-calls.txt" \
           "$FALCONET" commit 2>/dev/null )"

it "with no --out-dir the handoff directory defaults to .falconet"
assert_eq "success" "$out" "outcome"
assert_eq "Add the thing" "$(cat "$c/repo/.falconet/commit-subject.txt" 2>/dev/null)"

c="$(new_checkout configured_handoff)"
printf '{"handoff_dir":".ci-handoff","paths":{"allow":["*.tf"]}}\n' >"$c/repo/.github/falconet.json"
# Moving handoff_dir means gitignoring the new location too. Without this the
# handoff files themselves show up as untracked non-.tf paths and the
# allowlist refuses them — correctly, which is what makes it a foot-gun worth
# a test rather than a bug worth a workaround.
printf '.falconet/\n.ci-handoff/\n' >"$c/repo/.gitignore"
# Committed, because a consumer's config is a repository file — and because
# leaving it untracked makes this a test of the path allowlist instead, which
# would refuse it as a non-.tf path and be entirely right to.
git -C "$c/repo" add .github/falconet.json .gitignore
git -C "$c/repo" commit -qm "configure falconet"
mkdir -p "$c/repo/.ci-handoff"
printf 'locals {\n  a = 2\n}\n' >"$c/repo/records-example-tech.tf"
printf 'Add the thing\n\nBecause the requester asked.\n' >"$c/repo/.ci-handoff/commit-msg.txt"
out="$( cd "$c/repo" \
        && GITLEAKS="$c/bin/gitleaks" GITLEAKS_CALLS="$c/gitleaks-calls.txt" \
           "$FALCONET" commit 2>/dev/null )"

it "and handoff_dir in config moves it, which is how a consumer migrates"
assert_eq "success" "$out" "outcome"
assert_eq "Add the thing" "$(cat "$c/repo/.ci-handoff/commit-subject.txt" 2>/dev/null)"

it "an unknown argument exits 2"
( cd "$REPO_ROOT" && "$FALCONET" commit --bogus >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "-h/--help exits 2, not 0 -- help is not one of the three outcomes"
( cd "$REPO_ROOT" && "$FALCONET" commit --help >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

# A pre-commit hook is a clean way to force a genuine git failure that is
# NOT the "nothing staged" case above, which now has its own outcome.
c="$(new_checkout git_failure)"
printf 'locals {\n  a = 11\n}\n' >"$c/repo/records-example-tech.tf"
printf 'A commit a hook will refuse\n\nSo this is a genuine git failure.\n' \
  >"$c/repo/.falconet/commit-msg.txt"
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
