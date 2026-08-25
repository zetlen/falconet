# ADR-0005 — The agent job is handed its source

**Status:** Superseded by [ADR-0006](0006-the-rewrite-is-in-go.md) · 2026-08-21
*The mechanism this records is gone: no job checks falconet out into the
consumer's tree any more — every job installs the binary from a release.
Kept for the incident that produced it.*
**Amends:** the job-boundary section of
[`.github/workflows/falconet.yml`](../../.github/workflows/falconet.yml);
[ADR-0002](0002-extract-the-pipeline-into-falconet.md) D4 (one agent pass,
holding nothing it could publish with)
**Supersedes:** nothing

## Context

The first consumer — `zetlen/wayfinders-infra`, the repository this tool was
extracted from — installed falconet on 2026-08-21 and filed a canary issue.
The run reached four jobs and failed in one:

```
remote: Repository not found.
fatal: repository 'https://github.com/zetlen/wayfinders-infra/' not found
```

That is `actions/checkout` in the `implement` job. The job declares
`permissions: {}`, so its `GITHUB_TOKEN` carries no `contents: read`, and
GitHub answers an unauthorised request for a **private** repository with *not
found* rather than *forbidden* — a private repository is indistinguishable
from one that does not exist.

The boundary this project advertises therefore held only for public consumer
repositories, and nothing said so. No unit test could see it: every verb was
correct, the workflow was correct, and the assumption — that a job with no
token can still obtain the source — was never written down anywhere to be
checked.

Everything else in that run worked. The App minted tokens in all three jobs
that hold one, `prepare` claimed the issue, acknowledged the requester, cut
the branch and produced a baseline plan against real credentials, and
`contain` parked the issue `ready-for-human` with a link to the run. The
containment promise kept itself on its first real failure. Only the source
was missing.

## Options

**A — give the agent job `contents: read`.** One line. The token still cannot
push, comment, or open a pull request, so the property that actually matters
— the agent cannot publish — survives.

It costs the claim. The workflow's own header says *"the AGENT JOB never
holds a token. Not a blanked one — none"*, and argues that this is what makes
the boundary enforceable rather than best-effort. A tool whose pitch is an
enforceable boundary should not quietly retire the enforceable part the first
time it is inconvenient. The claim would have to be rewritten to "a token
that cannot write", which is a different and weaker sentence.

**B — hand the source over as an artifact.** `gate` already holds a token,
has already used it, and already uploads an artifact for `implement` to
collect. Let it upload the checkout too. The agent job then clones nothing of
the consumer's, and `permissions: {}` stays literally true.

It costs an upload and a download of the working tree, and it puts an
obligation on `gate`: what it ships must not be able to authenticate as
anybody.

## Decision

**B.**

The choice is not close. A is quicker and B is not expensive; what B buys is
that the sentence in the header stays true, and the sentence is load-bearing.
This is the second time integration with a real consumer has produced a
finding no test of a single verb could reach, and both times the answer has
been to write the assumption down as a case rather than to relax the design
around it.

### A tarball, not a bundle

The obvious shape — `git bundle` — is a trap here, and it is a quiet one.
From a `fetch-depth: 1` clone:

```
$ git bundle create out.bundle HEAD          # exit 0
$ git bundle verify out.bundle
The bundle records a complete history.       # it does not
$ git clone out.bundle elsewhere
error: Could not read 079e5f1…
fatal: Failed to traverse parents of commit 207844b…
```

The bundle carries a tip whose parent was never fetched, and the clone is not
marked shallow, so git believes it should be able to walk back and cannot.
The failure surfaces later, in something that traverses history, rather than
at the point of the mistake.

A tar of the working tree *with its `.git`* has none of that: what arrives is
exactly the shallow clone `gate` had, which is exactly what `actions/checkout`
would have produced in the agent job. Same semantics, no fetch, no token.

The range bundle from `implement` to `publish` is a different thing and stays:
`base..HEAD` records its base as a prerequisite rather than pretending to
carry it, and both ends already hold that commit. Verified from a shallow
clone on both sides.

### The credential comes out first

`actions/checkout` persists its token into `.git/config` as an extraheader,
and `gate` needs it while `prepare` runs — the in-flight check is `git
ls-remote origin`. By the time the archive is made, prepare is finished with
it. So `gate` unsets the extraheader and removes the remote, then greps
`.git/config` and fails the job if anything that authenticates is still
there. Shipping that credential would place a push-capable token inside the
one job built to hold none — the exact failure this ADR is about, inverted.

`implement` checks the other side of the same fence: the tree it unpacks must
have the base SHA `gate` recorded at `HEAD`, and must have no remote at all.
Both are hard failures.

## Consequences

- The agent job keeps `permissions: {}` and no secret but the model key. The
  claim in the workflow header stands unamended.
- A private consumer repository works. So does a public one, by the same path
  — there is now one path, not two.
- `gate` may not ship a credential, ever. Nine cases in
  [`tests/contract.test.sh`](../../tests/contract.test.sh) hold this shape:
  one checkout in the agent job and it is falconet's, the source arriving as
  an artifact, the unset preceding the tar, the fail-closed grep, the
  exclusions, both of `implement`'s refusals, and the ban on whole-history
  bundles.
- Artifact traffic grows by one working tree per run. For the repository this
  came from that is under a megabyte; for a large monorepo it would be worth
  measuring before assuming.
- Still unproven on a runner: this ADR is written before the re-run.
