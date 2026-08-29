#!/usr/bin/env bash
#
# config.test.sh — config resolution and the handoff directory: the plumbing
# every verb shares.
#
# Both live in sourced libraries rather than verbs, so the subject spawned
# here is `falconet config` — unlisted, like `prompt` — which sources them and
# prints. The assertions stay about stdout and exit codes.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"


# Every case runs in its own directory, because resolution 3 is relative to
# the working directory and a stray .github/falconet.json would otherwise
# leak between them.
proj() { # name -> echoes a fresh project dir
  local d="$WORK/$1"; mkdir -p "$d/.github"; printf '%s' "$d"
}

assert_dir() { # path
  [ -d "$1" ] && assert_eq "present" "present" "directory $1" \
               || assert_eq "present" "absent" "directory $1"
}

# Reports the 1-based position of an exact line, or the empty string.
line_of() { # needle haystack
  printf '%s\n' "$2" | grep -nxF "$1" | cut -d: -f1
}

probe() { # dir op [arg] -> sets OUT RC
  local d="$1"; shift
  OUT="$( cd "$d" && "$FALCONET" config "$@" 2>&1 )"; RC=$?
  return 0
}

# --- defaults ---------------------------------------------------------------
#
# Every key in the schema has a default, and the defaults reproduce the origin
# repository's behavior. A verb should never have to ask whether a key is set.

d="$(proj defaults)"

it "with no config file at all, the defaults stand"
probe "$d" get .issue.queue_label
assert_eq "0" "$RC" "exit code"
assert_eq "infra-request" "$OUT"

it "and nothing claims to have read a file"
probe "$d" file
assert_eq "" "$OUT" "config path"

it "the handoff directory defaults to .falconet"
probe "$d" get .handoff_dir
assert_eq ".falconet" "$OUT"

it "the plan-side keys are gone from the schema: the plan bot owns them"
probe "$d" get .plan.command
assert_eq "null" "$OUT"
probe "$d" get .stacks
assert_eq "null" "$OUT"

# --- discovery precedence ---------------------------------------------------

d="$(proj discovery)"
printf '{"issue":{"queue_label":"from-dot-github"}}\n' > "$d/.github/falconet.json"
printf '{"issue":{"queue_label":"from-env"}}\n'        > "$d/env.json"
printf '{"issue":{"queue_label":"from-flag"}}\n'       > "$d/flag.json"

it ".github/falconet.json is found without being asked for"
probe "$d" get .issue.queue_label
assert_eq "from-dot-github" "$OUT"

it "and reports the path it read"
probe "$d" file
assert_eq ".github/falconet.json" "$OUT"

it "\$FALCONET_CONFIG beats .github/falconet.json"
OUT="$( cd "$d" && FALCONET_CONFIG="$d/env.json" "$FALCONET" config get .issue.queue_label 2>&1 )"
assert_eq "from-env" "$OUT"

it "an explicit --config beats \$FALCONET_CONFIG"
OUT="$( cd "$d" && FALCONET_CONFIG="$d/env.json" \
        "$FALCONET" config --config "$d/flag.json" get .issue.queue_label 2>&1 )"
assert_eq "from-flag" "$OUT"

# --- a config that cannot be read is never a silent default -----------------
#
# "Your configuration is being ignored" is the failure that gets discovered in
# production, on the run where the allowlist mattered.

d="$(proj broken)"
printf '{"issue": {"queue_label": }\n' > "$d/.github/falconet.json"

it "malformed JSON is exit 1, not a shrug back to defaults"
probe "$d" get .issue.queue_label
assert_eq "1" "$RC" "exit code"

it "and the message names our file, not just jq's opinion"
assert_contains "$OUT" ".github/falconet.json is not valid JSON"

d="$(proj missing)"
it "--config naming a file that does not exist is exit 1"
OUT="$( cd "$d" && "$FALCONET" config --config "$d/nope.json" get .handoff_dir 2>&1 )"; RC=$?
assert_eq "1" "$RC" "exit code"
assert_contains "$OUT" "--config names no file"

# --- merge semantics --------------------------------------------------------

d="$(proj merge)"
printf '{"issue":{"queue_label":"ops"}}\n' > "$d/.github/falconet.json"

it "setting one key leaves its siblings at their defaults"
probe "$d" get .issue.opt_out_text
assert_eq "Not eligible for AI agents" "$OUT"

it "and leaves other sections alone entirely"
probe "$d" get .labels.human
assert_eq "ready-for-human" "$OUT"

it "while the key that was set is the one that changed"
probe "$d" get .issue.queue_label
assert_eq "ops" "$OUT"

d="$(proj arrays)"
printf '{"paths":{"allow":["*.tofu"]}}\n' > "$d/.github/falconet.json"

it "an array REPLACES the default rather than extending it"
probe "$d" array .paths.allow
assert_eq "*.tofu" "$OUT"

it "because an allowlist that grows by accident is not an allowlist"
assert_not_contains "$OUT" "*.tf
"

# --- deny_content has no default; order is load-bearing ---------------------
#
# templatefile( must be tested before file(, or a templatefile() call is
# reported as file(): the right refusal naming the wrong construct. Nothing
# downstream can recover the distinction, so it has to survive the config.

d="$(proj denydefault)"

it "the default denylist is empty"
probe "$d" array .paths.deny_content
assert_eq "" "$OUT" "paths.deny_content"

d="$(proj denycustom)"
printf '{"paths":{"deny_content":["zzz(","aaa("]}}\n' > "$d/.github/falconet.json"

it "a user's denylist keeps the order they wrote it in"
probe "$d" array .paths.deny_content
assert_eq "zzz(
aaa(" "$OUT"

# --- the handoff directory --------------------------------------------------

d="$(proj handoff)"

it "handoff_init creates the configured directory"
probe "$d" handoff
assert_eq "$d/.falconet" "$OUT"
assert_dir "$d/.falconet"

it "an explicit --out-dir wins over the config"
probe "$d" handoff "$d/elsewhere"
assert_eq "$d/elsewhere" "$OUT"

it "and a relative one resolves against the repository root, not the caller's cwd"
probe "$d" handoff "rel-dir"
assert_eq "$d/rel-dir" "$OUT"

d="$(proj cfgdir)"
printf '{"handoff_dir":".ci-handoff"}\n' > "$d/.github/falconet.json"
it "the directory is configurable, so a consumer mid-migration keeps its own"
probe "$d" handoff
assert_eq "$d/.ci-handoff" "$OUT"

# --- $GITHUB_ENV is optional ------------------------------------------------
#
# Handoff files are written always; this is only their CI mirror. A verb that
# determined an outcome must not fail because it is running on a laptop.

d="$(proj ghenv)"

it "with GITHUB_ENV set and writable, the export lands"
( cd "$d" && GITHUB_ENV="$d/gh_env" "$FALCONET" config env "BRANCH=issue-1-x" ) >/dev/null 2>&1
assert_contains "$(cat "$d/gh_env" 2>/dev/null)" "BRANCH=issue-1-x"

it "with GITHUB_ENV unset, it is a silent no-op and not a failure"
OUT="$( cd "$d" && env -u GITHUB_ENV "$FALCONET" config env "BRANCH=x" 2>&1 )"; RC=$?
assert_eq "0" "$RC" "exit code"
assert_eq "" "$OUT" "output"

it "with GITHUB_ENV pointing somewhere unwritable, still a no-op and still 0"
OUT="$( cd "$d" && GITHUB_ENV=/proc/nope/gh_env "$FALCONET" config env "BRANCH=x" 2>&1 )"; RC=$?
assert_eq "0" "$RC" "exit code"

summary
