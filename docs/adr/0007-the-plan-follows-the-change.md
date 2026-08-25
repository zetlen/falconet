# ADR-0007 — The plan is of what changed, and the repository's layout is discovered

**Status:** Accepted · 2026-08-25
**Amends:** [ADR-0003](0003-the-cli-surface.md) — `stacks.plan` and
`stacks.validate_only` keep their meanings and stop being the enumeration of
everything falconet knows about; the config's defaults for them are removed

## Context

v0.2.0 ran live, and the first thing it produced was a pull request nobody
should approve.

`zetlen/wayfinders-infra#120` asked for a bigger Cloud SQL tier. The agent
edited `talaria-gcp/variables.tf` — a correct, one-variable change. Run
32873023567 validated and planned exactly what `.github/falconet.json`
listed, opened `wayfinders-infra#121`, and gave it, as its entire plan:

```
No changes. Your infrastructure matches the configuration.
```

That is a true plan of `dns/`, which the pull request does not touch. Nothing
about `talaria-gcp/` was validated, let alone planned, and nothing anywhere
in the pull request said which stack the plan belonged to. A reviewer reads
"No changes" under a diff that changes a database tier.

Three things lined up, all of them in the design rather than in one bug:

1. **The path guard is `*.tf` anywhere.** `paths.allow` refuses paths outside
   the allowlist, and nothing checked that a changed file lived in a stack
   falconet had ever heard of. A `.tf` in an unknown directory passed.
2. **`validate` planned the CONFIGURED stacks, not the CHANGED ones.** With
   the diff entirely outside them, every configured stack was clean by
   construction, and the run was a success.
3. **A single planned stack got no heading.** `PlanHeading` separated stacks
   only when there were several, so with one, `plan.txt` — and the pull
   request body assembled from it — never named the stack at all.

The consumer-side cause was that `talaria-gcp/` was a day old and had not
been added to `falconet.json`. That is fixed there, with a contract test. But
falconet let it through, and the shape of what it let through is the problem:
a green run, a real plan, and a pull request that is evidence of the wrong
thing. This project's whole claim is that the pull request carries the
evidence. A plan of somewhere else is worse than no plan, because a reviewer
cannot tell it from the real one.

### What the fix must not be

The obvious narrowing — plan only the resources the diff mentions, with
`-target` — is refused outright. `-target` makes OpenTofu print
`Note: resource targeting is in effect ... The -target option is not for
routine use`, on every run, in a log an adopter reads while deciding whether
this tool is serious. More to the point it would be true: a targeted plan
does not show what an apply will do, and the human at the end of this
pipeline approves an untargeted apply. falconet plans whole stacks, or it
does not plan.

The correct fix is the ordinary one, and every other tool in this space
already made it: work out which root modules the change reaches, and plan
those.

## Decision

### D1 — The plan follows the diff

`validate` maps the changed paths onto the repository's stacks and plans the
planned stacks the change reaches, in the order the config named them. A
planned stack the change does not reach is validated and not planned:
"No changes." under a diff that changes something is the sentence this ADR
exists to stop printing, and the cheapest way not to print it is not to run
that plan.

`prepare`'s baseline is unchanged in scope — it runs before anyone has
touched anything, so there is no change yet to narrow it by, and a baseline
of everything a later change could be measured against is what makes it a
baseline.

### D2 — A change that reaches nothing plannable gets a person, not a pull request

Two refusals, because they are two different sentences to the requester:

- **Uncovered** — the change touched Terraform in no stack at all: a
  directory holding `.tf` files that the config names in neither list, or a
  `.tf` at the repository root, which is never a stack.
- **Unplanned** — the change reached stacks, and none of them is one this
  repository plans. Everything validated; there is simply no plan for a human
  to approve.

Both are collected sections in `validation-failure.txt`, which the workflow
already posts verbatim to the requester before pausing the issue
`ready-for-human`. Both say, in the report, that nothing about the request
caused it and nothing is wrong with the change — because that is true, and
because the person reading it asked for a database tier and is owed an
answer rather than a stack trace of somebody's config.

A repository that plans **nothing** — `stacks.validate_only` set and
`stacks.plan` empty — is not told this on every request. It said it plans
nothing; it is believed.

### D3 — Every plan is headed by its stack

`## <stack>` goes into `plan.txt` above every plan, including the only one.
It used to be written only when there was more than one stack to separate,
on the reasoning that a single stack's `plan.txt` should be exactly the bytes
tofu wrote. Two lines would have made `wayfinders-infra#121` obviously wrong
at a glance. A reviewer who cannot see what they are approving a plan **of**
is not reviewing.

### D4 — The layout is discovered, and the config classifies it

`internal/stacks` gains discovery, and it is the same function `falconet
init` already wrote its config from, so what `init` says and what a run
assumes cannot drift:

- A directory that directly holds `.tf` files is a **root module** — a stack
  — unless some other directory names it as a local module `source`, in which
  case it is a module and belongs to whatever sources it.
- The repository root is never a stack. falconet runs `tofu -chdir=<dir>`
  and never plans the tree it is standing in.
- A change inside a stack reaches that stack. A change inside a module
  reaches every stack that sources it, transitively — including the module's
  templates and data files, which change what it plans as surely as its `.tf`
  files do.

Every one of those is what Atlantis, Terragrunt and Spacelift assume of an
ordinary OpenTofu repository. They are assumptions falconet is entitled to
make from the tree, which is exactly the class of thing that should not
require a person to write it down twice.

`stacks.plan` and `stacks.validate_only` keep the meanings ADR-0003 gave
them, and stop being the enumeration of everything falconet knows about:

- **Naming neither list** means "discover them": every root module found is a
  stack, and every one of them is planned. This is a repository saying "you
  can see the shape of this as well as I can".
- **Naming either list** makes the config authoritative. A directory holding
  `.tf` files that appears in neither is one falconet refuses to guess about
  — `doctor` reports it `MISSING`, and a change landing in it is D2's first
  refusal.

Half a config is the one shape that goes wrong, and it is the shape
`wayfinders-infra` was in.

### D5 — The defaults name no stacks

`stacks.plan` was `["dns"]` and `stacks.validate_only` was
`["workspace", "site"]` — the origin repository's own three directories,
shipped as the default for everybody. A consumer whose directories were
called anything else met `config .stacks.plan names "dns", which is not a
directory` before it met anything of falconet's, and a consumer who wrote no
config at all had the origin's layout asserted over theirs. A default that
names somebody else's directories is not a default; it is one repository's
configuration with the label filed off.

Both are now empty, which under D4 means "discover", which is a real answer
rather than an absent one.

### D6 — `doctor` catches it standing still

A directory holding `.tf` files that a declared config names in neither list
is `MISSING` in step 1, with the hint naming both keys. That is #23 found
when the config is next looked at, rather than when a request lands in it —
which is the difference between a line in a report and a pull request nobody
should approve.

## Consequences

**Runs get cheaper and quieter.** A change in one stack no longer initialises
and plans every other configured one. In `wayfinders-infra` that is one
`tofu init`/`plan` instead of one per planned stack, and the plans that do
run are of directories the diff touches.

**Some previously-green runs now refuse.** A consumer whose config does not
cover its repository will see a request paused `ready-for-human` where v0.2.0
would have opened a pull request. That is the point: the pull request it
would have opened was wrong. `doctor` and `init` both name the gap first.

**A module nothing sources is reported as a stack.** By every test available
from the tree it is a root module, and it will be planned if a change reaches
it — `tofu plan` on a module with required variables fails, and the run goes
to a human. Guessing from a directory's name would be magic; a repository
that keeps an unused module says so in `stacks.validate_only`.

**Module edges are read with a regular expression, not with HCL.** `source =
"./x"` and `source = "../x"` in `.tf` files, resolved against the containing
directory; a registry address, a git address and a provider's own `source`
are all not paths in the tree and are dropped by the leading-dot test.
`.tf.json` is not read: a repository that writes its modules in JSON gets
discovery of its directories and no dependency edges, which is a smaller
wrong answer than a half-parsed one, and a change to such a module lands in
D2's first refusal rather than in a silently-missing plan.

**`plan.command` is untouched.** `-refresh=false -lock=false` remains its
default, from the origin's read-only state credential; a consumer who wants a
refreshing plan says so in one key. What the default does not contain, and
will not, is `-target`.
