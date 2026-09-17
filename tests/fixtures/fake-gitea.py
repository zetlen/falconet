#!/usr/bin/env python3
"""
fake-gitea.py — a Gitea REST API on loopback: fake-github.py's server with
Gitea's routes and Gitea's answers. python3 stdlib only.

    python3 tests/fixtures/fake-gitea.py --dir DIR [--port N]

Everything fake-github.py's docstring says of the port file, requests.log,
requests.jsonl, responses.json and its "times", the 401 without an
Authorization header, and exiting with its parent holds here unchanged,
because this file loads fake-github.py by path and runs its server. What it
replaces is the route table, the not-found answer, and the path:

    - Gitea's API lives under /api/v1, and tests/lib.sh points
      GITHUB_API_URL at http://127.0.0.1:<port>/api/v1. The prefix is
      stripped before a request is recorded or matched, so requests.log and
      responses.json spell paths as /repos/o/r/..., as on the GitHub fake. A
      request outside /api/v1 keeps its whole path and matches nothing.
    - a request no route and no rule matches is 404 with Gitea's body,
      {"errors", "message", "url"}.

The routes, and the Gitea behaviour each one copies (Gitea v1.26.4):

    GET    /user                              the bot, falconet-bot
    GET    /repos/o/r/issues/N                no default: a case scripts it
    PATCH  /repos/o/r/issues/N                201, echoing `assignees`; Gitea
                                              has no route that adds or
                                              removes one assignee
    GET    /repos/o/r/issues/N/comments       [], and no paging: Gitea's route
                                              answers the whole thread
    POST   /repos/o/r/issues/N/comments       201
    POST   /repos/o/r/issues/N/labels         200 with the posted names the
                                              repository has, as ids and
                                              names. Gitea answers every
                                              label on the issue (gitea
                                              routers/api/v1/repo/
                                              issue_label.go:117), which
                                              holds the same names and may
                                              hold more. Like Gitea, the fake
                                              drops an unknown name and
                                              still answers 200
                                              (issue_label.go:348)
    DELETE /repos/o/r/issues/N/labels/{id}    204 for a repository label's id,
                                              422 for any other (gitea
                                              issue_label.go:184)
    GET    /repos/o/r/pulls                   [] on every page, so a list
                                              ends on its first page
    GET    /repos/o/r/collaborators/{login}/permission
                                              no default: a case scripts the
                                              sender's permission

The repository's labels are LABELS below. A case that scripts a paged list
route in responses.json gives the rule "times": 1, or every page answers the
same list and the client reads to its page cap.
"""

import importlib.util
import os
import sys
from urllib.parse import urlsplit

HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location("fake_github", os.path.join(HERE, "fake-github.py"))
base = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(base)

API = "/api/v1"

BOT = {"id": 1, "login": "falconet-bot", "login_name": "", "full_name": "", "email": "bot@example.invalid"}

# The repository's labels, by name.
LABELS = {"falconet": 1, "needs-info": 2, "ready-for-human": 3, "falconet-pr": 4}


def _add_labels(m, b, q):
    names = (b or {}).get("labels", []) if isinstance(b, dict) else []
    kept = [{"id": LABELS[n], "name": n} for n in names if isinstance(n, str) and n in LABELS]
    return (200, kept)


def _delete_label(m, b, q):
    if int(m[4]) in LABELS.values():
        return (204, None)
    return (422, {"errors": None, "message": "label does not exist [id: %s]" % m[4], "url": ""})


base.ROUTES = [
    ("GET", r"^/user$", lambda m, b, q: (200, BOT)),
    # GET …/issues/N: deliberately no route (404), as on the GitHub fake.
    ("PATCH", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)$",
     lambda m, b, q: (201, {
         "number": int(m[3]),
         "assignees": [{"login": login} for login in (b or {}).get("assignees", [])]
         if isinstance(b, dict) else [],
     })),
    ("GET", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/comments$",
     lambda m, b, q: (200, [])),
    ("POST", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/comments$",
     lambda m, b, q: (201, {
         "id": 1,
         "user": BOT,
         "body": (b or {}).get("body") if isinstance(b, dict) else None,
     })),
    ("POST", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/labels$", _add_labels),
    ("DELETE", r"^/repos/([^/]+)/([^/]+)/issues/(\d+)/labels/(\d+)$", _delete_label),
    ("GET", r"^/repos/([^/]+)/([^/]+)/pulls$",
     lambda m, b, q: (200, [])),
    # GET …/collaborators/{login}/permission: no route, as on the GitHub fake.
]

base.NOT_FOUND = {"errors": None, "message": "The target couldn't be found.", "url": "http://127.0.0.1/api/swagger"}


class Handler(base.Handler):
    def _gitea(self):
        path = urlsplit(self.path).path
        if path == API or path.startswith(API + "/"):
            self.path = self.path[len(API):] or "/"
        base.Handler._handle(self)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = do_HEAD = _gitea


base.Handler = Handler

if __name__ == "__main__":
    sys.exit(base.main())
