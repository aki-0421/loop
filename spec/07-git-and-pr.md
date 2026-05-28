# Git and Pull Request Workflow

## Base branch

The base branch comes from config or `--base`. When neither is set, `loop run` uses the branch that was checked out when the command started. If the target differs from the remote default branch advertised by `origin/HEAD` or `upstream/HEAD`, the renderer shows a five-second confirmation screen before the run proceeds.

Before each iteration, the CLI updates the base branch when configured to pull automatically.

## Branch And Task Worktrees

Role-orchestrated runs create the iteration branch before planning and one task branch/worktree for each coding task. Coding agents edit only their assigned task worktree. After a task handoff is accepted, the CLI stages changes, creates the task commit from task metadata, removes the task worktree, and squash-merges the task branch into the iteration branch.

Task branches are implementation details and are deleted after merge. The iteration branch is integrated through pull request mode or local merge mode.

## Legacy Branch Names

The initial branch is numbered and temporary:

```text
wip/0001
wip/0002
```

Older single-agent workflows can rename the branch through the CLI before closing with `--merge`:

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

In role-orchestrated runs, the CLI creates commits after coding agents finish. The planner-provided task `commit_type` and `commit_message` produce the final subject.

Older single-agent workflows can request commits during the iteration by running:

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

TODOs are sized so that one completed TODO corresponds to one `loop commit` invocation, except skip-merge confirmations. The agent marks a TODO complete only after the matching commit exists, unless the TODO required no repository change.

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

In role-orchestrated runs, pull request mode is driven by the CLI after validation and review pass:

1. Generate PR title and body from task-tree and review context.
2. Push the iteration branch.
3. Create or reuse the pull request.
4. Wait for checks when configured.
5. If post-hoc review waiting is enabled, pause for external merge/review updates instead of auto-merging.
6. Otherwise squash-merge through `gh`, refresh the base branch, and clean up local runtime resources.

The older `loop pr` commands are retained for compatibility with single-agent workflows:

1. Generate PR title and body through the agent.
2. Run `loop pr create` to push the tracked branch and create or reuse the PR.
3. Run `loop pr checks` to push current commits and wait for checks when configured.
4. If checks fail, inspect `pr-checks`, fetch logs with `loop pr logs <job-url-or-id>`, fix the failure in the same agent context, commit through `loop commit`, and rerun `loop pr checks`.
5. Run `loop pr merge` after checks pass. The command runs configured validation, performs a final check wait, and merges through squash merge without asking `gh` to perform local branch cleanup. If the host reports failure after the remote PR has already merged, the command verifies the remote PR state and records the merged state locally. The squash commit subject is the generated PR title with the PR number suffix when available, such as `(#123)`.
6. Close with `loop iteration close --merge` only after `loop pr merge` records `pr-state.status=merged`.
7. The run loop pulls the base branch and deletes runtime resources after accepting the merge close. The CLI deletes the origin head branch through Git and prunes remote-tracking refs so stale `origin/<branch>` refs do not linger locally.

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

If the agent does not write the `pr-body` artifact, the CLI fallback reads the repository template when present and appends a minimal English loop note.

## Check waiting

When pull request mode has `waitChecks=true`, `loop pr checks` and `loop pr merge` wait for provider checks through `gh`. After PR creation or a fix push, the command waits `checksStartupDelaySeconds` before the first check query. If GitHub reports no checks for the PR branch, the CLI polls until `checksDiscoveryTimeoutSeconds` expires, using `checksPollIntervalSeconds` between attempts. Reported pending checks are watched until they pass, fail, are canceled, or `checksWatchTimeoutSeconds` expires. GitHub CLI pending exit code 8 is treated as pending rather than failure. If no checks are reported after the discovery timeout, the wait step is treated as skipped rather than failed. If checks fail:

- `loop pr checks` records full check output in the `pr-checks` artifact and writes only a concise pointer to `errors.log`.
- The agent fetches detailed job logs with `loop pr logs <job-url-or-id>` when needed.
- The agent fixes the failure in the same context, validates locally, commits through `loop commit`, and reruns `loop pr checks`.

`mergeWhenChecksPass` is retained for configuration compatibility, but PR-mode merge is now triggered by `loop pr merge`.

## Cleanup

Cleanup happens after both merge and skip-merge terminal actions:

| Resource | Cleanup |
| --- | --- |
| Local iteration branch | delete |
| Remote iteration branch in PR mode | delete through Git when available |
| Iteration worktree | remove |
| Target branch | checkout and pull with `--ff-only` when an upstream is configured |

Runtime logs remain under `.loop/runs/` until removed by the user.
