#!/usr/bin/env bash
#
# handoff.sh — the directory the verbs talk through, and the one place that
# knows $GITHUB_ENV is optional. Sourced; never executed.
#
# Verbs never call each other. They leave files for each other in
# handoff_dir (default .falconet/, gitignored), exactly as the stage scripts
# always did, which is what lets the identical sequence run on a workstation
# with no GitHub context around it.
#
# Requires config_init to have run: the directory is configurable.

# handoff_init sets HANDOFF and creates it. An explicit override — the
# --out-dir flag several verbs carry — wins over the config, and is resolved
# to an absolute path here because the verbs cd to the repository root and a
# relative path would then mean somewhere else.
handoff_init() { # [explicit-dir]
  local explicit="${1:-}"
  if [ -n "$explicit" ]; then
    HANDOFF="$explicit"
  else
    HANDOFF="$(config_get '.handoff_dir')"
  fi
  case "$HANDOFF" in
    /*) ;;
    *) HANDOFF="$PWD/$HANDOFF" ;;
  esac
  mkdir -p "$HANDOFF" || {
    echo "falconet: cannot create handoff directory $HANDOFF" >&2; exit 1; }
  export HANDOFF
}

# CI-facing exports append to $GITHUB_ENV when there is one and it can be
# written, and are a silent no-op otherwise. Handoff FILES are written always;
# this is only the CI mirror of them. A verb that made a decision must not
# fail because it happens to be running on a laptop.
github_env_append() { # KEY=value...
  [ -n "${GITHUB_ENV:-}" ] || return 0
  # -w on a path that does not exist yet is false, so test the directory too:
  # Actions creates the file, but a local run pointing at a fresh path should
  # still work.
  if [ -e "$GITHUB_ENV" ]; then
    [ -w "$GITHUB_ENV" ] || return 0
  else
    [ -w "$(dirname "$GITHUB_ENV")" ] || return 0
  fi
  printf '%s\n' "$@" >>"$GITHUB_ENV"
}
