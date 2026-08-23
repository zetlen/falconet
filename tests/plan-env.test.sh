#!/usr/bin/env bash
#
# plan-env.test.sh — the one secret, masked and turned into environment.
#
# `falconet plan-env` is the workflow's "Credentials for the stacks that
# plan" step: FALCONET_PLAN_ENV is one JSON object of environment variables,
# and the two jobs that run tofu turn it into ordinary environment so that
# tofu sees what it would see on a workstation and no verb has to know the
# name of a cloud. It was bash in YAML until #19, and its two invariants were
# held by contract.test.sh grepping the workflow for `::add-mask::`. They are
# held here now, by running the thing, and both are bytes:
#
#   - every non-empty line of every value reaches stdout as `::add-mask::`
#     BEFORE the value is written anywhere (add-mask is per line, and a PEM
#     is many lines);
#   - each variable reaches $GITHUB_ENV in the delimiter form, so a multi-line
#     value cannot become further variables.
#
# And the one thing a secret handler must never do: say a value. Every
# refusal below is checked for the value it refused.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

# Runs plan-env with the secret given (an unset secret when the argument is
# omitted), a fresh $GITHUB_ENV at $WORK/env unless NO_GITHUB_ENV is set, and
# leaves OUT, ERR, RC and the env file's bytes in ENV.
run() { # [secret]
  : >"$WORK/env"
  if [[ $# -eq 0 ]]; then
    OUT="$( unset FALCONET_PLAN_ENV; GITHUB_ENV="$WORK/env" "$FALCONET" plan-env 2>"$WORK/err" )"; RC=$?
  elif [[ -n "${NO_GITHUB_ENV:-}" ]]; then
    OUT="$( unset GITHUB_ENV; FALCONET_PLAN_ENV="$1" "$FALCONET" plan-env 2>"$WORK/err" )"; RC=$?
  else
    OUT="$( FALCONET_PLAN_ENV="$1" GITHUB_ENV="$WORK/env" "$FALCONET" plan-env 2>"$WORK/err" )"; RC=$?
  fi
  ERR="$(cat "$WORK/err")"
  ENV="$(cat "$WORK/env")"
  return 0
}

# --- an object: the masks, the word, and the bytes ---------------------------

run '{"B_TOKEN": "second", "A_KEY": "first"}'

it "an object is exported, one variable at a time, each masked first and then named"
assert_eq 0 "$RC" "exit code"
assert_eq "::add-mask::first
plan-env: set A_KEY
::add-mask::second
plan-env: set B_TOKEN" "$OUT" "stdout"

it "and lands in \$GITHUB_ENV in the delimiter form, exactly"
assert_eq "A_KEY<<FALCONET_PLAN_ENV_EOF
first
FALCONET_PLAN_ENV_EOF
B_TOKEN<<FALCONET_PLAN_ENV_EOF
second
FALCONET_PLAN_ENV_EOF" "$ENV" "\$GITHUB_ENV"

it "with nothing on stderr"
assert_eq "" "$ERR" "stderr"

it "and nothing of a value on stdout except the mask that hides it"
assert_eq 2 "$(grep -c 'first\|second' <<<"$OUT")" "lines naming a value"
assert_eq 2 "$(grep -c '^::add-mask::' <<<"$OUT")" "mask lines"

# --- a multi-line value: one mask per non-empty line --------------------------

pem='-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBg

kqhkiG9w0BAQEF
-----END PRIVATE KEY-----'
run "$(printf '{"KEY": %s}' "$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$pem")")"

it "a multi-line value is masked once per non-empty line, because add-mask is per line"
assert_eq 0 "$RC" "exit code"
assert_eq "::add-mask::-----BEGIN PRIVATE KEY-----
::add-mask::MIIEvQIBADANBg
::add-mask::kqhkiG9w0BAQEF
::add-mask::-----END PRIVATE KEY-----
plan-env: set KEY" "$OUT" "stdout"

it "and the whole value, blank line included, is between the delimiters"
assert_eq "KEY<<FALCONET_PLAN_ENV_EOF
$pem
FALCONET_PLAN_ENV_EOF" "$ENV" "\$GITHUB_ENV"

it "the masks come before the word that says the value has been written"
last_mask="$(grep -n '^::add-mask::' <<<"$OUT" | tail -1 | cut -d: -f1)"
set_line="$(grep -n '^plan-env: set KEY$' <<<"$OUT" | cut -d: -f1)"
assert_eq "true" "$([[ "$last_mask" -lt "$set_line" ]] && echo true || echo false)" \
  "last mask at $last_mask, set at $set_line"

# A value's trailing newline is part of the value. The bash read each value
# through $(...), which strips trailing newlines, so a PEM stored with its
# final newline was exported without it; the binary carries the bytes the
# operator stored.
run '{"KEY": "abc\n"}'

it "a trailing newline in a value is carried, not eaten"
assert_eq "KEY<<FALCONET_PLAN_ENV_EOF
abc

FALCONET_PLAN_ENV_EOF" "$ENV" "\$GITHUB_ENV"
assert_eq "::add-mask::abc
plan-env: set KEY" "$OUT" "stdout"

# --- no secret ---------------------------------------------------------------

run

it "no secret at all says so and exits 0: a repository may plan without credentials"
assert_eq 0 "$RC" "exit code"
assert_eq "no plan-env secret: the stacks must init and plan without one" "$OUT" "stdout"
assert_eq "" "$ENV" "\$GITHUB_ENV"

run ''

it "and an empty secret is the same thing"
assert_eq 0 "$RC" "exit code"
assert_eq "no plan-env secret: the stacks must init and plan without one" "$OUT" "stdout"

run '{}'

it "an empty object exports nothing and says nothing, and that is not an error"
assert_eq 0 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_eq "" "$ENV" "\$GITHUB_ENV"

# --- the shape, refused by name and never by value ---------------------------

run '["AWS_ACCESS_KEY_ID=super-secret-value"]'

it "an array is refused, naming the shape"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "must be a JSON object" "stderr"
assert_contains "$ERR" "array" "stderr"

it "and the refusal writes nothing, prints nothing, and never says the value"
assert_eq "" "$OUT" "stdout"
assert_eq "" "$ENV" "\$GITHUB_ENV"
assert_not_contains "$ERR" "super-secret-value" "stderr"

run '{"OK": "fine", "PORT": 8080}'

it "a value that is not a string is refused, naming the key and the shape"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "value of PORT is a number" "stderr"
assert_not_contains "$ERR" "8080" "stderr"

it "and nothing is exported — not even the entry that was fine"
assert_eq "" "$ENV" "\$GITHUB_ENV"
assert_eq "" "$OUT" "stdout"

run '{"AWS-KEY": "super-secret-value"}'

it "a key that is not an environment-variable name is refused by name"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" '"AWS-KEY" is not an environment-variable name' "stderr"
assert_not_contains "$ERR" "super-secret-value" "stderr"

run '{"A": "fine", "B": "x\nFALCONET_PLAN_ENV_EOF\nEVIL=1"}'

it "a value containing the delimiter is refused, because it would end itself early"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "the value of B contains the delimiter" "stderr"
assert_not_contains "$ERR" "EVIL" "stderr"

it "and nothing is exported, including the entry that sorts before it"
assert_eq "" "$ENV" "\$GITHUB_ENV"
assert_eq "" "$OUT" "stdout"

run '{"A": "not json'

it "something that is not JSON says where, not what"
assert_eq 1 "$RC" "exit code"
assert_contains "$ERR" "is not valid JSON" "stderr"
assert_not_contains "$ERR" "not json" "stderr"

# --- no $GITHUB_ENV: the masks are not optional -------------------------------

NO_GITHUB_ENV=1 run '{"TOKEN": "super-secret-value"}'

it "without a \$GITHUB_ENV the masks are still printed and the exit is 0"
assert_eq 0 "$RC" "exit code"
assert_eq "::add-mask::super-secret-value
plan-env: set TOKEN" "$OUT" "stdout"
assert_eq "" "$ERR" "stderr"

# --- usage ------------------------------------------------------------------

OUT="$("$FALCONET" plan-env --help 2>"$WORK/err")"; RC=$?

it "--help is usage on stderr, exit 2"
assert_eq 2 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$(cat "$WORK/err")" "plan-env —" "stderr"

OUT="$(FALCONET_PLAN_ENV='{"A":"1"}' "$FALCONET" plan-env extra 2>"$WORK/err")"; RC=$?

it "and so is any argument: the verb reads the environment and nothing else"
assert_eq 2 "$RC" "exit code"
assert_eq "" "$OUT" "stdout"
assert_contains "$(cat "$WORK/err")" "unknown argument: extra" "stderr"

summary
