#!/usr/bin/env bash
# browser.sh — the dumbest browser a test can ask for, spawned as
# $FALCONET_BROWSER by tests/setup-github.test.sh. Given a URL it:
#   - if the page carries a form with a `manifest` textarea (the
#     setup-github.sh form), POSTs it, follows the redirect Location, and
#     reports; or
#   - otherwise just GETs the page (the App install page).
# curl only; no JS, no cookies, and no opinion about what it just did.
set -euo pipefail

url="$1"
page="$(curl -fs "$url")"

action="$(sed -n 's/.*<form[^>]* action="\([^"]*\)".*/\1/p' <<<"$page")"
if [ -n "$action" ]; then
    manifest="$(sed -n 's,.*<textarea name="manifest"[^>]*>\(.*\)</textarea>.*,\1,p' <<<"$page")"
    [ -n "$manifest" ] || { echo "browser.sh: form but no manifest textarea" >&2; exit 1; }
    headers="$(curl -fs -o /dev/null -D - -X POST --data-urlencode "manifest=$manifest" "$action")"
    location="$(printf '%s' "$headers" | sed -n 's/^[Ll]ocation: \(.*\)\r$/\1/p')"
    [ -n "$location" ] || { echo "browser.sh: no Location on the form POST" >&2; exit 1; }
    curl -fs -o /dev/null "$location"
    exit 0
fi

curl -fs -o /dev/null "$url"
