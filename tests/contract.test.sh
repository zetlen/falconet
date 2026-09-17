#!/usr/bin/env bash
#
# contract.test.sh — structural invariants of the wrappers that a unit test of
# any single verb cannot see.
#
# Both bugs from run 32093607680 were WIRING, not logic: every piece was
# correct and one of them was in the wrong place. These cases guard the
# wiring. A new hand-over path that forgets to name its branch, a push that
# creeps back behind a condition, a tool grant that quietly widens, a job
# that grows a checkout it must not have — each fails here, where a unit
# test would see nothing wrong.
#
# The wrappers install the binary rather than check falconet out into the
# consumer's tree: a release asset at a tag, `go install` at any other ref.
# The cases below hold both paths, the release that produces the asset, and
# the pins that name it.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

WF="$REPO_ROOT/.github/workflows/falconet.yml"
ACTION="$REPO_ROOT/action.yml"

wf="$(cat "$WF")"
action="$(cat "$ACTION")"
# Comments stripped, for the cases about what a file DOES rather than what it
# says: the prose above a step often names the very thing it explains not to
# do.
wf_code="$(grep -v '^[[:space:]]*#' "$WF")"
action_code="$(grep -v '^[[:space:]]*#' "$ACTION")"

# One job's text, comments stripped, from its key to the next job's.
job() { # name
  awk -v j="$1" '$0 == "  " j ":" { f = 1; next } f && /^  [a-z][a-z-]*:$/ { exit } f' <<<"$wf_code"
}
gate_job="$(job gate)"
implement_job="$(job implement)"
publish_job="$(job publish)"
contain_job="$(job contain)"

# Every pause invocation, with its backslash continuations pulled into one
# line, so a check can see the whole argument list. pause is a `run:` step —
# its preamble is a sentence, and the action splits args on whitespace — so
# a call runs from `falconet pause` to the first line without a backslash.
pause_calls="$(awk '
  /falconet pause/ { inpause = 1; buf = "" }
  inpause {
    buf = buf " " $0
    if ($0 !~ /\\$/) { print buf; inpause = 0 }
  }
' <<<"$wf_code")"

# --- falconet's own events never start a run ------------------------------
#
# Every comment and label falconet writes is an issue event that fires the
# caller again. The gate skips a bot's event and a pull-request comment on
# the event alone, before any runner or token.

it "the gate job skips an event whose sender is a bot, or a comment on a pull request"
assert_contains "$gate_job" "if: github.event.sender.type != 'Bot' && !github.event.issue.pull_request" "the gate job"

# --- the agent holds nothing it could publish with -------------------------

it "the agent job holds exactly one secret, the model key, and exports it under the caller's name"
loop_step="$(awk '/name: Implement, and check, until the check passes or the cap/{f=1; print; next} f && /^      - /{exit} f' <<<"$implement_job")"
assert_eq 1 "$(grep -c 'secrets\.' <<<"$implement_job")" "secret references in the implement job"
assert_contains "$loop_step" 'MODEL_API_KEY: ${{ secrets.model-api-key }}' "the loop step"
assert_contains "$loop_step" 'export "$MODEL_API_KEY_ENV=$MODEL_API_KEY"' "the loop step"
assert_eq 0 "$(grep -c 'anthropic' <<<"$implement_job")" "harness-specific names in the implement job"

it "and the default harness grants the five file tools and no shell"
assert_contains "$(grep -A8 '"harness"' "$REPO_ROOT/internal/config/config.go")" '"Read,Edit,Write,Grep,Glob"' "the default harness.command"
assert_not_contains "$(grep -A8 '"harness"' "$REPO_ROOT/internal/config/config.go")" 'Bash' "the default harness.command"

# The README's implement contract shows the default harness as JSON, and the
# binary carries the same argv in internal/config. The two are one fact in
# two places, so the contract holds them equal: a default that drifts from
# its documentation hands a consumer a command they did not choose, and the
# reader who copies the block gets something other than what runs.
harness_block="$(awk '
  /^```json$/ { f = 1; buf = ""; next }
  f && /^```$/ { if (buf ~ /"harness"/) { printf "%s", buf; exit } f = 0; next }
  f { buf = buf $0 "\n" }
' "$REPO_ROOT/README.md")"
default_argv="$( (cd "$WORK" && "$FALCONET" config get .harness.command) | jq -Sc .)"
readme_argv="$(jq -Sc .harness.command <<<"{$harness_block}")"

it "the README's implement contract shows a harness block"
assert_contains "$harness_block" '"command"' "the README's harness block"

it "and it is the default the binary carries, argv for argv"
assert_eq "$default_argv" "$readme_argv" "harness.command: the README vs the binary"

it "the agent job holds no permissions at all"
assert_contains "$wf" "permissions: {}" "workflow"

it "and the workflow's default is the same, so a new job must opt in"
# Comment lines excluded: the header explains the rule and would be counted.
assert_eq 2 "$(grep -c 'permissions: {}' <<<"$wf_code")" "permissions: {} declarations"

# "the agent job checks out without persisting credentials" lived here until
# #19. The checkout it was about — falconet's own, the one ADR-0005 allowed —
# is gone, and a job with no checkout has nothing to persist. The stronger
# form is the two cases under "the agent job is handed its source".

it "and no step in the agent job is handed a token"
assert_not_contains "$implement_job" "steps.token.outputs.token" "implement job"

# --- exactly one of each thing that must happen once -----------------------

it "nothing plans: the repository's own checks on the pull request do that"
assert_eq 0 "$(grep -c -E 'verb: (validate|assemble)|falconet plan-env|plan-env:' <<<"$wf_code")" "plan-side steps"
assert_not_contains "$action_code" "setup-opentofu" "action"

it "the branch is pushed exactly once"
assert_eq 1 "$(grep -c 'verb: push' <<<"$wf_code")" "push steps"

it "and only by the push verb — nothing else runs git push"
assert_not_contains "$wf" "git push" "workflow"

it "the loop is on the check: commit still happens once, after it"
assert_eq 1 "$(grep -c 'verb: commit' <<<"$wf_code")" "commit steps"

# --- the check loop feeds the check back and never a guard -----------------
#
# Principle 3, as wiring. The repository's own check runs after every agent
# pass; a failing one sends the run back to a fresh pass, at most
# `max-attempts` times; the guards run once, in the commit step, after the
# last check, and nothing before the commit reads their answer. A retry
# conditioned on anything but the check's word — the commit's outcome, a
# failure-reason file — is a guard the agent can iterate against, and this
# is where it would show up.

it "the agent runs through the implement verb, and only inside the loop"
assert_eq 1 "$(grep -c 'falconet implement' <<<"$implement_job")" "implement invocations"
assert_contains "$loop_step" 'falconet implement $FALCONET_CONFIG_FLAG' "the loop step"
assert_eq 0 "$(grep -c 'claude-code-action\|claude_args\|allowedTools' <<<"$wf_code")" "harness-specific steps in the workflow"

it "the repository's own check runs after every agent pass, and the loop turns on its word"
assert_eq 1 "$(grep -c 'falconet check' <<<"$implement_job")" "check invocations"
assert_contains "$loop_step" 'word="$(falconet check $FALCONET_CONFIG_FLAG)"' "the loop step"
assert_contains "$loop_step" '[ "$word" = fail ] && [ "$attempt" -lt "$MAX_ATTEMPTS" ] || break' "the loop step"

it "and the cap is the max-attempts input"
assert_contains "$loop_step" 'MAX_ATTEMPTS: ${{ inputs.max-attempts }}' "the loop step"
assert_contains "$wf_code" "max-attempts:" "the workflow's inputs"

it "and on nothing else: nothing in the loop reads the commit's outcome or a guard's file"
assert_eq 0 "$(grep -c 'steps.commit\|failure-reason\|verb: commit' <<<"$loop_step")" "guard references in the loop"
assert_eq 0 "$(grep 'if:' <<<"$implement_job" | grep -c 'steps.commit\|failure-reason\|outcome == .failure.')" "retry conditions naming a guard"

it "the harness is installed by the caller's snippet, before the loop and after the tree arrives"
assert_contains "$implement_job" 'run: ${{ inputs.harness-setup }}' "the implement job"
setup_at="$(grep -n 'inputs.harness-setup' <<<"$implement_job" | cut -d: -f1)"
loop_at="$(grep -n 'falconet implement' <<<"$implement_job" | cut -d: -f1)"
branch_at="$(grep -n 'name: Take the working branch' <<<"$implement_job" | cut -d: -f1)"
assert_eq "true" "$([[ "$branch_at" -lt "$setup_at" && "$setup_at" -lt "$loop_at" ]] && echo true || echo false)" \
  "branch ($branch_at) < harness setup ($setup_at) < loop ($loop_at)"

it "and that snippet is the only template expression inside a run: block"
assert_eq 1 "$(grep -c 'run: \${{' <<<"$wf_code")" "run: lines that are expressions"

it "the commit runs after the loop, and is not conditioned on its word"
loop_at="$(grep -n 'falconet check' <<<"$implement_job" | cut -d: -f1)"
commit_at="$(grep -n 'verb: commit' <<<"$implement_job" | cut -d: -f1)"
assert_eq "true" "$([[ -n "$loop_at" && -n "$commit_at" && "$loop_at" -lt "$commit_at" ]] && echo true || echo false)" \
  "the loop ($loop_at) precedes the commit ($commit_at)"
commit_step="$(awk '/name: Commit$/{f=1} f && /verb: commit/{print; exit} f' <<<"$implement_job")"
assert_not_contains "$commit_step" "if:" "the commit step"

it "and the job reports the word of the last check that ran"
assert_contains "$implement_job" 'check: ${{ steps.loop.outputs.check }}' "the implement job's outputs"
assert_contains "$loop_step" 'echo "check=$word" >> "$GITHUB_OUTPUT"' "the loop step"

it "the pull request is opened only when that word is not fail"
pr_step="$(awk '/name: Open the pull request/{f=1} f && /gh pr create/{exit} f' <<<"$publish_job")"
assert_contains "$pr_step" "needs.implement.outputs.check != 'fail'" "the PR step's condition"

it "and at the cap the run hands off instead, naming the branch and carrying the check's output"
cap_pause="$(grep -- 'check-failure.txt' <<<"$pause_calls")"
assert_contains "$cap_pause" '--branch "${PUSHED_BRANCH:-}"' "the cap's pause"
assert_contains "$cap_pause" '--label ready-for-human' "the cap's pause"
assert_contains "$cap_pause" '--body-title' "the cap's pause: machine output is fenced"
cap_step="$(awk '/name: Hand over — the check failed at the cap/{f=1} f && /falconet pause/{exit} f' <<<"$publish_job")"
assert_contains "$cap_step" "needs.implement.outputs.check == 'fail'" "the cap step's condition"

# --- the push is unconditional and comes first -----------------------------
#
# The one guard run 32093607680 bought. Every other exit used to leave the
# work on a runner that was destroyed minutes later.

it "the push step carries no condition"
push_step="$(awk '/name: Push/{f=1} f && /verb: push/{print; exit} f' <<<"$wf_code")"
assert_not_contains "$push_step" "if:" "push step"

it "and nothing that publishes runs before it"
push_line="$(grep -n 'verb: push' "$WF" | cut -d: -f1)"
pr_line="$(grep -n 'gh pr create' "$WF" | cut -d: -f1)"
[[ "$push_line" -lt "$pr_line" ]] \
  && assert_eq "before" "before" "push at $push_line, pr create at $pr_line" \
  || assert_eq "push before pr create" "push=$push_line pr=$pr_line" "order"

# The push needs the binary and the binary is a compile, so one step stands
# between the restored branch and the remote, and it is that one.
it "and only the install stands between restoring the branch and the push"
publish_steps="$(sed -n 's/^      - name: //p' <<<"$publish_job")"
assert_eq "Restore the branch
Install falconet and gitleaks
Push" "$(grep -A2 '^Restore the branch$' <<<"$publish_steps")" "the three steps in order"

it "the work is bundled before the agent job ends, so it outlives the runner"
assert_contains "$wf" "git bundle create" "workflow"

# --- every hand-over names its branch --------------------------------------

it "every pause call passes --branch"
passing="$(printf '%s\n' "$pause_calls" | grep -c -- '--branch' || true)"
total="$(grep -c 'falconet pause' <<<"$wf_code")"
assert_eq "$total" "$passing" "pause calls passing --branch"

it "and there are four of them: three endings in publish and the containment"
assert_eq 4 "$total" "pause calls"

it "and the ones in publish read PUSHED_BRANCH rather than the branch prepare intended"
# The branch that IS on the remote, set by the push verb; empty when nothing
# was pushed, which pause takes as "no branch".
publish_pauses="$(awk '/falconet pause/ { p = 1; buf = "" } p { buf = buf " " $0; if ($0 !~ /\\$/) { print buf; p = 0 } }' <<<"$publish_job")"
assert_eq 3 "$(grep -c -- '--branch "${PUSHED_BRANCH:-}"' <<<"$publish_pauses")" "publish pauses on \$PUSHED_BRANCH"

# Unset, not empty, when nothing was pushed — and the two hand-overs for a
# question and a failure are exactly the paths with nothing to push. A bare
# "$PUSHED_BRANCH" under set -u ends the step before pause runs.
it "and never as a bare expansion, which set -u would refuse on the paths that need it most"
assert_eq 0 "$(grep -c -- '--branch "\$PUSHED_BRANCH"' <<<"$wf_code")" "bare \$PUSHED_BRANCH expansions"

it "and the containment's passes the empty string, because it does not know"
assert_contains "$(grep 'falconet pause' <<<"$pause_calls" | tail -1)" '--branch ""' "contain's pause"

it "there is a containment job that runs whatever happened"
assert_contains "$wf" "if: always() && needs.gate.outputs.outcome == 'ready'" "workflow"

# Terminal or not is decided in one step and acted on in the next, so the
# decision is an output a reader can see, and the pause cannot run on a
# guess.
it "the containment job decides first, and its pause is conditioned on the decision"
assert_contains "$contain_job" "id: check" "contain job"
assert_contains "$contain_job" "if: \"!cancelled() && steps.check.outputs.terminal != 'true'\"" "contain job"
assert_eq 1 "$(grep -c "steps.check.outputs.terminal != 'true'" <<<"$contain_job")" "conditioned pauses"

# A check that failed — gh could not read the issue — leaves the decision
# unset, and the pause must still run: the alternative is a red step and
# nothing on the issue. `always()` would also pause a cancelled run, which a
# person chose; `!cancelled()` is the line between the two.
it "and the pause runs after a failed check too, but never after a cancellation"
assert_contains "$contain_job" '!cancelled() &&' "contain job"
assert_not_contains "$contain_job" 'if: always() && steps.check' "contain job"

# AGENTS.md's trap, still true for the two run: steps that use gh: `grep -q`
# exits at the first match and can SIGPIPE gh, which under pipefail turns a
# FOUND match into a failed pipeline. Every answer is captured into a
# variable, then inspected.
it "and captures every gh answer before inspecting it — never gh … | grep"
assert_eq 0 "$(grep -cE 'gh [^|]*\|' <<<"$wf_code")" "gh commands piped anywhere"
assert_eq 3 "$(grep -cE '^ *[a-z]+="\$\(gh ' <<<"$contain_job")" "captured gh answers in contain"

it "and reads gh's JSON with gh's own template, because jq is no longer a dependency"
assert_eq 3 "$(grep -c -- '--template' <<<"$contain_job")" "gh --template uses in contain"

# --- the review protocol stays unwired -------------------------------------

it "the workflow names review-verdict zero times"
assert_not_contains "$wf" "review-verdict" "workflow"

it "and there is no second agent: the implement verb is the only way an agent runs"
assert_eq 0 "$(grep -c 'falconet prompt' <<<"$wf_code")" "prompts resolved outside the implement verb"
assert_eq 0 "$(grep -c 'review' <<<"$implement_job")" "review steps in the agent job"

# --- the pull request describes the change, not the request ----------------

it "the PR title comes from the commit subject the agent wrote"
assert_contains "$wf" 'cat .falconet/commit-subject.txt' "workflow"

it "and never from the issue title"
assert_not_contains "$wf" "github.event.issue.title" "workflow"

it "the body is the commit body and a Closes line, and no plan is quoted into it"
assert_contains "$wf_code" 'cat .falconet/commit-body.md' "workflow"
assert_contains "$wf_code" "Closes #" "workflow"
assert_not_contains "$wf_code" "plan.txt" "workflow"

it "and the label comes from the config every verb reads, through the binary"
assert_contains "$wf_code" 'label="$(falconet config $FALCONET_CONFIG_FLAG get .labels.pr)"' "workflow"
assert_contains "$wf_code" '--label "$label"' "workflow"

# --- pinned binaries, installed before anything depends on them ------------

it "gitleaks is pinned by version"
assert_contains "$action" "gitleaks-version" "action"

it "and by digest, because a tag is a mutable pointer"
assert_contains "$action" "sha256sum -c -" "action"

it "and proves it runs before anything depends on it"
assert_contains "$action" "gitleaks version" "action"

it "the digest is checked before the tarball is unpacked"
gitleaks_install="$(awk '/name: Install gitleaks/{f=1} /name: Choose how to install falconet/{f=0} f' <<<"$action_code")"
sha_line="$(grep -n 'sha256sum -c -' <<<"$gitleaks_install" | head -1 | cut -d: -f1)"
tar_line="$(grep -n 'tar -xzf' <<<"$gitleaks_install" | head -1 | cut -d: -f1)"
[[ "$sha_line" -lt "$tar_line" ]] \
  && assert_eq "before" "before" "sha at $sha_line, tar at $tar_line" \
  || assert_eq "sha before tar" "sha=$sha_line tar=$tar_line" "order"

# falconet arrives one of two ways, and the ref the caller's `uses:` named
# picks which. At a vX.Y.Z tag, on a runner that release has an asset for, it
# is that release's asset, checked against the release's checksums.txt. At
# any other ref, or on any other runner, it is `go install` of this module at
# that ref. Comments stripped: the prose above the steps names every shape
# they refuse.
choose_step="$(awk '/name: Choose how to install falconet/{f=1} /name: Set up Go/{f=0} f' <<<"$action_code")"
go_setup="$(awk '/name: Set up Go/{f=1} /name: Install falconet/{f=0} f' <<<"$action_code")"
falconet_install="$(awk '/name: Install falconet/{f=1} /name: Run$/{f=0} f' <<<"$action_code")"
MK="$REPO_ROOT/Makefile"

# The break: a branch or a commit sent looking for a release that cannot
# exist, or a tag like v1.2.3-rc1 matched loosely and sent to one that does
# not.
it "only a vX.Y.Z ref is looked for as a release"
assert_contains "$choose_step" '[[ "$FALCONET_REF" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]' "action"

# The break: an asset name the action builds one way and `make assets`
# writes another, which is a 404 in every job at the first tag that ships
# it.
it "the asset is the one make assets writes, for the platforms it writes"
assert_contains "$choose_step" 'asset="falconet_${FALCONET_REF#v}_${platform}.tar.gz"' "action"
assert_contains "$(cat "$MK")" 'falconet_$(VERSION:v%=%)_$${os}_$${arch}' "Makefile"
action_platforms="$(grep -oE 'platform=[a-z0-9]+_[a-z0-9]+' <<<"$choose_step" | cut -d= -f2 | sort)"
make_platforms="$(sed -n 's/^PLATFORMS *:= *//p' "$MK" | tr ' ' '\n' | tr / _ | sed '/^$/d' | sort)"
assert_eq "$make_platforms" "$action_platforms" "platforms the action maps"

# The break: a recipe that inherits the repository a git hook was running
# for. Under lefthook's pre-push, `make test` then runs every fixture's `git
# init` and `git config` against this clone's own .git/config.
it "no make recipe inherits the repository a git hook names"
printf 'hook-env-probe:\n\t@env | grep -E "^GIT_(DIR|WORK_TREE|COMMON_DIR|INDEX_FILE|CONFIG_PARAMETERS)=" || true\n' >"$WORK/hook-env-probe.mk"
assert_eq "" "$(GIT_DIR=/nowhere GIT_WORK_TREE=/nowhere GIT_COMMON_DIR=/nowhere GIT_INDEX_FILE=/nowhere/index GIT_CONFIG_PARAMETERS="'core.bare=true'" \
  make -s -C "$REPO_ROOT" -f Makefile -f "$WORK/hook-env-probe.mk" hook-env-probe)" "GIT_* a recipe sees"

it "and it comes from this repository's release at the action's ref"
assert_contains "$falconet_install" 'https://github.com/zetlen/falconet/releases/download/$FALCONET_REF' "action"

# The break: the digest checked after the tarball is unpacked, or not at
# all. A release asset is fetched over the network, and nothing from it runs
# before its bytes match the release's own record of them.
it "the asset is checked against the release's checksums before it is unpacked"
sum_at="$(grep -n 'shasum -a 256 -c -' <<<"$falconet_install" | head -1 | cut -d: -f1)"
untar_at="$(grep -n 'tar -xzf' <<<"$falconet_install" | head -1 | cut -d: -f1)"
assert_eq "true" "$([[ -n "$sum_at" && -n "$untar_at" && "$sum_at" -lt "$untar_at" ]] && echo true || echo false)" \
  "the checksum ($sum_at) precedes the unpack ($untar_at)"

# The break: a checksums.txt with no line for the asset piped into the
# check as nothing, which is a failure only if something says so.
it "and a checksums file with no line for the asset is refused"
assert_contains "$falconet_install" 'if [ -z "$sum" ]; then' "action"

# The break: the fallback dropped, so a branch ref, which has no release,
# installs nothing.
it "any other ref is go-installed from this module at that ref"
assert_contains "$falconet_install" 'go install "github.com/zetlen/falconet/cmd/falconet@$FALCONET_REF"' "action"

# The break: `${{ github.action_ref }}` pasted into a run: block instead.
# Inside a composite action it is populated when a step's env: is evaluated
# and EMPTY by the time its run: block is (actions/runner#2473), so the
# install would ask for a release named nothing, or run `go install …@`.
it "and the ref reaches the shell through env:, never through a run: block"
for step in "$choose_step" "$falconet_install"; do
  assert_contains "$step" 'FALCONET_REF: ${{ github.action_ref }}' "action"
  assert_eq 1 "$(grep -c 'github.action_ref' <<<"$step")" "mentions of github.action_ref in the step"
done

# The break: `uses: ./`. A local path has no ref, and an empty one must fail
# here, by name, rather than as whatever a download or `go install` makes of
# it.
it "and an empty ref is refused before anything is chosen"
assert_contains "$choose_step" 'if [ -z "$FALCONET_REF" ]' "action"

# The break: a floating tag on setup-go, or a go-version-file that names the
# WORKSPACE's go.mod, which is the consumer's tree and not a Go module,
# instead of this action's own. And a Go set up in a job that downloads the
# binary is time spent for nothing.
it "Go is set up from this action's own go.mod, by an action pinned to a SHA, only to compile"
assert_eq "true" "$(grep -Eq '^ *uses: actions/setup-go@[0-9a-f]{40}( #.*)?$' <<<"$go_setup" && echo true || echo false)" "setup-go pinned by a SHA"
assert_contains "$go_setup" 'go-version-file: ${{ github.action_path }}/go.mod' "setup-go's version file"
assert_contains "$go_setup" "if: inputs.setup == 'true' && steps.how.outputs.asset == ''" "setup-go's condition"

# The break: the proof dropped, or moved ahead of either install. `falconet
# version` shows the binary runs on this runner, and that it calls itself
# the tag the ref names.
it "and the installed binary is proved to run, and to be the tag, last"
installs_end="$(grep -n -e 'go install "github.com/zetlen/falconet' -e 'install -m 0755 "$RUNNER_TEMP/falconet"' <<<"$falconet_install" | tail -1 | cut -d: -f1)"
proof_at="$(grep -n '"$dest/falconet" version' <<<"$falconet_install" | cut -d: -f1)"
assert_eq 2 "$(grep -c -e 'go install "github.com/zetlen/falconet' -e 'install -m 0755 "$RUNNER_TEMP/falconet"' <<<"$falconet_install")" "install lines"
assert_eq "true" "$([[ -n "$installs_end" && -n "$proof_at" && "$installs_end" -lt "$proof_at" ]] && echo true || echo false)" \
  "both installs (last at $installs_end) precede the proof ($proof_at)"
assert_contains "$falconet_install" '"falconet $FALCONET_REF "*' "the version check"

it "the action with no verb is an install and nothing else"
verb_decl="$(awk '/^  verb:/{f=1} f && /^  [a-z]/ && !/^  verb:/{exit} f' "$ACTION")"
assert_contains "$verb_decl" "required: false" "verb input"
assert_contains "$verb_decl" "default: ''" "verb input"
assert_contains "$action_code" "if: inputs.verb != ''" "the Run step"

# "Check jq" lived here. The runner is asked for git, gitleaks, gh and the
# binary, and for nothing else; the case below that greps both files for jq
# is what replaced it.

# `setup: false` says "an earlier step in THIS job already installed them",
# and a job is a fresh runner, so the claim is about the job and never about
# the workflow. Since #19 every verb needs the install — the binary IS the
# install — so the rule is simply: in every job, the action with no verb
# (the install) comes before any step that runs falconet, through the action
# with setup: false or from PATH in a run: block.
#
# Dependency-shaped rather than positional, as before.
it "no step that runs falconet comes before the install in its job"
unmet="$(awk '
  function flush(   what) {
    if (buf ~ /uses: zetlen\/falconet@/ && buf !~ /setup: .false./) installed[job] = 1
    else {
      what = ""
      if (buf ~ /uses: zetlen\/falconet@/ && buf ~ /setup: .false./) { what = buf; sub(/.*verb: /, "", what); sub(/[^a-z-].*/, "", what) }
      else if (buf ~ /[ (]falconet [a-z-]+/) { what = buf; sub(/.*[ (]falconet /, "", what); sub(/[^a-z-].*/, "", what) }
      if (what != "" && !installed[job]) print job "/" what
    }
    buf = ""
  }
  /^  [a-z][a-z-]*:$/ { flush(); job = $1; sub(/:$/, "", job) }
  /^      - / { flush() }
  { buf = buf " " $0 }
  END { flush() }
' <<<"$wf_code")"
assert_eq "" "$unmet" "steps running falconet before their job installed it"

it "and every job installs exactly once"
assert_eq 4 "$(grep -c 'name: Install falconet and gitleaks' <<<"$wf_code")" "install steps"

# Four verbs read `git status`: prepare refuses a dirty tree, commit
# refuses every changed path outside the allowlist, untracked included, and
# implement and check refuse a config the agent changed. The
# handoff directory is written INSIDE the consumer's tree, it is not the
# agent's, and it is not anything a consumer's .gitignore can be relied on
# to know about. The tool's own checkout used to sit beside it — a composite
# action could only run from under the workspace — and without an exclude
# every run died in prepare on the checkout showing up as `??`, before the
# acknowledgment, the one failure the requester never hears about. The
# checkout is gone (#19); the handoff is not, and neither is the rule.
#
# Dependency-shaped, like the install check: the verbs that read git status
# must be preceded in their own job by the step that excludes the path.
it "the handoff is excluded before any verb reads git status"
unexcluded="$(awk '
  function flush(   verb) {
    if (buf ~ /name: Keep the handoff out of the working tree/)
      excluded[job] = 1
    verb = ""
    if (buf ~ /uses: zetlen\/falconet@/) {
      verb = buf; sub(/.*verb: /, "", verb); sub(/[^a-z].*/, "", verb)
    } else if (buf ~ /[ (]falconet (prepare|implement|check|commit)/) {
      verb = buf; sub(/.*[ (]falconet /, "", verb); sub(/[^a-z].*/, "", verb)
    }
    if (verb ~ /^(prepare|implement|check|commit)$/ && !excluded[job]) print job "/" verb
    buf = ""
  }
  /^  [a-z][a-z-]*:$/ { flush(); job = $1; sub(/:$/, "", job) }
  /^      - / { flush() }
  { buf = buf " " $0 }
  END { flush() }
' <<<"$wf_code")"
assert_eq "" "$unexcluded" "verbs reading git status with the handoff still visible"

it "and the exclude names the handoff directory and nothing else"
# ".falconet-tool/ .falconet/" until #19; the tool is on PATH now.
assert_eq 3 "$(grep -c "printf '%s\\\\n' .falconet/ >> .git/info/exclude" <<<"$wf_code")" "exclude lines"
assert_not_contains "$wf" ".falconet-tool" "workflow"

it "and writes it per clone, never into a file the commit verb could see"
assert_contains "$wf" ".git/info/exclude" "workflow"
assert_not_contains "$wf" ">> .gitignore" "workflow"

# --- attacker-controlled text never reaches a shell ------------------------

it "the action passes the verb through the environment, not a template"
assert_contains "$action" 'FALCONET_VERB: ${{ inputs.verb }}' "action"

it "and its arguments the same way"
assert_contains "$action" 'FALCONET_ARGS: ${{ inputs.args }}' "action"

it "and runs the binary from PATH, where the install put it"
assert_contains "$action_code" 'outcome="$(falconet "$FALCONET_VERB" $FALCONET_ARGS)"' "action"

it "no run block interpolates an issue body"
assert_not_contains "$wf" "github.event.issue.body" "workflow"

# --- the credential is an App, not a PAT and not a workaround --------------

it "tokens are minted per job by the App"
assert_contains "$wf" "actions/create-github-app-token" "workflow"

it "and the empty-commit workaround is not ported"
assert_not_contains "$wf" "allow-empty" "workflow"

# --- the README's caller grants what the jobs declare -----------------------
#
# The first consumer's first canary was a `startup_failure`: two runs, no
# jobs, no logs, and an issue with nothing on it. `publish` declares
# `contents: write` and step 8 of this README granted `contents: read`; a
# called workflow that requests more than its caller holds is rejected when
# the file is LOADED, which is before any job exists to report it and before
# the requester is acknowledged.
#
# So the install instructions are a contract too. Every permission the widest
# job declares must be the permission the README's caller tells people to
# grant, and the two drift the moment a job's needs change — silently, into
# somebody else's repository, where it costs them a failure that says
# nothing.
#
# The template is found by its markers, not by its heading, so it can move
# (to an appendix, say) without this file noticing.

caller="$(awk '/<!-- caller-workflow-template -->/ { s = 1; next } s && /<!-- \/caller-workflow-template -->/ { exit } s' "$REPO_ROOT/README.md")"
caller_perms="$(awk '/^permissions:/ { p = 1; next } p && /^[^[:space:]]/ { p = 0 } p' <<<"$caller")"

it "the README's caller template actually contains a caller with a permissions block"
# Everything below reads that block. Empty, and every case passes vacuously.
assert_contains "$caller" "uses: zetlen/falconet/.github/workflows/falconet.yml@" "README caller template"

it "and the block is not empty, which would pass every case below on nothing"
assert_eq "true" "$([[ -n "$caller_perms" ]] && echo true || echo false)" "the template's permissions block"

# `write` anywhere in the file at job-permission indentation is the widest a
# job asks for; otherwise `read`.
widest_declared() { # permission
  if grep -qE "^      $1: write$" "$WF"; then echo write; else echo read; fi
}

for perm in contents issues pull-requests; do
  want="$(widest_declared "$perm")"
  got="$(grep -E "^  $perm:" <<<"$caller_perms" | awk '{ print $2 }')"

  it "the README's caller grants $perm: $want, which is what the widest job declares"
  # assert_eq both ways round: granting LESS is the startup failure above,
  # granting MORE hands a consumer's repository an authority nothing here
  # needs, which is exactly the kind of over-grant nobody audits afterwards.
  assert_eq "$want" "$got" "the template's $perm grant"
done

it "and the README's input table names every input the workflow declares"
declared="$(awk '/^    inputs:$/{f=1; next} f && /^    secrets:$/{exit} f && /^      [a-z-]+:$/{sub(/^ +/, ""); sub(/:$/, ""); print}' "$WF" | sort)"
documented="$(awk -F'|' '/^\| Input \| Required/{f=1; next} f && !/^\|/{exit} f && /^\| `/{v=$2; gsub(/[` ]/, "", v); print v}' "$REPO_ROOT/README.md" | sort)"
assert_eq "$declared" "$documented" "inputs: declared vs the README's table"

it "and passes no falconet-ref, which the workflow no longer declares"
# A reusable workflow rejects an input it does not declare, at load: the
# same startup_failure, for a caller copied from an older README.
assert_not_contains "$caller" "falconet-ref" "README caller template"

it "the README's caller starts no run for a bot, a pull-request comment, or another label"
# falconet's comments and labels are issue events on the same issue. Each
# one that got past the caller would take a place in the issue's queue.
assert_contains "$caller" "github.event.sender.type != 'Bot'" "README caller template"
assert_contains "$caller" "!github.event.issue.pull_request" "README caller template"
assert_contains "$caller" "(github.event.action != 'labeled' || github.event.label.name == 'falconet')" "README caller template"

it "and listens for labeled, not opened: a request filed with the label fires labeled"
assert_contains "$caller" "types: [labeled, reopened]" "README caller template"

it "and queues every waiting run for an issue, so a newer event never cancels a person's reply"
# The default keeps one pending run per group and cancels the older.
assert_contains "$caller" "queue: max" "README caller template"
assert_eq 0 "$(grep -c '^concurrency:' <<<"$caller")" "workflow-level concurrency blocks in the template"

# --- the agent job is handed its source, because it cannot fetch it ---------
#
# The first consumer is a PRIVATE repository, and the first canary that got
# as far as a job died in `implement`: `permissions: {}` means a GITHUB_TOKEN
# with no `contents: read`, and a private repository answers that clone with
# "Repository not found". A public consumer would never have shown it.
#
# The fix keeps the boundary and moves the fetch: gate, which already holds a
# token, ships its checkout as an artifact. ADR-0005 then allowed the agent
# job exactly one checkout, falconet's own, because a composite action had to
# run from under the workspace; #19 retired that too — the action lives in
# the runner's action cache and what it fetches is this public module through
# the module proxy, with no token. So these cases guard the halves that make
# that safe: the
# agent job clones NOTHING, and what it receives cannot authenticate as
# anybody.

it "the agent job has no checkout at all — not of the consumer, not of falconet"
# "exactly one repository, and it is falconet" until #19.
assert_eq 0 "$(grep -c 'actions/checkout' <<<"$implement_job")" "checkouts in the implement job"

it "and no step anywhere names another repository to check out"
assert_eq 0 "$(grep -c '^ *repository:' <<<"$wf_code")" "repository: keys in the workflow"

it "the agent job takes the working tree from the gate's artifact"
assert_contains "$implement_job" "name: source-gate" "the implement job"

it "and refuses a tree whose HEAD is not the base the gate recorded"
# Every guard downstream compares against that commit. A silent mismatch
# would have the agent editing one tree and the reviewer reading another.
assert_contains "$implement_job" 'shipped HEAD is not the base the gate recorded' \
  "the implement job"

it "and refuses one that still has a remote to push to"
assert_contains "$implement_job" 'the shipped checkout still has a remote' "the implement job"

it "the gate strips the credential before it archives anything"
# checkout writes the token to a file under $RUNNER_TEMP and points
# .git/config at it with includeIf entries; prepare needs it while
# `git ls-remote origin` runs and not one step longer. Shipping it, or a
# pointer to it, would put a push-capable token within reach of the one job
# that must not have one.
tar_at="$(grep -n 'tar -czf' <<<"$gate_job" | head -1 | cut -d: -f1)"
# Each named line is matched as a fixed string: a step that keeps its
# listing but loses the command that acts on it fails here.
before_tar() {
  local at
  at="$(grep -nF -- "$1" <<<"$gate_job" | head -1 | cut -d: -f1)"
  assert_eq "true" "$([[ -n "$at" && -n "$tar_at" && "$at" -lt "$tar_at" ]] && echo true || echo false)" \
    "$2 ($at) comes before the tar ($tar_at)"
}
before_tar 'git config --local --unset-all "http.${GITHUB_SERVER_URL}/.extraheader"' "the extraheader unset"

# The break: the extraheader unset alone, which finds nothing in .git/config
# while the includeIf entries that name the credential file ship.
it "and the includeIf entries that point at the credential file come out before the tar"
before_tar "git config --local --name-only --get-regexp '^includeif\.'" "the includeIf listing"
before_tar 'git config --local --unset-all "$key"' "the unset of each listed includeIf key"
before_tar 'rm -f "$RUNNER_TEMP"/git-credentials-*.config' "the credential file's removal"

it "and fails closed if anything in .git still authenticates, or still points at something that does"
assert_contains "$gate_job" "refusing to ship a checkout that still authenticates" "the gate job"
failclosed_grep="$(grep 'refusing to ship' -B2 <<<"$gate_job" | grep -F 'grep -')"
assert_contains "$failclosed_grep" "-e includeif" "the gate's fail-closed grep"
# git writes checkout's section as [includeIf "gitdir:..."] and the header as
# AUTHORIZATION: a case-sensitive grep for the lowercase patterns sees neither.
assert_eq "true" "$([[ "$failclosed_grep" =~ grep\ -[A-Za-z]*i ]] && echo true || echo false)" \
  "the gate's fail-closed grep ignores case"

it "the archive leaves out the handoff, and has nothing else to leave out"
# "--exclude=./.falconet-tool --exclude=./.falconet" until #19.
tar_cmd="$(grep 'tar -czf' <<<"$gate_job")"
assert_contains "$tar_cmd" "--exclude=./.falconet " "the gate's tar"
assert_eq 1 "$(grep -o -- '--exclude=' <<<"$tar_cmd" | wc -l | tr -d ' ')" "exclusions in the gate's tar"

it "nothing bundles a whole history, which from a shallow clone is a broken bundle"
# `git bundle create <shallow> HEAD` exits 0 and `git bundle verify` calls it
# "a complete history"; the clone then dies on the first traversal, because
# the tip's parent was never fetched and nothing marks the result shallow.
# The one bundle here is a RANGE, whose prerequisite both ends already hold.
assert_eq 1 "$(grep -c 'git bundle create' <<<"$wf_code")" "git bundle create calls"

it "and the one bundle there is names a range, whose base both ends hold"
# Folded onto one line: the range sits on the continuation.
assert_contains "$(grep -A2 'git bundle create' <<<"$wf_code" | tr '\n' ' ')" \
  '..HEAD' "the bundle's ref argument"

# --- the artifacts that carry the handoff actually carry it -----------------
#
# The handoff directory's name starts with a dot, and
# `actions/upload-artifact` excludes hidden paths by DEFAULT — as a
# WARNING, with the step still green. So `gate` uploaded nothing, said
# success, and `implement` failed two jobs later on an artifact that had
# never existed. Every hand-off between jobs travels through one of these
# uploads, which makes a silent empty one the most expensive kind of green.

upload_flags="$(awk '
  /^      - uses: actions\/upload-artifact/ { inb = 1; path = 0; hidden = 0; nofiles = "none"; next }
  inb && /^      - / { print path, hidden, nofiles; inb = 0 }
  inb {
    if ($0 ~ /\.falconet/)                     path = 1
    if ($0 ~ /include-hidden-files: true/)     hidden = 1
    if ($0 ~ /if-no-files-found: error/)       nofiles = "error"
    if ($0 ~ /if-no-files-found: ignore/)      nofiles = "ignore"
  }
  END { if (inb) print path, hidden, nofiles }
' "$WF")"

it "there are artifact uploads to check, so the parse above found something"
assert_eq "true" "$([[ -n "$upload_flags" ]] && echo true || echo false)" "parsed upload steps"

it "every artifact whose path is the handoff directory includes hidden files"
unguarded=""
while read -r path hidden _; do
  [[ "$path" == 1 && "$hidden" != 1 ]] && unguarded="$unguarded one"
done <<<"$upload_flags"
assert_eq "" "$unguarded" "hidden-path uploads without include-hidden-files"

it "no upload names more than one path, which would move the archive's root"
# `path: |` with two entries roots the archive at their least common
# ancestor: `.falconet/` plus a file in RUNNER_TEMP came back as
# `<repo>/<repo>/.falconet/…`, and the consumer of that artifact looked where
# it had put things rather than where the uploader had. One path, one root.
assert_eq "" "$(awk '
  /^      - uses: actions\/upload-artifact/ { inb = 1; next }
  inb && /^      - / { inb = 0 }
  inb && /^          path: \|/ { print "multi" }
' "$WF")" "uploads with a multi-line path"

it "and every hand-off between jobs fails rather than upload nothing"
# The three that are plumbing: the handoff out of gate, the source out of
# gate, and the handoff out of implement.
# Comments stripped: the prose above the first upload names the setting.
assert_eq 3 "$(grep -c 'if-no-files-found: error' <<<"$wf_code")" \
  "uploads that fail on an empty result"

# --- a verb's stdout cannot break the step that ran it ----------------------
#
# The wrapper captures stdout and writes it to $GITHUB_OUTPUT. `push` printed
# "pushed <branch> (<sha>)", and `git push -u` prints "branch 'x' set up to
# track..." on stdout of its own — so the write was `name=value` with a
# newline in the value, which is "Invalid format". The `publish` job had
# already pushed the branch and then failed on the way out: no pull
# request, an issue parked for a human, over a log line.

it "the wrapper writes its output with a delimiter, not name=value"
assert_contains "$action" 'echo "outcome<<FALCONET_OUTCOME_EOF"' "action"

it "and closes it, because an unterminated heredoc swallows the rest of the file"
assert_eq 2 "$(grep -c 'FALCONET_OUTCOME_EOF' "$ACTION")" "delimiter lines"

# push's silence on stdout is asserted in push.test.sh, by running it. The grep
# of push.sh's source that used to sit here went with ADR-0006 D3 step 0.

# --- every job runs the same falconet, and it is the release's ---------------
#
# `uses:` cannot take an expression, so every verb step names a literal ref,
# and the action at that ref installs falconet at that ref. Four lines that
# disagree are a run whose jobs run two falconets: prepare's guards from one
# tag and commit's from another. A ref that is not a tag moves under a
# consumer between two runs. release-please rewrites the four lines in the
# release pull request, so the workflow at a tag names that tag, and between
# releases the lines name the last one, which is the manifest's version.

it "every uses: zetlen/falconet@ ref in the workflow is one literal"
refs="$(grep -o 'uses: zetlen/falconet@[^ ]*' <<<"$wf_code" | sort -u)"
assert_eq 1 "$(wc -l <<<"$refs" | tr -d ' ')" "distinct refs: $(tr '\n' ' ' <<<"$refs")"

it "and it is a tag, not a branch"
assert_eq "true" "$(grep -Eq '^uses: zetlen/falconet@v[0-9]+\.[0-9]+\.[0-9]+$' <<<"$refs" && echo true || echo false)" "the ref is vX.Y.Z: $(tr '\n' ' ' <<<"$refs")"

it "and every job pins one, so no job runs an unpinned falconet"
unpinned=""
for j in gate implement publish contain; do
  grep -q 'uses: zetlen/falconet@' <<<"$(job "$j")" || unpinned="$unpinned $j"
done
assert_eq "" "$unpinned" "jobs with no uses: zetlen/falconet@ step"

it "and none of the old shapes survives in either file"
assert_eq 0 "$(grep -c 'jq\|libexec\|falconet-tool\|falconet-ref\|bin/falconet' "$WF" "$ACTION" | awk -F: '{ n += $2 } END { print n + 0 }')" \
  "matches for the old shapes"
assert_not_contains "$wf" "uses: ./" "workflow"

# --- a release is a tag, its binaries, and the pins that name it --------------
#
# release-please opens a release pull request from the conventional commit
# subjects on main. Merging it creates a draft release and its tag. The
# release workflow then checks the pins, runs the suite, uploads the assets
# and publishes. Immutable releases lock the tag and the assets at publish.

RP_CONFIG="$REPO_ROOT/release-please-config.json"
RP_MANIFEST="$REPO_ROOT/.release-please-manifest.json"
REL="$REPO_ROOT/.github/workflows/release.yml"

# The break: a `uses:` line without the marker. release-please rewrites only
# the lines that carry it, so that job would keep the last tag while the
# other three move.
it "every uses: zetlen/falconet@ line carries the marker release-please rewrites"
pins="$(grep -c 'uses: zetlen/falconet@' <<<"$wf_code")"
marked="$(grep -c 'uses: zetlen/falconet@v[0-9.]* # x-release-please-version$' <<<"$wf_code")"
assert_eq "$pins" "$marked" "marked pins"

it "and release-please is told to rewrite that file"
assert_contains "$(cat "$RP_CONFIG")" '"path": ".github/workflows/falconet.yml"' "release-please config"

# The break: a manifest that drifts from the pins, which releases the next
# version with the wrong changelog base, or a pin bumped by hand.
it "the manifest's version is the tag the workflow pins"
manifest_version="$(sed -n 's/^ *"\." *: *"\([^"]*\)".*/\1/p' "$RP_MANIFEST")"
assert_eq "uses: zetlen/falconet@v$manifest_version" "$refs" "the pinned ref"

# The break: a release published the moment its tag exists. Immutable
# releases refuse an upload to a published release, and a consumer on that
# tag would find no asset.
it "a release is a draft, with its tag, until its binaries are on it"
assert_contains "$(cat "$RP_CONFIG")" '"draft": true' "release-please config"
assert_contains "$(cat "$RP_CONFIG")" '"force-tag-creation": true' "release-please config"

# The break: publishing before the checks, or before the upload. Every step
# must come before the one after it.
it "the release is published only after the pins, the suite, the build and the upload"
rel_code="$(grep -v '^[[:space:]]*#' "$REL")"
order=""
for needle in 'uses: zetlen/falconet@${FALCONET_TAG}' 'make test' 'make assets VERSION="$FALCONET_TAG"' 'gh release upload' '--draft=false'; do
  order="$order $(grep -n -F -- "$needle" <<<"$rel_code" | head -1 | cut -d: -f1)"
done
assert_eq "true" "$(awk '{ if (NF != 5) { print "false"; exit } for (i = 2; i <= NF; i++) if ($i + 0 <= $(i-1) + 0) { print "false"; exit } print "true" }' <<<"$order")" \
  "line numbers in order:$order"

it "and the release-please action is pinned to a SHA"
assert_eq "true" "$(grep -Eq '^ *uses: googleapis/release-please-action@[0-9a-f]{40}( #.*)?$' <<<"$rel_code" && echo true || echo false)" "release-please-action pinned"

# --- every action that is not falconet's is pinned to a commit ---------------
#
# A tag on someone else's repository is a pointer its owner can move, and the
# steps that run those actions hold the App token and the checkout. A 40-hex
# commit SHA names exactly one tree; the tag beside it in a comment is what a
# reader compares against the action's changelog when the pin moves.
# falconet's own refs are left to the cases above, and a local `./` path has
# no ref at all.
#
# The break: a `uses:` line on a tag or a branch, or a SHA with no tag beside
# it. The count is the break where the extraction matches nothing and every
# line passes on nothing: a new action is one more here.
third_party_uses="$(
  for f in "$REPO_ROOT"/.github/workflows/*.yml "$REPO_ROOT"/.github/workflows/*.yaml "$ACTION"; do
    [ -f "$f" ] || continue
    grep -v '^[[:space:]]*#' "$f"
  done
  grep -v '^[[:space:]]*#' <<<"$caller"
)"
third_party_uses="$(grep -E '^[[:space:]]*(-[[:space:]]+)?uses:' <<<"$third_party_uses" \
  | sed -E 's/^[[:space:]]*(-[[:space:]]+)?//' \
  | grep -v -E '^uses: (zetlen/falconet[@/]|\./)')"

it "every third-party action in the workflows, the action and the caller template is found"
assert_eq 17 "$(grep -c . <<<"$third_party_uses")" "third-party uses: lines"

it "and each is pinned to a commit SHA, with the tag it was taken from beside it"
assert_eq "" "$(grep -v -E '^uses: [A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40} # v[0-9]+\.[0-9]+\.[0-9]+$' <<<"$third_party_uses")" \
  "third-party uses: lines not pinned as owner/repo@<sha> # vX.Y.Z"

# The README's section on the binary is not a numbered step: the install
# needs no binary on a laptop. It names the prebuilt binary through mise and
# the compile through go install.
it "the README's section on the binary offers the release and go install"
binary_section="$(awk '/^## The binary on your machine/{f=1; next} f && /^## /{exit} f' "$REPO_ROOT/README.md")"
assert_contains "$binary_section" 'mise use github:zetlen/falconet' "README binary section"
assert_contains "$binary_section" 'go install github.com/zetlen/falconet/cmd/falconet@' "README binary section"

# --- one rule for a commit subject, in the hook and on the pull request ------
#
# release-please reads squash-merged pull request titles. The commit-msg
# hook and the pull request check run the same script, so the rule a
# contributor meets locally is the rule the merge meets.

SUBJECT_SCRIPT='scripts/conventional-subject.sh'

it "lefthook's commit-msg hook runs the subject script on the message file"
assert_contains "$(cat "$REPO_ROOT/lefthook.yml")" "run: bash $SUBJECT_SCRIPT {1}" "lefthook.yml"

it "the pull request check runs the same script"
title_code="$(grep -v '^[[:space:]]*#' "$REPO_ROOT/.github/workflows/pr-title.yml")"
assert_contains "$title_code" "bash $SUBJECT_SCRIPT" "pr-title.yml"

# The break: the title interpolated into the run: block. A title is typed by
# whoever opens the pull request.
it "and the title reaches it through env:, never through the run: block"
assert_contains "$title_code" 'PR_TITLE: ${{ github.event.pull_request.title }}' "pr-title.yml"
assert_eq 1 "$(grep -c 'github.event.pull_request.title' <<<"$title_code")" "mentions of the title"

summary
