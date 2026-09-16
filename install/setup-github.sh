#!/usr/bin/env bash
# setup-github.sh — register the GitHub App falconet runs as, store its two
# secrets in the repository, and wait until it is installed. This is README
# step 3 done by the manifest flow, as a standalone script rather than a
# verb: it runs once, on the workstation of the person installing falconet,
# and the binary an agent can reach does not grow for it.
#
# The flow (GitHub's "Registering a GitHub App from a manifest"):
#   1. A localhost listener serves a form POSTing the App's configuration to
#      GitHub (a browser cannot POST from a URL, so the script serves it);
#      the person clicks "Create GitHub App" — the one step that cannot be
#      automated, because it is the ownership boundary.
#   2. GitHub redirects back to the listener with a one-hour, single-use
#      code, which is exchanged at POST /app-manifests/{code}/conversions
#      for the App ID and private key. The PEM goes straight into the
#      repository's secrets and is never written to disk. The nonce in
#      ?state= must match exactly, or the code is refused — a mismatching
#      redirect is not the run's own.
#   3. The install page opens; GET /repos/{o}/{r}/installation is polled
#      with an RS256 JWT signed from the PEM until the App is installed.
#
# Dependencies: gh (authenticated), jq, curl, openssl, python3. Those are
# the tools the README already says the operator has.
#
#   bash install/setup-github.sh [--repo OWNER/NAME] [--app-name NAME]
#                                [--timeout SECONDS] [--no-browser]
#
# Honored overrides, for tests and GitHub Enterprise Server:
#   GITHUB_API_URL     the API base (default https://api.github.com)
#   GITHUB_SERVER_URL  the web base (default https://github.com)
#   FALCONET_BROWSER   the command used to open a URL, which is printed
#                      either way; "none" only prints (default: open,
#                      xdg-open, or nothing)

set -euo pipefail

usage() {
    cat <<'EOF'
Usage: setup-github.sh [--repo OWNER/NAME] [--app-name NAME]
                       [--timeout SECONDS] [--no-browser] [-h]

  --repo         the repository; default: $GITHUB_REPOSITORY or origin remote
  --app-name     name the App is registered as (default: falconet-<owner>-<repo>)
  --timeout      seconds to wait for you and a browser, per round trip
                 (default 600)
  --no-browser   do not open the URLs, only print them
  -h, --help     this text
EOF
}

die() { echo "setup-github.sh: $*" >&2; exit 1; }
note() { echo "setup-github.sh: $*" >&2; }

repo="${GITHUB_REPOSITORY:-}"
app_name=""
timeout=600
browser="${FALCONET_BROWSER:-}"

while [ $# -gt 0 ]; do
    case "$1" in
        --repo)       repo="$2"; shift 2 ;;
        --app-name)   app_name="$2"; shift 2 ;;
        --timeout)    timeout="$2"; shift 2 ;;
        --no-browser) browser=none; shift ;;
        -h|--help)    usage; exit 0 ;;
        *) die "unknown argument: $1 (try --help)" ;;
    esac
done

for dep in gh jq curl openssl python3; do
    command -v "$dep" >/dev/null 2>&1 || die "need $dep on PATH"
done
case "$timeout" in ''|*[!0-9]*) die "--timeout must be seconds, got: $timeout" ;; esac

# --- where, and which repository --------------------------------------------

if [ -z "$repo" ]; then
    origin="$(git remote get-url origin 2>/dev/null || true)"
    repo="$(printf '%s' "$origin" \
        | sed -e 's|^git@[^:]*:||' -e 's|^https://[^/]*/||' -e 's|\.git$||')"
fi
case "$repo" in */*) ;; *) die "no repository named: pass --repo owner/name, run inside a clone, or set GITHUB_REPOSITORY" ;; esac
owner="${repo%%/*}"; name="${repo#*/}"

server="${GITHUB_SERVER_URL:-https://github.com}"; server="${server%/}"
api="${GITHUB_API_URL:-https://api.github.com}"; api="${api%/}"
host="${server#*://}"

# Reads go through gh with an explicit Authorization header — the same
# discipline falconet itself uses — because gh attaches its token only to
# hosts it has configured, and a GITHUB_API_URL or GHES host is not one.
gh_api() {
    if [ -n "$TOKEN" ]; then
        gh api -H "Authorization: token $TOKEN" "$@"
    else
        gh api "$@"
    fi
}
# The write is gh's own, told the host: a bare owner/name would resolve
# against gh's default host, which on GitHub Enterprise Server is the wrong
# one. The value travels on stdin: argv is readable by every process on the
# workstation, and on bash 3.2 a here-string is a temporary file.
put_secret() { # name value
    printf '%s' "$2" | gh secret set "$1" --repo "$host/$repo"
}
TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
if [ -z "$TOKEN" ]; then TOKEN="$(gh auth token --hostname "$host" 2>/dev/null || true)"; fi

# Personal or organisation: the manifest must POST to the right namespace or
# the App ends up owned by the person and uninstallable on an org's repo.
owner_type="$(gh_api "$api/repos/$repo" --jq .owner.type)" \
    || die "cannot read $repo from $api"
org=""
if [ "$owner_type" = Organization ]; then org="$owner"; fi

if [ -z "$app_name" ]; then
    app_name="falconet-$owner-$name"
    # GitHub's limit is 34; cut, never leaving a trailing dash.
    if [ "${#app_name}" -gt 34 ]; then
        app_name="${app_name:0:34}"; app_name="${app_name%%-}"
        note "the App name is cut to GitHub's 34 characters: $app_name"
    fi
fi

# --- the listener, the nonce, the browser round trip ------------------------

nonce="$(openssl rand -hex 32)"

# A free loopback port, found then reused. Between finding and binding
# another process could take it; the python below fails loudly if so.
port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"

work="$(mktemp -d)"
LISTENER_PID=""
cleanup() {
    # Under errexit a failed kill would end the trap here, before the rm.
    if [ -n "$LISTENER_PID" ]; then kill "$LISTENER_PID" 2>/dev/null || true; fi
    rm -rf "$work"
}
trap cleanup EXIT

listener="http://127.0.0.1:$port/"
redirect="${listener}callback"
if [ -n "$org" ]; then
    action="$server/organizations/$org/settings/apps/new?state=$nonce"
else
    action="$server/settings/apps/new?state=$nonce"
fi

manifest="$(jq -nc \
    --arg name "$app_name" \
    --arg url "$server/$repo" \
    --arg lu "$listener" \
    --arg ru "$redirect" \
    '{name:$name, url:$url,
      hook_attributes:{url:$lu, active:false},
      redirect_url:$ru, public:false,
      default_permissions:{contents:"write", issues:"write", pull_requests:"write"},
      default_events:[]}')"

listen() {
    LISTENER_PORT="$port" NONCE="$nonce" ACTION="$action" MANIFEST="$manifest" \
    CODE_FILE="$work/code" \
    python3 - "$work" <<'PY' &
import html, http.server, os, sys, threading, urllib.parse

work = sys.argv[1]
port = int(os.environ["LISTENER_PORT"])
nonce = os.environ["NONCE"]
action = os.environ["ACTION"]
manifest = os.environ["MANIFEST"]
code_file = os.environ["CODE_FILE"]
self_hosts = {"127.0.0.1:%d" % port, "localhost:%d" % port}

page = """<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>setup-github.sh: register the GitHub App</title>
<style>body{font-family:system-ui,sans-serif;max-width:48em;margin:3em auto;padding:0 1em}
textarea{width:100%;font-family:monospace}button{font-size:1.1em;padding:.5em 1em}</style>
</head><body><h1>setup-github.sh</h1>
<p>This page is served by setup-github.sh on your machine. It sends the App
configuration below to GitHub, where you click <strong>Create GitHub
App</strong>; GitHub then sends this browser back here, and the script
stores the App's ID and private key as repository secrets. The key is never
written to disk.</p>
<form id="manifest" method="post" action="ACTION">
<p><textarea name="manifest" rows="20" readonly>MANIFEST</textarea></p>
<p><button type="submit">Continue to GitHub</button>
<span>(if nothing happens on its own, click this)</span></p></form>
<script>document.getElementById("manifest").submit();</script></body></html>
"""
# quote=False: JSON double quotes are safe inside a textarea, and the bytes
# must stay exactly parseable when a form POSTs them back to GitHub.
page = page.replace("ACTION", html.escape(action)).replace(
    "MANIFEST", html.escape(manifest, quote=False))

done_page = ("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">"
    "<title>setup-github.sh: registered</title></head><body><h1>Registered</h1>"
    "<p>Back in the terminal, setup-github.sh is storing the secrets and will "
    "open the install page next. You can close this tab.</p></body></html>")

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def _reply(self, status, body):
        data = body.encode()
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def do_GET(self):
        # The nonce's secrecy rests on no other origin being able to read
        # the page that carries it: answer only when asked by this
        # listener's own address, and 404 everything else — including any
        # name a rebinding page might answer to.
        host = self.headers.get("Host", "")
        parts = urllib.parse.urlsplit(self.path)
        if host not in self_hosts:
            return self._reply(404, "not found")
        if parts.path == "/":
            return self._reply(200, page)
        if parts.path != "/callback":
            return self._reply(404, "not found")
        q = urllib.parse.parse_qs(parts.query)
        state = q.get("state", [""])[0]
        code = q.get("code", [""])[0]
        if state != nonce or not code:
            # A stale tab, or something worse: either way not this run's
            # redirect, and refusing it is the whole of the defence.
            return self._reply(400, "setup-github.sh: state mismatch — refusing the code")
        with open(code_file, "w") as f:
            f.write(code)
        self._reply(200, done_page)
        # Flush above, then stop. The script polls for the file; do not
        # leave a listener behind it could mistake for itself.
        threading.Timer(0.1, httpd.shutdown).start()

httpd = http.server.ThreadingHTTPServer(("127.0.0.1", port), H)
httpd.serve_forever()
PY
    LISTENER_PID=$!
}
listen

# The listener binds inside the python child; wait for it to answer before
# handing anything its address.
listener_ready=""
for _ in $(seq 50); do
    if curl -fs -o /dev/null "$listener" 2>/dev/null; then listener_ready=1; break; fi
    if ! kill -0 "$LISTENER_PID" 2>/dev/null; then break; fi
    sleep 0.1
done
# `wait` returns the child's status; under errexit that would exit before die.
[ -n "$listener_ready" ] || { wait "$LISTENER_PID" 2>/dev/null || true; die "the listener did not start"; }

# The URL is always printed: a browser that opens somewhere the person is
# not looking (or, over SSH, nowhere) must not be the only copy of it.
open_url() {
    note "open: $1"
    if [ "$browser" = none ]; then
        return
    elif [ -n "$browser" ]; then
        "$browser" "$1" || die "FALCONET_BROWSER ($browser) failed on $1"
    elif [ "$(uname -s)" = Darwin ] && command -v open >/dev/null; then
        open "$1"
    elif command -v xdg-open >/dev/null; then
        xdg-open "$1" >/dev/null 2>&1 &
    fi
}

note "registering the GitHub App $app_name — click \"Create GitHub App\""
open_url "$listener"

# Wait: the code file, or the listener having died.
deadline=$(( $(date +%s) + timeout ))
while [ ! -s "$work/code" ]; do
    if ! kill -0 "$LISTENER_PID" 2>/dev/null; then
        cat "$work/listener.log" >&2 2>/dev/null || true
        die "the listener exited before GitHub's redirect arrived"
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
        die "no redirect from GitHub within ${timeout}s"
    fi
    sleep 0.2
done
code="$(cat "$work/code")"
kill "$LISTENER_PID" 2>/dev/null || true
wait "$LISTENER_PID" 2>/dev/null || true
LISTENER_PID=""

# --- the code for the App, the secrets, the installation --------------------

note "exchanging the code for the App's credentials"
response="$(curl -fsS -X POST "$api/app-manifests/$code/conversions")" \
    || die "the conversion was refused: $api/app-manifests/<redacted>/conversions"

# No here-strings below this line: on bash 3.2 they are temporary files,
# and the response holds the PEM.
app_id="$(printf '%s' "$response" | jq -r .id)"
pem="$(printf '%s' "$response" | jq -r .pem)"
slug="$(printf '%s' "$response" | jq -r '.html_url | split("/") | last')"
[ -n "$slug" ] && [ "$slug" != null ] \
    || slug="$(printf '%s' "$response" | jq -r '.name' | tr '[:upper:]' '[:lower:]' | tr ' ' '-')"

put_secret FALCONET_APP_ID "$app_id" \
    || die "the App exists but $api refused the first secret — run gh auth status"
# The PEM goes from the conversion response into a secret; it is never a
# file and never printed. The client and webhook secrets are not used.
put_secret FALCONET_APP_PRIVATE_KEY "$pem" \
    || die "the App exists and one secret is stored, but the second was refused"

install="$server/apps/$slug/installations/new"
note "secrets stored — installing $app_name on $repo"
open_url "$install"

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

jwt() {
    local now h p s
    now="$(date +%s)"
    h="$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)"
    p="$(printf '{"iat":%s,"exp":%s,"iss":"%s"}' "$((now - 60))" "$((now + 540))" "$app_id" | b64url)"
    s="$(printf '%s.%s' "$h" "$p" \
        | openssl dgst -sha256 -sign <(printf '%s' "$pem") | b64url)"
    printf '%s.%s.%s' "$h" "$p" "$s"
}

deadline=$(( $(date +%s) + timeout ))
installed=""
while :; do
    if curl -fs -H "Authorization: Bearer $(jwt)" \
            "$api/repos/$repo/installation" >/dev/null 2>&1; then
        installed=1; break
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then break; fi
    sleep 3
done

if [ -z "$installed" ]; then
    note "the App is registered and its secrets are stored, but installing it"
    note "on $repo was not confirmed in ${timeout}s: open $install"
    exit 1
fi

echo "done: $app_name registered, FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY stored, installed on $repo"
note "continue: README step 4 — store the model API key"
