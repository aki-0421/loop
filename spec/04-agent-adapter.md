# Agent Adapter

## Interface

The Go implementation exposes an adapter interface similar to:

```go
type AgentAdapter interface {
    Name() string
    Prepare(ctx context.Context, req PrepareRequest) (*PreparedAgent, error)
    Run(ctx context.Context, req RunRequest) (*RunResult, error)
}
```

`codex` is the built-in default adapter. Additional adapters use the same process contract.

## Run request

An adapter receives:

- Working directory.
- Environment variables.
- Code-generated skill bootstrap.
- Prompt text or prompt stream.
- Iteration directory path.
- Optional timeout.
- Role metadata such as `LOOP_ROLE` and, for coding agents, `LOOP_TASK_ID`.

## Prompt assembly

The assembled prompt is a compact, non-user-editable skill bootstrap:

1. A short instruction to use the `loop` skill.
2. A short instruction to use `loop iteration`, `loop issue`, and `loop handoff` commands.

Detailed loop behavior and CLI usage live in repository skills, especially `loop`. The prompt intentionally does not inline skill instructions, effective config, JSON contract details, required file paths, runtime values, cached GitHub context memory, goal text, instruction Markdown content, or instruction file paths.

## Bootstrap requirements

The code-generated bootstrap activates the `loop` skill. Skills use `loop iteration`, `loop issue`, and `loop handoff` commands for runtime context, GitHub Issues, and strict role handoffs. Agents do not receive a command for querying cached memory directly. The CLI owns commit creation, pull request text, check waiting, check failure handling, PR merge, and cleanup.

## Process output capture

The CLI records:

```text
agent-events.jsonl
tasks/<sequence>/agent-events.jsonl
errors.log        # only on non-successful phases
```

Iteration and task `agent-events.jsonl` files contain normalized events. Coding-agent events are written to the task file only. Every agent event includes `agent_type`; coding-agent events also include `task_id` and `task_dir`.

```json
{"type":"agent.started","ts":"2026-05-17T00:00:00Z","agent_type":"planner","command":"codex"}
{"type":"agent.message","ts":"2026-05-17T00:00:00Z","agent_type":"planner","text":"Inspecting the task contract before editing."}
{"type":"agent.usage","ts":"2026-05-17T00:00:00Z","agent_type":"coding","task_id":"add-tests","input_tokens":1200,"output_tokens":45,"cache_read_tokens":300,"cache_creation_tokens":0,"delta":true}
{"type":"agent.command","ts":"2026-05-17T00:00:01Z","agent_type":"coding","task_id":"add-tests","command":"make test"}
{"type":"agent.file_read","ts":"2026-05-17T00:00:02Z","agent_type":"review","path":"internal/cli/renderer.go"}
{"type":"agent.exited","ts":"2026-05-17T00:00:03Z","agent_type":"review","exit_code":0}
```

`agent.usage` is the canonical usage accounting event. Adapters should emit it whenever they can normalize model-reported token usage. `input_tokens` and `output_tokens` are required when known. `cache_read_tokens`, `cache_creation_tokens`, `reasoning_output_tokens`, and `total_tokens` are optional metadata. `delta=true` means the event is an increment to add to the current iteration total. When `delta` is absent or false, the event is a snapshot relative to the current agent process; renderers and summaries must combine it with the usage baseline captured at `agent.started`. `estimated=true` marks heuristic usage.

The built-in process adapter recognizes these provider streams without reading provider-owned session files:

- Codex `exec --json` `turn.completed.usage` events as usage deltas.
- Codex `token_count.info.total_token_usage` events as usage snapshots.
- Claude Code `--output-format stream-json` `result.usage` events as final usage snapshots.

`agent.message` records short, filtered assistant-visible message snippets so durable logs preserve agent progress context. Raw agent transcripts are not persisted. File contents, diffs, and thinking text may be shown transiently by the renderer after filtering, but must not be written to disk. `agent.stdout.log`, `agent.stderr.log`, and `agent-exit.json` are not created.

## Role Handoff Contract

Role-orchestrated agents write planner, task, and review handoffs with `loop handoff`. The CLI validates and stores those handoffs in `.loop/loop.db`, then copies audit JSON into the durable iteration directory. Agents do not commit, rename branches, create PRs, merge PRs, or close iterations in the role-orchestrated workflow.

## Legacy Result Handoff Contract

Older single-agent workflows can still write the master-DB terminal handoff with `loop iteration close --merge` or `loop iteration close --skip-merge`. The CLI validates it against the iteration close contract in `09-json-contracts.md`.

After a valid terminal handoff is observed, the CLI records the handoff but does not signal or cancel the agent. It waits for the agent process to finish by itself and continues draining stdout/stderr until that natural exit, so final provider events, especially usage events, can be recorded. Force cancellations such as a second Ctrl+C still terminate the process through the normal cancellation path.

If the terminal handoff is missing or invalid, the run fails the iteration contract. The CLI does not relaunch the agent for correction.

## Agent exit interpretation

| Agent exit | Close JSON | Working tree | CLI action |
| --- | --- | --- | --- |
| zero | valid merge | clean and committed | integrate |
| zero | valid skip-merge | any | do not integrate; clean up branch/worktree |
| zero | missing or invalid | any | fail iteration contract |
| non-zero | valid skip-merge | any | do not integrate; clean up branch/worktree |
| non-zero | missing or invalid | any | fail iteration contract |
