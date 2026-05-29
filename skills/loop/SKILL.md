---
name: loop
description: Execute one role inside the loop CLI orchestrator. Use when the CLI asks you to act as planner, coding, or review agent and return strict handoff JSON while using loop-owned commands for Git integration.
version: 1
---

# loop

You are running inside the `loop` harness. The CLI owns branches, worktrees, validation, pull requests, check waiting, cleanup, and iteration state. Your job is to perform the role named in `LOOP_ROLE`, write the required handoff with `loop handoff`, use loop-owned task merge commands when you are the coding agent, and exit.

## Shared Rules

- Do not run `git add`, `git commit`, branch rename/switch commands, `gh pr`, `loop commit`, `loop branch`, `loop pr`, or `loop iteration close`.
- Coding agents must use `loop task merge` to create the task commit and merge the task branch; do not exit a completed task before that command succeeds.
- If you exit before `loop task merge` succeeds, the orchestrator treats the attempt as unmerged, discards that task branch/worktree, and may retry the task in a fresh coding session.
- Read context with `loop iteration read runtime`, `loop iteration read instruction`, and focused repository inspection.
- Use `loop issue ask` only for important blocking product or policy questions; continue independent work when possible.
- Keep all generated repository content in English unless the repository explicitly requires another language.
- Handoff JSON must match the CLI contract exactly; unknown fields are rejected.
- Use `loop help agent handoff write` when you need the current handoff schema or command flags.

## Planner Role

Explore the repository enough to plan the iteration. One iteration produces one PR, and that PR should be sized like an AI development sprint: a coherent sprint goal that autonomous agents can complete in hours. Do not shrink the PR for review convenience; post-hoc review is outside planning and is not a scope constraint.

Write one task tree:

```bash
loop handoff write task-tree --file task-tree.json
```

The task tree contains `schema_version`, `summary`, `goal_evaluation`, optional `goal_complete`, and `tasks`. Each task must include `id`, `title`, `description`, `depends_on`, `conflicts_with`, `acceptance`, `commit_type`, and `commit_message`.

Task-tree shape:

```json
{
  "schema_version": 1,
  "summary": "One-sentence AI sprint-sized PR summary.",
  "goal_evaluation": "Current view of the CLI goal.",
  "goal_complete": false,
  "tasks": [
    {
      "id": "implement-core",
      "title": "Implement core behavior",
      "description": "Instructions for the coding agent.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["Concrete acceptance check."],
      "commit_type": "F",
      "commit_message": "implement core behavior"
    }
  ]
}
```

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

Complete only the assigned task from the prompt. Edit repository files as needed, run focused checks when useful, then write the task result outside repository changes:

```bash
loop handoff write task-result --task "$LOOP_TASK_ID" --file "$LOOP_TASK_DIR/task-result.json"
```

Task-result shape:

```json
{
  "schema_version": 1,
  "task_id": "implement-core",
  "status": "completed",
  "summary": "What changed.",
  "validation": ["Focused check that ran."],
  "notes": []
}
```

After writing a completed task result, run:

```bash
loop task merge
```

If the command reports conflicts, resolve them in the printed iteration worktree and run:

```bash
loop task merge --continue
```

Use `status: "completed"` only when the task implementation is ready to merge. The task is not complete until `loop task merge` succeeds. Use `failed` when the task cannot be safely completed and explain why in `summary` and `notes`.

## Review Role

Review the iteration branch after coding and validation. Inspect the diff, task tree, task results, and validation evidence. Write:

```bash
loop handoff write review-result --file review-result.json
```

Review-result shape:

```json
{
  "schema_version": 1,
  "status": "approved",
  "summary": "Review conclusion.",
  "goal_evaluation": "Whether the integrated PR satisfies the CLI goal.",
  "goal_complete": false,
  "findings": []
}
```

Use `status: "approved"` only when the iteration is ready for PR creation or merge. Use `changes_requested` with concrete `findings` that can be converted into repair tasks. Set `goal_complete` only when a CLI goal exists and the integrated PR would satisfy it.
