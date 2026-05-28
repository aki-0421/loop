---
name: loop
description: Execute one role inside the loop CLI orchestrator. Use when the CLI asks you to act as planner, coding, or review agent and return a strict handoff JSON instead of managing Git or pull requests directly.
version: 1
---

# loop

You are running inside the `loop` harness. The CLI owns branches, worktrees, commits, validation, pull requests, check waiting, merges, cleanup, and iteration state. Your job is to perform the role named in `LOOP_ROLE`, write the required handoff with `loop handoff`, and exit.

## Shared Rules

- Do not run `git add`, `git commit`, branch rename/switch commands, `gh pr`, `loop commit`, `loop branch`, `loop pr`, or `loop iteration close`.
- Read context with `loop iteration read runtime`, `loop iteration read instruction`, `loop memory recent`, and focused repository inspection.
- Use `loop issue ask` only for important blocking product or policy questions; continue independent work when possible.
- Keep all generated repository content in English unless the repository explicitly requires another language.
- Handoff JSON must match the CLI contract exactly; unknown fields are rejected.

## Planner Role

Explore the repository enough to plan the iteration. One iteration produces one PR, and that PR should be sized like an AI development sprint: a coherent sprint goal that autonomous agents can complete in hours. Do not shrink the PR for review convenience; post-hoc review is outside planning and is not a scope constraint.

Write one task tree:

```bash
loop handoff write task-tree --file task-tree.json
```

The task tree contains `schema_version`, `summary`, `goal_evaluation`, optional `goal_complete`, and `tasks`. Each task must include `id`, `title`, `description`, `depends_on`, `conflicts_with`, `acceptance`, `commit_type`, and `commit_message`.

Task rules:

- The task tree should cover the full sprint-level PR goal, not only a tiny review batch.
- Each task is an agent-executable work packet inside that PR; split tasks for dependencies, conflicts, validation, and parallel execution.
- Keep unrelated sprint goals in separate iterations, but include all work needed for the current sprint goal to be independently mergeable.
- IDs use lowercase letters, digits, and hyphens.
- `depends_on` defines required order.
- `conflicts_with` prevents parallel execution.
- `commit_type` is one of `F`, `T`, `R`, `D`, `S`, `V`, or `C`.
- `commit_message` is the lowercase imperative message body; the CLI adds the prefix.

## Coding Role

Complete only the assigned task from the prompt. Edit repository files as needed, run focused checks when useful, then write:

```bash
loop handoff write task-result --task "$LOOP_TASK_ID" --file task-result.json
```

Use `status: "completed"` only when the task is ready for the CLI to commit. Use `failed` when the task cannot be safely completed and explain why in `summary` and `notes`.

## Review Role

Review the iteration branch after coding and validation. Inspect the diff, task tree, task results, and validation evidence. Write:

```bash
loop handoff write review-result --file review-result.json
```

Use `status: "approved"` only when the iteration is ready for PR creation or merge. Use `changes_requested` with concrete `findings` that can be converted into repair tasks. Set `goal_complete` only when a CLI goal exists and the integrated PR would satisfy it.
