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
SETUP="$REPO_ROOT/install/setup-github.sh"
export GH_TOKEN=test-token
# The web base is the API base: the fake serves the manifest form's POST and
# the install page beside the REST routes.
export GITHUB_SERVER_URL="$GITHUB_API_URL"
export FALCONET_BROWSER="$REPO_ROOT/tests/fixtures/browser.sh"

# A gh that writes down `secret set` and swallows it, and gets out of the way
# of everything else. `gh secret set` cannot be pointed at the fake — it
# seals against the host's key and forces https — so the argv it is handed,
# and what arrives on its stdin, are the evidence a secret was stored.
REAL_GH="$(command -v gh)"
GH_LOG="$WORK/gh-argv.txt"
mkdir -p "$WORK/stubbin"
cat >"$WORK/stubbin/gh" <<STUB
#!/usr/bin/env bash
if [ "\$1" = secret ]; then
    printf '%s\n' "\$@" >>"$GH_LOG"
    printf -- '--\n' >>"$GH_LOG"
    cat >"$WORK/gh-stdin-\$3"
    exit 0
fi
exec "$REAL_GH" "\$@"
STUB
chmod +x "$WORK/stubbin/gh"
export PATH="$WORK/stubbin:$PATH"

run_setup() { # [args...]
    "$SETUP" "$@" 2>/dev/null </dev/null
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

secrets="$(cat "$GH_LOG")"
it "and both secrets are handed to gh secret set, the ID and then the PEM"
assert_contains "$secrets" "FALCONET_APP_ID"
assert_contains "$secrets" "FALCONET_APP_PRIVATE_KEY"

it "the secret write names the server's host, not gh's default one"
host="${GITHUB_SERVER_URL#*://}"
assert_contains "$secrets" "--repo
$host/o/r" "argv"

it "the PEM arrives on gh's stdin, where ps cannot see it"
assert_not_contains "$secrets" "BEGIN" "argv"
assert_contains "$(cat "$WORK/gh-stdin-FALCONET_APP_PRIVATE_KEY")" "-----BEGIN" "stdin"

it "and the installation poll is what ended the wait"
assert_contains "$reqs" "GET /repos/o/r/installation"

# --- a trailing slash on the API base is not a double slash in a path -------

: >"$FAKE_GITHUB/requests.log"
GITHUB_API_URL="$GITHUB_API_URL/" run_setup --repo o/r >/dev/null; rc=$?
it "GITHUB_API_URL with a trailing slash still completes the round trip"
assert_eq 0 "$rc" "exit code"
assert_contains "$(cat "$FAKE_GITHUB/requests.log")" "POST /app-manifests/" "paths"

# --- the manifest forks on organisation ownership -----------------------------

cat >"$FAKE_GITHUB/responses.json" <<'EOF'
[{"method":"GET","path":"/repos/o/r","status":200,"body":{"owner":{"type":"Organization","login":"o"}},"times":1}]
EOF
run_setup --repo o/r >/dev/null
rm -f "$FAKE_GITHUB/responses.json"

it "an org repository posts the manifest to the organisation namespace"
assert_contains "$(cat "$FAKE_GITHUB/requests.log")" "POST /organizations/o/settings/apps/new"

# --- invocations that do not run a browser ------------------------------------

err="$("$SETUP" --repo o/r --timeout 0 --no-browser 2>&1 </dev/null)"; rc=$?
it "--no-browser prints the URL rather than opening one"
assert_contains "$err" "open this yourself"

it "and exits 1 when no redirect can come"
assert_eq 1 "$rc" "exit code"

# --- a redirect that does not carry this run's nonce --------------------------

FALCONET_BROWSER=none "$SETUP" --repo o/r >"$WORK/browser_out" 2>&1 </dev/null &
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
