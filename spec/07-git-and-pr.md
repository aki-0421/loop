# Git and Pull Request Workflow

## Base branch

The base branch comes from config or `--base`. When neither is set, `loop run` uses the branch that was checked out when the command started. If the target differs from the remote default branch advertised by `origin/HEAD` or `upstream/HEAD`, the renderer shows a five-second confirmation screen before the run proceeds.

Before each iteration, the CLI updates the base branch when configured to pull automatically.

## Branch And Task Worktrees

Role-orchestrated runs create the iteration branch before planning and one task branch/worktree for each coding task. Coding agents create initial task-local TODOs before editing. During implementation they may add or reorder pending follow-up TODOs after the fixed done/active/cancelled boundary. Commit TODOs are started, implemented, staged for inspection with `loop task todo stage`, and completed serially with `loop task todo complete`, which commits the staged changes as one task-branch commit. no_commit TODOs are started and completed serially without staging or committing, and completion is rejected if repository changes remain. After writing a completed task handoff, the coding agent runs `loop task merge --type <type> <summary>`, which serializes access to the iteration branch and merges the task branch into the iteration branch while preserving task commit history. If the merge conflicts, the coding agent resolves conflicts in the iteration worktree and runs `loop task merge --continue`; the task is not complete until that command succeeds.

When a coding task attempt exits before `loop task merge` succeeds, the CLI treats that attempt as unmerged work. It removes the attempt worktree, deletes the attempt branch, clears stale task-result, task TODO, and task-merge files, and retries the task in a new branch/worktree when attempts remain. When attempts are exhausted, or the coding agent explicitly discards the task, the planner receives the discarded task context and writes a revised remaining task tree.

Task branches are implementation details and are deleted after merge. The iteration branch is integrated through pull request mode or local merge mode.

## Branch Names

The initial branch is numbered and temporary:

```text
wip/0001
wip/0002
```

Review agents rename the branch through the CLI before pull request creation:

```bash
loop branch rename feat/add-usage-report-command
loop branch rename --kind fix normalize-empty-config-values
```

The CLI validates the requested kind against loop's fixed preset, slugifies the branch subject, applies collision suffixes, updates runtime context, and prints the actual tracked branch:

```text
feat/add-usage-report-command
fix/normalize-empty-config-values
refactor/extract-git-runner
```

The agent must not use direct Git branch switching or renaming. Merge closes are rejected until `loop branch rename` has moved the branch away from the initial `wip/<iteration>` name. After integration starts, branch renames are rejected; pull request check fixes continue on the already tracked PR branch.

Branch kinds are fixed by the loop CLI:

- `feat`
- `fix`
- `refactor`
- `docs`
- `test`
- `style`
- `build`
- `ci`
- `chore`

Branch slugs:

- Use lowercase English words.
- Use hyphens between words.
- Avoid ticket-system assumptions.
- Avoid `loop/` prefixes by default.
- Append the iteration suffix only when needed to avoid collisions.

## Commit Creation Through The CLI

In role-orchestrated runs, coding agents use `loop task todo stage <n>` to inspect and adjust commit candidates, then `loop task todo complete <n>` to ask the CLI to create commits from staged changes. Planner tasks do not contain commit metadata. `loop task merge --type <type> <summary>` creates an iteration-branch merge commit for the completed task branch and preserves the task-branch commits in history.

The `loop commit` command remains available for agent-facing diagnostics and direct iteration utilities:

```bash
loop commit --type <type> <short imperative message>
```

The CLI stages repository changes, validates the message, creates the commit on the current iteration branch, and prints the resulting commit SHA and subject. Runtime files under `.loop/runs/`, including `prompt.md`, `effective-config.yaml`, `agent-events.jsonl`, and `errors.log`, are not committed.

Agents must not run `git add` or `git commit` directly. If `loop commit` rejects the type or message, the command exits non-zero with a human-readable validation error so the agent can immediately retry with corrected arguments.

Commit message format:

```text
<PREFIX>: <short imperative message>
```

Default prefixes:

| Prefix | Use for |
| --- | --- |
| `F` | Features, fixes, user-visible behavior |
| `T` | Tests and test utilities |
| `R` | Refactoring |
| `D` | Documentation |
| `S` | Style and presentation |
| `V` | Versioning and dependencies |
| `C` | Configuration and tooling |

The CLI rejects commits that do not match the loop commit pattern.

Task TODOs are sized so that one completed commit TODO corresponds to one task-branch commit. Pending TODOs may be added, removed, cancelled, or reordered after the fixed done/active/cancelled boundary. A commit TODO cannot be marked complete unless `loop task todo complete` creates the matching commit; a no_commit TODO cannot be marked complete unless no repository changes remain.

## Local merge mode

Local merge mode runs locally:

```bash
git checkout <base>
git pull --ff-only
git merge --squash <iteration-branch>
git commit -m "<summary_sentence>"
```

After commit, the CLI deletes the iteration branch, removes the worktree, checks out the base branch, and pulls with `--ff-only` when an upstream is configured.

## Pull Request Mode

In role-orchestrated runs, pull request mode is driven by the review agent through loop-owned commands after validation passes:

1. Review the iteration branch diff, task results, and validation evidence.
2. Rename the iteration branch away from `wip/<iteration>` with `loop branch rename`.
3. Read the PR template with `loop iteration read pr-template` and write `pr-title` and `pr-body`.
4. Run `loop pr create` and `loop pr checks`.
5. In `auto_merge` review mode, run `loop pr merge` and approve only after `pr-state.status=merged`.
6. In `parallel_human_review` review mode, do not run `loop pr merge`; approve only after `loop pr checks` records `pr-state.status=waiting_for_human`. The CLI cleans local resources, keeps the remote PR branch, records the pending PR title, number, and changed files, renders pending PR status while continuing other work, and starts later iterations from the base branch. At each iteration boundary, the CLI removes pending PRs that have merged or closed before planning. Planner runtime context includes the remaining pending PR records; the planner inspects their comments and review decisions from oldest to newest with `loop pr feedback <pr>`. When a PR needs repair, the planner returns `repair_pull_request` with repair tasks. The CLI then checks out the selected PR branch, runs the repair tasks, pushes the repaired branch, reruns checks, and leaves the PR waiting for human review again. If no safe non-overlapping work remains, the planner may return `wait_for_pending_prs=true` with an empty task tree so the CLI waits for pending PR changes.
7. In `serial_human_review` review mode, do not run `loop pr merge`; approve only after `loop pr checks` records `pr-state.status=waiting_for_human`. The CLI shows PR review wait status, polls every 5 minutes until an external human merge is observed, then pulls the base branch and cleans local resources before any next iteration.
8. If checks fail, inspect logs when needed and write `changes_requested` findings instead of approval.
9. After approval, the CLI verifies the state required by the configured review mode, refreshes the base branch when appropriate, and cleans up local runtime resources.

Command shape:

```bash
loop pr create
loop pr checks
loop pr logs <job-url-or-id>
loop pr merge
```

The implementation stores command outputs in the iteration directory.

## PR template handling

The agent reads pull request template text through `loop iteration read pr-template`, fills it, and writes the PR title and body artifacts in English by default. If a repository template exists, the command returns it. If no repository template exists, the command returns CLI-owned fallback text.

In role-orchestrated PR mode, `loop pr create` rejects missing `pr-title` or `pr-body` artifacts and rejects unrenamed `wip/<iteration>` branches. Fallback PR text is only available outside role-orchestrated PR mode.

## Check waiting

When pull request mode has `waitChecks=true`, `loop pr checks` and `loop pr merge` wait for provider checks through `gh`. After PR creation or a fix push, the command waits `checksStartupDelaySeconds` before the first check query. If GitHub reports no checks for the PR branch, the CLI polls until `checksDiscoveryTimeoutSeconds` expires, using `checksPollIntervalSeconds` between attempts. Reported pending checks are watched until they pass, fail, are canceled, or `checksWatchTimeoutSeconds` expires. GitHub CLI pending exit code 8 is treated as pending rather than failure. If no checks are reported after the discovery timeout, the wait step is treated as skipped rather than failed. If checks fail:

- `loop pr checks` records full check output in the `pr-checks` artifact and writes only a concise pointer to `errors.log`.
- The agent fetches detailed job logs with `loop pr logs <job-url-or-id>` when needed.
- In role-orchestrated mode, the review agent reports failed checks as `changes_requested` findings so repair tasks can be scheduled.

`mergeWhenChecksPass` is retained for configuration compatibility, but PR-mode auto merge is now triggered by `loop pr merge`. In human-review modes, `loop pr merge` is rejected because merging is an external human action.

## Cleanup

Cleanup happens after both merge and skip-merge terminal actions:

| Resource | Cleanup |
| --- | --- |
| Local iteration branch | delete |
| Remote iteration branch in PR mode | delete through Git when available |
| Iteration worktree | remove |
| Target branch | checkout and pull with `--ff-only` when an upstream is configured |

Runtime logs remain under `.loop/runs/` until removed by the user.
