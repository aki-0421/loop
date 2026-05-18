# Memory and Logs

## Directory layout

```text
.loop/
  config.yaml
  .gitignore
  loop.db
  runs/
    <run-id>/
      run-state.json
      iterations/
        0001/
          effective-config.yaml
          iteration.db
          prompt.md
          agent-events.jsonl
          errors.log        # only when an error occurs
  worktrees/
  tmp/
  locks/
```

Committed files:

- `.loop/config.yaml`
- `.agents/skills/` by default, or an existing discovered agent skill directory
- `.loop/.gitignore`

Ignored files:

- `.loop/runs/`
- `.loop/worktrees/`
- `.loop/tmp/`
- `.loop/locks/`
- `.loop/loop.db`
- SQLite sidecar files

Example `.loop/.gitignore`:

```gitignore
runs/
worktrees/
tmp/
locks/
loop.db
*.db
*.db-wal
*.db-shm
*.log
```

## SQLite artifact memory

Every iteration writes structured artifacts into its local SQLite DB, `.loop/runs/<run-id>/iterations/<id>/iteration.db`. Searchable content is mirrored into the global catalog at `.loop/loop.db`.

- `runtime`: JSON runtime context.
- `plan`: intended work.
- `todo`: execution checklist.
- `worklog`: notable commands, decisions, and issues.
- `validation`: validation commands and results.
- `summary`: concise summary for future context.
- `result`: JSON iteration result.
- `pr-title` and `pr-body`: pull request text.

Agents should read and write these artifacts through `loop iteration read`, `loop iteration write`, or `loop iteration append` so path resolution and artifact boundaries stay in the CLI.

The prompt never loads every historical file. The default context load is:

1. The latest `summary` artifacts up to `memory.recentLimit`.
2. SQLite FTS matches from older artifacts up to `memory.searchLimit`.
3. Full older artifacts only when a matching summary points to them and the skill requests them.

## Summary format

The `summary` artifact uses this format:

```markdown
# Iteration 0001 Summary

- Status: completed
- Branch: feat/add-password-reset-tests
- Squash summary: Add password reset validation tests
- Goal stop: false

## Completed

- Added password reset validation coverage.
- Updated test fixtures for expired tokens.

## Validation

- `make test`: passed

## Follow-up Context

- Token expiration helper may need broader cleanup in a later iteration.
```

## Search index

`.loop/loop.db` stores searchable records and FTS5 indexes:

```text
runs(run_id, updated_at)
iterations(run_id, iteration_id, updated_at)
artifact_index(run_id, iteration_id, artifact, content, updated_at)
artifact_index_fts(run_id, iteration_id, artifact, content)
```

Search uses SQLite FTS5 ranking and may be filtered by run id, iteration id, and artifact name.

## Event log

`agent-events.jsonl` is append-only. Events are newline-delimited JSON.

Required event fields:

| Field | Meaning |
| --- | --- |
| `type` | Event type |
| `ts` | RFC3339 timestamp |
| `iteration_id` | Iteration id when applicable |

Event examples:

```json
{"type":"iteration.started","ts":"2026-05-17T00:00:00Z","iteration_id":"0001"}
{"type":"agent.command","ts":"2026-05-17T00:00:01Z","command":"make test"}
{"type":"agent.file_read","ts":"2026-05-17T00:00:02Z","path":"internal/cli/renderer.go"}
{"type":"git.branch.created","ts":"2026-05-17T00:00:01Z","branch":"wip/0001"}
{"type":"git.branch.renamed","ts":"2026-05-17T00:00:10Z","from":"wip/0001","to":"feat/add-login-flow"}
{"type":"validation.command.completed","ts":"2026-05-17T00:03:00Z","name":"test","exit_code":0}
{"type":"iteration.completed","ts":"2026-05-17T00:04:00Z","status":"completed"}
```

`agent.stdout.log`, `agent.stderr.log`, and `agent-exit.json` are not written. `errors.log` is created only when an agent, process, or validation phase fails.

## Redaction

When `logs.redactEnv=true`, environment variables and command lines are redacted by key pattern before writing human-readable logs. The original process environment is never written wholesale.
