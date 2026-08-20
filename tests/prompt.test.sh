#!/usr/bin/env bash
#
# prompt.test.sh — the unlisted helper that keeps prompts out of YAML.
#
# It exists because a prompt written as a YAML block scalar picks up the
# indentation of the block it was written in, and four-space-indented markdown
# renders as a code block. It also exists so the prompt that runs in CI is
# byte-for-byte the prompt that runs on a workstation, which is what the
# placeholder substitution is for.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

FALCONET="$REPO_ROOT/bin/falconet"

proj() { local d="$WORK/$1"; mkdir -p "$d/.github"; printf '%s' "$d"; }

it "a shipped prompt prints"
out="$("$FALCONET" prompt implement)"; rc=$?
assert_eq 0 "$rc" "exit code"

it "and it is the implementing agent's prompt"
assert_contains "$out" "Scripts do the mechanics; you do the judgment." "prompt"

it "the tool grant it describes is still the narrow one"
assert_not_contains "$out" "Bash" "prompt"

# The origin's prompt spelled these as an Actions template expression, which
# means nothing on a workstation. One prompt, two places to run it.
it "{handoff} is substituted for a real path"
assert_contains "$out" "$REPO_ROOT/.falconet/request.md" "prompt"

it "and no Actions template expression survives"
assert_not_contains "$out" 'github.workspace' "prompt"

it "--out-dir moves the handoff paths the prompt names"
out="$("$FALCONET" prompt implement --out-dir /tmp/elsewhere)"
assert_contains "$out" "/tmp/elsewhere/request.md" "prompt"

it "the park preambles ship too"
assert_contains "$("$FALCONET" prompt park-needs-info)" \
  "I need a bit more from you" "prompt"

it "including the one for a run that could not finish"
assert_contains "$("$FALCONET" prompt park-failure)" \
  "This one needs a person." "prompt"

# --- config overrides -------------------------------------------------------

d="$(proj override)"
mkdir -p "$d/custom"
printf 'A different prompt entirely.\n' >"$d/custom/mine.md"
printf '{"prompts":{"implement":"custom/mine.md"}}\n' >"$d/.github/falconet.json"

it "a config override wins over the shipped copy"
out="$( cd "$d" && "$FALCONET" prompt implement --config "$d/.github/falconet.json" 2>&1 )"
assert_contains "$out" "A different prompt entirely." "prompt"

it "and the name is looked up with dashes folded to underscores"
printf 'Custom parking text.\n' >"$d/custom/park.md"
printf '{"prompts":{"park_needs_info":"custom/park.md"}}\n' >"$d/.github/falconet.json"
out="$( cd "$d" && "$FALCONET" prompt park-needs-info --config "$d/.github/falconet.json" 2>&1 )"
assert_contains "$out" "Custom parking text." "prompt"

it "an override pointing at nothing is an error, not a silent fallback"
printf '{"prompts":{"implement":"custom/gone.md"}}\n' >"$d/.github/falconet.json"
out="$( cd "$d" && "$FALCONET" prompt implement --config "$d/.github/falconet.json" 2>&1 )"; rc=$?
assert_eq 1 "$rc" "exit code"

it "and says which prompt it could not find"
assert_contains "$out" "custom/gone.md" "stderr"

# --- printing a prompt is a read --------------------------------------------

d="$(proj noside)"
it "printing a prompt leaves no handoff directory behind"
( cd "$d" && "$FALCONET" prompt implement >/dev/null 2>&1 )
assert_file_missing "$d/.falconet"

# --- usage ------------------------------------------------------------------

it "an unknown prompt name is an error"
"$FALCONET" prompt nosuch >/dev/null 2>&1
assert_eq 1 "$?" "exit code"

it "no name at all is a usage error"
"$FALCONET" prompt >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

it "two names at once is a usage error"
"$FALCONET" prompt implement park-failure >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

it "-h/--help is a usage error"
"$FALCONET" prompt --help >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

it "and prompt stays unlisted in the dispatcher's usage"
assert_not_contains "$("$FALCONET" --help 2>&1)" "prompt " "usage"

summary
