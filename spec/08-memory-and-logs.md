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

Every iteration writes structured runtime artifacts as files in `.loop/runs/<run-id>/iterations/<id>/`. These artifacts explain the current run and are not long-term memory.

- `runtime`: JSON runtime context.
- `plan`: intended work.
- `todo`: execution checklist.
- `worklog`: notable commands, decisions, and issues.
- `validation`: validation commands and results.
- `pr-title` and `pr-body`: pull request text.
- `pr-state`, `pr-checks`, and `pr-check-log`: pull request lifecycle state and check diagnostics written by `loop pr`.
- `github-updates`: newly observed GitHub Issue, PR, or comment diffs for an iteration boundary or sleep wake cycle.

Agents should read and write these artifacts through `loop iteration` commands so path resolution and artifact boundaries stay in the CLI. `plan` and `todo` have dedicated `loop iteration plan` and `loop iteration todo` commands; other writable artifacts use `loop iteration write` or `loop iteration append`. The iteration result is a master-DB handoff row written by `loop iteration result --write`.

After a completed or no-change iteration reaches its terminal action, disposable active files are removed. `prompt.md`, `effective-config.yaml`, `agent-events.jsonl`, `errors.log`, PR lifecycle diagnostics, GitHub update diffs, and run state remain for audit and replay.

## GitHub context memory

Long-term memory comes from GitHub pull requests, Issues, and Issue/PR comments. The local `.loop/loop.db` file is only a rebuildable cache of GitHub titles, bodies, and comments. GitHub is authoritative; local iteration summaries are not indexed as memory.

`loop run` syncs PR memory and GitHub Issue context before the first agent iteration when the repository has a GitHub `origin` remote. If the PR memory cache is empty and the initial PR sync fails, the run stops. If the Issue context cache is empty and the initial Issue sync fails, the run stops. After a successful initial sync, later per-iteration sync failures are recorded as warnings and the run continues with the existing cache. `loop pr merge` best-effort fetches the merged PR from GitHub and upserts it into PR memory after a successful host merge.

PR sync includes open and merged pull requests. Closed pull requests that were not merged are removed from PR memory during incremental sync. Issue sync includes all repository Issues, including non-loop Issues, and incremental GitHub context sync stores newly observed Issue state changes and Issue/PR comments.

The prompt never loads every historical GitHub record. Agents request only the amount of cached GitHub context they need:

1. Recent cached GitHub context records through `loop memory recent --limit <n>`.
2. SQLite FTS matches from PR titles/bodies, Issue titles/bodies, and cached comments through `loop memory search <query> --limit <n>`.
3. Full cached bodies only when the skill requests them through `loop memory`.

`loop memory recent` and `loop memory search` require an explicit positive `--limit` and never perform network access. They read the current cache and print records with kind, number, state, repository, URL, title, and excerpt. Kinds are `pr`, `issue`, `issue-comment`, and `pr-comment`.

## Clarification Issues

Important product, policy, or large blocking specification questions are asked through GitHub Issues, not local DB-only memory. Agents use:

```bash
loop issue ask --title <text> --body <text> [--blocking]
loop issue report --title <text> --body <text> [--kind <kind>] [--blocking]
```

The command creates and applies `loop:question`, and also `loop:blocking` when `--blocking` is supplied. It embeds loop run and iteration metadata in the Issue body and stores only the GitHub Issue reference in normal runtime artifacts.

`loop issue report` records concrete repository or harness improvement proposals. It creates and applies `loop:proposal`, plus `loop:blocking` when `--blocking` is supplied. Issue bodies should be GitHub-flavored Markdown and include evidence, impact, and a proposed repository or harness change. Unsupported agent capability findings are rediscovered each iteration and are not persisted as Issues.

After asking a question, agents continue TODOs that are unrelated to that clarification. They write a `blocked` result only when no safe independent work remains. When a blocked result references an open `loop:blocking` Issue, `loop run` enters in-memory sleep mode instead of adding a new result status. Sleep mode does not start a new iteration; it displays that it is waiting for GitHub Issue/PR updates, polls every five minutes for Issue/PR comment or closure diffs, writes `github-updates` when a diff appears, and relaunches the agent in the same iteration to decide whether work can proceed. If the update is unrelated, the agent may return `blocked` again and the CLI resumes sleep.

## Completed Context

Completed implementation context is durable in the pull request body. The local iteration directory may hold active work files while the iteration is running, but it does not keep a separate summary artifact after completion.

## Search index

`.loop/loop.db` stores searchable GitHub records and FTS5 indexes:

```text
global_metadata(key, value)
github_records(repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, merged_at, fetched_at)
github_records_fts(repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, merged_at, fetched_at)
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
