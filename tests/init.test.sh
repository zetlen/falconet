#!/usr/bin/env bash
#
# init.test.sh — README "Install it in your repository" steps 2, 7 and 8,
# done by the second setup verb: the local files committed and never pushed
# (#10). The labels and the secrets are #11.
#
# init has no bash predecessor (ADR-0006 D3 step 1), so nothing here is a
# ported assertion: each README step is the specification of a write, the
# issue fixes the Done-whens (a fresh clone gets one commit; doctor then
# reports steps 2, 7 and 8 ok; a second run makes none), and doctor's format
# fixes the shape. GitHub is tests/fixtures/fake-github.py on loopback, so
# that a run which reached for it would be seen to.
#
# stdin is never a terminal here, so the stacks are always flags.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_DATE='2026-08-22T12:00:00Z' GIT_COMMITTER_DATE='2026-08-22T12:00:00Z'

# --- the fake API, and no gh ------------------------------------------------

fake_github
export GITHUB_REPOSITORY=zetlen/wayfinders-infra
unset FALCONET_SETUP_TOKEN
unset GITHUB_SERVER_URL GH_TOKEN GITHUB_TOKEN

# A tripwire, as doctor.test.sh has: an init that shells out to gh must fail
# here, loudly, before the real gh on this machine could carry the token
# above anywhere.
mkdir -p "$WORK/no-gh"
cat >"$WORK/no-gh/gh" <<'TRIPWIRE'
#!/usr/bin/env bash
echo "gh: init.test.sh does not stub gh — the subject must speak GITHUB_API_URL" >&2
exit 1
TRIPWIRE
chmod +x "$WORK/no-gh/gh"
PATH="$WORK/no-gh:$PATH"
export PATH

# --- fixtures -----------------------------------------------------------------

# A qualifying repository with nothing installed: two stack directories with
# a .tf in each, a docs/ directory without, one clean commit on main, and
# none of the files init writes.
new_checkout() { # name -> echoes path
  local base="$WORK/$1"
  mkdir -p "$base/repo/dns" "$base/repo/workspace" "$base/repo/docs"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  printf 'locals {\n  a = 1\n}\n' >"$base/repo/dns/main.tf"
  printf 'locals {\n  b = 2\n}\n' >"$base/repo/workspace/main.tf"
  printf 'notes\n' >"$base/repo/docs/README.md"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  printf '%s' "$base"
}

# responses.json: whatever a case wants to exist. The fake's defaults hold
# no labels and no secrets, so a fresh repository is the default.
script() { # [rule-json ...]
  local rules="" r
  for r in "$@"; do rules="$rules${rules:+,}$r"; done
  printf '[%s]\n' "$rules" >"$FAKE_GITHUB/responses.json"
}
: >"$WORK/empty"

i() { # checkout stdin-file [args...] -> sets OUT ERR RC
  local c="$1" in="$2"; shift 2
  : >"$FAKE_GITHUB/requests.log"
  : >"$FAKE_GITHUB/requests.jsonl"
  OUT="$( cd "$c/repo" && "$FALCONET" init "$@" <"$in" 2>"$WORK/err" )"
  RC=$?
  ERR="$(cat "$WORK/err")"
  return 0
}

assert_line() { # haystack exact-line [what]
  if grep -Fxq -- "$2" <<<"$1"; then _pass; else
    _fail "${3:-output} has no line: [$2]" "got: [${1:0:800}]"
  fi
}

assert_no_line_matching() { # haystack regex [what]
  if grep -Eq -- "$2" <<<"$1"; then
    _fail "${3:-output} has a line matching: [$2]" "got: [${1:0:800}]"
  else _pass; fi
}

calls() { awk '{ print $1, $2 }' "$FAKE_GITHUB/requests.log"; }

head_of() { git -C "$1/repo" rev-parse HEAD; }
committed_files() { git -C "$1/repo" show --format= --name-only HEAD | sort; }

# The ref the workflow must carry: the binary's own version, and main for a
# dev build (the one the suite builds).
ver="$("$FALCONET" version | awk '{ print $2 }')"
ref="$ver"; [[ "$ver" == dev ]] && ref=main
reusable="zetlen/falconet/.github/workflows/falconet.yml"

# --- issue #10's Done-when: a fresh clone, one commit ------------------------

c="$(new_checkout fresh)"
before="$(head_of "$c")"
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "a fresh clone is exit 0"
assert_eq 0 "$RC" "exit code"

it "and leaves exactly one commit on top of the base"
assert_eq 2 "$(git -C "$c/repo" rev-list --count HEAD)" "commits"
assert_eq "Install falconet" "$(git -C "$c/repo" log -1 --format=%s)" "subject"

it "carrying the three files #10 names plus the prompt copy, and nothing else"
# #10 said "three files"; pointing prompts.implement at a copy of the shipped
# prompt, which #10 also asks for, makes it four.
assert_eq ".github/falconet.json
.github/workflows/infra-requests.yml
.gitignore
prompts/implement.md" "$(committed_files "$c")" "committed paths"

it "and the tree is clean afterwards"
assert_eq "" "$(git -C "$c/repo" status --porcelain)" "status"

it "the commit body names what was written"
assert_contains "$(git -C "$c/repo" log -1 --format=%b)" ".github/workflows/infra-requests.yml" "body"
assert_contains "$(git -C "$c/repo" log -1 --format=%b)" "prompts/implement.md" "body"

it "step 2: .gitignore holds the handoff directory and nothing else"
assert_eq ".falconet/" "$(cat "$c/repo/.gitignore")" ".gitignore"

it "step 7: --plan dns --validate-only workspace lands in the JSON exactly"
assert_eq '{
  "stacks": {
    "plan": [
      "dns"
    ],
    "validate_only": [
      "workspace"
    ]
  },
  "prompts": {
    "implement": "prompts/implement.md"
  }
}' "$(cat "$c/repo/.github/falconet.json")" "config"

it "step 7: the prompt copy is the shipped prompt, byte for byte"
assert_eq "$(cat "$REPO_ROOT/prompts/implement.md")" "$(cat "$c/repo/prompts/implement.md")" "prompt"

it "step 8: the workflow's uses: line carries the binary's version (dev builds pin main)"
assert_eq "    uses: $reusable@$ref" "$(grep -E '^ *uses: ' "$c/repo/.github/workflows/infra-requests.yml")" "uses line"

it "and has no falconet-ref input: post-cutover, the binary is the one coordinate"
assert_not_contains "$(cat "$c/repo/.github/workflows/infra-requests.yml")" "falconet-ref" "workflow"

it "and its permissions block is README step 8's, read the way contract.test.sh reads it"
readme_perms="$(awk '/^### 8\./ { s = 1 } s && /^### 9\./ { exit } s' "$REPO_ROOT/README.md" \
  | awk '/^permissions:/ { p = 1; next } p && /^[^[:space:]]/ { p = 0 } p')"
wf_perms="$(awk '/^permissions:/ { p = 1; next } p && /^[^[:space:]]/ { p = 0 } p' "$c/repo/.github/workflows/infra-requests.yml")"
assert_eq "$readme_perms" "$wf_perms" "permissions block"

it "and the four secrets lines"
assert_eq 4 "$(grep -cE '^      (app-id|app-private-key|anthropic-api-key|plan-env): \$\{\{ secrets\.' "$c/repo/.github/workflows/infra-requests.yml")" "secrets lines"

it "and the concurrency block"
assert_contains "$(cat "$c/repo/.github/workflows/infra-requests.yml")" 'group: falconet-${{ github.event.issue.number }}' "workflow"

it "the report is in doctor's format: one line per step, status word, step number"
assert_line "$OUT" "ok           1. the working tree is clean"
assert_line "$OUT" "done         2. .falconet/ added to .gitignore"
assert_line "$OUT" "done         7. .github/falconet.json written (plan: dns; validate_only: workspace)"
assert_line "$OUT" "done         7. prompts.implement names prompts/implement.md, copied from the shipped prompt"
assert_line "$OUT" "done         8. .github/workflows/infra-requests.yml written (uses $reusable@$ref)"
assert_line "$OUT" 'done         committed "Install falconet" (4 files)'

it "and every line is in the column, or the summary, or the Left-for-you block"
assert_eq 0 "$(grep -Ev '^(ok|done|skipped|MISSING|cannot tell|note) {1,11}([1-9]\. |[a-z])|^             [^ ]|^init: |^$|^Left for you:$|^  [0-9]+\. ' <<<"$OUT" | grep -c .)" \
  "lines outside the format"

it "every remote step is skipped: they are #11's"
assert_line "$OUT" "skipped      6. the four labels (#11)"
assert_line "$OUT" "skipped      4. secret ANTHROPIC_API_KEY (#11)"
assert_line "$OUT" "skipped      5. secret FALCONET_PLAN_ENV (#11)"
assert_line "$OUT" "skipped      3. secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (#11)"

it "and the summary counts them"
assert_line "$OUT" "init: 1 ok, 5 done, 4 skipped, 0 missing, 0 cannot tell"

it "Left for you: the push first, never run"
assert_eq "  1. git push origin main" "$(grep -A1 '^Left for you:' <<<"$OUT" | tail -1)" "first item"

it "then the App, the key, the plan env, the labels, the prompt's standing facts, the canary, and doctor — in that order"
assert_eq "3 4 5 6 7 9 then" "$(sed -n '/^Left for you:/,$p' <<<"$OUT" | grep -E '^  [2-9]\. ' | sed -E 's/^  [0-9]+\. step ([0-9]) .*/\1/; s/^  [0-9]+\. then: .*/then/' | tr '\n' ' ' | sed 's/ $//')" "item order"
assert_contains "$OUT" "step 7 — edit the standing-facts block in prompts/implement.md" "stdout"
assert_contains "$OUT" "step 9 — file a canary issue" "stdout"
assert_contains "$OUT" "step 3 — the GitHub App" "stdout"
assert_contains "$OUT" "gh secret set FALCONET_APP_ID --body '<the App ID>'" "stdout"
assert_eq "  8. then: falconet doctor" "$(tail -1 <<<"$OUT")" "last line"

it "and no call reached the API"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

# --- doctor afterwards ----------------------------------------------------------

DOUT="$( cd "$c/repo" && "$FALCONET" doctor 2>/dev/null )"

it "doctor afterwards reports steps 2, 7 and 8 ok"
assert_line "$DOUT" "ok           2. .falconet/ is gitignored"
assert_line "$DOUT" "ok           7. .github/falconet.json parses"
assert_line "$DOUT" "ok           7. prompts.implement names prompts/implement.md, which exists"
assert_line "$DOUT" "ok           8. .github/workflows/infra-requests.yml exists"
assert_line "$DOUT" "ok           8. it uses $reusable@$ref"
assert_line "$DOUT" "ok           8. permissions grants contents: write, issues: write, pull-requests: write"

it "and the stacks init sorted"
assert_line "$DOUT" "ok           1. stack dns (.stacks.plan) is a directory with .tf files"
assert_line "$DOUT" "ok           1. stack workspace (.stacks.validate_only) is a directory with .tf files"

# --- issue #10's third Done-when: a second init makes no commit ------------------

after="$(head_of "$c")"
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "a second init is exit 0"
assert_eq 0 "$RC" "exit code"

it "and makes no commit and changes nothing"
assert_eq "$after" "$(head_of "$c")" "HEAD"
assert_eq "" "$(git -C "$c/repo" status --porcelain)" "status"

it "and every local step reports ok, none done"
assert_line "$OUT" "ok           2. .falconet/ is gitignored"
assert_line "$OUT" "ok           7. .github/falconet.json parses"
assert_line "$OUT" "ok           7. prompts.implement names prompts/implement.md, which exists"
assert_line "$OUT" "ok           8. .github/workflows/infra-requests.yml exists"
assert_line "$OUT" "ok           nothing to commit: every file exists"
assert_no_line_matching "$OUT" '^done' "stdout"

it "and the prompt's standing facts are not left again: the file was not just copied"
assert_not_contains "$OUT" "standing-facts" "stdout"

it "nor the push"
assert_not_contains "$OUT" "git push" "stdout"

# --- the dirty tree ---------------------------------------------------------------

c="$(new_checkout dirty)"
printf 'locals {\n  a = 99\n}\n' >"$c/repo/dns/main.tf"
before="$(head_of "$c")"
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "a dirty tree is refused, exit 1, before anything else"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

it "naming the paths on stderr"
assert_contains "$ERR" " M dns/main.tf" "stderr"

it "before any call"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

it "and before any file"
assert_file_missing "$c/repo/.gitignore"
assert_file_missing "$c/repo/.github"
assert_eq "$before" "$(head_of "$c")" "HEAD"

git -C "$c/repo" checkout -q -- dns/main.tf
printf 'stray\n' >"$c/repo/stray.txt"
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "an untracked file counts as dirt, as it does for prepare"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "?? stray.txt" "stderr"

# --- --no-commit --------------------------------------------------------------------------

c="$(new_checkout nocommit)"
before="$(head_of "$c")"
i "$c" "$WORK/empty" --plan dns --validate-only workspace --no-commit

it "--no-commit leaves the four files staged and makes no commit"
assert_eq 0 "$RC" "exit code"
assert_eq "$before" "$(head_of "$c")" "HEAD"
assert_eq ".github/falconet.json
.github/workflows/infra-requests.yml
.gitignore
prompts/implement.md" "$(git -C "$c/repo" diff --cached --name-only | sort)" "staged"

it "and says so"
assert_line "$OUT" "skipped      the commit (--no-commit): 4 files staged"
assert_contains "$OUT" "git commit   (the files are staged; --no-commit left the commit to you), then git push origin main" "stdout"

# --- a repository that does not qualify, and the stacks --------------------------------

c="$(new_checkout notf)"
rm "$c/repo/dns/main.tf" "$c/repo/workspace/main.tf"
printf 'x\n' >"$c/repo/main.tf"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "no stacks"
before="$(head_of "$c")"
i "$c" "$WORK/empty"

it "a repository with no .tf in any subdirectory does not qualify: exit 1"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "does not qualify" "stderr"
assert_contains "$ERR" "a .tf at the root is not a stack" "stderr"

it "and nothing was written or called"
assert_eq "$before" "$(head_of "$c")" "HEAD"
assert_eq "" "$(git -C "$c/repo" status --porcelain)" "status"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

c="$(new_checkout stacks)"
i "$c" "$WORK/empty" --plan nosuch --validate-only dns,workspace

it "--plan naming a stack that was not discovered is usage, naming it and what was found"
assert_eq 2 "$RC" "exit code"
assert_contains "$ERR" "--plan names nosuch, which is not a directory with .tf files in it (found: dns, workspace)" "stderr"

i "$c" "$WORK/empty" --plan dns

it "a discovered stack named in neither flag is refused with stdin not a terminal, naming it"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "workspace is in neither" "stderr"
assert_contains "$ERR" "--validate-only" "stderr"
assert_eq "" "$(git -C "$c/repo" status --porcelain)" "status"

i "$c" "$WORK/empty" --plan dns --validate-only dns,workspace

it "a stack in both lists is usage"
assert_eq 2 "$RC" "exit code"

mkdir -p "$c/repo/envs/prod" "$c/repo/dns/.terraform/modules/m" "$c/repo/node_modules/x"
printf 'x\n' >"$c/repo/envs/prod/main.tf"; printf 'x\n' >"$c/repo/dns/.terraform/modules/m/main.tf"; printf 'x\n' >"$c/repo/node_modules/x/main.tf"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "more"
i "$c" "$WORK/empty" --plan dns,envs/prod --validate-only workspace/

it "discovery is recursive, skips .terraform and node_modules, and a trailing slash is fine"
assert_eq 0 "$RC" "exit code"
assert_eq '    "plan": [
      "dns",
      "envs/prod"
    ],' "$(grep -A3 '"plan"' "$c/repo/.github/falconet.json")" "plan list"

# --- an existing config is kept, never rewritten -----------------------------------------

c="$(new_checkout kept)"
mkdir -p "$c/repo/.github"
printf '{"stacks":{"plan":["dns"],"validate_only":["workspace","nope"]},"prompts":{"implement":"prompts/mine.md"}}\n' >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "config"
i "$c" "$WORK/empty"

it "an existing config is kept, and checked as doctor checks it"
assert_eq 0 "$RC" "exit code"
assert_line "$OUT" "ok           7. .github/falconet.json parses"
assert_line "$OUT" "MISSING      1. stack nope (.stacks.validate_only) is not a directory"
assert_contains "$OUT" "step 1 — stack nope (.stacks.validate_only) is not a directory: set .stacks.validate_only in .github/falconet.json" "stdout"
assert_eq '{"stacks":{"plan":["dns"],"validate_only":["workspace","nope"]},"prompts":{"implement":"prompts/mine.md"}}' "$(cat "$c/repo/.github/falconet.json")" "config unchanged"

it "and a prompts.implement naming a missing file gets the shipped prompt copied there"
assert_line "$OUT" "done         7. prompts.implement names prompts/mine.md, copied from the shipped prompt"
assert_eq "$(cat "$REPO_ROOT/prompts/implement.md")" "$(cat "$c/repo/prompts/mine.md")" "prompt copy"
assert_contains "$OUT" "step 7 — edit the standing-facts block in prompts/mine.md" "stdout"
assert_eq ".github/workflows/infra-requests.yml
.gitignore
prompts/mine.md" "$(committed_files "$c")" "committed paths"

it "--plan and --validate-only are not applied over an existing config"
( cd "$c/repo" && git reset -q --hard HEAD~1 )
i "$c" "$WORK/empty" --plan workspace --validate-only dns
assert_eq 0 "$RC" "exit code"
assert_eq '{"stacks":{"plan":["dns"],"validate_only":["workspace","nope"]},"prompts":{"implement":"prompts/mine.md"}}' "$(cat "$c/repo/.github/falconet.json")" "config unchanged"

c="$(new_checkout unset)"
mkdir -p "$c/repo/.github"
printf '{"stacks":{"plan":["dns"],"validate_only":["workspace"]}}\n' >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "config"
i "$c" "$WORK/empty"

it "a config that does not set prompts.implement is a note, and the shipped prompt's standing facts are left for you"
assert_line "$OUT" "note         7. prompts.implement is not set, so the shipped prompt is used, and its standing facts are the origin's"
assert_contains "$OUT" "step 7 — prompts.implement is not set" "stdout"
assert_file_missing "$c/repo/prompts/implement.md"

c="$(new_checkout badcfg)"
mkdir -p "$c/repo/.github"
printf '{"stacks": {"plan": ["dns"],}\n' >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "bad config"
i "$c" "$WORK/empty"

it "a config that does not parse is refused: init never rewrites a consumer's config"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "is not valid JSON" "stderr"
assert_contains "$ERR" "init never rewrites a config" "stderr"

c="$(new_checkout alt)"
printf '{"stacks":{"plan":["dns"],"validate_only":["workspace"]}}\n' >"$c/alt.json"
i "$c" "$WORK/empty" --config ../alt.json

it "--config names the config, relative to where the caller stands, and step 7 writes nothing"
assert_eq 0 "$RC" "exit code"
assert_line "$OUT" "ok           7. $c/alt.json parses"
assert_file_missing "$c/repo/.github/falconet.json"
assert_eq ".github/workflows/infra-requests.yml
.gitignore" "$(committed_files "$c")" "committed paths"

# --- existing files: step 2 and step 8 ------------------------------------------------------

c="$(new_checkout partial)"
printf 'node_modules/' >"$c/repo/.gitignore"
mkdir -p "$c/repo/.github/workflows"
sed -e 's/^  contents: write$/  contents: read/' "$WORK/fresh/repo/.github/workflows/infra-requests.yml" >"$c/repo/.github/workflows/infra-requests.yml"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "partial"
cp "$c/repo/.github/workflows/infra-requests.yml" "$WORK/partial.yml"
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "a .gitignore without a trailing newline gets the entry on its own line"
assert_eq "node_modules/
.falconet/" "$(cat "$c/repo/.gitignore")" ".gitignore"

it "an existing workflow is kept and checked as doctor checks it: contents: read is MISSING, left for you"
assert_line "$OUT" "ok           8. .github/workflows/infra-requests.yml exists"
assert_line "$OUT" "MISSING      8. permissions grants contents: read, and falconet's widest job declares contents: write, issues: write, pull-requests: write"
assert_contains "$OUT" "step 8 — permissions grants contents: read" "stdout"
assert_eq "$(cat "$WORK/partial.yml")" "$(cat "$c/repo/.github/workflows/infra-requests.yml")" "file untouched"
assert_eq 0 "$RC" "exit code: a MISSING init cannot fix is not a failure"

c="$(new_checkout excluded)"
printf '.falconet/\n' >>"$c/repo/.git/info/exclude"
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "a handoff directory ignored by .git/info/exclude is ok: git check-ignore is the check, as doctor's"
assert_line "$OUT" "ok           2. .falconet/ is gitignored"
assert_file_missing "$c/repo/.gitignore"

c="$(new_checkout handoff)"
mkdir -p "$c/repo/.github"
printf '{"handoff_dir":"scratch","stacks":{"plan":["dns"],"validate_only":["workspace"]}}\n' >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "config"
i "$c" "$WORK/empty"

it "a configured handoff_dir is what is ignored"
assert_line "$OUT" "done         2. scratch/ added to .gitignore"
assert_eq "scratch/" "$(cat "$c/repo/.gitignore")" ".gitignore"

# --- from a subdirectory ------------------------------------------------------------------------

c="$(new_checkout subdir)"
OUT="$( cd "$c/repo/dns" && "$FALCONET" init --plan dns --validate-only workspace <"$WORK/empty" 2>/dev/null )"; RC=$?

it "from a subdirectory the files land at the root: the verb runs from there"
assert_eq 0 "$RC" "exit code"
assert_eq ".falconet/" "$(cat "$c/repo/.gitignore")" ".gitignore"
assert_file_missing "$c/repo/dns/.gitignore"

# --- not a repository -------------------------------------------------------------------------

mkdir -p "$WORK/plain/dns"; printf 'x\n' >"$WORK/plain/dns/main.tf"
OUT="$( cd "$WORK/plain" && FALCONET_REPO="$WORK/plain" "$FALCONET" init --plan dns <"$WORK/empty" 2>"$WORK/err" )"; RC=$?

it "outside a git repository is exit 1: there is nothing to commit into"
assert_eq 1 "$RC" "exit code"
assert_contains "$(cat "$WORK/err")" "is not a git repository" "stderr"
assert_file_missing "$WORK/plain/.gitignore"

# --- usage ----------------------------------------------------------------------------------------

c="$(new_checkout usage)"

it "-h is a usage error, with nothing on stdout"
i "$c" "$WORK/empty" -h
assert_eq 2 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$ERR" "init — install falconet into this repository" "stderr"

it "an unknown flag is a usage error"
i "$c" "$WORK/empty" --bogus
assert_eq 2 "$RC" "exit code"
assert_contains "$ERR" "unknown argument: --bogus" "stderr"

it "a flag without its value is a usage error"
i "$c" "$WORK/empty" --plan
assert_eq 2 "$RC" "exit code"
i "$c" "$WORK/empty" --config
assert_eq 2 "$RC" "exit code"

it "a usage error makes no API call and writes nothing"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"
assert_eq "" "$(git -C "$c/repo" status --porcelain)" "status"

it "init is vocabulary: the dispatcher lists it after doctor"
assert_contains "$("$FALCONET" -h 2>&1)" "  init      " "usage"
assert_eq 1 "$("$FALCONET" -h 2>&1 | grep -A1 '^  doctor ' | grep -c '^  init ')" "init follows doctor"

summary
