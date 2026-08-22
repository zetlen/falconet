#!/usr/bin/env bash
#
# scan.sh — the dispatcher's door to lib/scan.sh, and nothing else.
#
# The secret scan is internal (ADR-0003): not vocabulary, and the commit verb
# is its only caller, which invokes lib/scan.sh by path and captures its
# stdout. This file exists so that `falconet scan` works — unlisted, like
# `prompt` — and the test suite can spawn the scan through the same door it
# spawns every verb, rather than by a path inside the tool (ADR-0006 D3
# step 0). It hands over with exec, so the scan owns its stdout, its
# arguments, and its 0 / 1 / 3 exit codes, unchanged. Run `falconet scan -h`
# for those.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FALCONET_HOME="$(dirname "$(dirname "$SCRIPT_DIR")")"

exec "$FALCONET_HOME/lib/scan.sh" "$@"
