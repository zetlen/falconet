#!/usr/bin/env python3
"""
fake-github.py — a GitHub REST API on loopback that answers from fixtures and
writes down what it was asked. python3 stdlib only. This is the suite's one
seam for every verb that has stopped shelling out to `gh` (ADR-0006 D2): the
verb is spawned with GITHUB_API_URL pointing here, and a test reads back what
it sent. Still a process boundary; still stdout, exit code, and files.

    python3 tests/fixtures/fake-github.py --dir DIR [--port N]

It binds 127.0.0.1 on an ephemeral port (or --port), then writes the port
number to DIR/port — the signal tests/lib.sh waits for before exporting
GITHUB_API_URL=http://127.0.0.1:<port>. Nothing here reaches the network: the
listener is loopback-only and no request is ever forwarded anywhere.

What it records, for every request, append-only:

    DIR/requests.log    one line each: METHOD PATH BODY, the body as compact
                        JSON with sorted keys, so a test can assert on a whole
                        call with assert_contains and no jq
    DIR/requests.jsonl  one JSON object each: method, path, query, headers
                        (names lowercased), body (parsed when it is JSON, the
                        raw string otherwise), for a test that wants one
                        field back out

What it answers:

    - a request with no Authorization header is 401, as GitHub's answer to an
      unauthenticated write is. A verb that forgot its token fails here
      rather than in production. One route is excepted, below: the App
      manifest conversion, whose whole point is that no credential exists
      yet (OPEN_ROUTES); a responses.json rule can still script a 401 for it.
    - DIR/responses.json, if present, is re-read on EVERY request: a list of
      {"method": "POST", "path": "/repos/o/r/issues/1/comments",
       "status": 500, "body": {...}, "headers": {"X-OAuth-Scopes": "repo"},
       "times": 2}
      objects, first match wins on method+path, every key optional. A test
      writes it to script a specific issue, pull-request list, label list,
      secret list, permissions or a failure, and removes it to let the next
      call through. "headers" are sent with the answer: that is how a test
      makes the fake look like a classic token's GitHub. "times" is how many
      requests the rule answers before it stops matching — "404 twice, then
      the default 200" is how a test watches a poll — counted in
      DIR/responses.state, which is reset whenever responses.json's bytes
      change, so a test that rewrites the rules starts every count over.
    - otherwise the routes below, with bodies shaped like GitHub's. The
      defaults are a private repository with issues enabled, Actions
      allowing every action, a read-only default token, NO secrets and NO
      labels (a test scripts the ones it wants to exist), and an empty
      pull-request and comment list. GET …/issues/N has NO default and is
      404 on purpose: a test that forgot to script its issue fails loudly
      rather than passing on an invented one.
    - anything else is 404 {"message": "Not Found"}.

The query string is recorded (requests.jsonl's "query") and ignored for
matching: "/labels?per_page=100" answers as "/labels".

The server exits on its own when its parent process does (it watches
getppid), so a test file that dies without reaching its trap leaks nothing;
tests/lib.sh kills it anyway.
"""

import argparse
import base64
import hashlib
import json
import os
import re
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

# The secrets public key is FIXED — 32 bytes, 1 through 32 — so a sealed
# value a test records is reproducible, and the key id is the one GitHub's
# own documentation uses as its example.
PUBLIC_KEY = {
    "key_id": "568250167242549743",
    "key": base64.b64encode(bytes(range(1, 33))).decode("ascii"),
}

# The App's private key, as POST /app-manifests/{code}/conversions answers
# it: a FIXED 2048-bit RSA key in the PKCS#1 form GitHub issues ("RSA
# PRIVATE KEY"), generated once with `openssl genrsa -traditional 2048` while
# building #12. It is a TEST KEY and nothing else: it has never been given to
# GitHub, signs nothing but the JWTs a test watches init poll with, and is
# committed as such. A test asserts that these bytes reach the fake only
# sealed, and reach no file under the scratch directory at all.
TEST_PEM = """-----BEGIN RSA PRIVATE KEY-----
MIIEogIBAAKCAQEA7O/Qoqy+MwFsW31PvW8iNkYtJr7En44Emt12w1h+yVQU1FeO
lfWnoLd1Um+MpfnLAhBIUgNgc/aiaKWpQ5zA/KUsHcti5C4rgHiKPR2X+cL252ay
7365FdJAscOeg5iUYjkabmIcrhmTDZle3o7TuU3CUFUmTmXaBD+vd6OayiPjVi59
pUC1e1nBYUl8WV3Bti4JTukInCCu+V/AzYfHCqWaFAMjUOTr3TN/zUgiHMhGYTzA
BRQ4ujDVVLFT+jrKMdtYkbunjSt9wVt1u9bzW1OgQiyCPu2uLCbwucAimJiz0oj0
dc/zU7eEtHeZh1M7UYj66h7PPZqcEtaWdrBjKwIDAQABAoIBAAihreyXNXCfPybQ
GF0S1OC8Sr3MGlajslg/9CkmqqvD8+ROckPVSK+Kr2v641lSkco39KKEO/nNhNoi
Gtn7Nm6dx88oDyi3497QxVx3nQc8yojgjWk7KRv55mBze21Y3CL96JAX4+qVxO0s
Zq/d7SmlgwwxJe2aOUiJV2eY3rU5aoT8v3P4XSjgTW8TLfe3ubvh4Q+WsJVCU4Wo
FyaDuSzbpXEhbfqCL1Kuw2/DQA6xXJpzNvA1akb0M4Ieo6r9kr2m+jLxvBQflt6f
AMBjhhNcuyOEzAJ679M0HrAPdcXal142PEX8spJVgAfSYEYPh+1o3epcu4sagd/A
DhfhUDkCgYEA/oy7+zgCh/nvTwjnMF9z37zSo4g/XAuj0e7TAv3SXRrxLMPP118L
Ashw2uqq6nrXIY40BaOHv5RSfDeDzdLnkPocXRe3U7Y3PJXK2TuiX/FS9sS1QOtE
EocESJjwaET5EacLqSRFEK2QhuDojdTA8ri00d+qYwDFYDWmHcFsqckCgYEA7klk
Tv0onJClVWhZLZbDZOJmEpKhuJRAFxr/K93gOtsVRdMi024pRm9NfOMvEBKXNC1/
feoP4RAoZB+uieR/e5DhXSrI3mPGvP4ddxdBjcwG70RSXd9h8ulIaWHT37btuSMd
YgbBkFmxbwWJI1jzwKRS607m8wmFMVQOVku2H1MCgYAJ2pbYVcW7T9CUxap1c+x/
Z8bbr8WMIaM42D7w6bSACC/35KiiFLrPY8UCpHzzUMgNM30ODtONdgdvaZK/n/94
3EXG0Mk3A2HGBaJxoF5bynLEvL6rgbAD65gd21XLI4hkx4urA41s5NsobYJzIx5J
Bx9w1HC7HmeFnu4MTtWPCQKBgH2e52jVAbH4d5EdH9Zu4reuu00TTHq4fUkxDFEd
+XZNxVs4QeXg75WYW+t5AXihtGDnk4zX9mMaV5DhOuyrL6H+8TBiu96yDzXXacU8
12xfoVBGXn3qpAKhhXE5B6+rCoxNvNHO6gClqGr1KiUeYfKvLp+qywpZfIRS4fQQ
mgTNAoGAc0021fD2fZssGg5BI5zBC3k65q5bj2pHmwkkm0hy0T25av1O9F+FuLXH
sxXD5TagsUSeeyQpHalE56cN8/PDJYZnVuPSrmIUtCTh7FKG47I0RDIFHZFFYkzn
qY3I382xBMtGmB99DW2SOfLIMMN3+hwW0A4rb7lIXIBnpTLZ+F0=
-----END RSA PRIVATE KEY-----"""

# The App the conversion registers, and the installation of it the poll
# waits for. Fixed, so a test can name them.
APP = {
    "id": 12345,
    "slug": "falconet-zetlen-wayfinders-infra",
    "name": "falconet-zetlen-wayfinders-infra",
    "node_id": "A_kwDOAAAAAc4AAAAB",
    "client_id": "Iv1.0123456789abcdef",
    "owner": {"login": "zetlen", "type": "User"},
    "html_url": "https://github.com/apps/falconet-zetlen-wayfinders-infra",
    "external_url": "https://github.com/zetlen/wayfinders-infra",
    "permissions": {"contents": "write", "issues": "write", "pull_requests": "write"},
    "events": [],
    # The two a verb must discard unused: neither is the credential.
    "client_secret": "CLIENT-SECRET-MARKER-0123456789",
    "webhook_secret": "WEBHOOK-SECRET-MARKER-0123456789",
    "pem": TEST_PEM,
}

# Routes that answer WITHOUT an Authorization header, the one exception to
# the 401 rule: the manifest conversion is the bootstrap of a credential,
# and the documentation lists no token for it. init tries it without a
# token first and a test asserts that it did; a responses.json rule is still
# consulted, so a test can script the 401 that would send init back with
# the setup token.
OPEN_ROUTES = [
    ("POST", r"^/app-manifests/([^/]+)/conversions$"),
]

ROUTES = [
    # (method, path regex, handler) — the handler gets the match and the
    # parsed body and returns (status, body).
    #
    # --- reads -------------------------------------------------------------
    ("GET", r"^/repos/([^/]+)/([^/]+)$",
     lambda m, b: (200, {
         "name": m[2], "full_name": f"{m[1]}/{m[2]}",
         "owner": {"login": m[1], "type": "User"},
         "html_url": f"https://github.com/{m[1]}/{m[2]}",
         "private": True, "visibility": "private", "has_issues": True,
         "default_branch": "main",
     })),
    # GET …/issues/N: deliberately no route (404) — see the docstring.
    ("GET", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/comments$",
     lambda m, b: (200, [])),
    ("GET", r"^/repos/([^/]+)/([^/]+)/pulls$",
     lambda m, b: (200, [])),
    ("GET", r"^/user$",
     lambda m, b: (200, {"login": "fake-user", "type": "User"})),
    ("GET", r"^/repos/([^/]+)/([^/]+)/actions/permissions$",
     lambda m, b: (200, {"enabled": True, "allowed_actions": "all"})),
    ("GET", r"^/repos/([^/]+)/([^/]+)/actions/permissions/selected-actions$",
     lambda m, b: (200, {"github_owned_allowed": True, "verified_allowed": False,
                         "patterns_allowed": []})),
    ("GET", r"^/repos/([^/]+)/([^/]+)/actions/permissions/workflow$",
     lambda m, b: (200, {"default_workflow_permissions": "read",
                         "can_approve_pull_request_reviews": False})),
    ("GET", r"^/repos/([^/]+)/([^/]+)/actions/secrets$",
     lambda m, b: (200, {"total_count": 0, "secrets": []})),
    ("GET", r"^/repos/([^/]+)/([^/]+)/actions/secrets/public-key$",
     lambda m, b: (200, dict(PUBLIC_KEY))),
    ("GET", r"^/repos/([^/]+)/([^/]+)/labels$",
     lambda m, b: (200, [])),
    # The App's installation on the repository: installed, by default. A
    # test scripts a 404 — with "times" — to watch init wait for the click.
    ("GET", r"^/repos/([^/]+)/([^/]+)/installation$",
     lambda m, b: (200, {
         "id": 777, "app_id": APP["id"], "target_type": "User",
         "account": {"login": m[1], "type": "User"},
         "repository_selection": "selected",
         "permissions": APP["permissions"], "events": [],
     })),
    # --- writes ------------------------------------------------------------
    # The manifest conversion: the App, its private key included. See
    # OPEN_ROUTES for why this answers without a token.
    ("POST", r"^/app-manifests/([^/]+)/conversions$",
     lambda m, b: (201, dict(APP))),
    ("POST", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/comments$",
     lambda m, b: (201, {
         "id": 1,
         "html_url": f"https://github.invalid/{m[1]}/{m[2]}/issues/{m[3]}#issuecomment-1",
         "body": (b or {}).get("body") if isinstance(b, dict) else None,
     })),
    ("POST", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/labels$",
     lambda m, b: (200, [{"name": name} for name in (b or {}).get("labels", [])]
                   if isinstance(b, dict) else [])),
    ("DELETE", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/assignees$",
     lambda m, b: (200, {"number": int(m[3]), "assignees": []})),
    ("DELETE", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/labels/([^/]+)$",
     lambda m, b: (200, [])),
    ("POST", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/assignees$",
     lambda m, b: (201, {
         "number": int(m[3]),
         "assignees": [{"login": login} for login in (b or {}).get("assignees", [])]
         if isinstance(b, dict) else [],
     })),
    ("POST", r"^/repos/([^/]+)/([^/]+)/labels$",
     lambda m, b: (201, {
         "id": 1,
         "name": (b or {}).get("name") if isinstance(b, dict) else None,
         "color": (b or {}).get("color", "ededed") if isinstance(b, dict) else "ededed",
         "description": (b or {}).get("description", "") if isinstance(b, dict) else "",
     })),
    ("PUT", r"^/repos/([^/]+)/([^/]+)/actions/secrets/([^/]+)$",
     lambda m, b: (201, {})),
]


class State:
    def __init__(self, directory):
        self.dir = directory
        self.lock = threading.Lock()

    def record(self, entry):
        line = "{} {} {}".format(
            entry["method"], entry["path"],
            json.dumps(entry["body"], sort_keys=True, separators=(",", ":")))
        with self.lock:
            with open(os.path.join(self.dir, "requests.log"), "a") as f:
                f.write(line + "\n")
            with open(os.path.join(self.dir, "requests.jsonl"), "a") as f:
                f.write(json.dumps(entry, sort_keys=True) + "\n")

    def scripted(self, method, path):
        # -> (status, body, headers) or None
        try:
            with open(os.path.join(self.dir, "responses.json"), "rb") as f:
                raw = f.read()
            rules = json.loads(raw.decode("utf-8"))
        except (OSError, ValueError):
            return None
        with self.lock:
            state = self._state(hashlib.sha256(raw).hexdigest())
            for i, rule in enumerate(rules):
                if "method" in rule and rule["method"] != method:
                    continue
                if "path" in rule and rule["path"] != path:
                    continue
                if "times" in rule:
                    used = state["used"].get(str(i), 0)
                    if used >= int(rule["times"]):
                        continue
                    state["used"][str(i)] = used + 1
                    self._save_state(state)
                return (int(rule.get("status", 200)), rule.get("body", {}),
                        rule.get("headers") or {})
        return None

    def _state(self, digest):
        # How many times each "times" rule has answered, keyed by the rule's
        # index, for the responses.json whose digest this is; any other
        # file's counts are stale and start over.
        try:
            with open(os.path.join(self.dir, "responses.state")) as f:
                state = json.load(f)
            if state.get("rules") == digest:
                return state
        except (OSError, ValueError):
            pass
        return {"rules": digest, "used": {}}

    def _save_state(self, state):
        tmp = os.path.join(self.dir, "responses.state.tmp")
        with open(tmp, "w") as f:
            json.dump(state, f)
        os.replace(tmp, os.path.join(self.dir, "responses.state"))


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    state = None  # set before serving

    def log_message(self, *args):  # quiet: the log files are the record
        pass

    def _handle(self):
        method = self.command
        parts = urlsplit(self.path)
        path, query = parts.path, parts.query
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        try:
            body = json.loads(raw.decode("utf-8")) if raw else None
        except ValueError:
            body = raw.decode("utf-8", "replace")
        self.state.record({
            "method": method,
            "path": path,
            "query": query,
            "headers": {k.lower(): v for k, v in self.headers.items()},
            "body": body,
        })

        extra = {}
        open_route = any(m == method and re.match(pattern, path)
                         for m, pattern in OPEN_ROUTES)
        if not self.headers.get("Authorization") and not open_route:
            status, answer = 401, {"message": "Requires authentication"}
        else:
            found = self.state.scripted(method, path)
            if found is not None:
                status, answer, extra = found
            else:
                found = None
                for m, pattern, handler in ROUTES:
                    match = re.match(pattern, path)
                    if m == method and match:
                        found = handler(match, body)
                        break
                if found is None:
                    found = 404, {"message": "Not Found",
                                  "documentation_url": "https://docs.github.com/rest"}
                status, answer = found

        payload = json.dumps(answer).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(payload)))
        for k, v in extra.items():
            self.send_header(k, v)
        self.end_headers()
        if method != "HEAD":
            self.wfile.write(payload)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = do_HEAD = _handle


def watch_parent(parent):
    # A test file that dies before its EXIT trap would otherwise leave this
    # process listening forever. Re-parented means the parent is gone.
    while True:
        time.sleep(0.2)
        if os.getppid() != parent:
            os._exit(0)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir", required=True, help="where to record, and where DIR/port is written")
    ap.add_argument("--port", type=int, default=0, help="0 (the default) picks a free one")
    args = ap.parse_args()

    os.makedirs(args.dir, exist_ok=True)
    Handler.state = State(args.dir)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.daemon_threads = True

    threading.Thread(target=watch_parent, args=(os.getppid(),), daemon=True).start()

    # The port file is the readiness signal, so it is written only once the
    # socket is bound, and atomically, so a reader never sees half a number.
    tmp = os.path.join(args.dir, "port.tmp")
    with open(tmp, "w") as f:
        f.write(str(server.server_address[1]) + "\n")
    os.replace(tmp, os.path.join(args.dir, "port"))

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
