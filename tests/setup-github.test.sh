#!/usr/bin/env bash
#
# setup-github.sh — the App-by-manifest round trip, the secrets, and the
# install, exercised through a loopback GitHub. $FALCONET_BROWSER is
# tests/fixtures/browser.sh: curl for a form, curl for a Location, curl for
# the install page.
set -uo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=tests/lib.sh
. "$REPO_ROOT/tests/lib.sh"

fake_github
BROWSER="$REPO_ROOT/tests/fixtures/browser.sh"
SETUP="$REPO_ROOT/install/setup-github.sh"
export GH_TOKEN=test-token

SECRETS_LOG="$WORK/secrets.log"
run_setup() { # [args...]
    GITHUB_API_URL="http://127.0.0.1:$(cat "$FAKE_GITHUB/port")" \
    GITHUB_SERVER_URL="http://127.0.0.1:$(cat "$FAKE_GITHUB/port")" \
    FALCONET_BROWSER="$BROWSER" \
    FALCONET_SECRETS_LOG="$SECRETS_LOG" \
    "$SETUP" "$@" 2>/dev/null
}

# --- the whole round trip ----------------------------------------------------
out="$(run_setup --repo o/r)"; rc=$?

it "exits 0 on the full round trip"
assert_eq 0 "$rc" "exit code"

it "done line names the stored pair and the repo"
assert_contains "$out" "done: falconet-o-r registered" "done line"
assert_contains "$out" "FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY stored" "done line"

reqs="$(cat "$FAKE_GITHUB/requests.log")"
it "the manifest posts to the personal namespace"
assert_contains "$reqs" "POST /settings/apps/new"

it "with the three permissions on it"
# The manifest travels urlencoded, so the log shows pull_requests= and write
# around %22s, not "pull_requests":"write".
assert_contains "$reqs" "pull_requests"
assert_contains "$reqs" "default_permissions"

it "then the code is converted"
assert_contains "$reqs" "POST /app-manifests/"

secrets="$(cat "$SECRETS_LOG")"
it "and both secrets are written, the ID and then the PEM"
assert_contains "$secrets" "FALCONET_APP_ID=42"
assert_contains "$secrets" "FALCONET_APP_PRIVATE_KEY=-----BEGIN"

it "and the installation poll is what ended the wait"
assert_contains "$reqs" "GET /repos/o/r/installation"

# --- the manifest forks on organisation ownership -----------------------------

cat >"$FAKE_GITHUB/responses.json" <<'EOF'
[{"method":"GET","path":"/repos/o/r","status":200,"body":{"owner":{"type":"Organization","login":"o"}},"times":1}]
EOF
run_setup --repo o/r >/dev/null
rm -f "$FAKE_GITHUB/responses.json"

it "an org repository posts the manifest to the organisation namespace"
assert_contains "$(cat "$FAKE_GITHUB/requests.log")" "POST /organizations/o/settings/apps/new"

# --- invocations that do not run a browser ------------------------------------

err="$(GITHUB_API_URL="http://127.0.0.1:$(cat "$FAKE_GITHUB/port")" \
       GITHUB_SERVER_URL="http://127.0.0.1:$(cat "$FAKE_GITHUB/port")" \
       "$SETUP" --repo o/r --timeout 3 --no-browser 2>&1)"; rc=$?
it "--no-browser prints the URL rather than opening one"
assert_contains "$err" "open this yourself"

it "and exits 1 when no redirect can come"
assert_eq 1 "$rc" "exit code"

# --- a redirect that does not carry this run's nonce --------------------------

FALCONET_BROWSER=none \
    GITHUB_API_URL="http://127.0.0.1:$(cat "$FAKE_GITHUB/port")" \
    GITHUB_SERVER_URL="http://127.0.0.1:$(cat "$FAKE_GITHUB/port")" \
    "$SETUP" --repo o/r >"$WORK/browser_out" 2>&1 &
pid=$!

port=""
for _ in $(seq 100); do
    port="$(grep -o 'http://127\.0\.0\.1:[0-9]*' "$WORK/browser_out" 2>/dev/null | head -1 | cut -d: -f3)"
    [ -n "$port" ] && break
    sleep 0.1
done
[ -n "$port" ] || { _fail "the script's listener URL never printed"; summary; exit 1; }

curl -fs -o /dev/null "http://127.0.0.1:$port/callback?state=WRONG&code=x" 2>/dev/null || true
curl -fs -o /dev/null "http://127.0.0.1:$port/callback?state=WRONG&code=x" 2>/dev/null || true
wait "$pid" 2>/dev/null; rc=$?

it "a second wrong-state callback is refused"
assert_eq 1 "$rc" "exit code"

pid_dead_ok=0; kill -0 "$pid" 2>/dev/null || pid_dead_ok=1

it "and the run is told so, in its message"
out_bad="$(cat "$WORK/browser_out")"
assert_contains "$out_bad" "wrong state"

# --- and the listener is not left behind on the refused path ------------------

it "(the run having ended, there is nothing to clean up)"
assert_eq 1 "$pid_dead_ok" "listener gone"

summary
