# Operating falconet

What the operator does, what only the operator can do, and where the pieces
live.

## Three things an agent cannot do for you

All three are credentials. Ask for them when they are needed; do not attempt to
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

**A token for the release pull request.** `RELEASE_PLEASE_TOKEN`, described
under *Releases* below. The operator mints it on GitHub's **Fine-grained
personal access tokens** page and sets it with `gh secret set`.

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
that tag installs falconet, in every job, from this repository's release at
that tag: the archive for the runner, checked against the release's
`checksums.txt` ([the register](decisions.md#release-binaries-at-a-tag)).
Upgrading is moving the tag.

A version is a release, and release-please cuts it:

1. Pull requests are squash-merged, so each title becomes a commit subject
   on `main`. The title is a Conventional Commits subject (AGENTS.md, *Commit
   subjects*). The `pr-title` workflow refuses any other title.
2. On every push to `main`, the `release` workflow keeps one release pull
   request open. It bumps `.release-please-manifest.json`, adds the
   release's section to `CHANGELOG.md`, and sets every
   `uses: zetlen/falconet@vX.Y.Z` line in `.github/workflows/falconet.yml`
   to the new tag.
3. Merging the release pull request creates the tag and a draft release.
   The `assets` job in the same workflow checks that the pins name the tag,
   runs `make test`, runs `make assets`, uploads the three archives and
   `checksums.txt`, and publishes the release.

release-please acts with `RELEASE_PLEASE_TOKEN`, a fine-grained personal
access token scoped to this repository with contents, pull requests and
workflows write. The workflows permission is what the release pull request
needs: it rewrites `.github/workflows/falconet.yml`, and GitHub refuses a
workflow file written with `GITHUB_TOKEN`. That token expires, and a release
that stops at the release-please step with `Error adding to tree` wants it
minted again and set with `gh secret set RELEASE_PLEASE_TOKEN`.

Because a personal access token is a person's, `ci.yml` runs on the release
pull request. A tag push starts no workflow here, so the `assets` job runs
the pin check and the suite itself.

The repository has immutable releases turned on (**Settings → General →
Releases**). Once a release is published, its assets cannot be added,
replaced or deleted, and its tag cannot be moved or deleted. A draft is not
locked. So a failed `assets` job leaves a draft that nobody can install
from. Re-run the job from the Actions tab when the failure was transient.
When the tagged tree itself is at fault, delete the draft and its tag with
`gh release delete vX.Y.Z --cleanup-tag`, and fix the fault on `main`.

Public means every push is a publication. Anything brought over from a
private repository must be read before it is committed here, not after.

**The consuming repository.** Development is integration: the orchestrator
is Actions YAML and runs only inside a repository that consumes falconet, so
a consuming repository, the operator's, is the integration environment, and
the canary in [README step 8](../README.md#8-file-the-canary) is how it
proves a tag against itself. Its pull requests need checks of their own
before any tag is worth pinning: falconet produces no evidence, and a pull
request nothing checks is one nobody can review.
