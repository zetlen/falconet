#!/usr/bin/env bash
#
# ci-commit-change.sh — read the implementing agent's outcome off the disk and,
# where there is one, make the commit that agent can no longer make itself.
#
# The implementing stage used to hold `Bash(git add:*)` and `Bash(git
# commit:*)`, and its prompt carried a paragraph of permission-matcher tax
# ("a single simple command ... no heredoc, no $(...), no &&"). It now holds
# no Bash at all: it edits files, writes its commit message to
# .ci-handoff/commit-msg.txt, and stops. This script does the rest.
#
# That is worth more than two fewer tool grants. "Did the agent commit?" used
# to be a claim to check — claude-code-action reports `conclusion: success`
# for a run that did nothing. It is now a question about the tree, and the
# tree does not have opinions.
#
# Modes:
#   ci-commit-change.sh [--out-dir DIR]
#
# Prints exactly one word on stdout — the outcome — and nothing else:
#
#   needs-info  DIR/needs-info.md is non-empty. The requester gets asked.
#   success     the tree is dirty AND DIR/commit-msg.txt is non-empty. The
#               touched .tf files have been formatted, everything is
#               committed, and the subject and body are filed for the
#               pull-request stage.
#   failure     anything else. DIR/failure-reason.txt says what, in prose a
#               requester can read.
#
# needs-info wins over an ordinary success: an agent that both committed work
# and asked a question keeps its commit, because the push step runs before
# the park, which is the ordering run 32093607680 taught this pipeline. A
# path or content violation is decided BEFORE needs-info is even consulted,
# though, and beats it regardless of whether questions were also written: a
# refused run commits nothing, so there is no committed work to protect, and
# a run that both tried to escalate and asked a question should fail loudly
# rather than park quietly.
#
# Outputs, written into DIR (default: .ci-handoff/ at the root of this
# repository):
#   commit-subject.txt   the message's first line — the pull-request TITLE
#   commit-body.md       the rest of the message — the pull-request BODY
#   failure-reason.txt   written only on failure
#
# ---------------------------------------------------------------------------
# The path allowlist
# ---------------------------------------------------------------------------
# Only `*.tf` may be committed. Anything else is a failure that names the
# path. It was `*.tf` and `scripts/record-manifest.txt` until #17 deleted the
# manifest: a DNS record now lives in exactly one file, which is a `.tf` file,
# so the second entry stopped naming anything a request could need.
#
# The issue title, body and comment thread are attacker-controlled text, and
# they are also the agent's instructions. An issue that asks it to "also
# update .github/workflows/infra-issues.yml to grant Bash" is a privilege
# escalation, and until now the only thing standing against it was a cheap
# model answering "are unrelated files touched?". That question is now a case
# statement. A request that genuinely needs a script change fails to a human,
# which is the right answer for a request that wants to edit the machinery
# that reviews it.
#
# COMMITTED files only, and that is the whole of it. This is a gate on the
# commit, not a sandbox. The agent holds unrestricted Read over the workspace,
# and what it writes into .ci-handoff/commit-msg.txt and
# .ci-handoff/needs-info.md is not a committed file at all — the first is
# published as the pull-request body, the second as a comment on the
# requester's issue. The allowlist decides what lands in the repository; it
# does not decide what the agent can see or what it can say. The secret scan
# below reads those two files, but it is a pattern matcher, not a boundary.
#
# ---------------------------------------------------------------------------
# The publish-boundary secret scan
# ---------------------------------------------------------------------------
# lib/scan.sh — gitleaks over commit-msg.txt, needs-info.md and
# the staged diff, before anything is committed. Issue #41: the agent can read
# the job's push token out of .git/config, and until this scan existed the two
# handoff files above carried whatever it wrote straight to the GitHub API,
# which does not apply the masking that hides $GITHUB_TOKEN in run logs.
#
# A hit is a `failure`, not a redaction: the run stops, nothing is committed,
# and the requester gets failure-reason.txt, which says a secret-like string
# was found and NEVER repeats it. Read that script's header for what this does
# not do — it matches known patterns, so it is evidence of a leak and never
# evidence of the absence of one, and the token is still readable by the agent
# either way.
#
# ---------------------------------------------------------------------------
# The content denylist
# ---------------------------------------------------------------------------
# The path guard above says WHERE an agent may write; it says nothing about
# WHAT. A `.tf` file is executable content in this pipeline: a
# `data "external"` block runs an arbitrary command during the `tofu plan`
# that happens two steps later, and a `provisioner` block — most concretely
# its `local-exec` (runs on the runner) and `remote-exec` (runs over the
# network) types — runs one during `tofu apply`. Both of those steps run on a
# runner holding the state backend's credential and a checkout whose git
# remote still carries a push token. So an issue that
# asks for one of these four constructs is the same privilege escalation as
# the workflow-file edit above, just aimed at a path the allowlist waves
# through. Refused the same way: failure, naming the file and the construct.
#
#
# The list covers READING as well as executing, which is why `file(`,
# `templatefile(` and `filebase64(` are on it. Nothing has to run for those to
# leak. A `.tf` containing
#
#     output "leak" { value = file("/etc/hosts") }
#
# makes `tofu plan` print that file's entire contents under
# `Changes to Outputs:` — no provider, no `tofu init`, and none of the four
# constructs above — and plan.txt is what ci-pr-body.sh attaches to the pull
# request. The best target is inside the workspace the agent is standing in:
# `file("${path.module}/.git/config")` is readable at Validate time because
# actions/checkout left the job's token there and ci-push-branch.sh rewrote
# the remote to `https://x-access-token:$GH_TOKEN@...` two steps earlier. This
# configuration uses none of the three today, so the entries cost nothing; a
# change that genuinely needs one fails to a human, which is the right answer
# for a change that wants to read a file off the runner.
#
# Exit codes: 0 = an outcome was determined and printed
#             1 = git, tofu or the secret scan refused; nothing is printed,
#                 stderr says why
#             2 = usage error (including --help: it isn't one of the three
#                 outcomes, so it does not get an outcome's exit code)
#
# $TOFU overrides the formatter and $GITLEAKS the secret scanner, for the
# tests.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Two levels: libexec/falconet/ sits where scripts/ used to sit one deep.
# Getting this wrong is silent — a wrong-but-existing directory is found and
# everything downstream misbehaves somewhere else entirely.
REPO_ROOT="$(dirname "$(dirname "$SCRIPT_DIR")")"
TOFU="${TOFU:-tofu}"
# lib/, not beside this script: the scan is internal but it is not this
# stage's private helper — see the header of lib/scan.sh.
SECRET_SCAN="$REPO_ROOT/lib/scan.sh"

. "$REPO_ROOT/lib/config.sh"
. "$REPO_ROOT/lib/handoff.sh"

OUT_DIR=""
CONFIG=""

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir) OUT_DIR="${2:?--out-dir needs a directory}"; shift 2 ;;
    --config)  CONFIG="${2:?--config needs a file}"; shift 2 ;;
    -h|--help) usage; exit 2 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# Resolve --out-dir against the caller's CWD before changing directories: git
# status below reports paths relative to REPO_ROOT, and every path built from
# those reports (the [[ -f ]] checks, tofu fmt, git add) only resolves
# correctly if this process is standing in REPO_ROOT when it uses them.
case "$OUT_DIR" in
  ""|/*) ;;
  *) OUT_DIR="$PWD/$OUT_DIR" ;;
esac

cd "$REPO_ROOT" || exit 1

# Config is read from the repository root, so this follows the cd. An explicit
# --out-dir still wins over handoff_dir; that is what handoff_init is given.
config_init "$CONFIG"
handoff_init "$OUT_DIR"
OUT_DIR="$HANDOFF"

# --- the policy, out of config and into arrays ------------------------------
#
# Read once, here, rather than at each use: a guard that re-reads its own rule
# mid-run is a guard whose behavior depends on when you look.
#
# The empty-array guards below are for macOS bash 3.2, where expanding an
# empty array under `set -u` is an unbound-variable error rather than nothing.

ALLOW=()
while IFS= read -r _entry; do
  [ -n "$_entry" ] && ALLOW+=("$_entry")
done < <(config_get_array '.paths.allow')

DENY=()
while IFS= read -r _entry; do
  [ -n "$_entry" ] && DENY+=("$_entry")
done < <(config_get_array '.paths.deny_content')

# A denylist entry is written the way a person writes the construct —
# `templatefile(`, `data "external"` — and matched the way HCL actually spells
# it, which is with whitespace in the joints. `templatefile (` and
# `data  "external"` are the same construct and must not be a way past the
# guard. So the literal becomes a regex: metacharacters escaped, then
# whitespace tolerated before an opening paren, around a quote, and wherever
# the literal has a space.
#
# This reproduces the hand-written regexes it replaces, character for
# character. That is the point: the config's default IS the old behavior.
deny_pattern() { # literal -> ERE
  local p
  p="$(printf '%s' "$1" | sed -e 's/[][\\.^$*+?{}|()]/\\&/g')"
  p="${p//\\(/[[:space:]]*\\(}"
  p="${p//\"/[[:space:]]*\"[[:space:]]*}"
  p="${p// /[[:space:]]*}"
  printf '%s' "$p"
}

# What the requester is told was found. `templatefile(` is how you write the
# rule; `templatefile()` is how you name the thing.
deny_label() { # literal -> display form
  case "$1" in
    *\() printf '%s)' "$1" ;;
    *)   printf '%s' "$1" ;;
  esac
}

DENY_PAT=()
for _entry in ${DENY[@]+"${DENY[@]}"}; do
  DENY_PAT+=("$(deny_pattern "$_entry")")
done
QUESTIONS="$OUT_DIR/needs-info.md"
MESSAGE="$OUT_DIR/commit-msg.txt"
REASON="$OUT_DIR/failure-reason.txt"
rm -f "$REASON"

# A failure is an outcome, not an error: print the word, exit 0, let the
# workflow route it.
give_up() { # reason-line...
  printf '%s\n' "$@" >"$REASON"
  echo failure
  exit 0
}

parked() { [[ -s "$QUESTIONS" ]]; }

# --- the publish-boundary secret scan ---------------------------------------
#
# See the header. The scanner's stdout is a list of the channels that matched,
# and it is CAPTURED rather than allowed through: this script's only contract
# with the workflow is that its own stdout is exactly one word, and the
# lesson of `tofu fmt` below is that a subprocess with something to say will
# say it into that contract if you let it.
#
# A broken scanner is a mechanical failure (exit 1, no outcome word), not a
# pass. "gitleaks is not installed" and "gitleaks found nothing" must never
# produce the same run.
refuse_on_secret() { # scan-argument...
  local found rc
  found="$("$SECRET_SCAN" "$@")"
  rc=$?
  case "$rc" in
    0) return 0 ;;
    3)
      give_up "I stopped this run before it published anything: a string" \
              "shaped like a credential turned up in the text it was about" \
              "to post. Nothing was committed and nothing was posted." \
              "The matching text is deliberately not repeated here — that" \
              "would publish it in this very comment. Where it matched:" \
              "$(printf '%s\n' "$found" | sed 's/^/  /')" \
              "(commit-msg.txt would have become this change's pull-request" \
              "description; needs-info.md would have been posted here as a" \
              "question; the staged change is the diff itself.)" \
              "A person needs to read the run log, and if that was a real" \
              "credential, rotate it. The scanner matches known patterns, so" \
              "treat this as evidence of a leak — never treat a quiet run as" \
              "evidence that there is nothing to find." ;;
    *)
      echo "the secret scan could not be run (exit $rc); refusing to" >&2
      echo "continue, because a scan that did not happen is not a pass" >&2
      exit 1 ;;
  esac
}

# --- what did the agent leave behind? ---------------------------------------
#
# -z, so a path with a space in it survives; --untracked-files=all, so a new
# records-*.tf counts. A rename or copy is not staged before this script runs
# and this agent cannot stage one itself, so none should appear — checked,
# not assumed, though: git status -z reports a rename as TWO NUL-terminated
# fields, a status-prefixed new path and then a bare old path with no prefix
# at all, and slicing that bare field with the same `${entry:3}` used for
# everything else corrupts it. Detected by its leading R or C and refused,
# rather than silently mis-parsed.
#
# Captured to a file, not a process substitution: `< <(...)` throws away the
# command's own exit status, and running outside a git repository must be a
# mechanical failure, not a false "the tree is untouched".
STATUS_FILE="$(mktemp)" || { echo "mktemp failed" >&2; exit 1; }
trap 'rm -f "$STATUS_FILE"' EXIT
if ! git status --porcelain --untracked-files=all -z >"$STATUS_FILE"; then
  echo "git status failed" >&2
  exit 1
fi

changed=()
while IFS= read -r -d '' entry; do
  case "${entry:0:1}" in
    R|C)
      give_up "The agent's change was reported as a rename or copy (git" \
              "status code '${entry:0:2}' for ${entry:3}), which this script" \
              "refuses to parse rather than risk misreading the paths" \
              "involved. Ask for the change as a plain add and delete" \
              "instead."
      ;;
    *)
      changed+=("${entry:3}")
      ;;
  esac
done <"$STATUS_FILE"

# --- the allowlist -----------------------------------------------------------
#
# Guarded: macOS ships bash 3.2, where expanding an EMPTY array under
# `set -u` is an "unbound variable" error rather than nothing, and `changed`
# is legitimately empty whenever the agent touched nothing.
# A path is allowed if ANY paths.allow glob matches it. The globs are matched
# unquoted in a `case`, which is what makes them globs rather than literals.
path_allowed() { # path
  local p="$1" pat
  for pat in ${ALLOW[@]+"${ALLOW[@]}"}; do
    case "$p" in $pat) return 0 ;; esac
  done
  return 1
}

denied=()
tf_changed=()
if [[ "${#changed[@]}" -gt 0 ]]; then
  for path in "${changed[@]}"; do
    if path_allowed "$path"; then
      # An allowed path that no longer exists on disk is neither scanned nor
      # refused: a deleted file cannot carry new executable content.
      [[ -f "$path" ]] && tf_changed+=("$path")
    else
      denied+=("$path")
    fi
  done
fi

# --- the content denylist ----------------------------------------------------
#
# See "The content denylist" above. Only files that still exist on disk are
# readable (tf_changed already filters that); a deleted .tf file cannot carry
# new executable content.
# First match wins, IN CONFIG ORDER, which is why the order is load-bearing
# and why lib/config.sh preserves it. `templatefile(` contains a `file(`, so a
# denylist that tested `file(` first would report a templatefile() call as
# file() — the right refusal naming the wrong construct, and nothing
# downstream can recover the distinction.
tf_denylist_hit() { # file -> echoes the matched construct; else exit 1
  local file="$1" i=0
  while [ "$i" -lt "${#DENY_PAT[@]}" ]; do
    if grep -Eq "${DENY_PAT[$i]}" "$file"; then
      deny_label "${DENY[$i]}"
      return 0
    fi
    i=$((i + 1))
  done
  return 1
}

tf_denied=()
if [[ "${#tf_changed[@]}" -gt 0 ]]; then
  for path in "${tf_changed[@]}"; do
    hit="$(tf_denylist_hit "$path")" && tf_denied+=("$path: $hit")
  done
fi

# Both refusals below run before either needs-info exit — Ruling B: a denied
# run commits nothing, so there is no committed work for needs-info's
# ordering to protect, and an issue that both tried to escalate and asked a
# question should fail loudly rather than park quietly.
if [[ "${#denied[@]}" -gt 0 ]]; then
  give_up "The agent changed files it is not allowed to change, so nothing" \
          "was committed. Only these paths may be changed in response to" \
          "an issue: ${ALLOW[*]}. Refused paths:" \
          "$(printf '  %s\n' "${denied[@]}")"
fi

if [[ "${#tf_denied[@]}" -gt 0 ]]; then
  give_up "The agent's .tf changes contain a construct that runs code, or" \
          "reads a file off the runner, during tofu plan or apply, so" \
          "nothing was committed. Constructs like data \"external\"," \
          "provisioner, local-exec and remote-exec run a command; file()," \
          "templatefile() and" \
          "filebase64() read a path and can print what they read into the" \
          "plan. All of them are refused wherever they appear, whatever the" \
          "commit message says. Refused:" \
          "$(printf '  %s\n' "${tf_denied[@]}")"
fi

# Above both needs-info exits, for the same reason the two refusals above are:
# a run that leaked something must not park quietly with the leak in the very
# comment that parks it. needs-info.md is scanned here precisely because it is
# the file ci-park-issue.sh posts unfenced.
refuse_on_secret -- "$MESSAGE" "$QUESTIONS"

if [[ "${#changed[@]}" -eq 0 ]]; then
  parked && { echo needs-info; exit 0; }
  give_up "The agent left the repository unchanged and asked no questions." \
          "Nothing was committed. There is no prepared change to look at."
fi

if [[ ! -s "$MESSAGE" ]]; then
  parked && { echo needs-info; exit 0; }
  give_up "The agent changed files but wrote no commit message, so there was" \
          "nothing to commit them under. It should have written its message" \
          "to .ci-handoff/commit-msg.txt. Changed paths:" \
          "$(printf '  %s\n' "${changed[@]}")"
fi

# --- format, then commit ----------------------------------------------------
#
# One target per invocation: `tofu fmt` takes a single file or directory, not
# a list. Never -recursive, and never a path the agent did not touch — main is
# fmt-clean today, and the point of the narrow scope is that a future
# regression somewhere else cannot ride into an unrelated change. The `--`
# guards a file whose name looks like a flag (an agent might create
# `-check.tf`); the `>&2` matters more: `tofu fmt` prints the reformatted
# file's own name to STDOUT on success, and this script's only contract with
# its caller is that stdout is exactly one of three words.
# Guarded: macOS ships bash 3.2, where expanding an EMPTY array under `set -u`
# is an "unbound variable" error rather than nothing. tf_changed is empty on a
# manifest-only change, which is an ordinary run, and tests/run.sh is run
# locally.
if [[ "${#tf_changed[@]}" -gt 0 ]]; then
  for path in "${tf_changed[@]}"; do
    "$TOFU" fmt -- "$path" >&2 || { echo "tofu fmt failed on $path" >&2; exit 1; }
  done
fi

# Stage exactly the vetted paths — not `git add -A` — so the security
# boundary enforced above is a pathspec you can read, not an invariant about
# what `-A` happens to pick up given everything checked so far. `>&2`: same
# reasoning as `tofu fmt` above, though `git add` is ordinarily silent.
git add -- "${changed[@]}" >&2 || { echo "git add failed" >&2; exit 1; }

# `tofu fmt` can turn an agent's edit into a no-op — whitespace it just
# undid, say — leaving nothing staged. `git commit` would fail on that, but
# with "nothing to commit, working tree clean" on STDOUT even under -q,
# which would otherwise leak into this script's own stdout contract. Caught
# here instead: an empty change is `failure`, not a mechanical error, so it
# gets failure's exit code (0) rather than a git failure's (1).
if git diff --cached --quiet; then
  give_up "The agent's change amounted to nothing once the touched .tf" \
          "files were formatted; the tree now matches what is already" \
          "committed, so there is nothing to commit. Changed paths:" \
          "$(printf '  %s\n' "${changed[@]}")"
fi

# The diff, now that there is one to read, and still before the commit: the
# branch is pushed immediately after this script returns, so a credential that
# reaches a commit reaches the remote. .tf content is also what `tofu plan`
# echoes into plan.txt and from there into the pull-request body, so this arm
# guards that channel too.
refuse_on_secret --staged

git commit -q -F "$MESSAGE" >&2 || { echo "git commit failed" >&2; exit 1; }

sed -n '1p' "$MESSAGE" >"$OUT_DIR/commit-subject.txt"
# Drop the subject, then drop the blank lines that separated it. An agent that
# wrote a subject and no body gets the subject as its description: a pull
# request with an empty body is worse than a repetitive one.
sed '1d' "$MESSAGE" | sed '/./,$!d' >"$OUT_DIR/commit-body.md"
[[ -s "$OUT_DIR/commit-body.md" ]] || cp "$OUT_DIR/commit-subject.txt" "$OUT_DIR/commit-body.md"

if parked; then
  echo needs-info
else
  echo success
fi
exit 0
