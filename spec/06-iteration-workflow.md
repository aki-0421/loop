# Iteration Workflow

## Run Creation

At `loop run` start, the CLI finds the repository root, loads configuration, resolves the repository runtime store under `~/.loop/workspaces/<repo-id>/`, validates the instruction file, detects the base branch, requires a clean repository unless explicitly allowed, creates a run id, writes `runs/<run-id>/run-state.json` in that runtime store, and starts iteration `0001`.

The instruction file is copied to each iteration's durable `prompt.md`. The CLI-generated bootstrap is sent to agents in memory and is not written into `prompt.md`.

## Role-Orchestrated Iteration

Each iteration is CLI-owned:

1. Create the iteration branch, usually `wip/<iteration>`, and its worktree before launching any agent.
2. Run the planner agent in the iteration worktree.
3. Validate the planner's `task-tree` handoff.
4. Schedule ready coding tasks by `depends_on` and `conflicts_with`, with at most `run.maxParallelTasks` active tasks.
5. For each coding task attempt, create a task branch and worktree from the current iteration branch, run the coding agent, require it to create and complete task-local TODOs before merging, validate its `task-result`, require `loop task merge --type <type> <summary>`, remove the task worktree, and continue only after the task branch has been merged into the iteration branch with its commits preserved.
6. Run configured validation commands from the iteration worktree.
7. Run the QA review agent with the task tree, task results, validation status, browser/UI context when relevant, and repository diff available.
8. If validation fails or QA review returns `changes_requested`, create repair tasks and repeat coding, validation, and review until approval or another terminal error occurs.
9. In PR mode, run the merge agent after QA approval. The merge agent renames the branch, writes PR title/body artifacts, creates the PR, waits for checks, and merges or hands off for human review through `loop pr`. If PR checks fail, it writes `merge-result.status=pr_check_failed` with repair findings. In local mode, perform local merge after QA approval.
10. Clean task and iteration worktrees and continue until `goal_complete=true`, the iteration limit is reached, or a terminal error occurs.

Agents use loop-owned commands for Git and GitHub lifecycle work. Coding agents create initial task-local TODOs before editing, may add or reorder pending follow-up TODOs after the fixed done/active/cancelled boundary during implementation, stage and inspect active commit TODOs through `loop task todo stage`, complete commit TODOs through CLI-created task-branch commits, complete no_commit TODOs with a clean task worktree, and use `loop task merge` to merge the completed task branch into the iteration branch while preserving task commit history. QA review agents write `review-result`; merge agents use `loop branch rename` and `loop pr` commands for PR integration.

If a coding agent exits without completing `loop task merge`, the CLI does not merge or salvage that task branch. It discards the unmerged attempt branch and worktree, clears stale task handoff and task TODO state, records a discard event, and starts the next attempt in a fresh branch and worktree until `run.maxTaskAttempts` is exhausted. When attempts are exhausted, or when the agent explicitly runs `loop task discard --reason`, the planner runs again with the discarded task and current plan context until it returns replacement tasks or another terminal error occurs.

## Pull Request Scope

One iteration maps to one AI sprint-sized pull request. The planner should select the largest coherent sprint goal suitable for an autonomous run while still producing an independently mergeable result, then split that goal into task-tree nodes for parallel coding, dependency ordering, conflict avoidance, and validation. Task boundaries should follow autonomy, dependency, conflict, validation, and parallelism needs. Each task should own a meaningful vertical outcome or substantial subsystem slice, not a file-level, layer-only, or commit-sized microtask. Documentation and validation should stay with the behavior-owning task unless a final cross-cutting hardening task adds distinct value.

## Runtime Artifacts

Durable iteration files include:

```text
~/.loop/workspaces/<repo-id>/runs/<run-id>/iterations/0001/
  prompt.md
  effective-config.yaml
  agent-events.jsonl
  task-tree.json
  tasks/0001/
    task.json
    task-todo.json
    task-result.json
    task-merge.json
    agent-events.jsonl
  review-result.json
  merge-result.json
  pr-state.json
  pr-checks.json
  errors.log
```

Runtime context, validation output, prompt audits, and transient task active directories are disposable. After the planner handoff is validated, the CLI creates durable task directories under `tasks/<sequence>/`. Coding-agent result handoff copies and event logs are written to the assigned task directory, not to a separate `task-results/` directory or the iteration-level `agent-events.jsonl`. Role handoffs are stored in the runtime `loop.db` and copied to durable JSON files for audit.

## Role Handoffs

Planner agents write:

```bash
loop handoff write task-tree --file task-tree.json
```

Coding agents write:

```bash
loop task todo add --type F --title "Add publish review route" --acceptance "The route renders the review workflow and focused coverage passes." add publish review route
loop task todo start 1
loop task todo stage 1
loop task todo complete 1
loop handoff write task-result --task "$LOOP_TASK_ID" --file "$LOOP_TASK_DIR/task-result.json"
loop task merge --type F complete "$LOOP_TASK_ID"
```

Pending task TODOs can be placed or reordered before implementation with `loop task todo add --after <n>` and `loop task todo move <n> --after <n>`. `--after 0` places an item at the top.

If `loop task merge` reports conflicts, the coding agent resolves the conflicts in the printed iteration worktree and completes the merge with `loop task merge --continue`.

Review agents write:

```bash
loop handoff write review-result --file review-result.json
```

Merge agents write:

```bash
loop handoff write merge-result --file merge-result.json
```

The CLI rejects unknown JSON fields, removed planner/review/merge commit metadata, and invalid dependency, conflict, status, or goal values before proceeding.

## Validation And Repair

Configured validation runs after all currently scheduled coding tasks are merged into the iteration branch. Required validation failures create a validation repair task, and the CLI reruns the coding/review loop until validation passes or another terminal error occurs.

Review findings and PR check findings are converted into repair tasks. Review and merge agents must provide enough finding detail for the CLI to create tasks with acceptance criteria.

## Integration

Pull request mode is the primary integration path:

- The merge agent renames the branch with `loop branch rename` before PR creation.
- With `git.integration.pr.reviewMode=auto_merge`, the merge agent reads `loop iteration read pr-template`, writes `pr-title` and `pr-body`, then runs `loop pr create`, `loop pr checks`, and `loop pr merge`.
- With `git.integration.pr.reviewMode=parallel_human_review`, the merge agent creates the PR and runs checks but does not merge. `loop pr checks` records `pr-state.status=waiting_for_human`; the CLI records the pending PR title, number, and changed files in run state and later runtime context, renders the pending PRs while continuing other work, cleans local resources, preserves the remote PR branch, and continues from the base branch so the planner can choose non-overlapping work. At each iteration boundary, the CLI removes pending PRs that have merged or closed before planning. The planner runs before any iteration worktree branch is created, inspects remaining pending PR feedback with `loop pr feedback <pr>` from oldest to newest, and can return `repair_pull_request` with repair tasks. After accepting that handoff, the CLI checks out the selected PR branch, runs the repair tasks, pushes the repaired branch, reruns checks, and returns the PR to `waiting_for_human`. If pending PRs reserve every safe implementation area, the planner can return `wait_for_pending_prs=true` with no tasks to put the CLI into PR review wait mode.
- With `git.integration.pr.reviewMode=serial_human_review`, the merge agent creates the PR and runs checks but does not merge. The CLI shows a PR review wait screen and polls every 5 minutes for an external human merge before cleanup and before starting another iteration.
- If checks fail, the merge agent writes `pr_check_failed` findings in `merge-result` instead of using QA `changes_requested`.

Local merge mode remains available for local-only repositories and tests. It squash-merges the approved iteration branch into the base branch.

## Stop Condition

`goal_complete=true` in planner or QA review output can stop the run only when the user supplied a non-empty CLI `--goal`. The CLI stops after successful integration. Without a CLI goal, role output cannot mark the full run complete.

## Cancellation Cleanup

For `loop run`, the first Ctrl+C or SIGTERM requests graceful shutdown. The active iteration keeps running with its existing agent, validation, review, and integration contexts, and the run stops before starting another iteration. A graceful shutdown requested before the configured goal or iteration limit is reached records the run as `cancelled` and exits with code 130 after the current iteration finishes.

A second Ctrl+C or SIGTERM cancels the active contexts immediately. If the CLI stops before integration completes, `loop` removes unintegrated task and iteration worktrees, task branches, task merge locks, and disposable active-temp files. Already integrated or remotely merged work is preserved.
