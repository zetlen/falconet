#!/usr/bin/env bash
#
# contract.test.sh — structural invariants of the wrappers that a unit test of
# any single verb cannot see.
#
# Both bugs from run 32093607680 were WIRING, not logic: every piece was
# correct and one of them was in the wrong place. These cases guard the
# wiring. A new hand-over path that forgets to name its branch, a push that
# creeps back behind a condition, a tool grant that quietly widens — each
# fails here, where a unit test would see nothing wrong.

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

WF="$REPO_ROOT/.github/workflows/falconet.yml"
ACTION="$REPO_ROOT/action.yml"

wf="$(cat "$WF")"
action="$(cat "$ACTION")"

# Every park invocation, with backslash and YAML folded continuations pulled
# into one line, so a check can see the whole argument list.
park_calls="$(awk '
  /verb: park/ { inpark = 1; buf = ""; next }
  inpark {
    buf = buf " " $0
    if ($0 ~ /^[ \t]*- name:/ || $0 ~ /^[ \t]*- uses:/) { print buf; inpark = 0 }
  }
  END { if (inpark) print buf }
' "$WF")"

# --- the agent holds nothing it could publish with -------------------------

it "the agent's tool grant is exactly the five file tools"
assert_contains "$wf" '--allowedTools "Read,Edit,Write,Grep,Glob"' "workflow"

it "and names no Bash tool, which is the whole boundary"
assert_not_contains "$wf" 'Bash(' "workflow"

it "the agent job holds no permissions at all"
assert_contains "$wf" "permissions: {}" "workflow"

it "and the workflow's default is the same, so a new job must opt in"
# Comment lines excluded: the header explains the rule and would be counted.
assert_eq 2 "$(grep -v '^[[:space:]]*#' "$WF" | grep -c 'permissions: {}')" \
  "permissions: {} declarations"

it "the agent job checks out without persisting credentials"
assert_contains "$wf" "persist-credentials: false" "workflow"

it "and no step in the agent job is handed a token"
implement_job="$(awk '/^  implement:/{f=1} /^  publish:/{f=0} f' "$WF")"
assert_not_contains "$implement_job" "steps.token.outputs.token" "implement job"

# --- exactly one of each thing that must happen once -----------------------

it "there is exactly one validate step"
assert_eq 1 "$(grep -c 'verb: validate' "$WF")" "validate steps"

it "the branch is pushed exactly once"
assert_eq 1 "$(grep -c 'verb: push' "$WF")" "push steps"

it "and only by the push verb — nothing else runs git push"
assert_not_contains "$wf" "git push" "workflow"

it "there is no repair loop: commit happens once"
assert_eq 1 "$(grep -c 'verb: commit' "$WF")" "commit steps"

# --- the push is unconditional and comes first -----------------------------
#
# The one guard run 32093607680 bought. Every other exit used to leave the
# work on a runner that was destroyed minutes later.

it "the push step carries no condition"
push_step="$(awk '/name: Push/{f=1} f && /verb: push/{print; exit} f' "$WF")"
assert_not_contains "$push_step" "if:" "push step"

it "and nothing that publishes runs before it"
push_line="$(grep -n 'verb: push' "$WF" | cut -d: -f1)"
pr_line="$(grep -n 'gh pr create' "$WF" | cut -d: -f1)"
[[ "$push_line" -lt "$pr_line" ]] \
  && assert_eq "before" "before" "push at $push_line, pr create at $pr_line" \
  || assert_eq "push before pr create" "push=$push_line pr=$pr_line" "order"

it "the work is bundled before the agent job ends, so it outlives the runner"
assert_contains "$wf" "git bundle create" "workflow"

# --- every hand-over names its branch --------------------------------------

it "every park call passes --branch"
missing="$(printf '%s\n' "$park_calls" | grep -c -- '--branch' || true)"
total="$(grep -c 'verb: park' "$WF")"
assert_eq "$total" "$missing" "park calls passing --branch"

it "and reads PUSHED_BRANCH rather than the branch prepare intended"
assert_contains "$wf" 'env.PUSHED_BRANCH' "workflow"

it "there is a containment job that runs whatever happened"
assert_contains "$wf" "if: always() && needs.gate.outputs.outcome == 'ready'" "workflow"

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

it "the whole plan goes in the body, assembled rather than quoted"
assert_contains "$wf" "verb: assemble" "workflow"

# --- pinned binaries, installed before anything depends on them ------------

it "gitleaks is pinned by version"
assert_contains "$action" "gitleaks-version" "action"

it "and by digest, because a tag is a mutable pointer"
assert_contains "$action" "sha256sum -c -" "action"

it "and proves it runs before anything depends on it"
assert_contains "$action" "gitleaks version" "action"

it "the digest is checked before the tarball is unpacked"
sha_line="$(grep -n 'sha256sum -c -' "$ACTION" | cut -d: -f1)"
tar_line="$(grep -n 'tar -xzf' "$ACTION" | cut -d: -f1)"
[[ "$sha_line" -lt "$tar_line" ]] \
  && assert_eq "before" "before" "sha at $sha_line, tar at $tar_line" \
  || assert_eq "sha before tar" "sha=$sha_line tar=$tar_line" "order"

# `setup: false` says "an earlier step in THIS job already installed them",
# and a job is a fresh runner, so the claim is about the job and never about
# the workflow. A step that needs the binaries and is the first falconet step
# in its job has nothing behind that claim, and the secret scan fails closed,
# so the run dies on a missing scanner rather than on anything real.
#
# Dependency-shaped rather than positional: push, park and assemble need none
# of the pinned binaries and are free to skip the install.
it "no verb that needs the pinned binaries runs before an install in its job"
unmet="$(awk '
  function flush(   verb, installs) {
    if (buf ~ /uses: \.\/\.falconet-tool/) {
      verb = buf; sub(/.*verb: /, "", verb); sub(/[^a-z].*/, "", verb)
      installs = (buf !~ /setup: .false./)
      if (verb ~ /^(prepare|commit|validate)$/ && !installed[job] && !installs)
        print job "/" verb
      if (installs) installed[job] = 1
    }
    buf = ""
  }
  /^  [a-z][a-z-]*:$/ { flush(); job = $1; sub(/:$/, "", job) }
  /^      - / { flush() }
  { buf = buf " " $0 }
  END { flush() }
' "$WF")"
assert_eq "" "$unmet" "steps needing an install that never got one"

# Two verbs read `git status`: prepare refuses a dirty tree, and commit
# refuses every changed path outside the allowlist, untracked included. The
# tool's own checkout sits INSIDE the consumer's tree -- a composite action
# can only run from under the workspace -- and the handoff directory is
# written there too. Neither is the agent's, and neither is anything a
# consumer's .gitignore can be relied on to know about. Without an exclude,
# every run died in prepare on `?? .falconet-tool/`, before the
# acknowledgment -- the one failure the requester never hears about.
#
# Dependency-shaped, like the install check: the verbs that read git status
# must be preceded in their own job by the step that excludes both paths.
it "the tool checkout and the handoff are excluded before any verb reads git status"
unexcluded="$(awk '
  function flush(   verb) {
    if (buf ~ /name: Keep the tool and the handoff out of the working tree/)
      excluded[job] = 1
    if (buf ~ /uses: \.\/\.falconet-tool/) {
      verb = buf; sub(/.*verb: /, "", verb); sub(/[^a-z].*/, "", verb)
      if (verb ~ /^(prepare|commit)$/ && !excluded[job]) print job "/" verb
    }
    buf = ""
  }
  /^  [a-z][a-z-]*:$/ { flush(); job = $1; sub(/:$/, "", job) }
  /^      - / { flush() }
  { buf = buf " " $0 }
  END { flush() }
' "$WF")"
assert_eq "" "$unexcluded" "verbs reading git status with the tool still in the tree"

it "and the exclude names both the tool path and the handoff directory"
assert_contains "$wf" ".falconet-tool/ .falconet/" "workflow"

it "and writes it per clone, never into a file the commit verb could see"
assert_contains "$wf" ".git/info/exclude" "workflow"
assert_not_contains "$wf" ">> .gitignore" "workflow"

# --- the planning credentials ----------------------------------------------
#
# One secret, loaded in the two jobs that run tofu and in neither of the
# others. Where it is loaded is the invariant; what is in it is the
# consumer's business.

it "the jobs that run tofu load the planning credentials"
assert_eq 2 "$(grep -c 'name: Credentials for the stacks that plan' "$WF")" \
  "credential-loading steps"

it "and the agent's job is not one of them, which is the whole boundary"
assert_not_contains "$implement_job" "plan-env" "implement job"

it "the secret is optional, because a repository may need none"
plan_env_decl="$(awk '/^      plan-env:/{f=1} f && /required:/{print; exit}' "$WF")"
assert_contains "$plan_env_decl" "required: false" "plan-env declaration"

it "the value travels by environment, never by template expression"
assert_eq 2 "$(grep -c 'FALCONET_PLAN_ENV: ${{ secrets.plan-env }}' "$WF")" \
  "env-passed references"

it "and every line of it is masked before it can reach a log"
assert_contains "$wf" "::add-mask::" "workflow"

# --- attacker-controlled text never reaches a shell ------------------------

it "the action passes the verb through the environment, not a template"
assert_contains "$action" 'FALCONET_VERB: ${{ inputs.verb }}' "action"

it "and its arguments the same way"
assert_contains "$action" 'FALCONET_ARGS: ${{ inputs.args }}' "action"

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
# job declares must be the permission step 8 tells people to grant, and the
# two drift the moment a job's needs change — silently, into somebody else's
# repository, where it costs them a failure that says nothing.

caller="$(awk '/^### 8\./ { s = 1 } s && /^### 9\./ { exit } s' "$REPO_ROOT/README.md")"
caller_perms="$(awk '/^permissions:/ { p = 1; next } p && /^[^[:space:]]/ { p = 0 } p' <<<"$caller")"

it "the README's step 8 actually contains a caller with a permissions block"
# Everything below reads that block. Empty, and every case passes vacuously.
assert_contains "$caller" "uses: zetlen/falconet/.github/workflows/falconet.yml@" "README step 8"

it "and the block is not empty, which would pass every case below on nothing"
assert_eq "true" "$([[ -n "$caller_perms" ]] && echo true || echo false)" "step 8's permissions block"

# `write` anywhere in the file at job-permission indentation is the widest a
# job asks for; otherwise `read`.
widest_declared() { # permission
  if grep -qE "^      $1: write$" "$WF"; then echo write; else echo read; fi
}

for perm in contents issues pull-requests; do
  want="$(widest_declared "$perm")"
  got="$(grep -E "^  $perm:" <<<"$caller_perms" | awk '{ print $2 }')"

  it "step 8 grants $perm: $want, which is what the widest job declares"
  # assert_eq both ways round: granting LESS is the startup failure above,
  # granting MORE hands a consumer's repository an authority nothing here
  # needs, which is exactly the kind of over-grant nobody audits afterwards.
  assert_eq "$want" "$got" "step 8's $perm grant"
done

# --- the agent job is handed its source, because it cannot fetch it ---------
#
# The first consumer is a PRIVATE repository, and the first canary that got
# as far as a job died in `implement`: `permissions: {}` means a GITHUB_TOKEN
# with no `contents: read`, and a private repository answers that clone with
# "Repository not found". A public consumer would never have shown it.
#
# The fix keeps the boundary and moves the fetch: gate, which already holds a
# token, ships its checkout as an artifact. So these cases guard the two
# halves that make that safe — the agent still clones nothing of the
# consumer's, and what it receives cannot authenticate as anybody.

gate_job="$(awk '/^  gate:/ { f = 1 } /^  implement:/ { f = 0 } f' "$WF")"

it "the agent job clones exactly one repository"
assert_eq 1 "$(grep -c 'uses: actions/checkout' <<<"$implement_job")" \
  "checkouts in the implement job"

it "and it is falconet, which is public, never the repository being worked on"
assert_contains "$implement_job" "repository: zetlen/falconet" "the implement job's checkout"

it "the agent job takes the working tree from the gate's artifact"
assert_contains "$implement_job" "name: source-gate" "the implement job"

it "and refuses a tree whose HEAD is not the base the gate recorded"
# Every guard downstream compares against that commit. A silent mismatch
# would have the agent editing one tree and the plan describing another.
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

it "the archive carries neither the tool nor the handoff"
assert_contains "$gate_job" "--exclude=./.falconet-tool --exclude=./.falconet" "the gate's tar"

it "nothing bundles a whole history, which from a shallow clone is a broken bundle"
# `git bundle create <shallow> HEAD` exits 0 and `git bundle verify` calls it
# "a complete history"; the clone then dies on the first traversal, because
# the tip's parent was never fetched and nothing marks the result shallow.
# The one bundle here is a RANGE, whose prerequisite both ends already hold.
# Comments stripped first: the prose above this file's tar step says
# "git bundle create" while explaining why it is not used.
wf_code="$(grep -v '^[[:space:]]*#' "$WF")"
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
# gate, and the handoff out of implement. Not the plan artifact — a run that
# parked before it planned has no plan, and that is not a failure.
# Comments stripped: the prose above the first upload names the setting.
assert_eq 3 "$(grep -v '^[[:space:]]*#' "$WF" | grep -c 'if-no-files-found: error')" \
  "uploads that fail on an empty result"

# --- a verb's stdout cannot break the step that ran it ----------------------
#
# The wrapper captures stdout and writes it to $GITHUB_OUTPUT. `push` printed
# "pushed <branch> (<sha>)", and `git push -u` prints "branch 'x' set up to
# track..." on stdout of its own — so the write was `name=value` with a
# newline in the value, which is "Invalid format". The `publish` job had
# already pushed the branch and then failed on the way out: no validate, no
# pull request, an issue parked for a human, over a log line.

it "the wrapper writes its output with a delimiter, not name=value"
assert_contains "$action" 'echo "outcome<<FALCONET_OUTCOME_EOF"' "action"

it "and closes it, because an unterminated heredoc swallows the rest of the file"
assert_eq 2 "$(grep -c 'FALCONET_OUTCOME_EOF' "$ACTION")" "delimiter lines"

# push's silence on stdout is asserted in push.test.sh, by running it. The grep
# of push.sh's source that used to sit here went with ADR-0006 D3 step 0.

# --- the release refuses to publish bytes it cannot reproduce ---------------
#
# ADR-0006 D6 asks a consumer's pinned SHA to vouch for a binary that did not
# exist when they pinned it: the SHA-256 of the linux_amd64 asset is committed
# in the tree BEFORE the tag, and the only thing that makes it true afterwards
# is that the build reproduces. The compare is what turns that from a hope
# into a check, and it is worth nothing unless it happens before anything is
# published — a release with one asset already uploaded is a release someone
# can download.
#
# So these cases hold the ordering, and they hold the flags, because every one
# of the four was measured to change the bytes: without -buildvcs=false a
# dirty pre-tag tree and a clean tree at the tag differ by construction;
# without -trimpath the absolute path of the checkout is in the binary;
# without CGO_ENABLED=0 the runner (building natively, with a C compiler
# present) turns cgo on where a laptop cross-compiling the same target leaves
# it off; without -buildid= the link stamps an id.

REL="$REPO_ROOT/.github/workflows/release.yml"
MK="$REPO_ROOT/Makefile"
rel="$(cat "$REL")"
mk="$(cat "$MK")"
# Comments stripped where a case is about what the file DOES: the prose above
# each step names the thing it is explaining not to do.
rel_code="$(grep -v '^[[:space:]]*#' "$REL")"

it "the release runs on a tag push and on nothing else"
assert_contains "$rel" "tags: ['v*']" "release workflow"
assert_not_contains "$rel_code" "workflow_dispatch" "release workflow"

it "and pins the Go toolchain to go.mod's, because GOTOOLCHAIN=auto is a floor"
assert_contains "$rel" "sed -n 's/^toolchain //p' go.mod" "release workflow"
assert_contains "$rel" 'echo "GOTOOLCHAIN=$tc" >> "$GITHUB_ENV"' "release workflow"

it "the build goes through the Makefile, so the flags have one definition"
assert_contains "$rel" "make release-build" "release workflow"

it "and the workflow holds no build flags of its own to drift from it"
assert_not_contains "$rel_code" "go build" "release workflow"

it "the Makefile's release build refuses VCS stamping"
assert_contains "$mk" "-buildvcs=false" "Makefile"

it "trims the path out of the binary"
assert_contains "$mk" "-trimpath" "Makefile"

it "turns cgo off explicitly, rather than inheriting the host's default"
assert_contains "$mk" "CGO_ENABLED=0" "Makefile"

it "and clears the build id"
assert_contains "$mk" "-buildid=" "Makefile"

it "the compare-and-refuse step comes before the release is created"
verify_line="$(grep -n 'make release-verify' "$REL" | cut -d: -f1)"
create_line="$(grep -n 'gh release create' "$REL" | cut -d: -f1)"
[[ -n "$verify_line" && -n "$create_line" && "$verify_line" -lt "$create_line" ]] \
  && assert_eq "before" "before" "verify at $verify_line, release create at $create_line" \
  || assert_eq "verify before release create" "verify=$verify_line create=$create_line" "order"

it "and before any asset is named for upload"
asset_line="$(grep -n 'dist/falconet_linux_amd64' "$REL" | tail -1 | cut -d: -f1)"
[[ -n "$verify_line" && -n "$asset_line" && "$verify_line" -lt "$asset_line" ]] \
  && assert_eq "before" "before" "verify at $verify_line, first asset at $asset_line" \
  || assert_eq "verify before upload" "verify=$verify_line asset=$asset_line" "order"

it "the tag reaches the shell as an environment variable, never a template"
# A tag name is chosen by whoever pushes the tag, and ${{ }} is pasted in
# before bash sees it: `$(…)` in a tag would run. The Makefile then refuses
# any tag that is not vX.Y.Z before it reaches a compiler flag.
assert_contains "$rel" 'VERSION="$GITHUB_REF_NAME"' "release workflow"
assert_not_contains "$rel_code" "github.ref_name" "release workflow"

it "only the job that publishes is granted anything"
assert_contains "$rel" "contents: write" "release workflow"
assert_eq 1 "$(grep -v '^[[:space:]]*#' "$REL" | grep -c 'permissions: {}')" \
  "permissions: {} declarations"

it "the digest in the tree is sha256sum's own format, so sha256sum -c reads it"
digest_file="$REPO_ROOT/release/falconet_linux_amd64.sha256"
assert_eq "true" "$([[ -f "$digest_file" ]] && echo true || echo false)" "$digest_file exists"
assert_eq "true" \
  "$(grep -Eq '^[0-9a-f]{64}  falconet_linux_amd64$' "$digest_file" && echo true || echo false)" \
  "digest line shape"

it "and the version recorded beside it is a release tag, so a stale digest is caught"
assert_eq "true" \
  "$(grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' "$REPO_ROOT/release/VERSION" && echo true || echo false)" \
  "release/VERSION"

summary
