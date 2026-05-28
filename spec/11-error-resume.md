# Error Handling and Stored State

## Error Categories

| Category | Examples | Default action |
| --- | --- | --- |
| Config error | Invalid YAML, unknown enum | Stop before run creation |
| Environment error | Missing Git repo, missing agent command | Stop before agent launch |
| Agent contract error | Missing close handoff or invalid close JSON | Fail the iteration contract |
| Dirty merge close | Uncommitted changes before `--merge` | Reject the close command before handoff |
| Validation error | Required validation failed | Do not integrate; clean up the branch and continue |
| Integration error | Merge conflict, push failure, check failure | Record state and stop |
| State read error | Missing or invalid run-state file | Stop with diagnostic |

## Close Contract Errors

The CLI does not relaunch the agent for correction. Missing or invalid terminal close after the agent exits remains a hard contract error.

Malformed merge closes are rejected before handoff by `loop iteration close --merge`. A merge close requires:

- a renamed branch;
- a clean working tree;
- at least one valid commit;
- passed or skipped validation;
- a merged PR in PR mode.

Skip-merge closes are intentionally loose. They do not require commits, branch rename, validation success, or success JSON. They still require `--reason`, `--should-stop`, and `--goal-evaluation`.

## GitHub Sleep Mode

Sleep mode is entered only when the agent explicitly closes with `loop iteration close --skip-merge --sleep --should-stop false` and a GitHub remote is available. `should_fully_stop` remains the goal-completion decision and is not used as the sleep trigger.

- The CLI closes any unmerged PR, deletes local and remote iteration branches, removes the worktree, and refreshes the target branch before sleeping.
- While sleeping, the CLI polls GitHub Issue/PR/comment diffs every five minutes.
- In interactive terminals, any keypress skips the remaining wait and fetches GitHub updates immediately.
- When a diff appears, the CLI writes `github-updates` into the next iteration and launches the next agent.
- The next agent decides whether to implement, comment or reopen an Issue, merge, skip merge again, or return to sleep.
- The CLI does not ask the user.

## Stored Run State

`loop resume <run-id>` currently reports stored run state and does not relaunch a run stage. Stored stages still describe where a run stopped:

- `branch_created`: the iteration branch was created before the agent phase completed.
- `agent_running`: the agent phase was in progress when state was last written.
- `validating`: configured validation was in progress when state was last written.
- `integrating`: merge or pull request integration was in progress when state was last written.
- `failed`: the run stopped after an error and needs manual inspection.

Sleep mode itself is not persisted as a run-state stage.

## State Reconstruction

State reconstruction is not currently performed. If `run-state.json` is incomplete or invalid, the command stops with a diagnostic rather than inferring replacement state from:

- Git branch list.
- Git commits on iteration branch.
- Terminal close handoff.
- Iteration or task `agent-events.jsonl` files.
- PR state files.

## Exit Codes

| Code | Meaning |
| --- | --- |
| `0` | Run completed or stopped cleanly with no pending error. |
| `1` | General runtime error. |
| `2` | Invalid usage or flags. |
| `3` | Invalid configuration. |
| `4` | Agent contract error. |
| `5` | Validation failed before a cleanup path was available. |
| `6` | Integration failed. |
