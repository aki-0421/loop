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

## Prompt assembly

The assembled prompt is a compact, non-user-editable skill bootstrap:

1. A short instruction to use the `loop` skill.
2. A short instruction to use `loop iteration`, `loop memory`, `loop issue`, and `loop commit` commands.

Detailed loop behavior and CLI usage live in repository skills, especially `loop`. The prompt intentionally does not inline skill instructions, effective config, JSON contract details, required file paths, runtime values, recent GitHub context memory, goal text, instruction Markdown content, or instruction file paths.

## Bootstrap requirements

The code-generated bootstrap activates the `loop` skill. In pull request mode it tells the agent to read template text with `loop iteration read pr-template`. Skills use `loop iteration`, `loop memory`, `loop issue`, and `loop pr` commands for mechanical runtime artifact reads, writes, and GitHub Issues. The agent decides when a commit-ready unit is complete, but commit creation goes through `loop commit`; terminal close JSON, pull request text, check failure fixes, and PR merge are handled inside the agent context through CLI commands.

## Process output capture

The CLI records:

```text
agent-events.jsonl
errors.log        # only on non-successful phases
```

`agent-events.jsonl` contains normalized events:

```json
{"type":"agent.started","ts":"2026-05-17T00:00:00Z","command":"codex"}
{"type":"agent.usage","ts":"2026-05-17T00:00:00Z","input_tokens":1200,"output_tokens":45,"cache_read_tokens":300,"cache_creation_tokens":0,"delta":true}
{"type":"agent.command","ts":"2026-05-17T00:00:01Z","command":"make test"}
{"type":"agent.file_read","ts":"2026-05-17T00:00:02Z","path":"internal/cli/renderer.go"}
{"type":"agent.exited","exit_code":0}
```

`agent.usage` is the canonical usage accounting event. Adapters should emit it whenever they can normalize model-reported token usage. `input_tokens` and `output_tokens` are required when known. `cache_read_tokens`, `cache_creation_tokens`, `reasoning_output_tokens`, and `total_tokens` are optional metadata. `delta=true` means the event is an increment to add to the current iteration total. When `delta` is absent or false, the event is a snapshot relative to the current agent process; renderers and summaries must combine it with the usage baseline captured at `agent.started`. `estimated=true` marks heuristic usage.

The built-in process adapter recognizes these provider streams without reading provider-owned session files:

- Codex `exec --json` `turn.completed.usage` events as usage deltas.
- Codex `token_count.info.total_token_usage` events as usage snapshots.
- Claude Code `--output-format stream-json` `result.usage` events as final usage snapshots.

Raw agent transcripts are not persisted. File contents, diffs, and thinking text may be shown transiently by the renderer after filtering, but must not be written to disk. `agent.stdout.log`, `agent.stderr.log`, and `agent-exit.json` are not created.

## Result handoff contract

The agent writes the master-DB terminal handoff with `loop iteration close --merge` or `loop iteration close --skip-merge`. The CLI validates it against the iteration close contract in `09-json-contracts.md`.

After a valid terminal handoff is observed, the CLI records the handoff but does not signal or cancel the agent. It waits for the agent process to finish by itself and continues draining stdout/stderr until that natural exit, so final provider events, especially usage events, can be recorded. External cancellations such as Ctrl+C still terminate the process through the normal cancellation path.

If the terminal handoff is missing or invalid, the run fails the iteration contract. The CLI does not relaunch the agent for correction.

## Agent exit interpretation

| Agent exit | Close JSON | Working tree | CLI action |
| --- | --- | --- | --- |
| zero | valid merge | clean and committed | integrate |
| zero | valid skip-merge | any | do not integrate; clean up branch/worktree |
| zero | missing or invalid | any | fail iteration contract |
| non-zero | valid skip-merge | any | do not integrate; clean up branch/worktree |
| non-zero | missing or invalid | any | fail iteration contract |
