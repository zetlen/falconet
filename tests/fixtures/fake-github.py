#!/usr/bin/env python3
"""
fake-github.py — a GitHub REST API on loopback that answers from fixtures and
writes down what it was asked. python3 stdlib only. The verbs shell out to
`gh api` with full URLs built from GITHUB_API_URL, which points here; a test
reads back what was sent. Still a process boundary; still stdout, exit code,
and files.

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
      rather than in production.
    - DIR/responses.json, if present, is re-read on EVERY request: a list of
      {"method": "POST", "path": "/repos/o/r/issues/1/comments",
       "status": 500, "body": {...}, "headers": {"X-OAuth-Scopes": "repo"},
       "times": 2}
      objects, first match wins on method+path, every key optional. A test
      writes it to script a specific issue, pull-request list or a failure,
      and removes it to let the next call through. "headers" are sent with
      the answer. "times" is how many requests the rule answers before it
      stops matching — "404 twice, then the default 200" is how a test
      watches a poll — counted in DIR/responses.state, which is reset
      whenever responses.json's bytes change, so a test that rewrites the
      rules starts every count over.
    - otherwise the routes below, with bodies shaped like GitHub's. The
      defaults are an empty pull-request and comment list. GET …/issues/N
      has NO default and is 404 on purpose: a test that forgot to script its
      issue fails loudly rather than passing on an invented one.
    - anything else is 404 {"message": "Not Found"}.

The query string is recorded (requests.jsonl's "query") and ignored for
matching: "/labels?per_page=100" answers as "/labels".

The server exits on its own when its parent process does (it watches
getppid), so a test file that dies without reaching its trap leaks nothing;
tests/lib.sh kills it anyway.
"""

import argparse
import hashlib
import json
import os
import re
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

ROUTES = [
    ("POST", r"^/app-manifests/([^/]+)/conversions$"),
]

ROUTES = [
    # (method, path regex, handler) — the handler gets the match and the
    # parsed body and returns (status, body).
    #
    # --- reads -------------------------------------------------------------
    # GET …/issues/N: deliberately no route (404) — see the docstring.
    ("GET", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/comments$",
     lambda m, b: (200, [])),
    ("GET", r"^/repos/([^/]+)/([^/]+)/pulls$",
     lambda m, b: (200, [])),
    ("GET", r"^/user$",
     lambda m, b: (200, {"login": "fake-user", "type": "User"})),
    # --- writes ------------------------------------------------------------
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
        if not self.headers.get("Authorization"):
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
