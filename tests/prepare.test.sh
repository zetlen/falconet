#!/usr/bin/env bash
#
# prepare.test.sh — the eligibility gate and the claim.
#
# This is the one verb with no ancestor script: its eligibility half was a
# job-level `if:` expression and its ready half was inline YAML. So none of
# this is a ported test, and every assertion is a decision. Where the two
# origin sources disagreed — the workflow and the human-facing skill that
# worked the same queue — the case name says which one is encoded and why.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null

# A gh that answers from canned JSON, records every call, honours --jq by
# running jq the way the real one does, and captures --body-file so a test can
# read what a requester would have seen.
make_gh() { # path
  cat >"$1" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GH_LOG"
case "${1:-}:${2:-}" in
  issue:view) src="$GH_ISSUE_JSON"; rc="${GH_VIEW_RC:-0}" ;;
  pr:list)    src="$GH_PR_JSON";    rc=0 ;;
  issue:edit)
    for a in "$@"; do [[ "$a" == "--remove-label" ]] && exit "${GH_REMOVE_RC:-0}"; done
    exit "${GH_EDIT_RC:-0}" ;;
  issue:comment)
    prev=""
    for a in "$@"; do [[ "$prev" == "--body-file" ]] && cp "$a" "$GH_COMMENT"; prev="$a"; done
    exit "${GH_COMMENT_RC:-0}" ;;
  *) exit 0 ;;
esac
[[ "$rc" -eq 0 ]] || { echo "gh: boom" >&2; exit "$rc"; }
filter=""; prev=""
for a in "$@"; do [[ "$prev" == "--jq" ]] && filter="$a"; prev="$a"; done
if [[ -n "$filter" ]]; then jq -r "$filter" <"$src"; else cat "$src"; fi
STUB
  chmod +x "$1"
}

# init and plan answer separately: this stage runs both, and a stub that
# failed them together would let "the baseline plan failed" pass on a run
# where the plan never happened.
make_tofu() { # path
  cat >"$1" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$TOFU_CALLS"
for a in "$@"; do
  [[ "$a" == "init" ]] || continue
  if [[ "${TOFU_INIT_RC:-0}" -ne 0 ]]; then
    echo "Error: Failed to get existing workspaces: InvalidAccessKeyId" >&2
    exit "$TOFU_INIT_RC"
  fi
  echo "OpenTofu has been successfully initialized!"
  exit 0
done
printf 'No changes. Your infrastructure matches the configuration.\n'
exit "${TOFU_RC:-0}"
STUB
  chmod +x "$1"
}

new_checkout() { # name -> echoes path
  local base="$WORK/$1"
  mkdir -p "$base/repo/.github" "$base/bin" "$base/repo/dns"
  git init --bare -q "$base/origin.git"
  git init -q -b main "$base/repo"
  git -C "$base/repo" config user.email ci@example.invalid
  git -C "$base/repo" config user.name ci
  printf 'locals {\n  a = 1\n}\n' >"$base/repo/dns/main.tf"
  printf '.falconet/\n' >"$base/repo/.gitignore"
  git -C "$base/repo" add -A
  git -C "$base/repo" commit -qm "base commit"
  git -C "$base/repo" remote add origin "$base/origin.git"
  git -C "$base/repo" push -q origin main
  make_gh "$base/bin/gh"
  make_tofu "$base/bin/tofu"
  printf '[]\n' >"$base/pr.json"
  printf '%s' "$base"
}

# The issue snapshot, as `gh issue view --json ...` returns it.
issue_json() { # path labels-csv body [state]
  local out="$1" labels="$2" body="$3" state="${4:-OPEN}"
  jq -n --arg b "$body" --arg s "$state" --arg l "$labels" \
    '{number:42, title:"Add MX records for papernapkin.tech", body:$b, state:$s,
      labels: ($l | if . == "" then [] else split(",") end | map({name:.})),
      comments: [{author:{login:"zetlen"}, createdAt:"2026-08-01T00:00:00Z", body:"bump"}]}' \
    >"$out"
}

p() { # checkout [args...] -> sets OUT ERR RC
  local c="$1"; shift
  OUT="$( cd "$c/repo" \
    && PATH="$c/bin:$PATH" \
       GH_LOG="$c/gh.log" GH_ISSUE_JSON="$c/issue.json" GH_PR_JSON="$c/pr.json" \
       GH_COMMENT="$c/posted.md" \
       GH_VIEW_RC="${VIEW_RC:-0}" GH_EDIT_RC="${EDIT_RC:-0}" \
       GH_REMOVE_RC="${REMOVE_RC:-0}" GH_COMMENT_RC="${COMMENT_RC:-0}" \
       TOFU="$c/bin/tofu" TOFU_CALLS="$c/tofu-calls.txt" TOFU_RC="${T_RC:-0}" \
       TOFU_INIT_RC="${T_INIT_RC:-0}" \
       GITHUB_ENV="${GH_ENV:-}" GITHUB_RUN_ID="${RUN_ID:-}" \
       "$FALCONET" prepare --issue 42 "$@" 2>"$c/err" )"
  RC=$?
  ERR="$(cat "$c/err")"
  return 0
}
reset() { VIEW_RC=""; EDIT_RC=""; REMOVE_RC=""; COMMENT_RC=""; T_RC=""; T_INIT_RC=""
          GH_ENV=""; RUN_ID=""; }
reset

ghlog() { cat "$1/gh.log" 2>/dev/null; }
hand()  { cat "$1/repo/.falconet/$2" 2>/dev/null; }

# --- the gate ---------------------------------------------------------------

c="$(new_checkout eligible)"; issue_json "$c/issue.json" "infra-request" "Please add MX."
p "$c"
it "a queued, open, unblocked issue with no open PR is ready"
assert_eq "ready" "$OUT" "outcome"
it "and exits 0"
assert_eq 0 "$RC" "exit code"

for lbl in ready-for-human do-not-apply wontfix needs-info; do
  c="$(new_checkout "block_$lbl")"; issue_json "$c/issue.json" "infra-request,$lbl" "x"
  p "$c"
  it "a blocking label ($lbl) makes it ineligible"
  assert_eq "ineligible" "$OUT" "outcome"
  it "and the reason names the label, because 'ineligible' alone is not a diagnostic"
  assert_contains "$ERR" "$lbl" "stderr"
done

c="$(new_checkout notqueued)"; issue_json "$c/issue.json" "bug" "x"
p "$c"
it "an issue without the queue label is ineligible"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout prefixlabel)"; issue_json "$c/issue.json" "infra-request-later" "x"
p "$c"
it "and the queue label is matched exactly, not as a prefix"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout nearblock)"; issue_json "$c/issue.json" "infra-request,needs-information" "x"
p "$c"
it "a label that merely starts like a blocking one does not block"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout closed)"; issue_json "$c/issue.json" "infra-request" "x" "CLOSED"
p "$c"
it "a closed issue is ineligible"
assert_eq "ineligible" "$OUT" "outcome"

# --- the opt-out box --------------------------------------------------------

c="$(new_checkout optout)"; issue_json "$c/issue.json" "infra-request" \
  "- [x] Not eligible for AI agents"
p "$c"
it "a ticked opt-out box is ineligible"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout optout_caps)"; issue_json "$c/issue.json" "infra-request" \
  "- [X] not eligible for ai agents"
p "$c"
it "and the whole line is matched case-insensitively"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout optout_star)"; issue_json "$c/issue.json" "infra-request" \
  "* [x] Not eligible for AI agents"
p "$c"
it "either list marker works"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout optout_indent)"; issue_json "$c/issue.json" "infra-request" \
  "  - [x] Not eligible for AI agents"
p "$c"
it "and an indented checkbox still opts out, because issue forms indent them"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout optout_unticked)"; issue_json "$c/issue.json" "infra-request" \
  "- [ ] Not eligible for AI agents"
p "$c"
it "an UNticked box does not opt out"
assert_eq "ready" "$OUT" "outcome"

# The origin's CI form was an unanchored substring test, so the sentence
# appearing anywhere -- quoted from another issue, say -- opted the issue out.
# The human-facing skill anchored it to a list item. Anchored is encoded here.
c="$(new_checkout optout_prose)"; issue_json "$c/issue.json" "infra-request" \
  "I do not think this is [x] Not eligible for AI agents, really"
p "$c"
it "the opt-out is anchored to a checkbox, not found anywhere in the prose"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout nullbody)"
jq -n '{number:42,title:"T",body:null,state:"OPEN",labels:[{name:"infra-request"}],comments:[]}' \
  >"$c/issue.json"
p "$c"
it "a null body is not a crash"
assert_eq "ready" "$OUT" "outcome"

# --- in flight --------------------------------------------------------------

c="$(new_checkout inflight)"; issue_json "$c/issue.json" "infra-request" "x"
printf '[{"number":57,"headRefName":"issue-42-add-mx"}]\n' >"$c/pr.json"
p "$c"
it "an open PR on this issue's branch is in-flight"
assert_eq "in-flight" "$OUT" "outcome"
it "and the reason names the pull request"
assert_contains "$ERR" "#57" "stderr"

c="$(new_checkout inflight_claude)"; issue_json "$c/issue.json" "infra-request" "x"
printf '[{"number":57,"headRefName":"claude/issue-42-20250101"}]\n' >"$c/pr.json"
p "$c"
it "the legacy claude/ prefix counts too"
assert_eq "in-flight" "$OUT" "outcome"

c="$(new_checkout inflight_other)"; issue_json "$c/issue.json" "infra-request" "x"
printf '[{"number":57,"headRefName":"issue-421-other"}]\n' >"$c/pr.json"
p "$c"
it "issue 421's PR does not make issue 42 in-flight"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout inflight_unanchored)"; issue_json "$c/issue.json" "infra-request" "x"
printf '[{"number":57,"headRefName":"feature/issue-42-x"}]\n' >"$c/pr.json"
p "$c"
it "and the match is anchored, so a nested name is not this issue's branch"
assert_eq "ready" "$OUT" "outcome"

# In flight means an OPEN PULL REQUEST, never a branch. Every run pushes its
# branch now, so a leftover branch is the ordinary state of a retried issue --
# and keying on branches would let one suppress every later run on the issue.
c="$(new_checkout branch_not_inflight)"; issue_json "$c/issue.json" "infra-request" "x"
git -C "$c/repo" switch -qc issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" push -q origin issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" switch -q main
p "$c"
it "a leftover branch on the remote does not make an issue in-flight"
assert_eq "ready" "$OUT" "outcome"

# --- ineligible and in-flight change nothing --------------------------------

c="$(new_checkout silent)"; issue_json "$c/issue.json" "infra-request,wontfix" "x"
GH_ENV="$c/github_env" p "$c"; reset
it "an ineligible issue leaves no handoff files"
assert_file_missing "$c/repo/.falconet/request.md"
it "and no issue.json, so 'ineligible' is not distinguishable from 'never ran'"
assert_file_missing "$c/repo/.falconet/issue.json"
it "and makes no mutating gh call"
assert_not_contains "$(ghlog "$c")" "issue edit" "gh log"
it "and posts no comment"
assert_not_contains "$(ghlog "$c")" "issue comment" "gh log"
it "and exports nothing"
assert_eq "" "$(cat "$c/github_env" 2>/dev/null)" "GITHUB_ENV"
it "and leaves the checkout on its original branch"
assert_eq "main" "$(git -C "$c/repo" branch --show-current)" "branch"

# --- the ready path ---------------------------------------------------------

c="$(new_checkout ready_full)"; issue_json "$c/issue.json" "infra-request" "Please add MX."
GH_ENV="$c/github_env" p "$c"; reset

it "ready writes the branch name"
assert_eq "issue-42-add-mx-records-for-papernapkin-tech" "$(hand "$c" branch.txt)" "branch.txt"
it "and really is on that branch"
assert_eq "$(hand "$c" branch.txt)" "$(git -C "$c/repo" branch --show-current)" "branch"
it "and records the commit the run started from"
assert_eq "$(git -C "$c/repo" rev-parse main)" "$(hand "$c" base-sha.txt)" "base-sha.txt"
it "and exports BRANCH for the wrappers"
assert_contains "$(cat "$c/github_env")" "BRANCH=issue-42-add-mx" "GITHUB_ENV"
it "and BASE_SHA"
assert_contains "$(cat "$c/github_env")" "BASE_SHA=" "GITHUB_ENV"
it "request.md leads with the issue"
assert_contains "$(hand "$c" request.md)" "# Issue #42: Add MX records" "request.md"
it "and carries the requester's words"
assert_contains "$(hand "$c" request.md)" "Please add MX." "request.md"
it "and the comment thread, oldest first"
assert_contains "$(hand "$c" request.md)" "### zetlen — 2026-08-01" "request.md"
it "but never this pipeline's own acknowledgment"
assert_not_contains "$(hand "$c" request.md)" "picked up and is being worked on" "request.md"
it "the baseline plan is captured"
assert_contains "$(hand "$c" plan-baseline.txt)" "No changes" "plan-baseline.txt"
it "and the planner is never asked for color"
assert_contains "$(cat "$c/tofu-calls.txt")" "-no-color" "tofu calls"
it "the claim is recorded"
assert_contains "$(ghlog "$c")" "--add-assignee" "gh log"
it "and the requester is thanked, in the words that promise only what is true"
assert_contains "$(cat "$c/posted.md" 2>/dev/null)" \
  "Thanks — this request has been picked up" "posted comment"
it "including that a person still decides"
assert_contains "$(cat "$c/posted.md" 2>/dev/null)" \
  "Nothing takes effect until a person has reviewed it." "posted comment"

c="$(new_checkout nocomments)"
jq -n '{number:42,title:"T",body:"b",state:"OPEN",labels:[{name:"infra-request"}],comments:[]}' \
  >"$c/issue.json"
p "$c"
it "an issue with no comments gets no comment-thread heading"
assert_not_contains "$(hand "$c" request.md)" "## Comment thread" "request.md"

# The issue body is attacker-controlled text AND the agent's instructions. It
# travels through files, never through a template expression, and nothing here
# executes any of it.
c="$(new_checkout hostile)"; issue_json "$c/issue.json" "infra-request" \
  'Add `$(touch /tmp/pwned)` and ```a fence``` please'
p "$c"
it "shell-shaped text in the body reaches request.md verbatim"
assert_contains "$(hand "$c" request.md)" '$(touch /tmp/pwned)' "request.md"
it "and is not executed on the way"
assert_file_missing "/tmp/pwned"

# stdout is the outcome and nothing else. The baseline plan is a much bigger
# chatterer than anything else in this pipeline.
it "a chatty planner does not leak into the outcome word"
assert_eq "ready" "$OUT" "outcome"

# --- the slug ---------------------------------------------------------------

slug_of() { # title -> echoes branch.txt
  local c; c="$(new_checkout "slug$RANDOM")"
  jq -n --arg t "$1" \
    '{number:42,title:$t,body:"b",state:"OPEN",labels:[{name:"infra-request"}],comments:[]}' \
    >"$c/issue.json"
  p "$c" >/dev/null
  hand "$c" branch.txt
}

it "a long title is cut to 40 slug characters"
b="$(slug_of "An extremely long issue title that goes well past the limit")"
assert_eq 39 "$(( ${#b} - 9 ))" "slug length (branch minus the issue-42- prefix)"

it "and the cut lands where the 40th character was, not at a word boundary"
assert_eq "issue-42-an-extremely-long-issue-title-that-goes" "$b" "branch"

it "and never ends in a dash, even when the cut lands mid-separator"
b="$(slug_of "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa - trailing")"
case "$b" in *-) assert_eq "no trailing dash" "trailing dash" "branch: $b" ;;
              *) assert_eq "ok" "ok" "branch: $b" ;; esac

it "a title with nothing sluggable falls back to 'request'"
assert_eq "issue-42-request" "$(slug_of '!!! ???')" "branch"

it "a non-ASCII title still produces a usable ref"
b="$(slug_of "Zoë's café")"
case "$b" in issue-42-[a-z0-9-]*) assert_eq ok ok "branch: $b" ;;
             *) assert_eq "[a-z0-9-] only" "$b" "branch" ;; esac

# --- the collision rename ---------------------------------------------------

c="$(new_checkout collide)"; issue_json "$c/issue.json" "infra-request" "x"
git -C "$c/repo" switch -qc issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" push -q origin issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" switch -q main
RUN_ID=99 p "$c"; reset
it "a branch already on the remote is renamed rather than pushed onto"
assert_eq "issue-42-add-mx-records-for-papernapkin-tech-99" "$(hand "$c" branch.txt)" "branch.txt"
it "and says so"
assert_contains "$ERR" "already exists on the remote" "stderr"

c="$(new_checkout collide_local)"; issue_json "$c/issue.json" "infra-request" "x"
git -C "$c/repo" switch -qc issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" push -q origin issue-42-add-mx-records-for-papernapkin-tech
git -C "$c/repo" switch -q main
p "$c"
it "and with no GITHUB_RUN_ID it still renames instead of dying under set -u"
assert_eq "ready" "$OUT" "outcome"
it "which is a bug that would only ever have appeared on a retry"
assert_not_contains "$ERR" "unbound variable" "stderr"

# --- the clean-tree assertion, and where it sits ----------------------------

c="$(new_checkout dirty)"; issue_json "$c/issue.json" "infra-request" "x"
printf 'locals {\n  a = 99\n}\n' >"$c/repo/dns/main.tf"
p "$c"
it "a dirty tree is a mechanical refusal, not an outcome"
assert_eq 1 "$RC" "exit code"
it "and prints no outcome word"
assert_eq "" "$OUT" "stdout"
it "and names what is dirty"
assert_contains "$ERR" "dns/main.tf" "stderr"

# The origin asserted this AFTER the claim, the ack and the branch, so a dirty
# tree thanked the requester, assigned the issue, cut a branch and then died.
# The human-facing skill put it in preflight. Preflight is encoded here.
it "and the requester is not thanked for a run that was never going to happen"
assert_not_contains "$(ghlog "$c")" "issue comment" "gh log"
it "nor is the issue claimed"
assert_not_contains "$(ghlog "$c")" "issue edit" "gh log"
it "nor is a branch left behind"
assert_eq "main" "$(git -C "$c/repo" branch --show-current)" "branch"

# --- re-entry (issue #25) ---------------------------------------------------
#
# needs-info blocks a first claim and admits a reply, which a flat precedence
# list cannot say. The event decides, or --re-entry does.

ev() { # path action extra-jq
  jq -n --argjson pr "${3:-null}" --arg t "${4:-User}" \
    '{action:$ARGS.named.a, comment:{user:{type:$t}},
      issue:{state:"open", pull_request:$pr,
             labels:[{name:"infra-request"},{name:"needs-info"}], body:"x"}}' \
    --arg a "$2" >"$1"
}

c="$(new_checkout reentry)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
ev "$c/event.json" created
p "$c" --event "$c/event.json"
it "a human's comment on a needs-info issue is a way back in"
assert_eq "ready" "$OUT" "outcome"
it "and the parking label is cleared, because requesters usually cannot"
assert_contains "$(ghlog "$c")" "--remove-label needs-info" "gh log"
it "but the requester is not thanked twice"
assert_not_contains "$(ghlog "$c")" "issue comment" "gh log"
it "and no ack.md is written"
assert_file_missing "$c/repo/.falconet/ack.md"

c="$(new_checkout reentry_bot)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
ev "$c/event.json" created null Bot
p "$c" --event "$c/event.json"
it "a bot's comment is not, or the pipeline would answer itself"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout reentry_pr)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
ev "$c/event.json" created '{"url":"x"}'
p "$c" --event "$c/event.json"
it "and neither is a comment on a pull request"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout reentry_flag)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
p "$c" --re-entry
it "--re-entry is how a workstation says it, with no event to read"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout reentry_blocked)"; issue_json "$c/issue.json" \
  "infra-request,needs-info,do-not-apply" "x"
p "$c" --re-entry
it "and re-entry admits needs-info only — the other blocking labels still block"
assert_eq "ineligible" "$OUT" "outcome"

c="$(new_checkout reentry_failclear)"; issue_json "$c/issue.json" "infra-request,needs-info" "x"
REMOVE_RC=1 p "$c" --re-entry; reset
it "a label that cannot be cleared is fatal, unlike the claim and the ack"
assert_eq 1 "$RC" "exit code"
it "because an issue left parked while a run proceeds is a contradiction"
assert_eq "" "$OUT" "stdout"

# --- best-effort calls really are best-effort -------------------------------

c="$(new_checkout claimfails)"; issue_json "$c/issue.json" "infra-request" "x"
EDIT_RC=1 p "$c"; reset
it "a failed claim does not fail the run"
assert_eq "ready" "$OUT" "outcome"
it "but it is said out loud"
assert_contains "$ERR" "could not assign" "stderr"

c="$(new_checkout ackfails)"; issue_json "$c/issue.json" "infra-request" "x"
COMMENT_RC=1 p "$c"; reset
it "nor does a failed acknowledgment"
assert_eq "ready" "$OUT" "outcome"

c="$(new_checkout noack)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c" --no-ack
it "--no-ack skips the greeting for a caller that only wants the branch"
assert_not_contains "$(ghlog "$c")" "issue comment" "gh log"

c="$(new_checkout assignee)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c" --assignee bob
it "--assignee names who the claim is recorded against"
assert_contains "$(ghlog "$c")" "--add-assignee bob" "gh log"

# --- hard failures ----------------------------------------------------------

c="$(new_checkout planfails)"; issue_json "$c/issue.json" "infra-request" "x"
T_RC=1 p "$c"; reset
it "a baseline plan that fails is fatal, because no agent time will fix main"
assert_eq 1 "$RC" "exit code"
it "and prints no outcome word"
assert_eq "" "$OUT" "stdout"

# --- the stacks it plans, it initialises first -------------------------------
#
# A runner is a fresh checkout with no .terraform/ in it, and `tofu plan`
# there is "missing required providers".

c="$(new_checkout initfirst)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c"
it "the stack is initialised before it is planned"
assert_contains "$(head -1 "$c/tofu-calls.txt")" "init -input=false" "first tofu call"

it "and the plan still happens after it"
assert_contains "$(cat "$c/tofu-calls.txt")" "plan -no-color" "tofu calls"

c="$(new_checkout initfails)"; issue_json "$c/issue.json" "infra-request" "x"
T_INIT_RC=1 p "$c"; reset
it "an init that fails is fatal and says so as itself"
assert_eq 1 "$RC" "exit code"
it "and names the stack rather than leaving tofu's error unattributed"
assert_contains "$ERR" "tofu init failed in dns/" "stderr"
it "and passes tofu's own reason through, because that is the actionable half"
assert_contains "$ERR" "InvalidAccessKeyId" "stderr"

# Initialisation belongs to whoever drives the plan: a plan command that is
# not tofu brings the stack up its own way.
c="$(new_checkout initcustom)"; issue_json "$c/issue.json" "infra-request" "x"
printf '{"plan":{"command":"./plan.sh {stack}"}}\n' >"$c/repo/.github/falconet.json"
printf '#!/usr/bin/env bash\necho "custom plan for $1"\n' >"$c/repo/plan.sh"
chmod +x "$c/repo/plan.sh"
git -C "$c/repo" add -A; git -C "$c/repo" commit -qm cfg
git -C "$c/repo" push -q origin main
p "$c"
it "a plan command that is not tofu initialises itself"
assert_eq "ready" "$OUT" "outcome"
it "and falconet runs no tofu at all"
assert_eq "" "$(cat "$c/tofu-calls.txt" 2>/dev/null)" "tofu calls"
it "and the baseline is whatever that command printed"
assert_contains "$(hand "$c" plan-baseline.txt)" "custom plan for" "plan-baseline.txt"

# The defaults name three stacks, so a repository with different ones meets
# this first.
c="$(new_checkout nostack)"; issue_json "$c/issue.json" "infra-request" "x"
printf '{"stacks":{"plan":["nowhere"],"validate_only":[]}}\n' \
  >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A; git -C "$c/repo" commit -qm cfg
git -C "$c/repo" push -q origin main
p "$c"
it "a stack that is not a directory is fatal"
assert_eq 1 "$RC" "exit code"
it "and the message names the config key rather than leaving tofu to explain"
assert_contains "$ERR" 'config .stacks.plan names "nowhere"' "stderr"
it "and names the file to edit"
assert_contains "$ERR" ".github/falconet.json" "stderr"

c="$(new_checkout viewfails)"; issue_json "$c/issue.json" "infra-request" "x"
VIEW_RC=1 p "$c"; reset
it "an issue that cannot be read is a mechanical failure, not an outcome"
assert_eq 1 "$RC" "exit code"

# --- $GITHUB_ENV is optional ------------------------------------------------

c="$(new_checkout noghenv)"; issue_json "$c/issue.json" "infra-request" "x"
p "$c"
it "with no GITHUB_ENV the files are still written and the run still succeeds"
assert_eq "ready" "$OUT" "outcome"
it "and branch.txt is there, because the files are the contract"
assert_contains "$(hand "$c" branch.txt)" "issue-42-" "branch.txt"

# --- config -----------------------------------------------------------------

c="$(new_checkout cfg)"
printf '{"issue":{"queue_label":"ops","branch_prefix":"req-"}}\n' \
  >"$c/repo/.github/falconet.json"
git -C "$c/repo" add -A; git -C "$c/repo" commit -qm cfg
git -C "$c/repo" push -q origin main
issue_json "$c/issue.json" "ops" "x"
p "$c"
it "the queue label comes from config"
assert_eq "ready" "$OUT" "outcome"
it "and so does the branch prefix"
assert_contains "$(hand "$c" branch.txt)" "req-42-" "branch.txt"

# --- usage ------------------------------------------------------------------

it "a missing --issue is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" prepare >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "an --issue that is not a number is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" prepare --issue abc >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "an unknown argument is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" prepare --bogus >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

it "-h/--help is a usage error"
( cd "$REPO_ROOT" && "$FALCONET" prepare --help >/dev/null 2>&1 )
assert_eq 2 "$?" "exit code"

summary
