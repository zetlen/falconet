#!/usr/bin/env bash
#
# config.sh — print what lib/config.sh and lib/handoff.sh would tell a verb.
#
# Modes:
#   config.sh [--config FILE] file              the path that was read, or nothing
#   config.sh [--config FILE] get <jq-path>     one value
#   config.sh [--config FILE] array <jq-path>   one element per line, in order
#   config.sh [--config FILE] handoff [DIR]     the resolved handoff directory
#   config.sh [--config FILE] env <KEY=value>   append to $GITHUB_ENV, if any
#
# Unlisted on purpose, like `prompt`: public in the sense that it works, not
# in the sense that it is vocabulary. config.sh and handoff.sh are sourced
# libraries, so there is no process to spawn at them directly — and the
# suite's rule is that no test reaches inside its subject. This is that
# process. It used to be tests/fixtures/config-probe, a stand-in caller that
# sourced the libraries; it lives behind the dispatcher now so that the suite
# spawns it the way it spawns every verb (ADR-0006 D3 step 0), and so that an
# implementation of `falconet config` in another language answers the same
# tests.
#
# --config is the flag each verb parses for itself, with the same meaning: an
# explicit file that beats $FALCONET_CONFIG and ./.github/falconet.json.
# Resolution is relative to the working directory, as it is for a verb.
#
# Exit codes: 0 = printed, 1 = the config could not be read, 2 = usage.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FALCONET_HOME="$(dirname "$(dirname "$SCRIPT_DIR")")"

. "$FALCONET_HOME/lib/config.sh"
. "$FALCONET_HOME/lib/handoff.sh"

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

CONFIG=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)  CONFIG="${2:?--config needs a file}"; shift 2 ;;
    -h|--help) usage >&2; exit 2 ;;
    -*)        echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
    *)         break ;;
  esac
done

config_init "$CONFIG"

case "${1:-}" in
  file)    printf '%s\n' "$FALCONET_CONFIG_FILE" ;;
  get)     config_get "${2:?get needs a jq path}" ;;
  array)   config_get_array "${2:?array needs a jq path}" ;;
  handoff) handoff_init "${2:-}"; printf '%s\n' "$HANDOFF" ;;
  env)     handoff_init ""; github_env_append "${2:?env needs KEY=value}" ;;
  *)       echo "config: unknown operation '${1:-}'" >&2; usage >&2; exit 2 ;;
esac
