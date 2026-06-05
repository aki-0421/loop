# Go Implementation Notes

## Module layout

```text
cmd/loop/
  main.go
internal/cli/
  cli.go
  help.go
  commit.go
  branch.go
  branch_runtime.go
  iteration_artifact.go
  iteration_plan_todo.go
  iteration_result.go
  pr_command.go
  renderer.go
  renderer_dashboard.go
  sleep_input.go
  terminal_size_*.go
internal/config/
  config.go
internal/agent/
  adapter.go
  process.go
  fake.go
  output_filter.go
  process_*.go
internal/prompt/
  assemble.go
internal/skills/
  skills.go
  discovery.go
internal/gitx/
  repo.go
  branch.go
  merge.go
  worktree.go
internal/pr/
  gh.go
internal/runstate/
  state.go
  events.go
internal/memory/
  memory.go
  github.go
  github_context.go
internal/validation/
  result.go
  runner.go
internal/workflow/
  tasktree.go
internal/artifactdb/
  artifactdb.go
internal/assets/
  assets.go
```

## Main packages

### `internal/config`

Responsibilities:

- Load config files.
- Merge defaults, user config, repo config, environment, and flags.
- Validate with known-field YAML decoding and contract-specific Go checks.
- Write effective config per iteration.

### `internal/agent`

Responsibilities:

- Resolve agent command.
- Prepare prompt delivery.
- Launch process.
- Capture stdout and stderr as sanitized audit events and filtered message snippets.
- Normalize events.

The built-in default adapter name is `codex`. It runs `codex exec --json` rather than the interactive Codex TUI because loop does not allocate a terminal to agent subprocesses and must avoid persisting raw agent transcripts or hidden reasoning.

### `internal/prompt`

Responsibilities:

- Build the compact code-generated skill bootstrap.
- Avoid duplicating the full skill contract in the prompt.
- Keep runtime metadata, goal text, instruction paths, and iteration paths out of the prompt.
- Direct agents to `loop iteration`, `loop issue`, and `loop handoff` commands.

### Iteration artifact commands

The CLI exposes `loop iteration path/read/write/append` for fixed artifact names and `loop handoff` for role handoff JSON. Role-orchestrated runs use `task-tree`, `task-result`, `review-result`, and `merge-result` handoffs, task-local TODO commands, branch rename, and PR commands. `loop iteration plan`, `loop iteration todo`, `loop iteration close`, and `loop commit` commands remain available as agent-facing utilities and diagnostics.

### `internal/workflow`

Responsibilities:

- Decode and validate task-tree, task-result, review-result, and merge-result JSON.
- Reject unknown fields, invalid task IDs, dependency cycles, unknown dependencies/conflicts, and removed planner/review commit metadata.
- Build dependency/conflict-respecting execution waves for coding task scheduling.

### `internal/gitx`

Responsibilities:

- Check repository cleanliness.
- Create initial branches.
- Create and remove task worktrees through CLI callers.
- Rename branches.
- Remove worktrees during cleanup.
- List commits.
- Squash merge.
- Cleanup branches and worktrees.

### `internal/pr`

Responsibilities:

- Verify `gh` availability.
- Create pull requests.
- Wait for checks.
- Merge pull requests.
- Fetch check logs and parse pull request references.

### `internal/runstate`

Responsibilities:

- Create run ids.
- Read and write `run-state.json` atomically.
- Append JSONL events.
- Support run-state reads for status and resume reporting.

### `internal/memory`

Responsibilities:

- Resolve the GitHub repository from `origin`.
- Sync open and merged pull request titles and bodies through `gh api graphql`.
- Sync repository Issues and Issue/PR comment diffs through `gh api graphql`.
- Create clarification and improvement proposal Issues and ensure `loop:question` and `loop:proposal` labels.
- Store PR memory and GitHub context in the rebuildable `.loop/loop.db` cache.
- Search recent and older PR, Issue, and comment context with SQLite FTS.
- Fetch and upsert a merged PR after `loop pr merge`.

### `internal/validation`

Responsibilities:

- Validate iteration close JSON.
- Run configured validation commands.
- Format validation Markdown and derive validation status.

## Atomic writes

State files are written through temp files and atomic rename:

```text
run-state.json.tmp
run-state.json
```

Ordinary state files use atomic writes. Role handoffs are stored in `.loop/loop.db` through `loop handoff` and copied to durable iteration JSON files for audit. Terminal close handoffs are also stored in `.loop/loop.db`.

## Locking

The CLI uses lock files:

```text
.loop/locks/repo.lock
.loop/runs/<run-id>/run.lock
```

Only one `loop run` may integrate into the same repository at a time. Multiple read-only commands may run concurrently.

## Timeouts

Publicly configurable timeout behavior is currently limited to pull request check timing:

- `checksStartupDelaySeconds`
- `checksDiscoveryTimeoutSeconds`
- `checksPollIntervalSeconds`
- `checksWatchTimeoutSeconds`

Internal Git and GitHub command runners also use bounded default timeouts. Validation command timeout support exists in the runner but is not exposed as a config key.

## Output

Human-readable CLI output is concise by default:

```text
Run: 20260517-000000-a1b2c3
Status: completed
Summary: Add login flow
Logs: .loop/runs/20260517-000000-a1b2c3
```

`--json` prints a structured object with the corresponding run id, status, summary, and log path fields.
