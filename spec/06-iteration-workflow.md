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
  prompt.md
  agent-events.jsonl
  errors.log        # only when an error occurs
```

Files are created as needed. `prompt.md`, `effective-config.yaml`, `agent-events.jsonl`, `errors.log`, and run state are durable audit/replay files. Result handoff state is stored in `.loop/loop.db`.

During an active iteration, disposable runtime files are stored in a Go temp directory created with `os.MkdirTemp`. The durable iteration directory is exposed to subprocesses as `LOOP_ITERATION_DIR`; the temp-backed active artifact directory is exposed as `LOOP_ACTIVE_ITERATION_DIR` only so `loop iteration` commands can resolve artifact paths. `runtime`, `plan`, `todo`, `validation`, PR text, validation output, and prompt-audit files are active runtime files. They are ignored by Git, must not be committed as iteration work, and are disposable after the iteration completes.

Agents access runtime artifacts through `loop iteration` commands instead of manually constructing paths. Writable agent artifacts are `plan`, `todo`, `pr-title`, and `pr-body`. `plan` uses `loop iteration plan`, `todo` uses `loop iteration todo`, and remaining writable artifacts use `loop iteration write` or `loop iteration append`. Agents close the iteration with `loop iteration close --merge` or `loop iteration close --skip-merge` so the CLI owns the mechanical JSON shape.

## Live renderer

During `loop run`, the CLI renders live progress to stderr unless JSON output is requested. On terminals that support cursor control, the renderer refreshes the same screen instead of appending log lines. The renderer shows:

- run id, agent, base branch, and log path;
- current stage, elapsed time, and iteration count;
- normalized token usage when the selected agent reports it;
- the current open TODO from the `todo` artifact;
- recent safe agent activity such as command execution, file reads, and short thinking/status messages;
- terminal title updates when the terminal supports them.

The renderer adapts to terminal dimensions. Compact screens show status and the current task only; larger screens include logs path, command, TODO details, and recent activity.

Persistent logs are audit logs only. `agent-events.jsonl` must not store raw file contents, raw diffs, or agent thinking text. It stores command executions, file-read paths, and normalized usage metadata as each audit event is observed and flushed. `errors.log` is created only when a non-successful agent, process, or validation phase needs diagnostics.

## Branch creation

The CLI creates a numbered initial branch before launching the agent:

```text
wip/0001
```

The agent starts on the initial branch and must rename it through the CLI before closing with `--merge`:

```bash
loop branch rename feat/add-password-reset-tests
loop branch rename --kind fix handle-empty-search-query
```

The command accepts only loop's fixed branch kind preset, slugifies the branch subject, applies collision suffixes, and stores the current branch in runtime context. Aliases such as `feature` are not accepted. Direct Git branch switches or renames are rejected as lifecycle mismatches.

Merge closes are rejected while the tracked branch still equals the numbered initial branch. Skip-merge closes do not require a branch rename.

Example final branch names:

```text
feat/add-password-reset-tests
fix/handle-empty-search-query
refactor/split-auth-service
```

## Worktree execution

Each iteration runs in a separate worktree:

```text
.loop/worktrees/<run-id>/0001/
```

The base checkout remains untouched during agent execution. Integration happens from the main repository checkout after the agent branch is complete.

## Prompt content

`prompt.md` is an exact copy of the user instruction file as read at the start of that iteration. It is the instruction snapshot for that iteration and must not contain system bootstrap text, goal text, or source file paths.

The agent bootstrap prompt is built in memory for every agent launch. It is intentionally compact and only bootstraps the loop skill workflow. It includes:

- A short instruction to use the `loop` skill.
- A short instruction to use `loop iteration`, `loop memory`, `loop issue`, and `loop commit` commands.
- In pull request mode, a short instruction to use `loop pr` after reading `loop iteration read pr-template`.

The agent bootstrap prompt does not inline runtime metadata, effective config, JSON contract details, full path lists, GitHub context memory, goal text, instruction file content, or instruction file paths. The `loop` skill tells the agent which CLI commands expose runtime context, writable artifacts, and GitHub Issues.

## Agent phase

The agent executes exactly one iteration. It may run multiple internal commands, edit files, and request multiple commits through `loop commit`.

Required agent outputs:

- `plan` artifact before repository edits.
- `todo` artifact before repository edits.
- Commits for complete logical units when changes are made, created through `loop commit`.
- `pr-title` and `pr-body` when pull request mode is enabled.
- Master-DB terminal handoff through `loop iteration close --merge` or `loop iteration close --skip-merge`.

The agent first uses the plan to select the review slice, then manages TODOs one item at a time to decompose that slice into commit-sized tasks. Each TODO uses the same `--type` and message shape as `loop commit`, and one completed TODO equals one `loop commit` invocation except skip-merge evidence. Before the iteration ends with `--merge`, the agent confirms that commits are complete and `git status --short` shows no changed files.

## Close handling

The CLI validates the master-DB terminal handoff and decides the next action. After either terminal action, the CLI deletes the Go temp directory that contains disposable active-work files such as `runtime.json`, `plan.md`, `todo.md`, validation output, PR text, and prompt-audit files. It preserves `prompt.md`, `effective-config.yaml`, `agent-events.jsonl`, `errors.log`, PR lifecycle diagnostics, GitHub update diffs, and run state in the durable iteration directory.

When a valid terminal handoff appears while the agent process is still running, the CLI waits for the agent to finish by itself and drains the process streams before cleanup. This drain is part of the audit contract: Codex and Claude Code often emit final usage only after the terminal action has completed. The drain must not read provider-owned session files or other external logs.

| Close action | Meaning | CLI action |
| --- | --- | --- |
| `merge` | Work for this iteration is ready to integrate | validate, integrate, delete branches/worktree, refresh the target branch |
| `skip_merge` | This branch should not be incorporated | close any unmerged PR, delete branches/worktree, refresh the target branch |

## Dirty state handling

If the agent exits with uncommitted changes:

1. The CLI records the dirty file list.
2. A merge close is rejected before terminal handoff when the working tree is dirty.
3. A skip-merge close discards the iteration branch during cleanup and never integrates the dirty work.
4. Integration never starts while the working tree is dirty, except for ignored `.loop/` runtime files.

## Validation phase

Validation commands come from configuration and the close handoff. Commands marked required must pass before integration.

Configured validation commands run from the current iteration work directory through the user's default shell as described in `03-configuration.md`.

Validation output is saved to the `validation` artifact and structured events are appended to `agent-events.jsonl`.

## Integration phase

A merge close with changes is integrated through the configured mode:

- Local merge mode: local squash merge into base branch.
- Pull request mode: require that the agent has already created, checked, fixed any check failures, and merged the PR through `loop pr`; then refresh the base branch and clean up local runtime resources.

The one-sentence `summary_sentence` from the close handoff becomes the squash commit message subject or the PR merge subject.

## Stop condition

`should_fully_stop=true` means the CLI-provided run goal is fully satisfied. It is invalid when the run has no CLI goal, regardless of instruction, Issue, PR, or comment text. In pull request mode, the agent must make this decision after `loop pr merge` succeeds when using `--merge`. The CLI stops after the current terminal action completes.

`loop iteration close --skip-merge --sleep --should-stop false` enters GitHub sleep mode after skip-merge cleanup. Sleep mode polls Issue, PR, and comment updates every five minutes; in an interactive terminal, any keypress triggers the next fetch immediately. When updates appear, the CLI writes the `github-updates` artifact into the next iteration and lets the next agent decide whether to implement, comment or reopen an Issue, merge, skip merge again, or return to sleep.

If `should_fully_stop=false`, the CLI starts the next iteration until the iteration limit or terminal state is reached.

## Cancellation cleanup

If the user cancels with Ctrl+C or the process receives SIGTERM, `loop` cancels the running agent and cleans up work that has not already been integrated:

- the iteration worktree is removed and its local branch is deleted;
- local merge integration reset happens only when the squash merge has not produced a new base commit.

Already integrated or merged work is preserved.
