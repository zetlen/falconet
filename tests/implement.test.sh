#!/usr/bin/env bash
#
# implement.test.sh — the agent, run once through whatever harness.command
# names.
#
# The harness in every case is a bash stub: what the agent IS is the
# operator's business, and what the verb hands it and does with its exit is
# this file's. The stub records its argv, its cwd, its stdin and its
# environment, so the implement contract — prompt on stdin and in the
# handoff, run from the repository root, the model key in the environment,
# nothing else decided here — is what these cases hold.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null

# A harness stub: records what it was given, then does what its case wants
# (the body, a bash snippet run with the stub's arguments in $@).
stub() { # path body
  cat >"$1" <<STUB
#!/usr/bin/env bash
dir="\$(dirname "\$0")"
printf '%s\n' "\$@" >"\$dir/argv"
pwd >"\$dir/cwd"
cat >"\$dir/stdin"
env >"\$dir/env"
$2
STUB
  chmod +x "$1"
}

# A checkout with one base commit, a committed config naming the harness
# the case wants, and an empty handoff directory.
new_checkout() { # name harness-json -> echoes the checkout path
  local base="$WORK/$1"
  mkdir -p "$base/repo/.falconet" "$base/repo/.github"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  printf 'a = 1\n' >"$base/repo/settings.toml"
  printf '.falconet/\n' >"$base/repo/.gitignore"
  printf '{"paths":{"allow":["*.toml"]},"harness":{"command":%s}}\n' "$2" \
    >"$base/repo/.github/falconet.json"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" switch -qc issue-1-thing
  printf '%s' "$base"
}

run_in() { # checkout [args...] -> sets OUT ERR RC
  local c="$1"; shift
  OUT="$( cd "$c/repo" && "$FALCONET" implement --out-dir "$c/repo/.falconet" "$@" 2>"$c/err" )"; RC=$?
  ERR="$(cat "$c/err")"
  return 0
}

# --- the contract, from the harness's side ------------------------------------

c="$(new_checkout plain '["'"$WORK"'/plain/harness","--flag","value"]')"
stub "$c/harness" 'echo "hello from the harness"; echo "and on stderr" >&2; printf "done by the agent\n" >"$dir/repo/.falconet/commit-msg.txt"'
printf '# 1 Do the thing\n' >"$c/repo/.falconet/request.md"
ANTHROPIC_API_KEY=sk-test-only run_in "$c"

it "a harness that exits 0 is the word done, exit 0"
assert_eq 0 "$RC" "exit code"
assert_eq "done" "$OUT" "stdout"

it "and its own output, both streams, goes to the run log and never to stdout"
assert_contains "$ERR" "hello from the harness" "stderr"
assert_contains "$ERR" "and on stderr" "stderr"

it "the harness gets the argv the config wrote, and nothing appended"
assert_eq $'--flag\nvalue' "$(cat "$c/argv")" "argv"

it "and runs from the repository root"
assert_eq "$(cd "$c/repo" && pwd -P)" "$(cd "$(cat "$c/cwd")" && pwd -P)" "cwd"

it "the rendered prompt is on its stdin"
assert_contains "$(cat "$c/stdin")" "$c/repo/.falconet/request.md" "stdin: the handoff placeholder rendered"
assert_contains "$(cat "$c/stdin")" '`*.toml`' "stdin: the allowlist rendered"
assert_not_contains "$(cat "$c/stdin")" "{handoff}" "stdin"

it "and the same bytes are in the handoff, for a harness that takes a file"
assert_eq "$(cat "$c/stdin")" "$(cat "$c/repo/.falconet/prompt.md")" "prompt.md"

it "the environment reaches it, which is how the model key arrives"
assert_contains "$(cat "$c/env")" "ANTHROPIC_API_KEY=sk-test-only" "env"

it "and what the harness left in the handoff is left alone: this verb does not read it"
assert_eq "done by the agent" "$(cat "$c/repo/.falconet/commit-msg.txt")" "commit-msg.txt"

it "and nothing was committed"
assert_eq 1 "$(git -C "$c/repo" rev-list --count HEAD)" "commits"

# --- a harness that fails ------------------------------------------------------

c="$(new_checkout failing '["'"$WORK"'/failing/harness"]')"
stub "$c/harness" 'echo "model unreachable" >&2; exit 7'
run_in "$c"

it "a harness that exits non-zero is a mechanical failure: exit 1, no word"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

it "saying so, with the harness's own words above it"
assert_contains "$ERR" "model unreachable" "stderr"
assert_contains "$ERR" "the harness failed" "stderr"

c="$(new_checkout missing '["'"$WORK"'/missing/no-such-harness"]')"
run_in "$c"

it "a harness that cannot be started is the same failure"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$ERR" "could not run" "stderr"

it "and the prompt was still written, so the failure can be read against it"
assert_eq "true" "$([[ -s "$c/repo/.falconet/prompt.md" ]] && echo true || echo false)" "prompt.md exists"

# --- no harness ----------------------------------------------------------------

c="$(new_checkout none '[]')"
run_in "$c"

it "an empty harness.command is refused, not skipped: a pipeline with no agent is a misconfiguration"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$ERR" "no harness configured" "stderr"

# --- the guard's own configuration -------------------------------------------
#
# The command comes from the config, and on a second pass the config is a
# file the agent of the first pass could have rewritten. A tree that changed
# it is refused before anything runs.

c="$(new_checkout tampered '["true"]')"
printf '{"paths":{"allow":["*"]},"harness":{"command":["touch","%s/ran"]}}\n' "$c" \
  >"$c/repo/.github/falconet.json"
run_in "$c"

it "a config file changed in the tree is refused before anything runs"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$ERR" ".github/falconet.json" "stderr"

it "and the harness the agent chose did not run"
assert_file_missing "$c/ran"

# --- the prompt is the config's --------------------------------------------------

c="$(new_checkout override '["'"$WORK"'/override/harness"]')"
stub "$c/harness" ':'
mkdir -p "$c/repo/prompts"
printf 'Custom prompt. Handoff is {handoff}.\n' >"$c/repo/prompts/mine.md"
printf '{"paths":{"allow":["*.toml"]},"harness":{"command":["%s/harness"]},"prompts":{"implement":"prompts/mine.md"}}\n' "$c" \
  >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "own prompt"
run_in "$c"

it "prompts.implement names the prompt the harness reads"
assert_eq 0 "$RC" "exit code"
assert_eq "Custom prompt. Handoff is $c/repo/.falconet." "$(cat "$c/stdin")" "stdin"

# --- outside a repository ------------------------------------------------------

it "outside a git repository is a mechanical failure"
d="$WORK/notarepo"; mkdir -p "$d/.github"
OUT="$( cd "$d" && "$FALCONET" implement 2>/dev/null )"; RC=$?
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

# --- usage ---------------------------------------------------------------------

it "an unknown argument exits 2"
OUT="$("$FALCONET" implement --bogus 2>/dev/null)"; RC=$?
assert_eq 2 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

it "and so does --help, because 0 would mean ran, fine"
"$FALCONET" implement --help >/dev/null 2>&1; RC=$?
assert_eq 2 "$RC" "exit code"

it "implement is a listed verb"
assert_contains "$("$FALCONET" 2>&1)" "implement" "usage"

summary
