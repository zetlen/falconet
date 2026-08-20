---
name: work-infra-issues
description: Process the infra-request issue queue — turn each plain-language infrastructure request into a branch, a tofu plan, and a PR labeled needs-plan-review. Use when asked to work infra issues or process infrastructure requests; loop it via /loop for polling.
---

# Work infra issues

One invocation is one pass over the queue, oldest first. The label vocabulary and approval flow live in `docs/agents/triage-labels.md`; repo conduct rules live in `AGENTS.md`. Both bind here. Your involvement with any issue ends at an open PR: approval, merge, and `tofu apply` are human actions.

## This skill and the CI pipeline are two ways to do the same job

`.github/workflows/infra-issues.yml` works the same queue automatically, but as a **staged pipeline** rather than one agent doing everything: scripted setup → an implementing agent with a very narrow toolset → scripted validation → an independent reviewing agent → scripted PR assembly. CI does not read this file and does not follow the procedure below; it carries its own per-stage prompts.

What the two share is the scripts in `scripts/`, and you should use them here too — they are the parts that turned out to be worth making deterministic:

| Script | What it does | Why it exists |
| ------ | ------------ | ------------- |
| `ci-validate.sh --base <sha>` | commit? `tofu validate`? `tofu plan`? — all three, into files | one gate, one report, no half-checked change |
| `ci-pr-body.sh` | builds a PR body from your prose plus the **whole** plan file | PR #28 shipped a hand-abridged plan |
| `ci-review-verdict.sh` | files a reviewing agent's verdict | CI only |
| `ci-park-issue.sh` | comment + parking label + release the claim, in one call | so "stopped" is never "silently nothing" |
| `ci-push-branch.sh` | pushes the working branch as soon as it has a commit | CI only — run 32093607680 destroyed a prepared change with its runner |

`tests/run.sh` exercises the scripts against fixtures (including that run's
verdict message) with no network and no GitHub. Run it after changing any of
them.

Every script prints its own usage with `-h`.

## Preflight

Stop and report to the operator in the session — never as issue comments — if any of these fail:

```sh
gh auth status
test -f .env && set -a && source .env && set +a
test -n "$NAMECHEAP_USER_NAME" && test -n "$NAMECHEAP_API_USER" && test -n "$NAMECHEAP_API_KEY" && test "$NAMECHEAP_USE_SANDBOX" = "true"   # sandbox only — no production access
test -n "$GOOGLEWORKSPACE_CUSTOMER_ID" && test -n "$GOOGLEWORKSPACE_CREDENTIALS"   # else every plan fails "customer_id is required"
test -n "$AWS_ACCESS_KEY_ID" && test -n "$AWS_SECRET_ACCESS_KEY"   # state backend — see versions.tf
git status --porcelain          # must be empty
git switch main && git pull
tofu init
```

Each command runs in a fresh shell: re-source .env (set -a; source .env; set +a) in the same shell as EVERY tofu invocation.

Never pipe `tofu plan` into `head`/`tail` — SIGPIPE kills tofu before it releases the state lock, stranding one that needs `tofu force-unlock`. Redirect to a file and grep the file.

Always pass `-no-color` when you redirect a plan to a file. Without it tofu writes ANSI escapes into the file and everything downstream — you, a reviewer, a PR body — has to strip them.

Your local credentials are the read-write pair, so plans here take a lock normally. `-lock=false` is a CI-only flag: CI's state credential is bucket-scoped read-only and a lock is a write.

## Requeue replies

Issues parked `needs-info` re-enter the queue when the requester replies. Requesters usually lack permission to edit labels, so remove the label for them:

```sh
me=$(gh api user --jq .login)
gh issue list --state open --label infra-request --label needs-info --json number --jq '.[].number' |
while read -r n; do
  last=$(gh issue view "$n" --json comments --jq '.comments[-1].author.login')
  [ -n "$last" ] && [ "$last" != "$me" ] && gh issue edit "$n" --remove-label needs-info
done
```

## Queue

```sh
gh issue list --state open --label infra-request \
  --json number,assignees,labels,createdAt \
  --jq '[.[] | select((.assignees|length)==0) | select(([.labels[].name] | map(IN("needs-info","ready-for-human","do-not-apply","wontfix")) | any) | not)] | sort_by(.createdAt) | .[].number'
```

Before working an issue `n`, two more exclusions:

- **Opted out** — the requester checked the form's opt-out box. Leave these issues completely alone: no claim, no comment, no label.

  ```sh
  gh issue view "$n" --json body --jq .body | grep -qiE '^[-*] \[[xX]\] Not eligible for AI agents' && skip
  ```

- **Already in flight** — an open PR exists for it:

  ```sh
  gh pr list --state open --json headRefName --jq '.[].headRefName' | grep -qE "^(claude/)?issue-${n}-" && skip
  ```

  Both conventions are matched on purpose. `issue-<n>-<slug>` is what this skill and today's CI both produce; `claude/issue-<n>-<timestamp>` is what CI produced before it stopped using claude-code-action's tag mode, and those branches and PRs still exist.

## Per issue, oldest first

1. **Claim**: `gh issue edit <n> --add-assignee @me`.

2. **Interpret** the request (`gh issue view <n> --json title,body,comments`). Three outcomes:

   - **Workable** — it maps to resources tf manages today (currently: DNS zones and records for `papernapkin.tech` and `ptown.tech`, the Google Workspace groups and their access tiers — `workspace.tf`, `permissions.tf` — and the plan-only GCP site stack) → continue to step 3.
   - **Ambiguous** — you cannot tell WHAT is being asked for. Ask, in plain language addressed to the requester, one question per bullet, and say why you need each answer; then park it:

     ```sh
     scripts/ci-park-issue.sh --issue <n> --label needs-info --unassign @me \
       --body questions.md \
       --preamble "Before I can prepare this change I need a bit more from you:"
     ```

     Parking an under-specified request is one of the most valuable things this system does. Reach for it rather than guessing at what someone meant. "I am not sure this is the tidiest implementation" is *not* ambiguity — build it and let review judge.
   - **Human-only, or tf doesn't manage that system yet** — same call with `--label ready-for-human` and a preamble explaining why, in plain language. A request to create, modify, or offboard a *person* falls here every time, however it arrives: users live in a directory administered where identity is administered, not here.

3. **Branch**: `git switch -c issue-<n>-<slug>` (short kebab slug from the request), from up-to-date `main`. Record the base: `base=$(git rev-parse main)`.

4. **Edit**: follow `AGENTS.md`. A record change is one edit, to the `locals` list in its `dns/records-*.tf` file — since #17 there is no mirror to keep in step. Never weaken, delete, or route around a guard in `guards*.tf`; mail-affecting DNS mistakes fail silently, which is why those guards exist. A failed `check` assertion in `dns/checks-live-dns.tf` is a warning about live DNS, not a guard — do not "fix" a declaration to match whatever public DNS happens to answer.

5. **Commit, then validate**:

   ```sh
   git add -A && git commit -m "<one-line summary>"
   scripts/ci-validate.sh --base "$base"
   ```

   That runs `tofu validate` and a plan, and writes `plan.txt`, `diff.patch` (`git log -p` over your commits — message and change together), `changed-files.txt` and — on failure — `validation-failure.txt` into `.ci-handoff/` at the repo root (override with `--out-dir`). That directory is CI's stage-to-stage handoff space, gitignored, and `ci-validate.sh` fails any commit that touches it — so never `git add -f` it.

   Exit criterion: the plan cleanly includes the requested change and nothing unintended. If `main` itself carries pending changes, those appear in the plan too — identify them as pre-existing in the PR body rather than trying to remove them. If a guard precondition fails, the guard is authoritative: quote its message, never weaken it.

   If a clean plan cannot be reached, push nothing: park the issue with `ci-park-issue.sh --label ready-for-human --body <the validation failure> --body-title "What the automated checks reported"`, `git switch main`, next issue.

   `ci-validate.sh` passes `-refresh=false`, which is what CI needs. Once it is green, run one refreshing plan by hand (`tofu plan -no-color > /tmp/plan.txt`) so the PR carries a plan that has actually talked to the sandbox, and use that file in step 6.

6. **PR**. Build the body with the script; do not hand-write it:

   ```sh
   git push -u origin issue-<n>-<slug>
   scripts/ci-pr-body.sh --body summary.md --plan /tmp/plan.txt --issue <n> --out /tmp/pr.md
   gh pr create --label needs-plan-review --title "<plain-language summary>" --body-file /tmp/pr.md
   ```

   `summary.md` is 2–3 sentences addressed to the requester, no jargon, **with no plan output in it**. `ci-pr-body.sh` appends the `Closes #<n>` line and the complete plan inside a `<details>` block, and if the result would exceed GitHub's 65536-character limit it truncates the plan on line boundaries with an explicit note saying how much was elided and where to read the rest.

   Never paste a plan by hand and never abridge one. PR #28 shipped with literal `# ... omitted here for length` comments inside its code fence, which meant the human who approved it was reading the agent's summary of the evidence instead of the evidence. `AGENTS.md` requires the pasted plan; this script is how you satisfy it without the opportunity to paraphrase.

   Area labels arrive via the labeler workflow. Then comment the PR link on the issue in non-technical language ("I've prepared this change — a human will review and apply it").

7. **Return** to `main` for the next issue.

## Terminal states

When you leave an issue it must be in exactly one of these, always:

- an open PR (workable),
- `needs-info` (you asked the requester a question),
- `ready-for-human` (not workable by an agent, or you could not finish).

If you cannot finish for ANY reason — a tool failed, the request turned out to be something else, you ran out of room — add `ready-for-human` with a brief plain-language comment rather than stopping silently. A request that vanishes into a run that produced nothing is the worst outcome this system has.

## End of pass

Report to the operator: issues worked into PRs, issues parked (`needs-info` / `ready-for-human`), issues skipped (opted out / in flight), and any preflight or environment problems. Under `/loop`, this report is the tick summary.
