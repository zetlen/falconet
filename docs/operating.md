# Operating falconet

What the operator does, what only the operator can do, and where the pieces
live.

## Two things an agent cannot do for you

Both are credentials. Ask for them when they are needed; do not attempt to
create GitHub resources, register apps, or mint keys on the operator's
behalf. The operator makes each one by hand, at the keyboard, following the
README's steps, and puts it into a repository secret by hand.

**A GitHub App, registered purely as a credential.** No webhook, nothing
hosted: an App ID and a private key stored as repository secrets. The
workflow mints installation tokens with `actions/create-github-app-token`,
so output is authored by `falconet[bot]` rather than by a person. The
operator registers it on GitHub's **New GitHub App** page, installs it on
the one repository, and puts the App ID and the downloaded private key into
`FALCONET_APP_ID` and `FALCONET_APP_PRIVATE_KEY` with `gh secret set`
([README step 3](../README.md#3-create-the-github-app-and-store-its-two-secrets));
the `.pem` is deleted once the secret holds it.

The App token also carries a property `GITHUB_TOKEN` lacks: pull requests
opened with `GITHUB_TOKEN` do not trigger workflows, so CI never runs on
them, while pushes authenticated with an App token trigger them normally.

**A dedicated model API key, with a budget alert.** The one secret the agent
job holds. For the default harness it is an Anthropic API key rather than a
subscription token, so falconet's spend stays a separate number. The
default harness's turn cap and the agent job's 60-minute timeout are the
run guardrails. The operator mints it and stores it under the name the
harness reads, `ANTHROPIC_API_KEY` by default, with `gh secret set`
([README step 4](../README.md#4-store-the-model-api-key)).

**No cloud credential at all.** falconet produces no evidence for the
reviewer, so no job of its holds a backend key or a provider token; the
agent's job holds no credential but the model key. The checks the consuming
repository runs on its pull requests hold whatever they need, on their own
side, and that is the operator's to configure, including making sure they
run on the pull requests the App opens
([the register](decisions.md#falconet-produces-no-evidence)).

## Where things are

**This repository** is public at `zetlen/falconet`. `main` is integration:
development lands there, and it moves. A consumer pins a **tag** in `uses:`,
`zetlen/falconet/.github/workflows/falconet.yml@v1.0.0`, and the workflow at
that tag compiles falconet, in every job, from this module at that tag:
`go install github.com/zetlen/falconet/cmd/falconet@v1.0.0`, served by Go's
module proxy and vouched for by its checksum database
([the register](decisions.md#the-binary-is-go-installed-at-the-callers-ref)).
Upgrading is moving the tag.

A version is a git tag and nothing else. To name one: set the four
`uses: zetlen/falconet@vX.Y.Z` lines in `.github/workflows/falconet.yml` to
the new tag, as the **last** commit before it (the workflow at a tag names
that tag, and `contract.test.sh` refuses four lines that disagree or a ref
that is not a tag), then `git tag vX.Y.Z`, and push the branch and the tag.
There is no release to cut, no asset to build and nothing to upload: the
proxy fetches the tag the first time anyone installs it.

Public means every push is a publication. Anything brought over from a
private repository must be read before it is committed here, not after.

**The consuming repository.** Development is integration: the orchestrator
is Actions YAML and runs only inside a repository that consumes falconet, so
a consuming repository, the operator's, is the integration environment, and
the canary in [README step 8](../README.md#8-file-the-canary) is how it
proves a tag against itself. Its pull requests need checks of their own
before any tag is worth pinning: falconet produces no evidence, and a pull
request nothing checks is one nobody can review.
