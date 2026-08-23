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
      rather than in production.
    - DIR/responses.json, if present, is re-read on EVERY request: a list of
      {"method": "POST", "path": "/repos/o/r/issues/1/comments",
       "status": 500, "body": {...}} objects, first match wins, either key
      optional. A test writes it to make one call fail and removes it to let
      the next one through.
    - otherwise the routes below, with bodies shaped like GitHub's.
    - anything else is 404 {"message": "Not Found"}.

The server exits on its own when its parent process does (it watches
getppid), so a test file that dies without reaching its trap leaks nothing;
tests/lib.sh kills it anyway.
"""

import argparse
import json
import os
import re
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

ROUTES = [
    # (method, path regex, handler) — the handler gets the match and the
    # parsed body and returns (status, body).
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
        try:
            with open(os.path.join(self.dir, "responses.json")) as f:
                rules = json.load(f)
        except (OSError, ValueError):
            return None
        for rule in rules:
            if "method" in rule and rule["method"] != method:
                continue
            if "path" in rule and rule["path"] != path:
                continue
            return int(rule.get("status", 200)), rule.get("body", {})
        return None


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

        if not self.headers.get("Authorization"):
            status, answer = 401, {"message": "Requires authentication"}
        else:
            found = self.state.scripted(method, path)
            if found is None:
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
