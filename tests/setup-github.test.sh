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

it "with the three permissions, private, and no webhook"
# The manifest travels urlencoded; the fake writes the decoded one down.
m="$FAKE_GITHUB/manifest.json"
assert_eq '{"contents":"write","issues":"write","pull_requests":"write"}' \
    "$(jq -c .default_permissions "$m")" "permissions"
assert_eq "false" "$(jq .public "$m")" "public"
assert_eq "false" "$(jq .hook_attributes.active "$m")" "webhook active"

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

it "the install page opened is the slug GitHub answered with, not the name"
assert_contains "$reqs" "GET /apps/falconet-o-r-1/installations/new"

it "and the installation poll is what ended the wait"
assert_contains "$reqs" "GET /repos/o/r/installation"

# --- a run whose browser never installs the App does not end well ------------

# A browser that submits the form but never visits the install page.
cat >"$WORK/stubbin/no-install-browser" <<STUB
#!/usr/bin/env bash
case "\$1" in */installations/new) exit 0 ;; esac
exec "$FALCONET_BROWSER" "\$1"
STUB
chmod +x "$WORK/stubbin/no-install-browser"
err="$(FALCONET_BROWSER="$WORK/stubbin/no-install-browser" \
       "$SETUP" --repo o/r --timeout 0 2>&1 >/dev/null </dev/null)"; rc=$?
it "a registered but uninstalled App is exit 1 with the install URL"
assert_eq 1 "$rc" "exit code"
assert_contains "$err" "was not confirmed"
assert_contains "$err" "/installations/new"

# --- --app-name is held to GitHub's 34 characters as the default is ---------

err="$("$SETUP" --repo o/r --app-name "$(printf 'x%.0s' $(seq 33))-yy" 2>&1 >/dev/null </dev/null)"
it "an --app-name over 34 characters is cut, with no trailing dash, and said so"
assert_eq "$(printf 'x%.0s' $(seq 33))" "$(jq -r .name "$FAKE_GITHUB/manifest.json")" "name"
assert_contains "$err" "cut to GitHub's 34 characters"

# --- a trailing slash on the API base is not a double slash in a path -------

: >"$FAKE_GITHUB/requests.log"
GITHUB_API_URL="$GITHUB_API_URL/" run_setup --repo o/r >/dev/null; rc=$?
it "GITHUB_API_URL with a trailing slash still completes the round trip"
assert_eq 0 "$rc" "exit code"
assert_contains "$(cat "$FAKE_GITHUB/requests.log")" "POST /app-manifests/" "paths"

# --- failure after the redirect leaves nothing behind -------------------------

cat >"$FAKE_GITHUB/responses.json" <<'EOF'
[{"method":"POST","path":"/app-manifests/fake-code/conversions","status":500,"body":{"message":"no"},"times":1}]
EOF
mkdir -p "$WORK/tmpdir"
err="$(TMPDIR="$WORK/tmpdir" "$SETUP" --repo o/r 2>&1 </dev/null)"; rc=$?
rm -f "$FAKE_GITHUB/responses.json"
it "a refused conversion is reported, once the listener has already gone"
assert_eq 1 "$rc" "exit code"
assert_contains "$err" "the conversion was refused"
it "and the work directory that held the code is removed on the way out"
assert_eq "" "$(ls -A "$WORK/tmpdir")" "leftovers in TMPDIR"

# --- a listener that cannot start is the script's own message ----------------

# A python3 that dies when asked to be the listener — argv is `- <workdir>`
# — and is the real one for the port pick, which has no argument.
REAL_PYTHON3="$(command -v python3)"
cat >"$WORK/stubbin/python3" <<STUB
#!/usr/bin/env bash
if [ "\$1" = - ] && [ \$# -ge 2 ]; then echo "OSError: address in use" >&2; exit 1; fi
exec "$REAL_PYTHON3" "\$@"
STUB
chmod +x "$WORK/stubbin/python3"
err="$(TMPDIR="$WORK/tmpdir" "$SETUP" --repo o/r 2>&1 </dev/null)"; rc=$?
rm -f "$WORK/stubbin/python3"
it "a listener that exits at once is named as the reason, with its own words"
assert_eq 1 "$rc" "exit code"
assert_contains "$err" "the listener did not start"
assert_contains "$err" "OSError: address in use"
it "and the work directory is removed even though there is no listener to kill"
assert_eq "" "$(ls -A "$WORK/tmpdir")" "leftovers in TMPDIR"

# --- the manifest forks on organisation ownership -----------------------------

cat >"$FAKE_GITHUB/responses.json" <<'EOF'
[{"method":"GET","path":"/repos/o/r","status":200,"body":{"owner":{"type":"Organization","login":"o"}},"times":1}]
EOF
run_setup --repo o/r >/dev/null
rm -f "$FAKE_GITHUB/responses.json"

it "an org repository posts the manifest to the organisation namespace"
assert_contains "$(cat "$FAKE_GITHUB/requests.log")" "POST /organizations/o/settings/apps/new"

# --- the URLs are always on stderr, browser or not ---------------------------

err="$("$SETUP" --repo o/r 2>&1 >/dev/null </dev/null)"
it "with a browser, the listener and install URLs are still printed"
assert_contains "$err" "http://127.0.0.1:" "listener URL"
assert_contains "$err" "/installations/new" "install URL"

# --- invocations that do not run a browser ------------------------------------

err="$("$SETUP" --repo o/r --timeout 0 --no-browser 2>&1 </dev/null)"; rc=$?
it "--no-browser prints the URL rather than opening one"
assert_contains "$err" "open: http://127.0.0.1:"

it "and exits 1 when no redirect can come"
assert_eq 1 "$rc" "exit code"

# --- redirects that do not carry this run's nonce are refused, and that is all

# A browser with two stale tabs: it hits the callback with the wrong state
# twice, then behaves — and, like a real browser, returns to the script at
# once and does all of that in the background. The nonce is the protection;
# a stale tab is not a reason to stop.
cat >"$WORK/stubbin/stale-browser" <<STUB
#!/usr/bin/env bash
(
    case "\$1" in
        http://127.0.0.1:*/)
            curl -s -o /dev/null -w '%{http_code}\n' "\$1callback?state=WRONG&code=x" >>"$WORK/stale-status"
            curl -s -o /dev/null -w '%{http_code}\n' "\$1callback?state=WRONG&code=x" >>"$WORK/stale-status"
            printf '%s\n' "\$1" >"$WORK/listener-url"
            sleep 0.5 ;;
    esac
    "$FALCONET_BROWSER" "\$1"
) >/dev/null 2>&1 &
STUB
chmod +x "$WORK/stubbin/stale-browser"
: >"$GH_LOG"
FALCONET_BROWSER="$WORK/stubbin/stale-browser" run_setup --repo o/r >/dev/null; rc=$?

it "each wrong-state callback is answered 400"
assert_eq "400
400" "$(cat "$WORK/stale-status")" "statuses"

it "and the run still completes"
assert_eq 0 "$rc" "exit code"
assert_contains "$(cat "$GH_LOG")" "FALCONET_APP_PRIVATE_KEY"

it "and the listener is gone once the run is"
assert_eq "refused" "$(curl -s -o /dev/null "$(cat "$WORK/listener-url")" && echo answered || echo refused)" "listener"

summary
