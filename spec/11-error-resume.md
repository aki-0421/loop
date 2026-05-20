# Error Handling and Resume

## Error categories

| Category | Examples | Default action |
| --- | --- | --- |
| Config error | Invalid YAML, unknown enum | Stop before run creation |
| Environment error | Missing Git repo, missing agent command | Stop before agent launch |
| Agent contract error | Missing result JSON or invalid JSON | Repair when attempts remain |
| Dirty state | Uncommitted changes after agent exit | Repair when attempts remain |
| Validation error | Required validation failed | Repair or stop |
| Integration error | Merge conflict, push failure, check failure | Record state and stop or repair when configured |
| Resume error | Branch missing, state file invalid | Stop with diagnostic |

## Repair flow

Repair is another agent invocation on the same iteration branch.

Repair prompt includes:

- The original instruction file.
- The previous prompt path.
- The current error.
- Dirty file list when applicable.
- Validation failures when applicable.
- Required result JSON schema.
- Remaining repair attempt count.

Repair outputs the same result JSON schema through the `result` artifact. If repair changes files, it must commit complete units or revert incomplete work.

## Blocked state

A blocked state means the agent cannot proceed safely without external information or permissions:

- If the blocked reason references an open GitHub Issue labeled `loop:blocking`, the current `loop run` process enters in-memory sleep mode instead of exiting.
- While sleeping, the CLI does not start a new iteration. It polls GitHub Issue/PR diffs every five minutes, writes `github-updates` when a diff appears, and relaunches the agent in the same iteration to decide whether work can proceed.
- If there is no open blocking Issue reference, or if a wake attempt still cannot proceed and no open blocking Issue remains, the run stops as blocked.
- The blocked reason is written to the `result` artifact and `run-state.json`.
- The CLI does not ask the user.

## Resume state

The CLI must be able to resume from these stages:

- `branch_created`: continue agent phase.
- `agent_running`: inspect process marker; if no process exists, repair or restart agent phase.
- `repair_running`: inspect process marker; if no process exists, retry repair or stop.
- `validating`: rerun validation.
- `integrating`: inspect Git and PR state, then complete integration or stop.
- `blocked`: resume only after instruction/config changes, or by starting a new run that observes updated GitHub Issues during normal startup. Sleep mode itself is not persisted as a run-state stage.
- `failed`: resume only with `--repair` or explicit iteration selection.

## State reconstruction

When `run-state.json` is incomplete but iteration files exist, `loop resume` may reconstruct state from:

- Git branch list.
- Git commits on iteration branch.
- `result` artifact.
- `agent-events.jsonl`.
- PR URL files.

Reconstruction writes a backup of the old state file before overwriting it.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Run completed or stopped cleanly with no pending error. |
| `1` | General runtime error. |
| `2` | Invalid usage or flags. |
| `3` | Invalid configuration. |
| `4` | Agent contract error not repaired. |
| `5` | Validation failed. |
| `6` | Integration failed. |
| `7` | Blocked. |
