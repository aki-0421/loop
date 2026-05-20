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

## Runtime artifacts

Every iteration writes structured runtime artifacts into its local SQLite DB, `.loop/runs/<run-id>/iterations/<id>/iteration.db`. These artifacts explain the current run and are not long-term memory.

- `runtime`: JSON runtime context.
- `plan`: intended work.
- `todo`: execution checklist.
- `worklog`: notable commands, decisions, and issues.
- `validation`: validation commands and results.
- `summary`: concise iteration summary for runtime audit and result review.
- `result`: JSON iteration result.
- `pr-title` and `pr-body`: pull request text.
- `pr-state`, `pr-checks`, and `pr-check-log`: pull request lifecycle state and check diagnostics written by `loop pr`.

Agents should read and write these artifacts through `loop iteration` commands so path resolution and artifact boundaries stay in the CLI. `plan` and `todo` have dedicated `loop iteration plan` and `loop iteration todo` commands; other writable artifacts use `loop iteration write` or `loop iteration append`.

## GitHub PR memory

Long-term memory comes from GitHub pull requests. The local `.loop/loop.db` file is only a rebuildable cache of GitHub PR titles and bodies. GitHub is authoritative; local iteration summaries are not indexed as memory.

`loop run` syncs PR memory before the first agent iteration when the repository has a GitHub `origin` remote. If the PR memory cache is empty and this initial sync fails, the run stops. After a successful initial sync, later per-iteration sync failures are recorded as warnings and the run continues with the existing cache. `loop pr merge` best-effort fetches the merged PR from GitHub and upserts it into memory after a successful host merge.

The sync includes open and merged pull requests. Closed pull requests that were not merged are removed from memory during incremental sync.

The prompt never loads every historical pull request. The default context load is:

1. Recent open and merged PR records up to `memory.recentLimit`.
2. SQLite FTS matches from PR titles and bodies up to `memory.searchLimit`.
3. Full PR bodies only when the skill requests them through `loop memory`.

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

`.loop/loop.db` stores searchable GitHub PR records and FTS5 indexes:

```text
global_metadata(key, value)
pr_memory(repo, number, url, state, title, body, updated_at, merged_at, fetched_at)
pr_memory_fts(repo, number, url, state, title, body, updated_at, merged_at, fetched_at)
```

Search uses SQLite FTS5 ranking and may be filtered by GitHub repository.

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
