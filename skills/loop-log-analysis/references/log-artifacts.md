# Loop Log Artifacts

## Durable Layout

```text
.loop/
  runs/
    <run-id>/
      run-state.json
      errors.log
      iterations/
        <iteration-id>/
          prompt.md
          effective-config.yaml
          agent-events.jsonl
          task-tree.json
          review-result.json
          merge-result.json
          validation.md
          validation-output-<name>.log
          errors.log
          pr-state.json
          pr-checks.json
          pr-check-log.txt
          github-updates.md
          tasks/
            <sequence>/
              task.json
              task-todo.json
              task-result.json
              task-merge.json
              agent-events.jsonl
```

Some artifacts only exist when that phase happened. `errors.log`, PR artifacts, validation outputs, and task merge audits are conditional.

## Event Streams

- Iteration `agent-events.jsonl` records planner, coding, review, and CLI lifecycle events.
- Task `tasks/<sequence>/agent-events.jsonl` records only that coding agent's events.
- Required event fields are `type` and `ts`. Common metadata includes `iteration_id`, `agent_type`, `task_id`, and `task_dir`.
- Important event types:
  - `agent.started`: adapter process launched; begins a usage snapshot window.
  - `agent.message`: short, filtered assistant-visible progress message.
  - `agent.usage`: normalized token usage; `delta=true` means incremental usage.
  - `agent.command`: command requested by the agent.
  - `agent.file_read`: file read reported by the agent adapter.
  - `agent.exited`: adapter process finished; non-zero `exit_code` is suspicious.
  - `validation.command.completed`: validation command result with `exit_code`.
  - `task.attempt.discarded`: coding attempt was discarded before retry.
  - `task.merge.completed`: task branch merged into the iteration branch.
  - `git.branch.renamed`: merge agent renamed the tracked branch.
  - `pr.merge.recovered`: PR merge state was recovered after host-side merge.
  - `iteration.active_temp.cleanup.completed`: disposable active artifacts were removed.

## Triage Checklist

1. Read `run-state.json` for stage, current iteration, branch fields, summaries, and `should_fully_stop`.
2. Check run-level and iteration-level `errors.log`.
3. Count `agent.started` and `agent.exited` by `agent_type`; missing exits can indicate interruption.
4. Review non-zero exits from `agent.exited` and `validation.command.completed`.
5. Inspect discarded task attempts and retry counts before blaming the final task result.
6. In PR mode, inspect `merge-result.json`, `pr-state.json`, `pr-checks.json`, and `pr-check-log.txt` before concluding review or merge failed.
7. Use `validation-output-*.log` only for the focused command that failed; do not paste large logs into the final answer.

## Token Usage

`agent.usage` is audit metadata, not a transcript. Add usage deltas directly. For snapshot events without `delta`, count the largest input/output value within the current agent process window after `agent.started`. If any usage event has `estimated=true`, mark the final usage number as estimated.
