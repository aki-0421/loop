# Iteration Workflow

## Run Creation

At `loop run` start, the CLI finds the repository root, loads configuration, validates the instruction file, detects the base branch, requires a clean repository unless explicitly allowed, creates a run id, writes `.loop/runs/<run-id>/run-state.json`, and starts iteration `0001`.

The instruction file is copied to each iteration's durable `prompt.md`. The CLI-generated bootstrap is sent to agents in memory and is not written into `prompt.md`.

## Role-Orchestrated Iteration

Each iteration is CLI-owned:

1. Create the iteration branch, usually `wip/<iteration>`, and its worktree before launching any agent.
2. Run the planner agent in the iteration worktree.
3. Validate the planner's `task-tree` handoff.
4. Schedule ready coding tasks by `depends_on` and `conflicts_with`, with at most `run.maxParallelTasks` active tasks.
5. For each coding task attempt, create a task branch and worktree from the current iteration branch, run the coding agent, validate its `task-result`, require the coding agent to run `loop task merge`, remove the task worktree, and continue only after the task branch has been squash-merged into the iteration branch.
6. Run configured validation commands from the iteration worktree.
7. Run the review agent with the task tree, task results, validation status, and repository diff available.
8. If validation fails or review returns `changes_requested`, create repair tasks and repeat coding, validation, and review until approval or `run.maxReviewFixCycles` is exhausted.
9. Create and integrate a pull request, or perform local merge mode when configured.
10. Clean task and iteration worktrees and continue until `goal_complete=true`, the iteration limit is reached, or a terminal error occurs.

Agents do not create branches, PRs, or iteration close handoffs in the role-orchestrated workflow. Coding agents do not run Git directly; they use `loop task merge` to create the task commit from task metadata, squash-merge into the iteration branch, and resolve conflicts before exiting.

If a coding agent exits without completing `loop task merge`, the CLI does not merge or salvage that task branch. It discards the unmerged attempt branch and worktree, clears stale task handoff state, records a discard event, and starts the next attempt in a fresh branch and worktree until `run.maxTaskAttempts` is exhausted.

## Pull Request Scope

One iteration maps to one AI sprint-sized pull request. The planner should select a coherent sprint goal that autonomous agents can complete in hours, then split that goal into task-tree nodes for parallel coding, dependency ordering, conflict avoidance, and validation. The planner must not reduce the PR to a small review-sized change; post-hoc review is outside the PR sizing decision.

## Runtime Artifacts

Durable iteration files include:

```text
.loop/runs/<run-id>/iterations/0001/
  prompt.md
  effective-config.yaml
  agent-events.jsonl
  task-tree.json
  tasks/0001/
    task.json
    task-result.json
    task-merge.json
    agent-events.jsonl
  review-result.json
  pr-state.json
  pr-checks.json
  errors.log
```

Runtime context, validation output, prompt audits, and transient task active directories are disposable. After the planner handoff is validated, the CLI creates durable task directories under `tasks/<sequence>/`. Coding-agent result handoff copies and event logs are written to the assigned task directory, not to a separate `task-results/` directory or the iteration-level `agent-events.jsonl`. Role handoffs are stored in `.loop/loop.db` and copied to durable JSON files for audit.

## Role Handoffs

Planner agents write:

```bash
loop handoff write task-tree --file task-tree.json
```

Coding agents write:

```bash
loop handoff write task-result --task "$LOOP_TASK_ID" --file "$LOOP_TASK_DIR/task-result.json"
loop task merge
```

If `loop task merge` reports conflicts, the coding agent resolves the conflicts in the printed iteration worktree and completes the merge with `loop task merge --continue`.

Review agents write:

```bash
loop handoff write review-result --file review-result.json
```

The CLI rejects unknown JSON fields and invalid dependency, conflict, status, goal, or commit metadata before proceeding.

## Validation And Repair

Configured validation runs after all currently scheduled coding tasks are merged into the iteration branch. Required validation failures do not integrate. When fix cycles remain, the CLI creates a validation repair task and reruns the coding/review loop.

Review findings are converted into repair tasks. A review agent must provide enough finding detail for the CLI to create tasks with acceptance criteria and commit metadata.

## Integration

Pull request mode is the primary integration path:

- The CLI writes PR title/body artifacts from task and review context.
- The CLI pushes the iteration branch, creates or reuses the PR, waits for checks when configured, and squash-merges when checks pass.
- With `git.integration.pr.humanReview=true` or `loop run --human-review`, the CLI creates the PR and pauses for external post-hoc review or merge updates instead of auto-merging.

Local merge mode remains available for local-only repositories and tests. It squash-merges the approved iteration branch into the base branch.

## Stop Condition

`goal_complete=true` in planner or review output can stop the run only when the user supplied a non-empty CLI `--goal`. The CLI stops after successful integration. Without a CLI goal, role output cannot mark the full run complete.

## Cancellation Cleanup

If the user cancels with Ctrl+C or the process receives SIGTERM, `loop` cancels running agents and removes unintegrated task and iteration worktrees. Already integrated or remotely merged work is preserved.
