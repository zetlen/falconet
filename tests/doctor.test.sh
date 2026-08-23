#!/usr/bin/env bash
#
# doctor.test.sh — README "Install it in your repository" steps 1–8, checked
# by the first setup verb and reported one line each.
#
# doctor has no bash predecessor (ADR-0006 D3 step 1), so nothing here is a
# ported assertion: each step's Check: line in the README is the
# specification, and the issue (#9) fixes the shape — `ok`, `MISSING`,
# `cannot tell (why)`, a fixed-width column, a summary, exit 0 only when
# every check is ok. GitHub is tests/fixtures/fake-github.py on loopback;
# the secrets and labels a case wants to exist are scripted in
# responses.json, because the fake's defaults hold none of either.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null

# --- the fake API, and no gh ------------------------------------------------

fake_github
export FALCONET_SETUP_TOKEN=test-token
export GITHUB_REPOSITORY=zetlen/wayfinders-infra
unset GITHUB_SERVER_URL GH_TOKEN GITHUB_TOKEN

# A tripwire, as pause.test.sh has: a doctor that shells out to gh must fail
# here, loudly, before the real gh on this machine could carry the token
# above anywhere.
mkdir -p "$WORK/no-gh"
cat >"$WORK/no-gh/gh" <<'TRIPWIRE'
#!/usr/bin/env bash
echo "gh: doctor.test.sh does not stub gh — the subject must speak GITHUB_API_URL" >&2
exit 1
TRIPWIRE
chmod +x "$WORK/no-gh/gh"
PATH="$WORK/no-gh:$PATH"
export PATH

# --- fixtures -----------------------------------------------------------------

# The caller workflow README step 8 prescribes, minimal and correct, with the
# ref pinned so that nothing in it is a note.
caller() { # [contents-level]
  cat <<EOF
name: infra requests

on:
  issues:
    types: [opened, labeled, reopened]
  issue_comment:
    types: [created]

# A called workflow can only narrow the caller's token, never widen it.
permissions:
  contents: ${1:-write}
  issues: write
  pull-requests: write

jobs:
  falconet:
    uses: zetlen/falconet/.github/workflows/falconet.yml@0123abcd
    with:
      issue: \${{ github.event.issue.number }}
    secrets:
      app-id: \${{ secrets.FALCONET_APP_ID }}
EOF
}

# A repository installed to the letter: three stacks with .tf in them, the
# handoff directory ignored, a config naming the stacks and one prompt, and
# the caller above. Built like prepare.test.sh's new_checkout.
new_checkout() { # name -> echoes path
  local base="$WORK/$1"
  mkdir -p "$base/repo/.github/workflows" "$base/repo/dns" "$base/repo/workspace" "$base/repo/site" "$base/repo/prompts"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  for s in dns workspace site; do
    printf 'locals {\n  a = 1\n}\n' >"$base/repo/$s/main.tf"
  done
  printf '.falconet/\n' >"$base/repo/.gitignore"
  printf '{"stacks":{"plan":["dns"],"validate_only":["workspace","site"]},"prompts":{"implement":"prompts/implement.md"}}\n' \
    >"$base/repo/.github/falconet.json"
  printf 'Implement the request.\n' >"$base/repo/prompts/implement.md"
  caller >"$base/repo/.github/workflows/infra-requests.yml"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  printf '%s' "$base"
}

# responses.json: the four labels and the four secrets exist; anything a
# case wants on top is passed as extra rules, which come FIRST so that they
# win.
script() { # [extra-rule-json ...]
  local extra="" r
  for r in "$@"; do extra="$extra$r,"; done
  printf '[%s
    {"method":"GET","path":"/repos/zetlen/wayfinders-infra/labels","body":[{"name":"infra-request"},{"name":"needs-info"},{"name":"ready-for-human"},{"name":"needs-plan-review"}]},
    {"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets","body":{"total_count":4,"secrets":[{"name":"FALCONET_APP_ID"},{"name":"FALCONET_APP_PRIVATE_KEY"},{"name":"ANTHROPIC_API_KEY"},{"name":"FALCONET_PLAN_ENV"}]}}
  ]\n' "$extra" >"$FAKE_GITHUB/responses.json"
}

d() { # checkout [args...] -> sets OUT ERR RC
  local c="$1"; shift
  : >"$FAKE_GITHUB/requests.log"
  : >"$FAKE_GITHUB/requests.jsonl"
  OUT="$( cd "$c/repo" && "$FALCONET" doctor "$@" 2>"$WORK/err" )"
  RC=$?
  ERR="$(cat "$WORK/err")"
  return 0
}

assert_line() { # haystack exact-line [what]
  if grep -Fxq -- "$2" <<<"$1"; then _pass; else
    _fail "${3:-output} has no line: [$2]" "got: [${1:0:600}]"
  fi
}

assert_no_line_matching() { # haystack regex [what]
  if grep -Eq -- "$2" <<<"$1"; then
    _fail "${3:-output} has a line matching: [$2]" "got: [${1:0:600}]"
  else _pass; fi
}

calls() { awk '{ print $1, $2 }' "$FAKE_GITHUB/requests.log"; }

# --- every step ok ------------------------------------------------------------

c="$(new_checkout ok)"
script
d "$c"

it "a repository installed to the letter is exit 0"
assert_eq 0 "$RC" "exit code"

it "and the summary says so, counting the checks and not the notes"
assert_eq "doctor: 19 ok, 0 missing, 0 cannot tell" "$(tail -1 <<<"$OUT")" "summary"

it "and nothing is MISSING or cannot tell"
assert_no_line_matching "$OUT" '^(MISSING|cannot tell)' "stdout"

it "every line is the status word, the step number and the check, in one column"
assert_eq 0 "$(grep -Ev '^(ok|MISSING|cannot tell|note) {1,11}[1-8]\. |^             [^ ]|^note         the token|^doctor: ' <<<"$OUT" | grep -c .)" \
  "lines outside the format"

it "step 1: each configured stack is a directory with .tf in it, naming the key"
assert_line "$OUT" "ok           1. stack dns (.stacks.plan) is a directory with .tf files"
assert_line "$OUT" "ok           1. stack workspace (.stacks.validate_only) is a directory with .tf files"
assert_line "$OUT" "ok           1. stack site (.stacks.validate_only) is a directory with .tf files"

it "step 1: issues enabled, allowed_actions all"
assert_line "$OUT" "ok           1. the repository has issues enabled"
assert_line "$OUT" "ok           1. allowed_actions is all"

it "step 1: default_workflow_permissions read is a note, never MISSING"
assert_line "$OUT" "note         1. default_workflow_permissions is read (fine: the caller workflow grants what it needs)"

it "step 1: Linux x64 runners is a note, not a check"
assert_contains "$OUT" "note         1. runners must be Linux x64" "stdout"

it "step 2: the handoff directory is ignored"
assert_line "$OUT" "ok           2. .falconet/ is gitignored"

it "steps 3–5: the four secrets exist by name, and the line says a value is never readable"
assert_line "$OUT" "ok           3. secret FALCONET_APP_ID exists (a value can never be read back, so the name is the check)"
assert_line "$OUT" "ok           3. secret FALCONET_APP_PRIVATE_KEY exists (a value can never be read back, so the name is the check)"
assert_line "$OUT" "ok           4. secret ANTHROPIC_API_KEY exists (a value can never be read back, so the name is the check)"
assert_line "$OUT" "ok           5. secret FALCONET_PLAN_ENV exists (a value can never be read back, so the name is the check)"

it "step 6: the four labels, one line each"
assert_line "$OUT" "ok           6. label infra-request"
assert_line "$OUT" "ok           6. label needs-info"
assert_line "$OUT" "ok           6. label ready-for-human"
assert_line "$OUT" "ok           6. label needs-plan-review"

it "step 7: the config parses and the prompt override exists"
assert_line "$OUT" "ok           7. .github/falconet.json parses"
assert_line "$OUT" "ok           7. prompts.implement names prompts/implement.md, which exists"

it "step 8: the caller exists, uses the reusable workflow, grants what the widest job declares"
assert_line "$OUT" "ok           8. .github/workflows/infra-requests.yml exists"
assert_line "$OUT" "ok           8. it uses zetlen/falconet/.github/workflows/falconet.yml@0123abcd"
assert_line "$OUT" "ok           8. permissions grants contents: write, issues: write, pull-requests: write"

it "stderr is silent on a clean run with a token"
assert_eq "" "$ERR" "stderr"

it "doctor reads and never writes: every API call is a GET"
assert_eq "" "$(grep -Ev '^GET ' <<<"$(calls)")" "non-GET calls"

it "and it asked the repository Actions named, for everything step 1–6 needs"
assert_eq "GET /repos/zetlen/wayfinders-infra
GET /repos/zetlen/wayfinders-infra/actions/permissions
GET /repos/zetlen/wayfinders-infra/actions/permissions/workflow
GET /repos/zetlen/wayfinders-infra/actions/secrets
GET /repos/zetlen/wayfinders-infra/labels" "$(calls)" "API calls"

it "the token travels as a bearer header, on every call"
assert_eq "Bearer test-token" "$(jq -r '.headers.authorization' "$FAKE_GITHUB/requests.jsonl" | sort -u)" "Authorization"

it "the list reads ask for a hundred"
assert_eq "per_page=100
per_page=100" "$(jq -r 'select(.path | test("secrets|labels")) | .query' "$FAKE_GITHUB/requests.jsonl")" "query strings"

it "from a subdirectory the report is the same: the verb runs from the root"
OUT2="$( cd "$c/repo/dns" && "$FALCONET" doctor 2>/dev/null )"
assert_eq "$OUT" "$OUT2" "stdout from dns/"

# --- issue #9's second Done-when: remove one label ----------------------------

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/labels","body":[{"name":"infra-request"},{"name":"ready-for-human"},{"name":"needs-plan-review"}]}'
d "$c"

it "one label gone: MISSING for step 6, naming it"
assert_line "$OUT" "MISSING      6. label needs-info"

it "and the next line says how to create it"
assert_line "$OUT" "             create it: gh label create needs-info   (or: falconet init)"

it "and exit 1"
assert_eq 1 "$RC" "exit code"

it "and the other three are still ok: nothing stops at the first finding"
assert_line "$OUT" "ok           6. label infra-request"
assert_line "$OUT" "ok           6. label ready-for-human"
assert_line "$OUT" "ok           6. label needs-plan-review"

it "and the summary counts it"
assert_eq "doctor: 18 ok, 1 missing, 0 cannot tell" "$(tail -1 <<<"$OUT")" "summary"

# --- no token: it degrades to the README, never to nothing --------------------

script
( unset FALCONET_SETUP_TOKEN; d "$c"; printf '%s\n' "$RC" >"$WORK/rc"; printf '%s' "$OUT" >"$WORK/out"; printf '%s' "$ERR" >"$WORK/err2" )
RC="$(cat "$WORK/rc")"; OUT="$(cat "$WORK/out")"; ERR="$(cat "$WORK/err2")"

it "without FALCONET_SETUP_TOKEN every remote check says so"
assert_eq 11 "$(grep -c ' (no FALCONET_SETUP_TOKEN)$' <<<"$OUT")" "cannot-tell lines"
assert_line "$OUT" "cannot tell  3. secret FALCONET_APP_ID (no FALCONET_SETUP_TOKEN)"
assert_line "$OUT" "cannot tell  6. label needs-info (no FALCONET_SETUP_TOKEN)"
assert_line "$OUT" "cannot tell  1. the repository has issues enabled (no FALCONET_SETUP_TOKEN)"

it "and the local checks still run"
assert_line "$OUT" "ok           2. .falconet/ is gitignored"
assert_line "$OUT" "ok           7. .github/falconet.json parses"
assert_line "$OUT" "ok           8. permissions grants contents: write, issues: write, pull-requests: write"

it "and cannot tell is not ok: exit 1, and the summary says which"
assert_eq 1 "$RC" "exit code"
assert_eq "doctor: 9 ok, 0 missing, 11 cannot tell" "$(tail -1 <<<"$OUT")" "summary"

it "and the permission table is on stderr, once, with the seven-day advice"
assert_eq 1 "$(grep -c 'no FALCONET_SETUP_TOKEN in the environment' <<<"$ERR")" "hint count"
assert_contains "$ERR" "Administration   read" "stderr"
assert_contains "$ERR" "seven-day expiry" "stderr"
assert_contains "$ERR" "GITHUB_TOKEN and GH_TOKEN are deliberately" "stderr"

it "and no call reached the API"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

it "GITHUB_TOKEN and GH_TOKEN are not fallbacks"
( unset FALCONET_SETUP_TOKEN; export GITHUB_TOKEN=actions-token GH_TOKEN=gh-token; d "$c"; printf '%s' "$OUT" >"$WORK/out" )
assert_eq 11 "$(grep -c ' (no FALCONET_SETUP_TOKEN)$' "$WORK/out")" "cannot-tell lines"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

# --- step 7: the config ---------------------------------------------------------

c="$(new_checkout badcfg)"
printf '{"stacks": {"plan": ["dns"],}\n' >"$c/repo/.github/falconet.json"
script
d "$c"

it "a malformed config is MISSING for step 7, quoting the parse error"
assert_contains "$OUT" 'MISSING      7. the config does not parse: ".github/falconet.json is not valid JSON: ' "stdout"

it "and then everything the config names is cannot tell, not a silent fall back to defaults"
assert_line "$OUT" "cannot tell  1. the configured stacks (the config did not parse)"
assert_line "$OUT" "cannot tell  2. the handoff directory is gitignored (the config did not parse)"
assert_line "$OUT" "cannot tell  6. the four labels (the config did not parse)"
assert_line "$OUT" "cannot tell  7. the prompt overrides (the config did not parse)"

it "while the checks that do not need it still run"
assert_line "$OUT" "ok           1. the repository has issues enabled"
assert_line "$OUT" "ok           3. secret FALCONET_APP_ID exists (a value can never be read back, so the name is the check)"
assert_line "$OUT" "ok           8. permissions grants contents: write, issues: write, pull-requests: write"

it "and exit 1"
assert_eq 1 "$RC" "exit code"

c="$(new_checkout nocfg)"
rm "$c/repo/.github/falconet.json"
script
d "$c"

it "no config file is ok for step 7: the defaults stand alone"
assert_contains "$OUT" "ok           7. no .github/falconet.json, so the defaults stand alone" "stdout"

it "and the default stacks are what is checked"
assert_line "$OUT" "ok           1. stack dns (.stacks.plan) is a directory with .tf files"
assert_eq 0 "$RC" "exit code"

c="$(new_checkout prompt)"
printf '{"stacks":{"plan":["dns"],"validate_only":[]},"prompts":{"implement":"prompts/mine.md","park_needs_info":"prompts/park.md"}}\n' \
  >"$c/repo/.github/falconet.json"
script
d "$c"

it "a prompt override naming a missing file is MISSING"
assert_line "$OUT" "MISSING      7. prompts.implement names prompts/mine.md, which does not exist"

it "and a prompts key falconet does not read is a note"
assert_line "$OUT" "note         7. prompts.park_needs_info is not a prompt falconet reads (the two are implement and pause_needs_info)"

it "--config names another file, relative to the repository root as every verb's is"
printf '{"stacks":{"plan":["dns"],"validate_only":["site"]}}\n' >"$c/alt.json"
OUT="$( cd "$c/repo/dns" && "$FALCONET" doctor --config ../alt.json 2>/dev/null )"
assert_line "$OUT" "ok           7. ../alt.json parses"
assert_line "$OUT" "ok           1. stack site (.stacks.validate_only) is a directory with .tf files"
assert_no_line_matching "$OUT" 'stack workspace' "stdout"

# --- step 1: the stacks ---------------------------------------------------------

c="$(new_checkout stacks)"
rm "$c/repo/site/main.tf"; printf 'README\n' >"$c/repo/site/README.md"
printf '{"stacks":{"plan":["dns","nope"],"validate_only":["site"]}}\n' >"$c/repo/.github/falconet.json"
script
d "$c"

it "a stack directory with no .tf in it is MISSING"
assert_line "$OUT" "MISSING      1. stack site (.stacks.validate_only) has no .tf files"

it "a configured stack that is not a directory is MISSING, naming the key"
assert_line "$OUT" "MISSING      1. stack nope (.stacks.plan) is not a directory"
assert_line "$OUT" "             set .stacks.plan in .github/falconet.json to the directories your OpenTofu stacks live in"

it "and the one that is fine is still ok"
assert_line "$OUT" "ok           1. stack dns (.stacks.plan) is a directory with .tf files"
assert_eq 1 "$RC" "exit code"

c="$(new_checkout noplan)"
printf '{"stacks":{"plan":[],"validate_only":["workspace","site"]}}\n' >"$c/repo/.github/falconet.json"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets","body":{"total_count":3,"secrets":[{"name":"FALCONET_APP_ID"},{"name":"FALCONET_APP_PRIVATE_KEY"},{"name":"ANTHROPIC_API_KEY"}]}}'
d "$c"

it "FALCONET_PLAN_ENV absent with no planned stacks is a note, not MISSING"
assert_line "$OUT" "note         5. secret FALCONET_PLAN_ENV is not set (no planned stacks, so no planning environment is needed)"
assert_eq 0 "$RC" "exit code"

c="$(new_checkout nosecret)"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets","body":{"total_count":3,"secrets":[{"name":"FALCONET_APP_ID"},{"name":"FALCONET_APP_PRIVATE_KEY"},{"name":"ANTHROPIC_API_KEY"}]}}'
d "$c"

it "but with a planned stack it is MISSING, with the command"
assert_line "$OUT" "MISSING      5. secret FALCONET_PLAN_ENV"
assert_line "$OUT" "             store it: gh secret set FALCONET_PLAN_ENV   (or: falconet init)"
assert_eq 1 "$RC" "exit code"

# --- step 2: the handoff directory ----------------------------------------------

c="$(new_checkout unignored)"
: >"$c/repo/.gitignore"
script
d "$c"

it "a handoff directory that is not ignored is MISSING, with the line to add"
assert_line "$OUT" "MISSING      2. .falconet/ is not gitignored"
assert_line "$OUT" "             printf '.falconet/\\n' >> .gitignore   (or: falconet init)"
assert_eq 1 "$RC" "exit code"

it "and a configured handoff_dir is what is checked"
printf '{"handoff_dir":"scratch"}\n' >"$c/repo/.github/falconet.json"
printf 'scratch/\n' >"$c/repo/.gitignore"
d "$c"
assert_line "$OUT" "ok           2. scratch/ is gitignored"

# --- step 1: the Actions policy --------------------------------------------------

c="$(new_checkout selected)"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/permissions","body":{"enabled":true,"allowed_actions":"selected"}}' \
  '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/permissions/selected-actions","body":{"github_owned_allowed":true,"verified_allowed":false,"patterns_allowed":["zetlen/*","opentofu/setup-opentofu@*","anthropics/claude-code-action"]}}'
d "$c"

it "allowed_actions selected with the patterns is ok"
assert_line "$OUT" "ok           1. allowed_actions is selected, covering zetlen/falconet, actions/*, opentofu/setup-opentofu and anthropics/claude-code-action"
assert_eq 0 "$RC" "exit code"

it "and the selected-actions list was read only because the policy is selected"
assert_contains "$(calls)" "GET /repos/zetlen/wayfinders-infra/actions/permissions/selected-actions" "API calls"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/permissions","body":{"enabled":true,"allowed_actions":"selected"}}' \
  '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/permissions/selected-actions","body":{"github_owned_allowed":true,"verified_allowed":false,"patterns_allowed":["zetlen/falconet"]}}'
d "$c"

it "selected without the patterns is MISSING, naming which of the four is not covered"
assert_line "$OUT" "MISSING      1. allowed_actions is selected and does not cover opentofu/setup-opentofu, anthropics/claude-code-action"
assert_eq 1 "$RC" "exit code"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/permissions","body":{"enabled":true,"allowed_actions":"local_only"}}'
d "$c"

it "local_only is MISSING: nothing from outside the repository can run"
assert_line "$OUT" "MISSING      1. allowed_actions is local_only: workflows from outside the repository cannot run"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra","body":{"name":"wayfinders-infra","full_name":"zetlen/wayfinders-infra","owner":{"login":"zetlen"},"private":true,"visibility":"private","has_issues":false,"default_branch":"main"}}'
d "$c"

it "issues disabled is MISSING"
assert_line "$OUT" "MISSING      1. the repository has issues disabled"

# --- step 8: the caller workflow -------------------------------------------------

c="$(new_checkout caller)"
caller read >"$c/repo/.github/workflows/infra-requests.yml"
script
d "$c"

it "a caller granting contents: read is MISSING, naming the permission"
assert_line "$OUT" "MISSING      8. permissions grants contents: read, and falconet's widest job declares contents: write, issues: write, pull-requests: write"
assert_contains "$OUT" "startup_failure" "stdout"
assert_eq 1 "$RC" "exit code"

sed -e 's/@0123abcd/@main/' \
  "$WORK/ok/repo/.github/workflows/infra-requests.yml" >"$c/repo/.github/workflows/infra-requests.yml"
d "$c"

it "@main is a note, not MISSING: the first canary has to run from somewhere"
assert_line "$OUT" "ok           8. it uses zetlen/falconet/.github/workflows/falconet.yml@main"
assert_line "$OUT" "note         8. the ref is main: unpinned — pin a SHA or tag once a canary has reached a pull request"
assert_eq 0 "$RC" "exit code"

# falconet-ref was an input until #19 chose which falconet the jobs checked
# out; the checkout is gone and so is the input, and a reusable workflow
# rejects an input it does not declare when the caller's file is LOADED —
# the silent startup_failure the README's troubleshooting opens with.
awk '{ print } /^      issue: /{ print "      falconet-ref: 0123abcd" }' \
  "$WORK/ok/repo/.github/workflows/infra-requests.yml" >"$c/repo/.github/workflows/infra-requests.yml"
d "$c"

it "a caller still passing falconet-ref is MISSING, because the workflow would not even load"
assert_line "$OUT" "MISSING      8. falconet-ref is no longer an input; remove it"
assert_contains "$OUT" "startup_failure" "stdout"

it "and that alone makes the report red, in step with uses: or not"
assert_eq 1 "$RC" "exit code"

rm "$c/repo/.github/workflows/infra-requests.yml"
d "$c"

it "no caller workflow at all is MISSING"
assert_line "$OUT" "MISSING      8. .github/workflows/infra-requests.yml"
assert_eq 1 "$RC" "exit code"

# --- which repository -------------------------------------------------------------

c="$(new_checkout which)"
script
d "$c" --repo other/place

it "--repo beats GITHUB_REPOSITORY"
assert_contains "$(calls)" "GET /repos/other/place" "API calls"
assert_not_contains "$(calls)" "wayfinders-infra" "API calls"

git -C "$c/repo" remote add origin https://github.com/someone/elsewhere.git
( unset GITHUB_REPOSITORY; d "$c"; cp "$FAKE_GITHUB/requests.log" "$WORK/which.log" )

it "with no GITHUB_REPOSITORY the origin remote on github.com is the repository"
assert_contains "$(awk '{ print $1, $2 }' "$WORK/which.log")" "GET /repos/someone/elsewhere/labels" "API calls"

git -C "$c/repo" remote set-url origin https://gitlab.com/someone/elsewhere.git
( unset GITHUB_REPOSITORY; d "$c"; printf '%s\n' "$RC" >"$WORK/rc"; printf '%s' "$OUT" >"$WORK/out"; printf '%s' "$ERR" >"$WORK/err2" )

it "neither is exit 1 with a message naming both fixes, and nothing on stdout"
assert_eq 1 "$(cat "$WORK/rc")" "exit code"
assert_eq "" "$(cat "$WORK/out")" "stdout"
assert_contains "$(cat "$WORK/err2")" "set GITHUB_REPOSITORY=owner/name, or run from a clone whose origin is on github.com" "stderr"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

it "a --repo that is not owner/name is a usage error"
d "$c" --repo nope
assert_eq 2 "$RC" "exit code"

# --- refusals: every probe is its own check ---------------------------------------

c="$(new_checkout refused)"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets","status":403,"body":{"message":"Resource not accessible by personal access token"}}'
d "$c"

it "a 403 on the secrets is cannot tell on each secret, naming the permission the endpoint needs"
assert_line "$OUT" "cannot tell  3. secret FALCONET_APP_ID (403 Resource not accessible by personal access token — needs Secrets: read)"
assert_line "$OUT" "cannot tell  5. secret FALCONET_PLAN_ENV (403 Resource not accessible by personal access token — needs Secrets: read)"

it "and the labels were still checked: doctor never stops at the first refusal"
assert_line "$OUT" "ok           6. label needs-info"
assert_contains "$(calls)" "GET /repos/zetlen/wayfinders-infra/labels" "API calls"
assert_eq 1 "$RC" "exit code"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra","status":404,"body":{"message":"Not Found"}}' \
  '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/labels","status":403,"body":{"message":"Resource not accessible by personal access token"}}'
d "$c"

it "a 404 on the repository says not found or no access, as ADR-0005 reads it"
assert_line "$OUT" "cannot tell  1. the repository has issues enabled (404 not found, or no access — needs Metadata: read)"

it "a 403 on the labels names Issues: read, one line per label"
assert_eq 4 "$(grep -c ' — needs Issues: read)$' <<<"$OUT")" "label lines"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra","headers":{"X-OAuth-Scopes":"gist"}}'
d "$c"

it "a classic token without repo gets one note, first, with the scopes it has"
assert_eq "note         the token is classic and its scopes (gist) do not include repo, which a classic token needs" "$(head -1 <<<"$OUT")" "first line"
assert_eq 1 "$(grep -c 'the token is classic' <<<"$OUT")" "note count"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra","headers":{"X-OAuth-Scopes":"repo, gist"}}'
d "$c"

it "and a classic token with repo gets none"
assert_not_contains "$OUT" "the token is classic" "stdout"

script
( export GITHUB_API_URL=http://127.0.0.1:1; d "$c"; printf '%s\n' "$RC" >"$WORK/rc"; printf '%s' "$OUT" >"$WORK/out"; printf '%s' "$ERR" >"$WORK/err2" )

it "an unreachable GITHUB_API_URL is cannot tell on every remote check, not a crash"
assert_eq 11 "$(grep -c ' (GITHUB_API_URL unreachable)$' "$WORK/out")" "cannot-tell lines"
assert_eq 1 "$(cat "$WORK/rc")" "exit code"

it "and one line on stderr"
assert_eq 1 "$(grep -c . "$WORK/err2")" "stderr lines"

# --- what the first review of this verb found ------------------------------------

c="$(new_checkout outside)"
printf '{"handoff_dir":"/tmp/falconet-elsewhere","stacks":{"plan":["dns"],"validate_only":["workspace","site"]}}\n' \
  >"$c/repo/.github/falconet.json"
script
d "$c"
it "an absolute handoff_dir outside the tree has nothing to gitignore: a note, not a failure"
assert_line "$OUT" "note         2. /tmp/falconet-elsewhere is outside the repository, so nothing needs ignoring"
assert_no_line_matching "$OUT" '^cannot tell  2\.' "stdout"

c="$(new_checkout emptyoverride)"
printf '{"prompts":{"implement":"","pause_needs_info":null},"stacks":{"plan":["dns"],"validate_only":["workspace","site"]}}\n' \
  >"$c/repo/.github/falconet.json"
script
d "$c"
it "an empty or null prompts override is no override, as prompt treats it: nothing to report"
assert_no_line_matching "$OUT" '^MISSING      7\. prompts' "stdout"
assert_eq 0 "$RC" "exit code"

it "--repo with a byte GitHub would not accept is a usage error, not a request for another repository"
( cd "$c/repo" && "$FALCONET" doctor --repo 'zetlen/falconet?x' >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

c="$(new_checkout atremote)"
git -C "$c/repo" remote set-url origin 'github.com:zetlen/falconet@v1'
it "an origin with an '@' after the colon is refused in words, never a crash"
OUT="$( cd "$c/repo" && env -u GITHUB_REPOSITORY "$FALCONET" doctor 2>"$WORK/err" )"; rc=$?
assert_eq 1 "$rc" "exit code"
assert_contains "$(cat "$WORK/err")" "set GITHUB_REPOSITORY=owner/name" "stderr"

# --- usage ------------------------------------------------------------------------

it "-h is a usage error, with nothing on stdout"
d "$c" -h
assert_eq 2 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$ERR" "doctor — check this repository" "stderr"

it "an unknown flag is a usage error"
d "$c" --bogus
assert_eq 2 "$RC" "exit code"
assert_contains "$ERR" "unknown argument: --bogus" "stderr"

it "a flag without its value is a usage error"
d "$c" --config
assert_eq 2 "$RC" "exit code"

it "a usage error makes no API call"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

summary
