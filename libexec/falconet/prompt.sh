#!/usr/bin/env bash
#
# prompt.sh — print a prompt, from the config's override if there is one and
# from the shipped copy otherwise.
#
# Modes:
#   prompt.sh NAME [--config FILE] [--out-dir DIR]
#
# Unlisted on purpose. It exists so the action wrappers can keep prompts
# config-driven without embedding heredocs in YAML, which is how a prompt
# picks up the indentation of the block scalar it was written in and starts
# rendering as a code block. It is public in the sense that it works, not in
# the sense that it is vocabulary.
#
# The name is looked up at `prompts.<name>` in the config — with `-` folded to
# `_`, so `falconet prompt pause-needs-info` finds `prompts.pause_needs_info`.
# An override is a path relative to the repository root. With no override the
# shipped `prompts/<name>.md` is printed.
#
# Two placeholders are substituted on the way out, which is what lets one
# prompt serve CI and a workstation:
#
#   {handoff}     the absolute handoff directory
#   {workspace}   the absolute repository root
#
# The origin's prompt spelled these as `${{ github.workspace }}/.ci-handoff/`,
# an Actions template expression that means nothing anywhere else — and the
# whole point of a CLI-first design is that the same prompt text is what runs
# locally.
#
# Exit codes: 0 = printed, 1 = no such prompt, 2 = usage error.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FALCONET_HOME="$(dirname "$(dirname "$SCRIPT_DIR")")"

. "$FALCONET_HOME/lib/config.sh"
. "$FALCONET_HOME/lib/repo.sh"
. "$FALCONET_HOME/lib/handoff.sh"

repo_root_init

NAME=""
CONFIG=""
OUT_DIR=""

usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)  CONFIG="${2:?--config needs a file}"; shift 2 ;;
    --out-dir) OUT_DIR="${2:?--out-dir needs a directory}"; shift 2 ;;
    -h|--help) usage >&2; exit 2 ;;
    -*) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
    *)
      [[ -z "$NAME" ]] || { echo "one prompt at a time" >&2; exit 2; }
      NAME="$1"; shift ;;
  esac
done

[[ -n "$NAME" ]] || { usage >&2; exit 2; }

case "$OUT_DIR" in ""|/*) ;; *) OUT_DIR="$PWD/$OUT_DIR" ;; esac
cd "$REPO_ROOT" || exit 1
config_init "$CONFIG"

key="${NAME//-/_}"
override="$(config_get ".prompts.\"$key\" // \"\"")"

if [[ -n "$override" && "$override" != "null" ]]; then
  path="$override"
  case "$path" in /*) ;; *) path="$REPO_ROOT/$path" ;; esac
  [[ -f "$path" ]] || { echo "prompt: '$NAME' points at a file that is not there: $override" >&2; exit 1; }
else
  path="$FALCONET_HOME/prompts/$NAME.md"
  [[ -f "$path" ]] || { echo "prompt: no prompt named '$NAME'" >&2; exit 1; }
fi

# The handoff directory is resolved but NOT created: printing a prompt is a
# read, and a caller asking what the text says should not leave a directory
# behind. handoff_init creates, so this repeats its resolution instead.
hd="$(config_get '.handoff_dir')"
case "$hd" in /*) ;; *) hd="$REPO_ROOT/$hd" ;; esac
[[ -n "$OUT_DIR" ]] && hd="$OUT_DIR"

out="$(cat "$path")"
out="${out//\{handoff\}/$hd}"
out="${out//\{workspace\}/$REPO_ROOT}"
printf '%s\n' "$out"
