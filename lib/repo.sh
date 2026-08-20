#!/usr/bin/env bash
#
# repo.sh — which repository is this verb operating on? Sourced; never run.
#
# The origin's scripts lived INSIDE the repository they worked on, so "one
# directory above scripts/" answered both questions at once: where the code
# is, and where the work is. falconet is a separate tool. In CI the composite
# action checks it out somewhere of its own, and in the strangler's endgame it
# is a compiled binary on $PATH. The two answers come apart, and a verb that
# keeps using its own location to find the working tree operates on falconet
# instead of on the consumer's repository — silently, reporting an outcome
# about the wrong tree.
#
# So there are two roots and they are never the same variable:
#
#   FALCONET_HOME  where the tool lives — lib/, libexec/, prompts/.
#                  Each verb derives it from its own $BASH_SOURCE.
#   REPO_ROOT      the repository being worked on. From the working
#                  directory, which is the only thing that can know.
#
# $FALCONET_REPO overrides, for a caller that wants to name it explicitly.

repo_root_init() {
  if [ -n "${FALCONET_REPO:-}" ]; then
    REPO_ROOT="$FALCONET_REPO"
    [ -d "$REPO_ROOT" ] || {
      echo "falconet: \$FALCONET_REPO names no directory: $REPO_ROOT" >&2
      exit 1; }
    REPO_ROOT="$(cd "$REPO_ROOT" && pwd -P)"
  elif REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" \
       && [ -n "$REPO_ROOT" ]; then
    :
  else
    # Not in a git repository. Some verbs need one and will say so in their
    # own words; assemble and prompt do not, and should still work here.
    REPO_ROOT="$PWD"
  fi
  export REPO_ROOT
}
