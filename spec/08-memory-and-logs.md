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

Every active iteration writes disposable runtime artifacts as files in a Go temp directory. These artifacts explain the current run and are not long-term memory.

- `runtime`: JSON runtime context.
- `plan`: intended work.
- `todo`: execution checklist.
- `validation`: validation commands and results.
- `pr-title` and `pr-body`: pull request text.

The durable iteration directory stores audit and replay files plus PR lifecycle diagnostics:

- `prompt`: instruction snapshot for the iteration.
- `effective-config`: effective configuration snapshot.
- `agent-events`: structured audit events.
- `errors`: process, result, validation, or sync warnings.
- `pr-state`, `pr-checks`, and `pr-check-log`: pull request lifecycle state and check diagnostics written by `loop pr`.
- `github-updates`: newly observed GitHub Issue, PR, or comment diffs for an iteration boundary or sleep wake cycle.

Agents should read and write these artifacts through `loop iteration` commands so path resolution and artifact boundaries stay in the CLI. `plan` and `todo` have dedicated `loop iteration plan` and `loop iteration todo` commands; other writable artifacts use `loop iteration write` or `loop iteration append`. The terminal close is a master-DB handoff row written by `loop iteration close`.

After a merge or skip-merge terminal action, the active temp directory is removed. `prompt.md`, `effective-config.yaml`, `agent-events.jsonl`, `errors.log`, PR lifecycle diagnostics, GitHub update diffs, and run state remain for audit and replay.

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

Important product, policy, or large specification questions are asked through GitHub Issues, not local DB-only memory. Agents use:

```bash
loop issue ask --title <text> --body <text>
loop issue report --title <text> --body <text> [--kind <kind>]
```

The command creates and applies `loop:question`. It embeds loop run and iteration metadata in the Issue body and stores only the GitHub Issue reference in normal runtime artifacts.

`loop issue report` records concrete repository or harness improvement proposals. It creates and applies `loop:proposal`. Issue bodies should be GitHub-flavored Markdown and include evidence, impact, and a proposed repository or harness change. Unsupported agent capability findings are rediscovered each iteration and are not persisted as Issues.

After asking a question, agents continue TODOs that are unrelated to that clarification. They close with `--skip-merge` only when no safe mergeable work remains. When GitHub context is needed before more useful work can happen, the agent adds `--sleep` and keeps `--should-stop false` on the skip-merge close. Sleep mode displays that it is waiting for GitHub Issue/PR updates, polls every five minutes for Issue/PR comment or closure diffs, and lets an interactive user press any key to fetch immediately. It writes `github-updates` into the next iteration when a diff appears, and lets the next agent decide whether work can proceed, another Issue action is needed, merge is possible, or skip-merge should return to sleep.

## Completed Context

Completed implementation context is durable in the pull request body. The local iteration directory does not hold disposable active work files; those live in the active Go temp directory while the iteration is running. The iteration directory does not keep a separate summary artifact after completion.

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
{"type":"agent.usage","ts":"2026-05-17T00:00:03Z","input_tokens":1200,"output_tokens":45,"cache_read_tokens":300,"cache_creation_tokens":0,"delta":true}
{"type":"git.branch.created","ts":"2026-05-17T00:00:01Z","branch":"wip/0001"}
{"type":"git.branch.renamed","ts":"2026-05-17T00:00:10Z","from":"wip/0001","to":"feat/add-login-flow"}
{"type":"validation.command.completed","ts":"2026-05-17T00:03:00Z","name":"test","exit_code":0}
{"type":"iteration.merge","ts":"2026-05-17T00:04:00Z","action":"merge"}
```

Usage events are audit metadata, not raw transcripts. `agent.usage` records normalized model usage with `input_tokens` and `output_tokens` when known. Cache, reasoning, and total-token fields may be included when an adapter can source them. Events with `delta=true` are incremental. Events without `delta` are snapshots relative to the current agent process. Events with `estimated=true` are heuristic and must remain visibly marked in user-facing summaries.

`agent.stdout.log`, `agent.stderr.log`, and `agent-exit.json` are not written. `errors.log` is created only when an agent, process, or validation phase fails.

## Redaction

When `logs.redactEnv=true`, environment variables and command lines are redacted by key pattern before writing human-readable logs. The original process environment is never written wholesale.
