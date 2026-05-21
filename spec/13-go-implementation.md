# Go Implementation Notes

## Module layout

```text
cmd/loop/
  main.go
internal/cli/
  root.go
  init.go
  run.go
  resume.go
  status.go
  skills.go
  memory.go
  doctor.go
internal/config/
  load.go
  schema.go
  defaults.go
internal/agent/
  adapter.go
  process.go
  codex.go
  fake.go
internal/prompt/
  assemble.go
  templates.go
internal/skills/
  install.go
  sync.go
  validate.go
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
  resume.go
internal/memory/
  recent.go
  search.go
  compact.go
internal/validation/
  runner.go
internal/logging/
  redact.go
```

## Main packages

### `internal/config`

Responsibilities:

- Load config files.
- Merge defaults, user config, repo config, environment, and flags.
- Validate against JSON Schema.
- Write effective config per iteration.

### `internal/agent`

Responsibilities:

- Resolve agent command.
- Prepare prompt delivery.
- Launch process.
- Capture stdout and stderr as sanitized audit events.
- Normalize events.
- Validate terminal close handoff presence.

The built-in default adapter name is `codex`. It runs `codex exec --json` rather than the interactive Codex TUI because loop does not allocate a terminal to agent subprocesses and must avoid persisting raw agent transcripts.

### `internal/prompt`

Responsibilities:

- Build the compact code-generated skill bootstrap.
- Avoid duplicating the full skill contract in the prompt.
- Keep runtime metadata, goal text, instruction paths, and iteration paths out of the prompt.
- Direct agents to `loop iteration` artifact commands.

### Iteration artifact commands

The CLI exposes `loop iteration path/read/write/append` for fixed artifact names, plus dedicated `loop iteration plan` and `loop iteration todo` namespaces for planning artifacts. Agent-writable artifacts are limited to `plan`, `todo`, `pr-title`, and `pr-body`; CLI-owned logs, prompt, runtime context, effective config, and validation output are read-only. `loop iteration close` builds valid terminal JSON from runtime data plus semantic flags and writes the master-DB result handoff. The CLI also exposes `loop commit --type <type> <message>` and `loop branch rename ...` so agents request validated commits and tracked branch renames without running raw Git lifecycle commands.

### `internal/gitx`

Responsibilities:

- Check repository cleanliness.
- Create initial branches.
- Rename branches.
- Manage worktrees.
- List commits.
- Create validated commits for dirty repository changes.
- Squash merge.
- Cleanup branches and worktrees.

### `internal/pr`

Responsibilities:

- Verify `gh` availability.
- Create pull requests.
- Wait for checks.
- Merge pull requests.
- Pull base branch after merge.

### `internal/runstate`

Responsibilities:

- Create run ids.
- Read and write `run-state.json` atomically.
- Append JSONL events.
- Support resume and reconstruction.

### `internal/memory`

Responsibilities:

- Resolve the GitHub repository from `origin`.
- Sync open and merged pull request titles and bodies through `gh api graphql`.
- Sync repository Issues and Issue/PR comment diffs through `gh api graphql`.
- Create clarification and improvement proposal Issues and ensure `loop:question` and `loop:proposal` labels.
- Store PR memory and GitHub context in the rebuildable `.loop/loop.db` cache.
- Search recent and older PR, Issue, and comment context with SQLite FTS.
- Fetch and upsert a merged PR after `loop pr merge`.

## Atomic writes

State files are written through temp files and atomic rename:

```text
run-state.json.tmp
run-state.json
```

The same rule applies when the CLI writes terminal close metadata. Agent-written artifacts are validated after process exit.

## Locking

The CLI uses lock files:

```text
.loop/locks/repo.lock
.loop/runs/<run-id>/run.lock
```

Only one `loop run` may integrate into the same repository at a time. Multiple read-only commands may run concurrently.

## Timeouts

Configurable timeouts:

- Agent process timeout.
- Repair process timeout.
- Validation command timeout.
- PR check wait timeout.
- Git command timeout.

Timeouts are recorded as events and mapped to the relevant error category.

## Output

Human-readable CLI output is concise by default:

```text
Run: 20260517-000000-a1b2c3
Iteration: 0001
Branch: feat/add-login-flow
Status: completed
Summary: Add login flow
Next: integrating into develop
Logs: .loop/runs/20260517-000000-a1b2c3
```

`--json` prints a structured object with the same fields.
