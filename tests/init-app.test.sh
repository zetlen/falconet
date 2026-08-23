#!/usr/bin/env bash
#
# init-app.test.sh — README step 3 done by manifest (#12, ADR-0006 D5): init
# listens on loopback, serves a form that sends the App's configuration to
# GitHub, takes GitHub's redirect back with a code, converts the code into
# an App, seals the App's ID and private key straight into the repository's
# secrets, and polls for the installation with a JWT it signs itself. The
# private key never touches disk, and the assertion at the end of the happy
# path is the grep that proves it.
#
# GitHub is tests/fixtures/fake-github.py on loopback, and the BROWSER is
# this file: init runs in the background with --no-browser, prints the
# listener's URL on stderr, and python3's urllib (stdlib — python3 is
# already in the suite's dependency set; curl is on every machine here too,
# but is not) fetches the form, reads the nonce out of its action, and
# fetches /callback the way GitHub's redirect would. Still a process
# boundary: stdout, stderr, exit code, files, and what the fake was asked.
#
# stdin is never a terminal here.

# shellcheck source=tests/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_DATE='2026-08-22T12:00:00Z' GIT_COMMITTER_DATE='2026-08-22T12:00:00Z'

# --- the fake API, and no gh ------------------------------------------------

fake_github
export FALCONET_SETUP_TOKEN=test-token
export GITHUB_REPOSITORY=zetlen/wayfinders-infra
unset GITHUB_SERVER_URL GH_TOKEN GITHUB_TOKEN

mkdir -p "$WORK/no-gh"
cat >"$WORK/no-gh/gh" <<'TRIPWIRE'
#!/usr/bin/env bash
echo "gh: init-app.test.sh does not stub gh — the subject must speak GITHUB_API_URL" >&2
exit 1
TRIPWIRE
chmod +x "$WORK/no-gh/gh"
PATH="$WORK/no-gh:$PATH"
export PATH

# --- fixtures -----------------------------------------------------------------

new_checkout() { # name -> echoes path
  local base="$WORK/$1"
  mkdir -p "$base/repo/dns" "$base/repo/workspace"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  printf 'locals {\n  a = 1\n}\n' >"$base/repo/dns/main.tf"
  printf 'locals {\n  b = 2\n}\n' >"$base/repo/workspace/main.tf"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  printf '%s' "$base"
}

script() { # [rule-json ...]
  local rules="" r
  for r in "$@"; do rules="$rules${rules:+,}$r"; done
  printf '[%s]\n' "$rules" >"$FAKE_GITHUB/responses.json"
}

: >"$WORK/empty"

installation="/repos/zetlen/wayfinders-infra/installation"
conversion="/app-manifests/testcode/conversions"
install_url="https://github.com/apps/falconet-zetlen-wayfinders-infra/installations/new"

# The fixture's private key, by length only: a sealed value is 48 + the
# plaintext, and the plaintext is what the fake answered. -B: an import
# writes bytecode beside the module otherwise, into the checkout.
pem_len="$(python3 -B -c '
import importlib.util, sys
spec = importlib.util.spec_from_file_location("fake", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
print(len(mod.TEST_PEM.encode()))' "$REPO_ROOT/tests/fixtures/fake-github.py")"

assert_line() { # haystack exact-line [what]
  if grep -Fxq -- "$2" <<<"$1"; then _pass; else
    _fail "${3:-output} has no line: [$2]" "got: [${1:0:800}]"
  fi
}

calls() { awk '{ print $1, $2 }' "$FAKE_GITHUB/requests.log"; }

sealed_len() { # secret-name
  jq -r "select(.method == \"PUT\" and (.path | endswith(\"/$1\"))) | .body.encrypted_value" "$FAKE_GITHUB/requests.jsonl" \
    | python3 -c 'import base64, sys; print(len(base64.b64decode(sys.stdin.read().strip())))'
}

# --- the browser --------------------------------------------------------------

# fetch URL -> STATUS, BODY. A 4xx is an answer, not an exception.
fetch() {
  python3 - "$1" <<'PY' >"$WORK/fetch.out"
import sys, urllib.request, urllib.error
try:
    with urllib.request.urlopen(sys.argv[1], timeout=10) as r:
        print(r.status)
        sys.stdout.write(r.read().decode("utf-8", "replace"))
except urllib.error.HTTPError as e:
    print(e.code)
    sys.stdout.write(e.read().decode("utf-8", "replace"))
PY
  STATUS="$(head -1 "$WORK/fetch.out")"
  BODY="$(tail -n +2 "$WORK/fetch.out")"
}

# The form's action, and the manifest field's content, HTML-unescaped.
action_of() { # page
  python3 -c 'import html, re, sys
m = re.search(r"<form[^>]*\baction=\"([^\"]*)\"", sys.stdin.read())
print(html.unescape(m.group(1)) if m else "")' <<<"$1"
}
manifest_of() { # page -> the JSON
  python3 -c 'import html, re, sys
m = re.search(r"<textarea name=\"manifest\"[^>]*>(.*?)</textarea>", sys.stdin.read(), re.S)
print(html.unescape(m.group(1)) if m else "")' <<<"$1"
}

# start_init checkout timeout [args...]: init in the background with
# --no-browser, until the listener's URL is on stderr; then / is fetched and
# the nonce read out of the form's action. Sets INIT_PID, URL, PAGE,
# PAGE_STATUS, NONCE.
start_init() {
  local c="$1" t="$2"; shift 2
  : >"$FAKE_GITHUB/requests.log"
  : >"$FAKE_GITHUB/requests.jsonl"
  : >"$WORK/app.out"; : >"$WORK/app.err"
  ( cd "$c/repo" && "$FALCONET" init --no-browser --app-timeout "$t" "$@" <"$WORK/empty" >"$WORK/app.out" 2>"$WORK/app.err" ) &
  INIT_PID=$!
  local waited=0
  URL=""; PAGE=""; PAGE_STATUS=""; NONCE=""
  until grep -q '^open this in a browser: http://127\.0\.0\.1:' "$WORK/app.err" 2>/dev/null; do
    waited=$((waited + 1))
    if [ "$waited" -gt 300 ] || ! kill -0 "$INIT_PID" 2>/dev/null; then
      return 1
    fi
    sleep 0.1
  done
  URL="$(sed -n 's|^open this in a browser: \(http://127\.0\.0\.1:[0-9]*/\)$|\1|p' "$WORK/app.err" | head -1)"
  fetch "$URL"
  PAGE="$BODY"; PAGE_STATUS="$STATUS"
  NONCE="$(action_of "$PAGE" | sed -n 's/.*[?&]state=\([0-9a-f]*\).*/\1/p')"
}

# callback query: GitHub's redirect, as the browser would follow it.
callback() { fetch "${URL}callback?$1"; }

# finish_init: wait for init; sets RC, OUT, ERR.
finish_init() {
  wait "$INIT_PID"; RC=$?
  OUT="$(cat "$WORK/app.out")"
  ERR="$(cat "$WORK/app.err")"
}

# The JWT's header and claims, decoded: "alg typ iss exp-iat parts".
jwt_of() { # authorization-header-value
  python3 -c '
import base64, json, sys
raw = sys.argv[1]
if not raw.startswith("Bearer "):
    print("not a bearer:", raw); sys.exit(0)
t = raw[len("Bearer "):].split(".")
pad = lambda s: s + "=" * (-len(s) % 4)
h = json.loads(base64.urlsafe_b64decode(pad(t[0])))
p = json.loads(base64.urlsafe_b64decode(pad(t[1])))
print(h["alg"], h["typ"], p["iss"], p["exp"] - p["iat"], len(t))' "$1"
}

# --- the happy path: a redirect, a conversion, two secrets, an installation ----

c="$(new_checkout happy)"
script "{\"method\":\"GET\",\"path\":\"$installation\",\"status\":404,\"body\":{\"message\":\"Not Found\"},\"times\":2}"
start_init "$c" 40s --plan dns --validate-only workspace

it "init listens on 127.0.0.1 and says where, on stderr"
assert_contains "$(cat "$WORK/app.err")" "open this in a browser: http://127.0.0.1:" "stderr"
assert_eq 200 "$PAGE_STATUS" "GET / status"

it "and says what to click on GitHub's page"
assert_contains "$(cat "$WORK/app.err")" 'click "Create GitHub App"' "stderr"

it "the page is a form that POSTs to GitHub's new-App page with the nonce as state"
assert_eq "https://github.com/settings/apps/new?state=$NONCE" "$(action_of "$PAGE")" "form action"
assert_eq 64 "${#NONCE}" "nonce length (32 bytes, hex)"
assert_contains "$PAGE" 'method="post"' "form method"

it "that submits itself, with a visible button as the fallback"
assert_contains "$PAGE" '.submit()' "page"
assert_contains "$PAGE" '<button type="submit"' "page"

it "with one field, manifest, whose JSON carries exactly the App README step 3 describes"
manifest="$(manifest_of "$PAGE")"
assert_eq "default_events default_permissions hook_attributes name public redirect_url url" \
  "$(jq -r 'keys | join(" ")' <<<"$manifest")" "manifest keys"
assert_eq '{"contents":"write","issues":"write","pull_requests":"write"}' "$(jq -c .default_permissions <<<"$manifest")" "permissions"
assert_eq "false" "$(jq -r .public <<<"$manifest")" "public"
assert_eq "false" "$(jq -r .hook_attributes.active <<<"$manifest")" "hook active"
assert_eq "$URL" "$(jq -r .hook_attributes.url <<<"$manifest")" "hook url: the listener's own"
assert_eq "${URL}callback" "$(jq -r .redirect_url <<<"$manifest")" "redirect_url"
assert_eq "[]" "$(jq -c .default_events <<<"$manifest")" "events"
assert_eq "falconet-zetlen-wayfinders-infra" "$(jq -r .name <<<"$manifest")" "name"
assert_eq "https://github.com/zetlen/wayfinders-infra" "$(jq -r .url <<<"$manifest")" "url: the repository's html_url"

it "nothing reaches GitHub's API until the redirect: the reads, the labels, the other secrets"
assert_not_contains "$(calls)" "app-manifests" "API calls"

callback "code=testcode&state=$NONCE"

it "GitHub's redirect with the nonce is accepted, and the browser is told"
assert_eq 200 "$STATUS" "callback status"
assert_contains "$BODY" "Registered" "callback page"

finish_init

it "init is exit 0"
assert_eq 0 "$RC" "exit code"

it "and reports step 3 done: registered, installed, two secrets stored"
assert_line "$OUT" "done         3. the GitHub App falconet-zetlen-wayfinders-infra (ID 12345) is registered, installed on zetlen/wayfinders-infra, and its two secrets are stored"
assert_line "$OUT" "done         3. secret FALCONET_APP_ID stored (sealed to key 568250167242549743)"
assert_line "$OUT" "done         3. secret FALCONET_APP_PRIVATE_KEY stored (sealed to key 568250167242549743)"

it "POST /app-manifests/testcode/conversions once, and without an authorization header"
assert_eq 1 "$(grep -c "^POST $conversion" "$FAKE_GITHUB/requests.log")" "conversion POSTs"
assert_eq "none" "$(jq -r "select(.path == \"$conversion\") | .headers.authorization // \"none\"" "$FAKE_GITHUB/requests.jsonl")" "Authorization on the conversion"

it "and stderr records that the endpoint took it without a token"
assert_contains "$ERR" "the conversion endpoint accepted the request without a token" "stderr"

it "the two App secrets are PUT with the key's id and sealed values of the right length"
assert_eq "FALCONET_APP_ID
FALCONET_APP_PRIVATE_KEY" "$(jq -r 'select(.method == "PUT") | .path | split("/") | last' "$FAKE_GITHUB/requests.jsonl")" "PUTs, in order"
assert_eq "568250167242549743
568250167242549743" "$(jq -r 'select(.method == "PUT") | .body.key_id' "$FAKE_GITHUB/requests.jsonl")" "key_id"
assert_eq 53 "$(sealed_len FALCONET_APP_ID)" "app id sealed length (48 + 5)"
assert_eq "$((48 + pem_len))" "$(sealed_len FALCONET_APP_PRIVATE_KEY)" "pem sealed length (48 + the PEM)"
assert_eq 2 "$(jq -r 'select(.method == "PUT") | .body | keys | join(",")' "$FAKE_GITHUB/requests.jsonl" | grep -c '^encrypted_value,key_id$')" "body keys"

it "the secrets are stored before the install poll starts"
assert_eq "$(( $(grep -n "^PUT .*/FALCONET_APP_PRIVATE_KEY" "$FAKE_GITHUB/requests.log" | cut -d: -f1 | tail -1) < $(grep -n "^GET $installation" "$FAKE_GITHUB/requests.log" | cut -d: -f1 | head -1) ))" 1 "PUT before the first GET …/installation"

it "the installation is polled with a Bearer JWT: RS256, iss 12345, good for 11 minutes (iat −60s, exp +10m)"
assert_eq "RS256 JWT 12345 660 3" "$(jwt_of "$(jq -r "select(.path == \"$installation\") | .headers.authorization" "$FAKE_GITHUB/requests.jsonl" | head -1)")" "JWT"

it "and polled again, every 3 seconds, while the first two answers are 404"
assert_eq 3 "$(grep -c "^GET $installation" "$FAKE_GITHUB/requests.log")" "installation polls"

it "the install page and what to click are on stderr"
assert_contains "$ERR" "open this in a browser: $install_url" "stderr"
assert_contains "$ERR" 'click "Install", then "Only select repositories", and pick zetlen/wayfinders-infra' "stderr"

it "the private key's BEGIN line is in no file under the scratch directory, and on neither stream"
assert_eq "" "$(grep -rl 'BEGIN RSA PRIVATE KEY' "$WORK" 2>/dev/null)" "files holding the PEM"
assert_not_contains "$OUT$ERR" "BEGIN RSA PRIVATE KEY" "stdout+stderr"
assert_not_contains "$OUT$ERR" "CLIENT-SECRET-MARKER" "stdout+stderr"
assert_not_contains "$(cat "$FAKE_GITHUB/requests.log" "$FAKE_GITHUB/requests.jsonl")" "SECRET-MARKER" "requests"

it "and the PEM reaches the fake only as a sealed encrypted_value"
assert_eq 0 "$(grep -c 'PRIVATE KEY-----' "$FAKE_GITHUB/requests.jsonl")" "PEM lines in requests.jsonl"
assert_eq 1 "$(jq -r 'select(.method == "PUT" and (.path | endswith("/FALCONET_APP_PRIVATE_KEY"))) | .body.encrypted_value' "$FAKE_GITHUB/requests.jsonl" | grep -c '^[A-Za-z0-9+/]*=*$')" "a base64 encrypted_value"

it "the App is not left for you"
assert_not_contains "$OUT" "step 3 —" "stdout"

it "and the local steps and the commit still happen"
assert_eq "Install falconet" "$(git -C "$c/repo" log -1 --format=%s)" "subject"
assert_eq "" "$(git -C "$c/repo" status --porcelain)" "status"

it "the listener is gone when init is"
fetch "$URL" 2>/dev/null; assert_eq "" "$STATUS" "GET / after exit"

# --- a state mismatch is refused, and the right redirect afterwards still works ----

c="$(new_checkout state)"
script
start_init "$c" 40s --plan dns --validate-only workspace
callback "code=x&state=wrong"

it "a redirect whose state is not the nonce is a 400 to the browser"
assert_eq 400 "$STATUS" "callback status"
assert_contains "$BODY" "state mismatch" "callback body"

it "refused on stderr, with no conversion POST, and init still waiting"
assert_contains "$(cat "$WORK/app.err")" "state mismatch — refusing the code" "stderr"
assert_eq 0 "$(grep -c "app-manifests" "$FAKE_GITHUB/requests.log")" "conversion POSTs"
assert_eq 0 "$(kill -0 "$INIT_PID" 2>/dev/null; echo $?)" "init running"

callback "code=testcode&state=$NONCE"
finish_init

it "and the redirect with the nonce afterwards is accepted: the run completes on that code"
assert_eq 200 "$STATUS" "callback status"
assert_eq 0 "$RC" "exit code"
assert_eq 1 "$(grep -c "^POST $conversion" "$FAKE_GITHUB/requests.log")" "conversion of testcode"
assert_eq 0 "$(grep -c "app-manifests/x/" "$FAKE_GITHUB/requests.log")" "conversion of the refused code"
assert_line "$OUT" "done         3. the GitHub App falconet-zetlen-wayfinders-infra (ID 12345) is registered, installed on zetlen/wayfinders-infra, and its two secrets are stored"

c="$(new_checkout twice)"
start_init "$c" 40s --plan dns --validate-only workspace
callback "code=x&state=wrong"
callback "code=y&state=alsowrong"
finish_init

it "a second mismatch ends the step: exit 0, nothing converted, nothing stored, the App left for you, the local steps done"
assert_eq 0 "$RC" "exit code"
assert_eq 0 "$(grep -c "app-manifests" "$FAKE_GITHUB/requests.log")" "conversion POSTs"
assert_eq 0 "$(grep -c "^PUT" "$FAKE_GITHUB/requests.log")" "PUTs"
assert_line "$OUT" "skipped      3. secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (the App was not registered: two redirects arrived with the wrong state)"
assert_contains "$OUT" "step 3 — the GitHub App: falconet init with FALCONET_SETUP_TOKEN set registers one by manifest" "stdout"
assert_contains "$ERR" "the App is left for you: run init again, or register it by hand (README step 3)" "stderr"
assert_eq "Install falconet" "$(git -C "$c/repo" log -1 --format=%s)" "subject"

# --- no redirect within --app-timeout -----------------------------------------------

c="$(new_checkout timeout)"
start_init "$c" 2s --plan dns --validate-only workspace
finish_init

it "no redirect within --app-timeout: exit 0, the step skipped with the reason, the local steps done"
assert_eq 0 "$RC" "exit code"
assert_line "$OUT" "skipped      3. secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (the App was not registered: no redirect from GitHub within 2s)"
assert_eq 0 "$(grep -c "app-manifests" "$FAKE_GITHUB/requests.log")" "conversion POSTs"
assert_contains "$OUT" "step 3 — the GitHub App" "stdout"
assert_eq "Install falconet" "$(git -C "$c/repo" log -1 --format=%s)" "subject"

# --- the conversion refused without a token: once more with the setup token ----------

c="$(new_checkout retry)"
script "{\"method\":\"POST\",\"path\":\"$conversion\",\"status\":401,\"body\":{\"message\":\"Requires authentication\"},\"times\":1}"
start_init "$c" 40s --plan dns --validate-only workspace
callback "code=testcode&state=$NONCE"
finish_init

it "a 401 on the tokenless conversion is retried with the setup token, and succeeds"
assert_eq 0 "$RC" "exit code"
assert_eq 2 "$(grep -c "^POST $conversion" "$FAKE_GITHUB/requests.log")" "conversion POSTs"
assert_eq "none
Bearer test-token" "$(jq -r "select(.path == \"$conversion\") | .headers.authorization // \"none\"" "$FAKE_GITHUB/requests.jsonl")" "Authorization, first then second"
assert_line "$OUT" "done         3. the GitHub App falconet-zetlen-wayfinders-infra (ID 12345) is registered, installed on zetlen/wayfinders-infra, and its two secrets are stored"

it "and stderr records that the endpoint needed the token"
assert_contains "$ERR" "the conversion endpoint needed the setup token (a 401 without one)" "stderr"

c="$(new_checkout refused)"
script "{\"method\":\"POST\",\"path\":\"$conversion\",\"status\":404,\"body\":{\"message\":\"Not Found\"}}"
before="$(git -C "$c/repo" rev-parse HEAD)"
start_init "$c" 40s --plan dns --validate-only workspace
callback "code=testcode&state=$NONCE"
finish_init

it "a conversion GitHub refuses outright is exit 1, after the labels and before any local file"
assert_eq 1 "$RC" "exit code"
assert_eq 1 "$(grep -c "^POST $conversion" "$FAKE_GITHUB/requests.log")" "conversion POSTs: a 404 is not retried with the token"
assert_contains "$ERR" "could not convert the manifest code into an App" "stderr"
it "and the line does not carry the code, which is still good for a conversion"
assert_not_contains "$ERR" "testcode" "stderr"
assert_contains "$ERR" "stopped at step 3" "stderr"
assert_eq 4 "$(grep -c '^POST /repos/zetlen/wayfinders-infra/labels' "$FAKE_GITHUB/requests.log")" "label POSTs"
assert_eq 0 "$(grep -c "^PUT .*FALCONET_APP" "$FAKE_GITHUB/requests.log")" "App PUTs"
assert_file_missing "$c/repo/.gitignore"
assert_eq "$before" "$(git -C "$c/repo" rev-parse HEAD)" "HEAD"

# --- an installation that never answers 200 ---------------------------------------------

c="$(new_checkout uninstalled)"
script "{\"method\":\"GET\",\"path\":\"$installation\",\"status\":404,\"body\":{\"message\":\"Not Found\"}}"
start_init "$c" 7s --plan dns --validate-only workspace
callback "code=testcode&state=$NONCE"
finish_init

it "an installation that never answers 200: exit 0, the secrets stored, the install left for you with its URL"
assert_eq 0 "$RC" "exit code"
assert_line "$OUT" "done         3. secret FALCONET_APP_ID stored (sealed to key 568250167242549743)"
assert_line "$OUT" "done         3. secret FALCONET_APP_PRIVATE_KEY stored (sealed to key 568250167242549743)"
assert_line "$OUT" "cannot tell  3. the App is installed (timed out after 7s — install it at $install_url, then run falconet doctor)"
assert_contains "$OUT" "step 3 — install the App: $install_url → Install → Only select repositories → zetlen/wayfinders-infra, then: falconet doctor" "stdout"

it "after at least two polls, and with the local steps done"
assert_eq 1 "$(( $(grep -c "^GET $installation" "$FAKE_GITHUB/requests.log") >= 2 ))" "two or more polls"
assert_eq "Install falconet" "$(git -C "$c/repo" log -1 --format=%s)" "subject"

it "the summary counts the cannot tell"
assert_contains "$OUT" "1 cannot tell" "summary"

# --- --no-app: the by-hand path, untouched ---------------------------------------------

c="$(new_checkout noapp)"
script
: >"$FAKE_GITHUB/requests.log"; : >"$FAKE_GITHUB/requests.jsonl"
OUT="$( cd "$c/repo" && "$FALCONET" init --no-app --plan dns --validate-only workspace <"$WORK/empty" 2>"$WORK/err" )"; RC=$?
ERR="$(cat "$WORK/err")"

it "--no-app: no listener, no conversion, step 3 skipped and left for you in the README's words"
assert_eq 0 "$RC" "exit code"
assert_not_contains "$ERR" "open this in a browser" "stderr"
assert_eq 0 "$(grep -c "app-manifests\|installation" "$FAKE_GITHUB/requests.log")" "App calls"
assert_line "$OUT" "skipped      3. secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (--no-app)"
assert_contains "$OUT" "falconet init --app-id <App ID> --app-key <the .pem>" "stdout"

c="$(new_checkout byhand)"
printf -- '-----BEGIN RSA PRIVATE KEY-----\nbm90IHJlYWxseSBhIGtleQ==\n-----END RSA PRIVATE KEY-----\n' >"$c/app.pem"
: >"$FAKE_GITHUB/requests.log"; : >"$FAKE_GITHUB/requests.jsonl"
OUT="$( cd "$c/repo" && "$FALCONET" init --plan dns --validate-only workspace --app-id 123 --app-key "$c/app.pem" <"$WORK/empty" 2>"$WORK/err" )"; RC=$?
ERR="$(cat "$WORK/err")"

it "--app-id and --app-key win: no listener, the flags' App sealed"
assert_eq 0 "$RC" "exit code"
assert_not_contains "$ERR" "open this in a browser" "stderr"
assert_eq 0 "$(grep -c "app-manifests\|installation" "$FAKE_GITHUB/requests.log")" "App calls"
assert_eq 51 "$(sealed_len FALCONET_APP_ID)" "app id sealed length (48 + 3)"

script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets","body":{"total_count":2,"secrets":[{"name":"FALCONET_APP_ID"},{"name":"FALCONET_APP_PRIVATE_KEY"}]}}'
c="$(new_checkout existing)"
: >"$FAKE_GITHUB/requests.log"
OUT="$( cd "$c/repo" && "$FALCONET" init --plan dns --validate-only workspace <"$WORK/empty" 2>"$WORK/err" )"; RC=$?

it "with the App's secrets existing, nothing is registered and both are ok"
assert_eq 0 "$RC" "exit code"
assert_not_contains "$(cat "$WORK/err")" "open this in a browser" "stderr"
assert_line "$OUT" "ok           3. secret FALCONET_APP_ID exists (not replaced; --replace-secrets would)"

# --- an organisation's repository -----------------------------------------------------

c="$(new_checkout org)"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra","body":{"name":"wayfinders-infra","full_name":"zetlen/wayfinders-infra","owner":{"login":"zetlen","type":"Organization"},"html_url":"https://github.com/zetlen/wayfinders-infra","private":true,"visibility":"private","has_issues":true,"default_branch":"main"}}'
start_init "$c" 40s --plan dns --validate-only workspace

it "when the owner is an organisation, the form POSTs to the organisation's new-App page"
assert_eq "https://github.com/organizations/zetlen/settings/apps/new?state=$NONCE" "$(action_of "$PAGE")" "form action"

callback "code=testcode&state=$NONCE"
finish_init
assert_eq 0 "$RC" "exit code"

# --- the name ---------------------------------------------------------------------------

c="$(new_checkout named)"
script
start_init "$c" 40s --plan dns --validate-only workspace --app-name "My Falconet"

it "--app-name is the manifest's name"
assert_eq "My Falconet" "$(manifest_of "$PAGE" | jq -r .name)" "name"
callback "code=testcode&state=$NONCE"
finish_init
assert_eq 0 "$RC" "exit code"

c="$(new_checkout long)"
start_init "$c" 40s --plan dns --validate-only workspace --repo zetlen/a-very-long-repository-name-indeed-yes

it "a default name over 34 characters is cut to 34, and stderr says so"
assert_eq "falconet-zetlen-a-very-long-reposi" "$(manifest_of "$PAGE" | jq -r .name)" "name"
assert_eq 34 "$(manifest_of "$PAGE" | jq -r '.name | length')" "name length"
assert_contains "$(cat "$WORK/app.err")" "is longer than GitHub's 34 characters, so it is falconet-zetlen-a-very-long-reposi" "stderr"
callback "code=testcode&state=$NONCE"
finish_init
assert_eq 0 "$RC" "exit code"

it "an --app-name over 34 characters is a usage error, before any call"
: >"$FAKE_GITHUB/requests.log"
OUT="$( cd "$c/repo" && "$FALCONET" init --app-name "a-name-that-is-thirty-five-chars-xx" <"$WORK/empty" 2>"$WORK/err" )"; RC=$?
assert_eq 2 "$RC" "exit code"
assert_contains "$(cat "$WORK/err")" "--app-name is 35 characters, and GitHub allows 34" "stderr"
assert_eq "" "$(cat "$FAKE_GITHUB/requests.log")" "API calls"

it "an --app-timeout that is not a positive duration is a usage error"
OUT="$( cd "$c/repo" && "$FALCONET" init --app-timeout soon <"$WORK/empty" 2>"$WORK/err" )"; RC=$?
assert_eq 2 "$RC" "exit code"
OUT="$( cd "$c/repo" && "$FALCONET" init --app-timeout 0s <"$WORK/empty" 2>"$WORK/err" )"; RC=$?
assert_eq 2 "$RC" "exit code"
OUT="$( cd "$c/repo" && "$FALCONET" init --app-timeout <"$WORK/empty" 2>"$WORK/err" )"; RC=$?
assert_eq 2 "$RC" "exit code"

it "the usage text names the flags"
assert_contains "$("$FALCONET" init -h 2>&1)" "--app-timeout" "usage"
assert_contains "$("$FALCONET" init -h 2>&1)" "--no-app" "usage"
assert_contains "$("$FALCONET" init -h 2>&1)" "--no-browser" "usage"


# --- --replace-secrets alone keeps the App -----------------------------------------------
#
# GitHub does not delete Apps through this flow, so registering a new one over
# existing secrets orphans the first. It happens only when the new App is named.

c="$(new_checkout keepapp)"
script '{"method":"GET","path":"/repos/zetlen/wayfinders-infra/actions/secrets","body":{"total_count":2,"secrets":[{"name":"FALCONET_APP_ID"},{"name":"FALCONET_APP_PRIVATE_KEY"}]}}'
: >"$FAKE_GITHUB/requests.log"; : >"$FAKE_GITHUB/requests.jsonl"
OUT="$( cd "$c/repo" && "$FALCONET" init --replace-secrets --no-browser --app-timeout 3s --plan dns --validate-only workspace <"$WORK/empty" 2>"$WORK/err" )"; RC=$?

it "--replace-secrets with the App's secrets present and no --app-name keeps the App, and says how to replace it"
assert_eq 0 "$RC" "exit code"
assert_line "$OUT" "ok           3. secret FALCONET_APP_ID exists (not replaced; --replace-secrets would)"
assert_line "$OUT" "note         3. --replace-secrets keeps the App: to register a new one over these secrets, name it with --app-name"

it "and registers nothing"
assert_eq 0 "$(grep -c "^POST $conversion" "$FAKE_GITHUB/requests.log")" "conversion POSTs"
assert_eq 0 "$(grep -c '^PUT .*/FALCONET_APP_' "$FAKE_GITHUB/requests.log")" "App secret PUTs"

summary
