# Operating falconet

What the operator does, what only the operator can do, and where the pieces
live. This is not a contributor guide — see [Support](../README.md#support)
for why there isn't one.

## Three things an agent cannot do for you

All three are credentials. Ask for them when they're needed; do not attempt to
create GitHub resources, register apps, or mint keys on the operator's behalf.

**A GitHub App, registered purely as a credential.** No webhook, nothing
hosted — just an App ID and a private key stored as repository secrets. The
action mints installation tokens with `actions/create-github-app-token`, so
output is authored by `falconet[bot]` rather than by a person.

This also fixes a real bug for free. Pull requests opened with `GITHUB_TOKEN`
do not trigger workflows, so CI never runs on them; pushes authenticated with
an App token do. The old workaround was pushing an empty commit with a scoped
PAT — **that idea is deleted, not ported.**

**A dedicated `ANTHROPIC_API_KEY`, with a budget alert.** Deliberately an API
key rather than a subscription OAuth token, so falconet's spend stays a
separate number instead of disappearing into the operator's subscription.
`max-turns` and a 30-minute timeout are the run guardrails.

**The environment the stacks plan in, as `plan-env`.** A JSON object of the
variables `tofu init` and `tofu plan` need — backend keys, provider tokens,
`TF_VAR_*` — stored as one repository secret in the consuming repository. The
workflow masks each value and loads it into the two jobs that run tofu, and
into neither of the other two: the agent's job holds no credential of any
kind. Scope it to what falconet plans; it never applies, so it needs nothing
that could.

## Where things are

**This repository** is public at `zetlen/falconet`, and `main` is what
consumers pin — both `uses: zetlen/falconet/.github/workflows/falconet.yml@main`
and the `falconet-ref` input default to it. A tag is worth cutting once a run
has actually succeeded; until then a moving `main` is the point, because
development is integration.

Public means every push is a publication. One value was already redacted
during extraction (a Cloudflare account ID); `wayfinders-infra` is private, so
anything further brought over from it must be read before it is committed
here, not after.

**The consuming repository** is `wayfinders-infra` (private,
`zetlen/wayfinders-infra`) — falconet's only integration environment, and
deliberately so until falconet stops being a personal project
([ADR-0002](adr/0002-extract-the-pipeline-into-falconet.md), move 3). It still
holds its own copies of `scripts/`: they were copied, not moved, because its
`work-infra-issues` skill is currently the only thing working its request
queue.

Development *is* integration. There is no build-then-integrate phase — the
orchestrator is Actions YAML and cannot run outside a consuming repository, so
`wayfinders-infra` consumes `falconet@main` from the first working commit.

**The provenance** is [`docs/provenance/`](provenance/), extracted at
`wayfinders-infra@97b5669` — the last commit where the pipeline was live.

## What deliberately stayed behind

`ci-deploy-receipt.sh` reports on an apply. falconet never applies, so it
remains in `wayfinders-infra` and is not part of this corpus.

## What deliberately is not added

Public from day one, MIT, a personal project. No code of conduct, no issue
templates, no contributor guide, no marketplace listing. Those are a different
level of commitment and it has not been made. The README says so plainly, and
that is the whole of the promise.
