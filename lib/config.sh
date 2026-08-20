#!/usr/bin/env bash
#
# config.sh — one JSON file, every key optional, defaults that reproduce the
# origin repository's behavior exactly. Sourced by the verbs that need it;
# never executed.
#
# The defaults live here as a JSON document rather than as a fallback argument
# at each call site, and the user's file is merged OVER them with jq's `*`.
# That is deliberate: a default spelled at the call site drifts from the
# schema in docs/adr/0003-the-cli-surface.md the first time someone edits one
# and not the other, and a config key whose default nobody can find is a key
# nobody configures.
#
# jq's `*` recurses into objects and REPLACES arrays wholesale, which is the
# behavior these keys want. Setting paths.allow means "this list instead of
# the default", never "these in addition to *.tf" — an allowlist that grows by
# accident is not an allowlist.
#
# Discovery, in precedence order:
#   1. --config PATH        (each verb parses its own flag and passes it here)
#   2. $FALCONET_CONFIG
#   3. ./.github/falconet.json
#   4. nothing — the defaults below stand alone
#
# Resolution 3 is relative to the working directory, not to this file: verbs
# cd to the repository root before they read config, and a tool run inside a
# repository should read that repository's configuration rather than the one
# beside its own install.
#
# A malformed config is exit 1 with jq's parse error. It is never a silent
# fall back to defaults: "your config is being ignored" is exactly the failure
# that gets discovered in production.

falconet_default_config() {
  cat <<'JSON'
{
  "handoff_dir": ".falconet",
  "issue": {
    "queue_label": "infra-request",
    "opt_out_text": "Not eligible for AI agents",
    "branch_prefix": "issue-",
    "in_flight_prefixes": ["issue-", "claude/issue-"],
    "blocking_labels": ["needs-info", "ready-for-human", "do-not-apply", "wontfix"]
  },
  "labels": {
    "needs_info": "needs-info",
    "human": "ready-for-human",
    "pr": "needs-plan-review"
  },
  "paths": {
    "allow": ["*.tf"],
    "deny_content": [
      "data \"external\"",
      "provisioner",
      "local-exec",
      "remote-exec",
      "templatefile(",
      "filebase64(",
      "file("
    ]
  },
  "stacks": {
    "plan": ["dns"],
    "validate_only": ["workspace", "site"]
  },
  "plan": {
    "command": "tofu -chdir={stack} plan -no-color -input=false -refresh=false -lock=false"
  },
  "prompts": {
    "implement": "prompts/implement.md",
    "park_needs_info": "prompts/park-needs-info.md"
  }
}
JSON
}

# Set by config_init. FALCONET_CONFIG_FILE is the path actually read, or empty
# when no file was found — some verbs report it, and every test wants to know
# which of the four resolutions fired.
FALCONET_CONFIG_DOC=""
FALCONET_CONFIG_FILE=""

config_init() { # [explicit-path]
  local explicit="${1:-}" path=""

  if [ -n "$explicit" ]; then
    path="$explicit"
    [ -f "$path" ] || {
      echo "falconet: --config names no file: $path" >&2; exit 1; }
  elif [ -n "${FALCONET_CONFIG:-}" ]; then
    path="$FALCONET_CONFIG"
    [ -f "$path" ] || {
      echo "falconet: \$FALCONET_CONFIG names no file: $path" >&2; exit 1; }
  elif [ -f .github/falconet.json ]; then
    path=".github/falconet.json"
  fi

  FALCONET_CONFIG_FILE="$path"

  if [ -z "$path" ]; then
    FALCONET_CONFIG_DOC="$(falconet_default_config)"
    return 0
  fi

  local merged err
  # stderr is captured so the message names OUR file rather than leaving jq's
  # bare "parse error" in a log with nothing around it.
  if ! merged="$(falconet_default_config \
      | jq -s --slurpfile _u "$path" '.[0] * $_u[0]' 2>/dev/null)"; then
    err="$(jq empty "$path" 2>&1)"
    echo "falconet: $path is not valid JSON: ${err:-parse error}" >&2
    exit 1
  fi
  FALCONET_CONFIG_DOC="$merged"
}

# A scalar. Absent is impossible for a key in the schema — the defaults cover
# every one — so an empty result means the user set it empty, and that is
# their answer.
config_get() { # jq-path
  printf '%s' "$FALCONET_CONFIG_DOC" | jq -r "$1"
}

# An array, one element per line, IN ORDER. Order is load-bearing for
# paths.deny_content: `templatefile(` is tested before `file(`, and reversing
# them reports a templatefile() call as file() — the right refusal naming the
# wrong construct.
config_get_array() { # jq-path
  printf '%s' "$FALCONET_CONFIG_DOC" | jq -r "$1 // [] | .[]"
}
