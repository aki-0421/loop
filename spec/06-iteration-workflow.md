# Iteration Workflow

## Run creation

At `loop run` start, the CLI:

1. Finds the repository root.
2. Loads and validates configuration.
3. Validates the instruction file.
4. Detects the base branch.
5. Requires a clean repository unless configuration allows otherwise.
6. Creates a run id.
7. Creates `.loop/runs/<run-id>/run-state.json`.
8. Starts iteration `0001`.

Run id format:

```text
YYYYMMDD-HHMMSS-<short-random>
```

## Iteration directory

Each iteration has its own directory:

```text
.loop/runs/<run-id>/iterations/0001/
  effective-config.yaml
  iteration.db
  prompt.md
  agent-events.jsonl
  errors.log        # only when an error occurs
```

Files are created as needed. `prompt.md`, `agent-events.jsonl`, and the `result` artifact in `iteration.db` are required after the agent phase completes.

`plan` is runtime memory. It is stored in `iteration.db`, ignored by Git, and must not be committed as part of the iteration work.

Agents access runtime artifacts through `loop iteration` commands instead of manually constructing paths. Writable agent artifacts are `plan`, `todo`, `worklog`, `summary`, `result`, `pr-title`, and `pr-body`. Agents should generate the `result` artifact with `loop iteration result --write` so the CLI owns the mechanical JSON shape.

## Live renderer

During `loop run`, the CLI renders live progress to stderr unless JSON output is requested. On terminals that support cursor control, the renderer refreshes the same screen instead of appending log lines. The renderer shows:

- run id, agent, base branch, and log path;
- current stage, elapsed time, and iteration count;
- the current open TODO from the `todo` artifact;
- recent safe agent activity such as command execution, file reads, and short thinking/status messages;
- terminal title updates when the terminal supports them.

The renderer adapts to terminal dimensions. Compact screens show status and the current task only; larger screens include logs path, command, TODO details, and recent activity.

Persistent logs are audit logs only. `agent-events.jsonl` must not store raw file contents, raw diffs, or agent thinking text. It stores command executions and file-read paths as each audit event is observed and flushed. `errors.log` is created only when a non-successful agent, process, or validation phase needs diagnostics.

## Branch creation

The CLI creates a numbered initial branch before launching the agent:

```text
wip/0001
```

The agent starts on the initial branch and must rename it through the CLI before reporting completed work:

```bash
loop branch rename feat/add-password-reset-tests
loop branch rename --kind fix handle-empty-search-query
```

The command accepts only loop's fixed branch kind preset, slugifies the branch subject, applies collision suffixes, and stores the current branch in runtime context. Aliases such as `feature` are not accepted. Direct Git branch switches or renames are rejected as lifecycle mismatches.

Completed results are rejected while the tracked branch still equals the numbered initial branch. `no_change` results do not require a branch rename.

Example final branch names:

```text
feat/add-password-reset-tests
fix/handle-empty-search-query
refactor/split-auth-service
```

## Worktree execution

When `git.worktree=true`, each iteration runs in a separate worktree:

```text
.loop/worktrees/<run-id>/0001/
```

The base checkout remains untouched during agent execution. Integration happens from the main repository checkout after the agent branch is complete.

## Prompt content

`prompt.md` is an exact copy of the user instruction file as read at the start of that iteration. It is the instruction snapshot for that iteration and must not contain system bootstrap text, goal text, or source file paths.

The agent bootstrap prompt is built in memory for every agent launch. It is intentionally compact and only bootstraps the loop skill workflow. It includes:

- A short instruction to use the `loop` skill.
- A short instruction to use `loop iteration` and `loop memory` commands.

The agent bootstrap prompt does not inline runtime metadata, effective config, schema summaries, full path lists, memory, goal text, instruction file content, or instruction file paths. The `loop` skill tells the agent which CLI commands expose runtime context and writable artifacts.

## Agent phase

The agent executes exactly one iteration. It may run multiple internal commands, edit files, and request multiple commits through `loop commit`.

Required agent outputs:

- `plan` artifact before repository edits.
- `todo` artifact before repository edits.
- Commits for complete logical units when changes are made, created through `loop commit`.
- `summary` artifact with a concise iteration summary.
- `result` artifact matching the schema.

The agent writes TODOs so that one completed TODO equals one `loop commit` invocation, except no-change confirmations. Before the iteration ends, the agent confirms that commits are complete and `git status --short` shows no changed files.

## Result handling

The CLI validates the `result` artifact and decides the next action.

| Result status | Meaning | CLI action |
| --- | --- | --- |
| `completed` | Work for this iteration is ready to integrate | validate and integrate |
| `no_change` | No repository change was needed | stop or continue based on `should_fully_stop` |
| `needs_repair` | Agent requests repair flow | launch repair if attempts remain |
| `blocked` | No safe automated path exists | stop the run |
| `failed` | Agent could not complete the contract | repair or stop |

## Dirty state handling

If the agent exits with uncommitted changes:

1. The CLI records the dirty file list.
2. The CLI relaunches the agent with a repair note when attempts remain.
3. The repair agent must either commit complete work through `loop commit`, revert incomplete work, or produce a blocked result.
4. Integration never starts while the working tree is dirty, except for ignored `.loop/` runtime files.

## Validation phase

Validation commands come from configuration and agent result JSON. Commands marked required must pass before integration.

Configured validation commands run from the current iteration work directory through the user's default shell as described in `03-configuration.md`.

Validation output is saved to the `validation` artifact and structured events are appended to `agent-events.jsonl`.

## Integration phase

A completed iteration with changes is integrated through the configured mode:

- Local merge mode: local squash merge into base branch.
- Pull request mode: require that the agent has already created, checked, repaired, and merged the PR through `loop pr`; then pull the base branch and clean up local runtime resources.

The one-sentence `summary_sentence` from result JSON becomes the squash commit message subject or the PR merge subject.

## Stop condition

`should_fully_stop=true` means the goal is fully satisfied after this iteration is integrated, or no change is needed. In pull request mode, the agent must make this decision after `loop pr merge` succeeds, so CI repair prompts cannot replace the original run-goal evaluation. The CLI stops after the current integration action completes.

If `should_fully_stop=false`, the CLI starts the next iteration until the iteration limit or terminal state is reached.

## Cancellation cleanup

If the user cancels with Ctrl+C or the process receives SIGTERM, `loop` cancels the running agent and cleans up work that has not already been integrated:

- worktree execution removes the iteration worktree and deletes its local branch;
- main worktree execution resets and cleans the temporary branch, checks out the base branch, and deletes the temporary branch;
- local merge integration reset happens only when the squash merge has not produced a new base commit.

Already integrated or merged work is preserved.
