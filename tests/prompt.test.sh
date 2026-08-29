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

it "the pause preambles ship too"
assert_contains "$("$FALCONET" prompt pause-needs-info)" \
  "I need a bit more from you" "prompt"

it "including the one for a run that could not finish"
assert_contains "$("$FALCONET" prompt pause-failure)" \
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
printf 'Custom pause text.\n' >"$d/custom/pause.md"
printf '{"prompts":{"pause_needs_info":"custom/pause.md"}}\n' >"$d/.github/falconet.json"
out="$( cd "$d" && "$FALCONET" prompt pause-needs-info --config "$d/.github/falconet.json" 2>&1 )"
assert_contains "$out" "Custom pause text." "prompt"

it "an override pointing at nothing is an error, not a silent fallback"
printf '{"prompts":{"implement":"custom/gone.md"}}\n' >"$d/.github/falconet.json"
out="$( cd "$d" && "$FALCONET" prompt implement --config "$d/.github/falconet.json" 2>&1 )"; rc=$?
assert_eq 1 "$rc" "exit code"

it "and says which prompt it could not find"
assert_contains "$out" "custom/gone.md" "stderr"

# --- the prompt says what the guards' config says ---------------------------

# The shipped prompt once carried the repository falconet was extracted from:
# its registrar sandbox, its scratch tenant, its file names, and a hand-written
# `.tf` allowlist and HCL denylist that the config might not agree with. Every
# adopter's agent was told about a sandbox it did not have. The prompt now
# renders `paths.allow` at {allow} and `paths.deny_content` at {deny}, and
# names nothing of any particular repository's.
d="$(proj guards)"
printf '{"paths":{"allow":["docs/*.md","config/**"],"deny_content":[]}}\n' >"$d/.github/falconet.json"
out="$( cd "$d" && "$FALCONET" prompt implement --config "$d/.github/falconet.json" 2>&1 )"

it "the allowlist the agent is told is the config's, as written"
assert_contains "$out" '`docs/*.md` or `config/**`' "prompt"

it "and not the default"
assert_not_contains "$out" '*.tf' "prompt"

it "an empty denylist leaves no sentence about refused content"
assert_not_contains "$out" 'refuses a changed file' "prompt"

it "and no placeholder behind"
assert_not_contains "$out" '{deny}' "prompt"

it "nothing of the origin repository is in the shipped prompt"
assert_not_contains "$out" 'Namecheap' "prompt"

it "not its site"
assert_not_contains "$out" 'papernapkin' "prompt"

it "not its file layout"
assert_not_contains "$out" 'records-*.tf' "prompt"

it "and not its tooling"
assert_not_contains "$out" 'tofu' "prompt"

it "with no config at all, the prompt names the default allowlist"
out="$("$FALCONET" prompt implement)"
assert_contains "$out" '`*.tf`' "prompt"

it "and the default denylist, in config order"
assert_contains "$out" '`data "external"`, `provisioner`, `local-exec`, `remote-exec`, `templatefile(`, `filebase64(` or `file(`' "prompt"

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
"$FALCONET" prompt implement pause-failure >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

it "-h/--help is a usage error"
"$FALCONET" prompt --help >/dev/null 2>&1
assert_eq 2 "$?" "exit code"

it "and prompt stays unlisted in the dispatcher's usage"
assert_not_contains "$("$FALCONET" --help 2>&1)" "prompt " "usage"

summary
