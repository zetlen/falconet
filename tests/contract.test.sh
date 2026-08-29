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
# Since #19 the wrappers install the binary instead of checking falconet out
# into the consumer's tree — first as a release asset with a digest in the
# tree, and since the release apparatus went, as a `go install` of this
# module at the action's own ref. The cases that held the checkout's
# invariants — the tool path in the exclude and in the tar, the jq check,
# the falconet-ref input, the one permitted checkout in the agent job — and
# then the asset's — the digest file, the release workflow, the Makefile's
# release targets — are retired below, each where it stood, with what
# replaced it.

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

# --- the agent holds nothing it could publish with -------------------------

it "the agent's tool grant is exactly the five file tools"
assert_contains "$wf" '--allowedTools "Read,Edit,Write,Grep,Glob"' "workflow"

it "and names no Bash tool, which is the whole boundary"
assert_not_contains "$wf" 'Bash(' "workflow"

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

it "nothing plans: the plan bot on the pull request does that"
assert_eq 0 "$(grep -c -E 'verb: (validate|assemble)|falconet plan-env|plan-env:' <<<"$wf_code")" "plan-side steps"
assert_not_contains "$action_code" "setup-opentofu" "action"

it "the branch is pushed exactly once"
assert_eq 1 "$(grep -c 'verb: push' <<<"$wf_code")" "push steps"

it "and only by the push verb — nothing else runs git push"
assert_not_contains "$wf" "git push" "workflow"

it "there is no repair loop: commit happens once"
assert_eq 1 "$(grep -c 'verb: commit' <<<"$wf_code")" "commit steps"

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

it "and there are three of them: two endings in publish and the containment"
assert_eq 3 "$total" "pause calls"

it "and the ones in publish read PUSHED_BRANCH rather than the branch prepare intended"
# The branch that IS on the remote, set by the push verb; empty when nothing
# was pushed, which pause takes as "no branch".
publish_pauses="$(awk '/falconet pause/ { p = 1; buf = "" } p { buf = buf " " $0; if ($0 !~ /\\$/) { print buf; p = 0 } }' <<<"$publish_job")"
assert_eq 2 "$(grep -c -- '--branch "${PUSHED_BRANCH:-}"' <<<"$publish_pauses")" "publish pauses on \$PUSHED_BRANCH"

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

it "and there is no second agent"
assert_eq 1 "$(grep -c 'claude-code-action' "$WF")" "agent invocations"

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
sha_line="$(grep -n 'sha256sum -c -' "$ACTION" | head -1 | cut -d: -f1)"
tar_line="$(grep -n 'tar -xzf' "$ACTION" | cut -d: -f1)"
[[ "$sha_line" -lt "$tar_line" ]] \
  && assert_eq "before" "before" "sha at $sha_line, tar at $tar_line" \
  || assert_eq "sha before tar" "sha=$sha_line tar=$tar_line" "order"

# falconet itself is not downloaded at all: it is `go install`ed from this
# module at the ref the caller's `uses:` named. There is no asset to hash —
# the module proxy serves the source and the checksum database vouches for
# it — so what these cases hold is that the install IS that, at THAT ref,
# and that the ref reaches the shell in the one way that works. Comments
# stripped: the prose above the steps names every shape they refuse.
go_setup="$(awk '/name: Set up Go/{f=1} /name: Install falconet/{f=0} f' <<<"$action_code")"
falconet_install="$(awk '/name: Install falconet/{f=1} /name: Run$/{f=0} f' <<<"$action_code")"

# The break: a URL, a tarball, a digest file — any install that is not a
# compile of this module is the retired row growing back without its row;
# and a version written into the action would have every ref install the
# same one.
it "falconet is go-installed from this module at the action's ref"
assert_contains "$falconet_install" 'go install "github.com/zetlen/falconet/cmd/falconet@$FALCONET_REF"' "action"

# The break: `${{ github.action_ref }}` pasted into the run: block instead.
# Inside a composite action it is populated when a step's env: is evaluated
# and EMPTY by the time its run: block is (actions/runner#2473) — so the
# install would be `go install …@`, an error, in every job of every run.
it "and the ref reaches the shell through env:, never through the run: block"
assert_contains "$falconet_install" 'FALCONET_REF: ${{ github.action_ref }}' "action"
assert_eq 1 "$(grep -c 'github.action_ref' <<<"$falconet_install")" "mentions of github.action_ref in the install step"

# The break: `uses: ./`. A local path has no ref, and an empty one must fail
# here, by name, rather than as whatever `go install` makes of it.
it "and refuses an empty ref rather than installing something else"
assert_contains "$falconet_install" 'if [ -z "$FALCONET_REF" ]' "action"

# The break: a floating tag on setup-go — every other action here floats,
# and the compiler is not to be one more moving part — or a go-version-file
# that names the WORKSPACE's go.mod, which is the consumer's tree and not a
# Go module, instead of this action's own.
it "Go is set up from this action's own go.mod, by an action pinned to a SHA"
assert_eq "true" "$(grep -Eq '^ *uses: actions/setup-go@[0-9a-f]{40}( #.*)?$' <<<"$go_setup" && echo true || echo false)" "setup-go pinned by a SHA"
assert_contains "$go_setup" 'go-version-file: ${{ github.action_path }}/go.mod' "setup-go's version file"

# The break: the proof dropped, or moved ahead of the install. `falconet
# version` is what shows the compile produced something that runs, and that
# the version the go command resolved the ref to is the tag the ref names.
it "and the installed binary is proved to run, and to be the tag, last"
go_install_at="$(grep -n 'go install "github.com/zetlen/falconet' <<<"$falconet_install" | cut -d: -f1)"
proof_at="$(grep -n '"$dest/falconet" version' <<<"$falconet_install" | cut -d: -f1)"
assert_eq "true" "$([[ -n "$go_install_at" && -n "$proof_at" && "$go_install_at" -lt "$proof_at" ]] && echo true || echo false)" \
  "the install ($go_install_at) precedes the proof ($proof_at)"
assert_contains "$falconet_install" '"falconet $FALCONET_REF "*' "the version check"

it "the action with no verb is an install and nothing else"
verb_decl="$(awk '/^  verb:/{f=1} f && /^  [a-z]/ && !/^  verb:/{exit} f' "$ACTION")"
assert_contains "$verb_decl" "required: false" "verb input"
assert_contains "$verb_decl" "default: ''" "verb input"
assert_contains "$action_code" "if: inputs.verb != ''" "the Run step"

# "Check jq" lived here. The runner is asked for git, gitleaks and the
# binary, and for nothing else (ADR-0006 D2); the case below that greps both
# files for jq is what replaced it.

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

# Two verbs read `git status`: prepare refuses a dirty tree, and commit
# refuses every changed path outside the allowlist, untracked included. The
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
    if (buf ~ /uses: zetlen\/falconet@/) {
      verb = buf; sub(/.*verb: /, "", verb); sub(/[^a-z].*/, "", verb)
      if (verb ~ /^(prepare|commit)$/ && !excluded[job]) print job "/" verb
    }
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

it "and passes no falconet-ref, which the workflow no longer declares"
# A reusable workflow rejects an input it does not declare, at load: the
# same startup_failure, for a caller copied from an older README.
assert_not_contains "$caller" "falconet-ref" "README caller template"

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
# checkout persists it into .git/config as an extraheader; prepare needs it
# while `git ls-remote origin` runs and not one step longer. Shipping it
# would put a push-capable token inside the one job that must not have one.
unset_at="$(grep -n 'unset-all' <<<"$gate_job" | head -1 | cut -d: -f1)"
tar_at="$(grep -n 'tar -czf' <<<"$gate_job" | head -1 | cut -d: -f1)"
assert_eq "true" "$([[ -n "$unset_at" && -n "$tar_at" && "$unset_at" -lt "$tar_at" ]] && echo true || echo false)" \
  "the unset ($unset_at) comes before the tar ($tar_at)"

it "and fails closed if anything in .git still authenticates"
assert_contains "$gate_job" "refusing to ship a checkout that still authenticates" "the gate job"

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
# `actions/upload-artifact@v4` excludes hidden paths by DEFAULT — as a
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

# --- every job runs the same falconet, and it is a tag's --------------------
#
# `uses:` cannot take an expression, so every verb step names a literal ref,
# and the action at that ref go-installs the module at that ref. Four lines
# that disagree are a run whose jobs run two falconets — prepare's guards
# from one tag and commit's from another — and a ref that is not a tag is
# one that moves under a consumer between two runs. WHICH tag is not for a
# test in the tree to know: the workflow at a tag names that tag, written by
# hand as the last commit before it (operating.md), and between tags the
# lines name the last one.

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
# #19's Done-when, widened: the local action path, the tool checkout, the
# input that chose it, the jq dependency, the bash dispatcher's path.
assert_eq 0 "$(grep -c 'jq\|libexec\|falconet-tool\|falconet-ref\|bin/falconet' "$WF" "$ACTION" | awk -F: '{ n += $2 } END { print n + 0 }')" \
  "matches for the old shapes"
assert_not_contains "$wf" "uses: ./" "workflow"

# --- there is no release apparatus -------------------------------------------
#
# The retired row: a digest committed ahead of the tag, a release workflow
# that rebuilt the bytes and refused to publish on a mismatch, four assets
# and a checksums file beside them, and a Makefile that wrote the version,
# the digest and the workflow's refs in one second. The break is any of it
# growing back — a release/ directory, a release.yml, a Makefile target, a
# README step that downloads an asset — without the register row that would
# have to come back with it.

MK="$REPO_ROOT/Makefile"

it "nothing names the release directory, its targets, or a release asset"
old_release="$(grep -n -E 'release/VERSION|release-prep|release-verify|release-build|zetlen/falconet/releases/download|checksums\.txt' \
  "$WF" "$ACTION" "$MK" "$REPO_ROOT/README.md" "$REPO_ROOT/AGENTS.md" \
  "$REPO_ROOT/.github/workflows/ci.yml" "$REPO_ROOT/docs/operating.md" "$REPO_ROOT/docs/decisions.md" || true)"
assert_eq "" "$old_release" "references to the release apparatus"

it "and there is no release directory and no release workflow"
assert_file_missing "$REPO_ROOT/release"
assert_file_missing "$REPO_ROOT/.github/workflows/release.yml"

it "and the Makefile has no release target"
assert_eq 0 "$(grep -c -E '^release[a-z-]*:' "$MK")" "release targets"

it "and the README's first step is go install of this module, not a download"
step1="$(awk '/^### 1\. Install the binary/{f=1; next} f && /^### /{exit} f' "$REPO_ROOT/README.md")"
assert_contains "$step1" 'go install github.com/zetlen/falconet/cmd/falconet@' "README step 1"
assert_not_contains "$step1" 'curl' "README step 1"

summary
