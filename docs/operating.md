# Operating falconet

What the operator does, what only the operator can do, and where the pieces
live. This is not a contributor guide — see [Support](../README.md#support)
for why there isn't one.

## Three things an agent cannot do for you

All three are credentials. Ask for them when they're needed; do not attempt to
create GitHub resources, register apps, or mint keys on the operator's behalf
— not without the operator running `falconet init`, which is the one place
two of the three are created, with the operator at the keyboard, and the
third is stored. A fourth credential exists only to install the other three:
`FALCONET_SETUP_TOKEN`, a fine-grained personal access token the operator
mints, scoped to the one repository with a seven-day expiry (README step 2).
`init` writes through it and `doctor` reads through it; neither reads
`GITHUB_TOKEN` or `GH_TOKEN`, on purpose (ADR-0006 D4).

**A GitHub App, registered purely as a credential.** No webhook, nothing
hosted — just an App ID and a private key stored as repository secrets. The
action mints installation tokens with `actions/create-github-app-token`, so
output is authored by `falconet[bot]` rather than by a person. `init`
registers it by manifest: it serves a page on localhost, the operator clicks
**Create GitHub App** on GitHub's page and **Install** on the App's, and the
private key goes from GitHub's answer into a sealed box and into the
repository's secrets — never to disk (ADR-0006 D5). An App made by hand is
handed over with `--app-id` and `--app-key` instead.

This also fixes a real bug for free. Pull requests opened with `GITHUB_TOKEN`
do not trigger workflows, so CI never runs on them; pushes authenticated with
an App token do. The old workaround was pushing an empty commit with a scoped
PAT — **that idea is deleted, not ported.**

**A dedicated `ANTHROPIC_API_KEY`, with a budget alert.** Deliberately an API
key rather than a subscription OAuth token, so falconet's spend stays a
separate number instead of disappearing into the operator's subscription.
`max-turns` and a 30-minute timeout are the run guardrails. The operator
mints it; `init` reads it from a no-echo prompt, or from stdin — never from
an argument — and seals it into the repository's secrets.

**The environment the stacks plan in, as `plan-env`.** A JSON object of the
variables `tofu init` and `tofu plan` need — backend keys, provider tokens,
`TF_VAR_*` — stored as one repository secret in the consuming repository. The
workflow masks each value and loads it into the two jobs that run tofu, and
into neither of the other two: the agent's job holds no credential of any
kind. Scope it to what falconet plans; it never applies, so it needs nothing
that could. `init --plan-env-file` seals it from a file the operator names,
kept outside the repository, and refuses the file unless it is a JSON object
whose values are all strings.

## Where things are

**This repository** is public at `zetlen/falconet`. `main` is integration:
development lands there, and it moves. A consumer pins a **tag** in `uses:`
— `zetlen/falconet/.github/workflows/falconet.yml@v0.2.0` — and the
workflow at that tag installs, in every job, the binary whose digest the
tree at that tag holds (ADR-0006 D6); `falconet init` writes the tag of the
binary that ran it, and upgrading is moving the tag. A release is cut the
way the Makefile says: `make release-prep VERSION=vX.Y.Z` as the **last**
commit before the tag — it writes `release/VERSION`, the linux_amd64 digest
and the workflow's own `uses: zetlen/falconet@vX.Y.Z` refs, prints the `git
add`, `git commit`, `git tag` and `git push` for a person to run, and says
why it must be last: "a digest describes one build of one tree, and any
later commit that touches cmd/, internal/ or go.mod makes it stale." The tag
push runs `release.yml`, which rebuilds those bytes and refuses to publish
anything if they differ.

Public means every push is a publication. One value was already redacted
during extraction (a Cloudflare account ID); `wayfinders-infra` is private, so
anything further brought over from it must be read before it is committed
here, not after.

**The consuming repository** is `wayfinders-infra` (private,
`zetlen/wayfinders-infra`) — falconet's only integration environment, and
deliberately so until falconet stops being a personal project
([ADR-0002](history/0002-extract-the-pipeline-into-falconet.md), move 3). It still
holds its own copies of `scripts/`: they were copied, not moved, because its
`work-infra-issues` skill is currently the only thing working its request
queue.

Development *is* integration. There is no build-then-integrate phase — the
orchestrator is Actions YAML and cannot run outside a consuming repository.
`wayfinders-infra` consumed `falconet@main` from the first working commit and
ran the one live run so far on it (2026-08-21, issue #106 → PR #108, on the
bash); it pins a tag now, and the canary after v0.2.0 is the binary's first
live run.

## What deliberately stayed behind

`ci-deploy-receipt.sh` reports on an apply. falconet never applies, so it
remains in `wayfinders-infra` and is not part of this corpus.

## What deliberately is not added

Public from day one, MIT, a personal project. No code of conduct, no issue
templates, no contributor guide, no marketplace listing. Those are a different
level of commitment and it has not been made. The README says so plainly, and
that is the whole of the promise.
