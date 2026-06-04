# Error Handling and Stored State

## Error Categories

| Category | Examples | Default action |
| --- | --- | --- |
| Config error | Invalid YAML, unknown enum | Stop before run creation |
| Environment error | Missing Git repo, missing agent command | Stop before agent launch |
| Agent rate limit | Codex `rate_limit_exceeded`, Codex `429 Too Many Requests`, Claude Code `You've hit your session limit · resets ...`, Claude Code `Server is temporarily limiting requests` | Wait until the parsed reset/retry time, then relaunch the same role agent without consuming task attempts |
| Agent contract error | Missing close handoff or invalid close JSON | Fail the iteration contract and clean local runtime resources |
| Dirty merge close | Uncommitted changes before `--merge` | Reject the close command before handoff |
| Validation error | Required validation failed | Do not integrate; clean local runtime resources, then stop or create a repair task when configured |
| Integration error | Merge conflict, push failure, check failure | Record state, clean local runtime resources when integration did not complete, and stop |
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

## Agent Rate Limit Wait Mode

When a role agent subprocess exits after printing a waitable Codex or Claude Code rate-limit message, the CLI records `agent.rate_limit_wait`, waits for the parsed reset or retry time, and relaunches the same planner, coding, or review role.

- Codex detection covers `rate_limit_exceeded`, `rate_limit_reached`, `Rate limit reached ... Please try again in ...`, `exceeded retry limit, last status: 429 Too Many Requests`, `account/rateLimits/read` payloads with `primary.resetsAt` / `secondary.resetsAt`, and usage-limit reset banners such as `resets 13:37`.
- Claude Code detection covers usage-limit messages such as `You've hit your session limit · resets 3:45pm`, `You've hit your weekly limit · resets Mon 12:00am`, `You've hit your Opus limit · resets ...`, `Server is temporarily limiting requests`, and `Request rejected (429)`.
- If the message includes a retry delay, the CLI waits that duration. If it includes a reset clock, the CLI waits until that clock in the local timezone. If no wait time can be parsed, the CLI waits five minutes before retrying.
- Rate-limit waits do not consume `run.maxRoleAgentRestarts` or `run.maxTaskAttempts`; idle-timeout restarts still use `run.maxRoleAgentRestarts`.
- Non-waitable hard stops such as oversized requests, missing login, invalid API keys, or low credit balance remain normal agent errors.

## Stored Run State

When a role-orchestrated iteration stops on an error before integration completes, the CLI removes local task worktrees, task branches, task merge locks, the iteration worktree, the iteration branch, and disposable active-temp files. Cleanup failures are recorded in the iteration event log; durable audit files remain under `.loop/runs/`.

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
