#!/usr/bin/env bash
#
# init.test.sh — README "Install it in your repository" steps 2–8, done by
# the second setup verb: the local files committed and never pushed (#10),
# the labels first and the secrets in sealed boxes (#11).
#
# init has no bash predecessor (ADR-0006 D3 step 1), so nothing here is a
# ported assertion: each README step is the specification of a write, the
# two issues fix the order (every read before any write; the first write is
# the labels, the idempotent one) and the Done-whens (a fresh clone gets one
# commit; a second run makes none; a token short of Secrets: write fails
# after the labels and before any secret), and doctor's format fixes the
# shape. GitHub is tests/fixtures/fake-github.py on loopback; a secret the
# fake is handed is a sealed box it cannot open, so the request's shape is
# the check — key_id, and a base64 encrypted_value of 48 + len bytes — and
# the plaintext's absence from every log is the other.
#
# stdin is never a terminal here, so every prompt path is exercised through
# flags and stdin: the API key is piped, the plan env is a file, the stacks
# are flags.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_DATE='2026-08-22T12:00:00Z' GIT_COMMITTER_DATE='2026-08-22T12:00:00Z'

# --- the fake API, and no gh ------------------------------------------------

fake_github
export FALCONET_SETUP_TOKEN=test-token
export GITHUB_REPOSITORY=zetlen/wayfinders-infra
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
all_labels='{"method":"GET","path":"/repos/zetlen/wayfinders-infra/labels","body":[{"name":"infra-request"},{"name":"needs-info"},{"name":"ready-for-human"},{"name":"needs-plan-review"}]}'
all_secrets='{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets","body":{"total_count":4,"secrets":[{"name":"FALCONET_APP_ID"},{"name":"FALCONET_APP_PRIVATE_KEY"},{"name":"ANTHROPIC_API_KEY"},{"name":"FALCONET_PLAN_ENV"}]}}'

: >"$WORK/empty"
printf 'sk-ant-PLAINTEXT-KEY-MARKER' >"$WORK/key"

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

# The sealed value's length in bytes, from the PUT for one secret: base64 is
# decoded by python3, since jq's length counts code points of what it takes
# for text and a sealed box is not text.
sealed_len() { # secret-name
  jq -r "select(.method == \"PUT\" and (.path | endswith(\"/$1\"))) | .body.encrypted_value" "$FAKE_GITHUB/requests.jsonl" \
    | python3 -c 'import base64, sys; print(len(base64.b64decode(sys.stdin.read().strip())))'
}
head_of() { git -C "$1/repo" rev-parse HEAD; }
committed_files() { git -C "$1/repo" show --format= --name-only HEAD | sort; }

# The ref the workflow must carry: the binary's own version, and main for a
# dev build (the one the suite builds).
ver="$("$FALCONET" version | awk '{ print $2 }')"
ref="$ver"; [[ "$ver" == dev ]] && ref=main
reusable="zetlen/falconet/.github/workflows/falconet.yml"

# --- issue #10's Done-when: a fresh clone, no token, one commit ----------------

c="$(new_checkout fresh)"
before="$(head_of "$c")"
( unset FALCONET_SETUP_TOKEN; i "$c" "$WORK/empty" --plan dns --validate-only workspace
  printf '%s\n' "$RC" >"$WORK/rc"; printf '%s' "$OUT" >"$WORK/out"; printf '%s' "$ERR" >"$WORK/err2" )
RC="$(cat "$WORK/rc")"; OUT="$(cat "$WORK/out")"; ERR="$(cat "$WORK/err2")"

it "with no token, a fresh clone is exit 0"
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

it "the remote checks say cannot tell once, and every remote step is skipped"
assert_eq 1 "$(grep -c ' (no FALCONET_SETUP_TOKEN)$' <<<"$(grep '^cannot tell' <<<"$OUT")")" "cannot-tell lines"
assert_line "$OUT" "skipped      6. the four labels (no FALCONET_SETUP_TOKEN)"
assert_line "$OUT" "skipped      4. secret ANTHROPIC_API_KEY (no FALCONET_SETUP_TOKEN)"
assert_line "$OUT" "skipped      5. secret FALCONET_PLAN_ENV (no FALCONET_SETUP_TOKEN)"
assert_line "$OUT" "skipped      3. secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (no FALCONET_SETUP_TOKEN)"

it "and the summary counts them"
assert_line "$OUT" "init: 1 ok, 5 done, 4 skipped, 0 missing, 1 cannot tell"

it "Left for you: the push first, never run"
assert_eq "  1. git push origin main" "$(grep -A1 '^Left for you:' <<<"$OUT" | tail -1)" "first item"

it "then the App, the key, the plan env, the labels, the prompt's standing facts, the canary, and doctor — in that order"
assert_eq "3 4 5 6 7 9 then" "$(sed -n '/^Left for you:/,$p' <<<"$OUT" | grep -E '^  [2-9]\. ' | sed -E 's/^  [0-9]+\. step ([0-9]) .*/\1/; s/^  [0-9]+\. then: .*/then/' | tr '\n' ' ' | sed 's/ $//')" "item order"
assert_contains "$OUT" "step 7 — edit the standing-facts block in prompts/implement.md" "stdout"
assert_contains "$OUT" "step 9 — file a canary issue" "stdout"
assert_contains "$OUT" "step 3 — the GitHub App" "stdout"
assert_contains "$OUT" "falconet init --app-id <App ID> --app-key <the .pem>" "stdout"
assert_eq "  8. then: falconet doctor" "$(tail -1 <<<"$OUT")" "last line"

it "the permission table is on stderr, once, with init's column"
assert_eq 1 "$(grep -c 'no FALCONET_SETUP_TOKEN in the environment' <<<"$ERR")" "hint count"
assert_contains "$ERR" "Secrets          read     write" "stderr"

it "and no call reached the API"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

# --- doctor afterwards ----------------------------------------------------------

script "$all_labels" "$all_secrets"
DOUT="$( cd "$c/repo" && "$FALCONET" doctor 2>/dev/null )"; DRC=$?

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

it "and, with the labels and secrets scripted as present, every check is ok"
assert_eq 0 "$DRC" "doctor's exit code"

# --- issue #10's third Done-when: a second init makes no commit ------------------

script
after="$(head_of "$c")"
( unset FALCONET_SETUP_TOKEN; i "$c" "$WORK/empty" --plan dns --validate-only workspace
  printf '%s\n' "$RC" >"$WORK/rc"; printf '%s' "$OUT" >"$WORK/out" )
RC="$(cat "$WORK/rc")"; OUT="$(cat "$WORK/out")"

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
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "a dirty tree is refused, exit 1, before anything else"
assert_eq 1 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"

it "naming the paths on stderr"
assert_contains "$ERR" " M dns/main.tf" "stderr"

it "before any call: with a token in the environment, requests.log is empty"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

it "and before any file"
assert_file_missing "$c/repo/.gitignore"
assert_file_missing "$c/repo/.github"
assert_eq "$before" "$(head_of "$c")" "HEAD"

git -C "$c/repo" checkout -q -- dns/main.tf
printf 'stray\n' >"$c/repo/stray.txt"
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "an untracked file counts as dirt, as it does for prepare"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "?? stray.txt" "stderr"

# --- the full run with a token: issue #11 ------------------------------------------

c="$(new_checkout full)"
script
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "with a token, a fresh repository is exit 0"
assert_eq 0 "$RC" "exit code"

it "every read happens before any write, and the labels are the first write"
assert_eq "GET /repos/zetlen/wayfinders-infra
GET /repos/zetlen/wayfinders-infra/actions/permissions
GET /repos/zetlen/wayfinders-infra/actions/permissions/workflow
GET /repos/zetlen/wayfinders-infra/actions/secrets
GET /repos/zetlen/wayfinders-infra/labels
POST /repos/zetlen/wayfinders-infra/labels
POST /repos/zetlen/wayfinders-infra/labels
POST /repos/zetlen/wayfinders-infra/labels
POST /repos/zetlen/wayfinders-infra/labels
GET /repos/zetlen/wayfinders-infra/actions/secrets/public-key
PUT /repos/zetlen/wayfinders-infra/actions/secrets/ANTHROPIC_API_KEY" "$(calls)" "API calls"

it "four POST …/labels when none exist, each with a name, a colour and a description"
assert_contains "$(cat "$FAKE_GITHUB/requests.log")" 'POST /repos/zetlen/wayfinders-infra/labels {"color":"1d76db","description":"Queued for falconet: a person applies this to request a change","name":"infra-request"}' "requests.log"
assert_eq "infra-request
needs-info
ready-for-human
needs-plan-review" "$(jq -r 'select(.method == "POST") | .body.name' "$FAKE_GITHUB/requests.jsonl")" "labels created, in README order"
assert_eq 4 "$(jq -r 'select(.method == "POST") | .body.color' "$FAKE_GITHUB/requests.jsonl" | grep -cE '^[0-9a-f]{6}$')" "colours"

it "and the report says so"
assert_line "$OUT" "done         6. label infra-request created"
assert_line "$OUT" "done         6. label needs-info created"
assert_line "$OUT" "done         6. label ready-for-human created"
assert_line "$OUT" "done         6. label needs-plan-review created"

it "the public key is read, then ANTHROPIC_API_KEY is PUT with the key's id"
assert_eq "568250167242549743" "$(jq -r 'select(.method == "PUT") | .body.key_id' "$FAKE_GITHUB/requests.jsonl")" "key_id"

it "and an encrypted_value that is base64 of 48 + len(plaintext) bytes"
# The key piped on stdin is 27 bytes; a sealed box is 32 (ephemeral public
# key) + 16 (MAC) + the plaintext.
assert_eq 75 "$(sealed_len ANTHROPIC_API_KEY)" "sealed length"
assert_eq 1 "$(jq -r 'select(.method == "PUT") | .body | keys | join(",")' "$FAKE_GITHUB/requests.jsonl" | grep -c '^encrypted_value,key_id$')" "body keys"

it "and the plaintext appears in no log, and on neither stream"
assert_not_contains "$(cat "$FAKE_GITHUB/requests.log" "$FAKE_GITHUB/requests.jsonl")" "PLAINTEXT-KEY-MARKER" "requests"
assert_not_contains "$OUT$ERR" "PLAINTEXT-KEY-MARKER" "stdout+stderr"

it "the report: stored, sealed to the key"
assert_line "$OUT" "done         4. secret ANTHROPIC_API_KEY stored (sealed to key 568250167242549743)"

it "doctor's step-1 reads are reported in doctor's words"
assert_line "$OUT" "ok           1. the repository has issues enabled"
assert_line "$OUT" "ok           1. allowed_actions is all"
assert_line "$OUT" "note         1. default_workflow_permissions is read (fine: the caller workflow grants what it needs)"

it "the plan env without --plan-env-file is skipped, and the App without its flags"
assert_line "$OUT" "skipped      5. secret FALCONET_PLAN_ENV (no --plan-env-file)"
assert_line "$OUT" "skipped      3. secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (no --app-id and --app-key; #12 will create the App by manifest)"

it "and the local files and the commit still happen"
assert_eq "Install falconet" "$(git -C "$c/repo" log -1 --format=%s)" "subject"
assert_line "$OUT" "init: 3 ok, 10 done, 2 skipped, 0 missing, 0 cannot tell"

it "stderr is silent on a clean run with a token"
assert_eq "" "$ERR" "stderr"

it "the token travels as a bearer header on every call"
assert_eq "Bearer test-token" "$(jq -r '.headers.authorization' "$FAKE_GITHUB/requests.jsonl" | sort -u)" "Authorization"

# --- labels: idempotent ----------------------------------------------------------------

c="$(new_checkout labels)"
script "$all_labels"
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "with all four labels existing, no POST"
assert_eq 0 "$(grep -c '^POST' "$FAKE_GITHUB/requests.log")" "POSTs"
assert_line "$OUT" "ok           6. label infra-request exists"
assert_line "$OUT" "ok           6. label needs-plan-review exists"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/labels","body":[{"name":"infra-request"},{"name":"ready-for-human"},{"name":"needs-plan-review"}]}'
( cd "$c/repo" && git reset -q --hard HEAD~1 )
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "with three existing, one POST, naming the fourth"
assert_eq 1 "$(grep -c '^POST' "$FAKE_GITHUB/requests.log")" "POSTs"
assert_eq "needs-info" "$(jq -r 'select(.method == "POST") | .body.name' "$FAKE_GITHUB/requests.jsonl")" "created"
assert_line "$OUT" "done         6. label needs-info created"
assert_line "$OUT" "ok           6. label infra-request exists"

it "an empty stdin is a skipped key, with step 4 left for you"
assert_line "$OUT" "skipped      4. secret ANTHROPIC_API_KEY (nothing on stdin)"
assert_contains "$OUT" "step 4 — store the Anthropic API key" "stdout"
assert_eq 0 "$(grep -c '^PUT' "$FAKE_GITHUB/requests.log")" "PUTs"

it "a configured label name is what is created"
( cd "$c/repo" && git reset -q --hard HEAD~1 )
mkdir -p "$c/repo/.github"
printf '{"stacks":{"plan":["dns"],"validate_only":["workspace"]},"labels":{"needs_info":"more-info-please"}}\n' >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A && git -C "$c/repo" commit -qm "config"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/labels","body":[{"name":"infra-request"},{"name":"ready-for-human"},{"name":"needs-plan-review"}]}'
i "$c" "$WORK/empty"
assert_eq "more-info-please" "$(jq -r 'select(.method == "POST") | .body.name' "$FAKE_GITHUB/requests.jsonl")" "created"

# --- the plan env ---------------------------------------------------------------------

c="$(new_checkout planenv)"
printf '{"AWS_ACCESS_KEY_ID": "AKIA-PLAINTEXT-ENV-MARKER", "TF_VAR_zone": "example.com"}\n' >"$c/planenv.json"
script
i "$c" "$WORK/empty" --plan dns --validate-only workspace --plan-env-file "$c/planenv.json"

it "--plan-env-file with a valid object is PUT as FALCONET_PLAN_ENV"
assert_eq 0 "$RC" "exit code"
assert_contains "$(calls)" "PUT /repos/zetlen/wayfinders-infra/actions/secrets/FALCONET_PLAN_ENV" "API calls"
assert_line "$OUT" "done         5. secret FALCONET_PLAN_ENV stored (sealed to key 568250167242549743)"

it "sealed over the file's bytes exactly"
assert_eq "$(( 48 + $(wc -c <"$c/planenv.json") ))" "$(sealed_len FALCONET_PLAN_ENV)" "sealed length"

it "and the plaintext appears nowhere"
assert_not_contains "$(cat "$FAKE_GITHUB/requests.log" "$FAKE_GITHUB/requests.jsonl")" "PLAINTEXT-ENV-MARKER" "requests"
assert_not_contains "$OUT$ERR" "PLAINTEXT-ENV-MARKER" "stdout+stderr"

for bad in '["AWS_KEY=PLAINTEXT-ENV-MARKER"]' '{"AWS_KEY": 12345, "X": "PLAINTEXT-ENV-MARKER"}' '{"bad-key": "PLAINTEXT-ENV-MARKER"}' '{"A": {"B": "PLAINTEXT-ENV-MARKER"}}' '{"A": "PLAINTEXT-ENV-MARKER" oops}'; do
  c="$(new_checkout "bad-$(printf '%s' "$bad" | cksum | awk '{ print $1 }')")"
  printf '%s\n' "$bad" >"$c/planenv.json"
  before="$(head_of "$c")"
  i "$c" "$WORK/key" --plan dns --validate-only workspace --plan-env-file "$c/planenv.json"

  it "an invalid plan env ($bad) is exit 1"
  assert_eq 1 "$RC" "exit code"

  it "naming only the shape on stderr, with the plaintext absent from stdout, stderr and requests.log"
  assert_contains "$ERR" "init: validation: FALCONET_PLAN_ENV" "stderr"
  assert_not_contains "$OUT$ERR$(cat "$FAKE_GITHUB/requests.log" "$FAKE_GITHUB/requests.jsonl")" "PLAINTEXT-ENV-MARKER" "everything"

  it "and refused before any write: no call, no file, no commit"
  assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"
  assert_file_missing "$c/repo/.gitignore"
  assert_eq "$before" "$(head_of "$c")" "HEAD"
done

it "the error names the key for a bad key, and the shape for a bad value"
c="$(new_checkout badkey)"; printf '{"bad-key": "x"}\n' >"$c/planenv.json"
i "$c" "$WORK/key" --plan-env-file "$c/planenv.json" --plan dns --validate-only workspace
assert_contains "$ERR" 'key "bad-key" is not an environment-variable name' "stderr"
printf '{"A": 1}\n' >"$c/planenv.json"
i "$c" "$WORK/key" --plan-env-file "$c/planenv.json" --plan dns --validate-only workspace
assert_contains "$ERR" 'value of A is a number, and every value must be a string' "stderr"

it "an empty object is valid: no credentials is a statement"
printf '{}\n' >"$c/planenv.json"
i "$c" "$WORK/key" --plan-env-file "$c/planenv.json" --plan dns --validate-only workspace
assert_eq 0 "$RC" "exit code"
assert_contains "$(calls)" "PUT /repos/zetlen/wayfinders-infra/actions/secrets/FALCONET_PLAN_ENV" "API calls"

it "with no planned stacks and no file, FALCONET_PLAN_ENV is a note, as doctor says"
c="$(new_checkout noplan)"
i "$c" "$WORK/empty" --validate-only dns,workspace
assert_line "$OUT" "note         5. secret FALCONET_PLAN_ENV is not set (no planned stacks, so no planning environment is needed)"
assert_not_contains "$OUT" "step 5 —" "stdout"

# --- an existing secret ----------------------------------------------------------------

c="$(new_checkout existing)"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets","body":{"total_count":1,"secrets":[{"name":"ANTHROPIC_API_KEY"}]}}'
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "an existing secret is not replaced, and no PUT is made"
assert_line "$OUT" "ok           4. secret ANTHROPIC_API_KEY exists (not replaced; --replace-secrets would)"
assert_eq 0 "$(grep -c '^PUT' "$FAKE_GITHUB/requests.log")" "PUTs"
assert_eq 0 "$(grep -c 'public-key' "$FAKE_GITHUB/requests.log")" "public-key reads: nothing to seal, nothing fetched"

( cd "$c/repo" && git reset -q --hard HEAD~1 )
i "$c" "$WORK/key" --plan dns --validate-only workspace --replace-secrets

it "--replace-secrets seals a new value over it"
assert_line "$OUT" "done         4. secret ANTHROPIC_API_KEY replaced (sealed to key 568250167242549743)"
assert_contains "$(calls)" "PUT /repos/zetlen/wayfinders-infra/actions/secrets/ANTHROPIC_API_KEY" "API calls"

# --- a token short of Secrets: write, after the labels and before any secret -----------

c="$(new_checkout refused)"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets/public-key","status":403,"body":{"message":"Resource not accessible by personal access token"}}'
before="$(head_of "$c")"
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "a 403 on the public key is exit 1"
assert_eq 1 "$RC" "exit code"

it "after the four label POSTs"
assert_eq 4 "$(grep -c '^POST /repos/zetlen/wayfinders-infra/labels' "$FAKE_GITHUB/requests.log")" "label POSTs"
assert_line "$OUT" "done         6. label needs-plan-review created"

it "before any PUT"
assert_eq 0 "$(grep -c '^PUT' "$FAKE_GITHUB/requests.log")" "PUTs"

it "and before any local file"
assert_file_missing "$c/repo/.gitignore"
assert_file_missing "$c/repo/.github"
assert_eq "$before" "$(head_of "$c")" "HEAD"
assert_eq "" "$(git -C "$c/repo" status --porcelain)" "status"

it "naming the permission"
assert_contains "$ERR" "403" "stderr"
assert_contains "$ERR" "the token needs Secrets: write" "stderr"

script '{"method":"PUT","path":"/repos/zetlen/wayfinders-infra/actions/secrets/ANTHROPIC_API_KEY","status":403,"body":{"message":"Resource not accessible by personal access token"}}'
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "a 403 on the PUT is the same refusal"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "could not store secret ANTHROPIC_API_KEY" "stderr"
assert_contains "$ERR" "the token needs Secrets: write" "stderr"
assert_file_missing "$c/repo/.gitignore"

script '{"method":"POST","path":"/repos/zetlen/wayfinders-infra/labels","status":403,"body":{"message":"Resource not accessible by personal access token"}}'
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "a 403 on a label names Issues: write, and stops before anything harder to undo"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "could not create label infra-request" "stderr"
assert_contains "$ERR" "the token needs Issues: write" "stderr"
assert_eq 1 "$(grep -c '^POST' "$FAKE_GITHUB/requests.log")" "POSTs"
assert_eq 0 "$(grep -c '^PUT\|public-key' "$FAKE_GITHUB/requests.log")" "secret calls"
assert_file_missing "$c/repo/.gitignore"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra","status":404,"body":{"message":"Not Found"}}'
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "a 404 on the repository is not found, or no access: exit 1 before any write"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "not found, or no access" "stderr"
assert_eq "GET /repos/zetlen/wayfinders-infra" "$(calls)" "API calls"
assert_file_missing "$c/repo/.gitignore"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra","body":{"name":"wayfinders-infra","full_name":"zetlen/wayfinders-infra","owner":{"login":"zetlen"},"private":true,"visibility":"private","has_issues":false,"default_branch":"main"}}' \
  '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/permissions","body":{"enabled":true,"allowed_actions":"local_only"}}'
i "$c" "$WORK/key" --plan dns --validate-only workspace

it "issues disabled and a local_only policy are MISSING, reported and left for you, and do not stop the run"
assert_eq 0 "$RC" "exit code"
assert_line "$OUT" "MISSING      1. the repository has issues disabled"
assert_line "$OUT" "MISSING      1. allowed_actions is local_only: workflows from outside the repository cannot run"
assert_contains "$OUT" "step 1 — the repository has issues disabled: enable them: Settings → General → Features → Issues" "stdout"
assert_contains "$OUT" "step 1 — allowed_actions is local_only" "stdout"
assert_eq "Install falconet" "$(git -C "$c/repo" log -1 --format=%s)" "subject"

( export GITHUB_API_URL=http://127.0.0.1:1; i "$c" "$WORK/key" --plan dns --validate-only workspace; printf '%s\n' "$RC" >"$WORK/rc"; printf '%s' "$ERR" >"$WORK/err2" )

it "an unreachable GITHUB_API_URL with a token is exit 1, not a crash and not a write"
assert_eq 1 "$(cat "$WORK/rc")" "exit code"
assert_contains "$(cat "$WORK/err2")" "did not answer" "stderr"

# --- the App, by hand, until #12 -----------------------------------------------------------

c="$(new_checkout app)"
printf -- '-----BEGIN RSA PRIVATE KEY-----\nbm90IHJlYWxseSBhIGtleSBQRU0tUExBSU5URVhULU1BUktFUg==\n-----END RSA PRIVATE KEY-----\n' >"$c/app.pem"
script
i "$c" "$WORK/key" --plan dns --validate-only workspace --app-id 123 --app-key "$c/app.pem"

it "--app-id N --app-key FILE seals two more secrets"
assert_eq 0 "$RC" "exit code"
assert_eq "ANTHROPIC_API_KEY
FALCONET_APP_ID
FALCONET_APP_PRIVATE_KEY" "$(jq -r 'select(.method == "PUT") | .path | split("/") | last' "$FAKE_GITHUB/requests.jsonl")" "PUTs, in order"
assert_line "$OUT" "done         3. secret FALCONET_APP_ID stored (sealed to key 568250167242549743)"
assert_line "$OUT" "done         3. secret FALCONET_APP_PRIVATE_KEY stored (sealed to key 568250167242549743)"

it "the App ID is sealed as its digits, the key as the whole PEM"
assert_eq 51 "$(sealed_len FALCONET_APP_ID)" "app id sealed length (48 + 3)"
assert_eq "$(( 48 + $(wc -c <"$c/app.pem") ))" "$(sealed_len FALCONET_APP_PRIVATE_KEY)" "pem sealed length"
assert_not_contains "$(cat "$FAKE_GITHUB/requests.log")$OUT$ERR" "bm90IHJlYWxseSBhIGtleSBQRU0" "requests+streams"

it "and the App is not left for you"
assert_not_contains "$OUT" "step 3 —" "stdout"

it "--app-id without --app-key is a usage error, and the other way round"
i "$c" "$WORK/empty" --app-id 123
assert_eq 2 "$RC" "exit code"
i "$c" "$WORK/empty" --app-key "$c/app.pem"
assert_eq 2 "$RC" "exit code"
i "$c" "$WORK/empty" --app-id abc --app-key "$c/app.pem"
assert_eq 2 "$RC" "exit code"

it "a --app-key that is not a PEM private key is refused before any call"
printf 'not a pem\n' >"$c/notpem"
i "$c" "$WORK/empty" --app-id 123 --app-key "$c/notpem"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "is not a PEM private key" "stderr"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

script "$all_secrets"
i "$c" "$WORK/empty" --plan dns --validate-only workspace

it "with the App's secrets existing and no flags, both are ok"
assert_line "$OUT" "ok           3. secret FALCONET_APP_ID exists (not replaced; --replace-secrets would)"
assert_line "$OUT" "ok           3. secret FALCONET_APP_PRIVATE_KEY exists (not replaced; --replace-secrets would)"
assert_eq 0 "$(grep -c '^PUT' "$FAKE_GITHUB/requests.log")" "PUTs"

# --- --no-commit --------------------------------------------------------------------------

c="$(new_checkout nocommit)"
before="$(head_of "$c")"
( unset FALCONET_SETUP_TOKEN; i "$c" "$WORK/empty" --plan dns --validate-only workspace --no-commit; printf '%s\n' "$RC" >"$WORK/rc"; printf '%s' "$OUT" >"$WORK/out" )
RC="$(cat "$WORK/rc")"; OUT="$(cat "$WORK/out")"

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

c="$(new_checkout unsorted)"
i "$c" "$WORK/empty" --plan dns

it "a discovered stack named in neither flag goes to validate_only, the README's rule, and says so"
assert_eq 0 "$RC" "exit code"
assert_line "$OUT" "note         7. stack workspace is named in neither --plan nor --validate-only: validate_only, the README's rule for every other directory with .tf in it"
assert_eq '    "validate_only": [
      "workspace"
    ]' "$(grep -A2 '"validate_only"' "$c/repo/.github/falconet.json")" "validate_only list"

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
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

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

# --- from a subdirectory, and which repository -------------------------------------------------

c="$(new_checkout subdir)"
OUT="$( cd "$c/repo/dns" && "$FALCONET" init --plan dns --validate-only workspace <"$WORK/empty" 2>/dev/null )"; RC=$?

it "from a subdirectory the files land at the root: the verb runs from there"
assert_eq 0 "$RC" "exit code"
assert_eq ".falconet/" "$(cat "$c/repo/.gitignore")" ".gitignore"
assert_file_missing "$c/repo/dns/.gitignore"

c="$(new_checkout which)"
script
i "$c" "$WORK/empty" --repo other/place --plan dns --validate-only workspace

it "--repo beats GITHUB_REPOSITORY"
assert_contains "$(calls)" "POST /repos/other/place/labels" "API calls"
assert_not_contains "$(calls)" "wayfinders-infra" "API calls"

c="$(new_checkout remote)"
git -C "$c/repo" remote add origin https://github.com/someone/elsewhere.git
( unset GITHUB_REPOSITORY; i "$c" "$WORK/empty" --plan dns --validate-only workspace; cp "$FAKE_GITHUB/requests.log" "$WORK/remote.log" )

it "with no GITHUB_REPOSITORY the origin remote is the repository, as doctor reads it"
assert_contains "$(awk '{ print $1, $2 }' "$WORK/remote.log")" "POST /repos/someone/elsewhere/labels" "API calls"

c="$(new_checkout noremote)"
( unset GITHUB_REPOSITORY; i "$c" "$WORK/empty" --plan dns --validate-only workspace; printf '%s\n' "$RC" >"$WORK/rc"; printf '%s' "$ERR" >"$WORK/err2" )

it "with a token and no way to name the repository, exit 1 before any write"
assert_eq 1 "$(cat "$WORK/rc")" "exit code"
assert_contains "$(cat "$WORK/err2")" "set GITHUB_REPOSITORY=owner/name" "stderr"
assert_file_missing "$c/repo/.gitignore"

( unset GITHUB_REPOSITORY FALCONET_SETUP_TOKEN; i "$c" "$WORK/empty" --plan dns --validate-only workspace; printf '%s\n' "$RC" >"$WORK/rc" )

it "but without a token the local steps do not need to know which repository"
assert_eq 0 "$(cat "$WORK/rc")" "exit code"
assert_eq "Install falconet" "$(git -C "$c/repo" log -1 --format=%s)" "subject"

it "GITHUB_TOKEN and GH_TOKEN are not fallbacks"
c="$(new_checkout nofallback)"
( unset FALCONET_SETUP_TOKEN; export GITHUB_TOKEN=actions-token GH_TOKEN=gh-token; i "$c" "$WORK/empty" --plan dns --validate-only workspace; printf '%s' "$OUT" >"$WORK/out" )
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"
assert_contains "$(cat "$WORK/out")" "skipped      6. the four labels (no FALCONET_SETUP_TOKEN)" "stdout"

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
i "$c" "$WORK/empty" --plan-env-file
assert_eq 2 "$RC" "exit code"
i "$c" "$WORK/empty" --config
assert_eq 2 "$RC" "exit code"

it "a --repo that is not owner/name is a usage error"
i "$c" "$WORK/empty" --repo nope --plan dns --validate-only workspace
assert_eq 2 "$RC" "exit code"

it "a usage error makes no API call and writes nothing"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"
assert_eq "" "$(git -C "$c/repo" status --porcelain)" "status"

it "init is vocabulary: the dispatcher lists it after doctor"
assert_contains "$("$FALCONET" -h 2>&1)" "  init      " "usage"
assert_eq 1 "$("$FALCONET" -h 2>&1 | grep -A1 '^  doctor ' | grep -c '^  init ')" "init follows doctor"

summary
