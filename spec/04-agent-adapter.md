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
- Expected result JSON path.
- Iteration directory path.
- Configured timeout.

## Prompt assembly

The assembled prompt is a compact, non-user-editable skill bootstrap:

1. A short instruction to use the `loop` skill.
2. A short instruction to use `loop iteration`, `loop memory`, and `loop commit` commands.

Detailed loop behavior and CLI usage live in repository skills, especially `loop`. The prompt intentionally does not inline skill instructions, effective config, schema summaries, required file paths, runtime values, recent memory, goal text, instruction Markdown content, or instruction file paths.

## Bootstrap requirements

The code-generated bootstrap activates the `loop` skill. In pull request mode it tells the agent to read template text with `loop iteration read pr-template`. Skills use `loop iteration`, `loop memory`, and `loop pr` commands for mechanical runtime artifact reads and writes. The agent decides when a commit-ready unit is complete, but commit creation goes through `loop commit`; result JSON, pull request text, check repair, and PR merge are handled inside the agent context through CLI commands.

## Process output capture

The CLI records:

```text
agent-events.jsonl
errors.log        # only on non-successful phases
```

`agent-events.jsonl` contains normalized events:

```json
{"type":"agent.started","ts":"2026-05-17T00:00:00Z","command":"codex"}
{"type":"agent.command","ts":"2026-05-17T00:00:01Z","command":"make test"}
{"type":"agent.file_read","ts":"2026-05-17T00:00:02Z","path":"internal/cli/renderer.go"}
{"type":"agent.exited","exit_code":0}
```

Raw agent transcripts are not persisted. File contents, diffs, and thinking text may be shown transiently by the renderer after filtering, but must not be written to disk. `agent.stdout.log`, `agent.stderr.log`, and `agent-exit.json` are not created.

## Result artifact contract

The agent writes the `result` artifact before exiting. The CLI validates it against `schemas/iteration-result.schema.json`.

If the result artifact is missing or invalid, the CLI runs the repair flow when repair attempts remain. The code-generated repair contract includes the validation error and the required schema path.

## Agent exit interpretation

| Agent exit | Result JSON | Working tree | CLI action |
| --- | --- | --- | --- |
| zero | valid | clean or committed | integrate or stop |
| zero | valid | dirty | repair dirty state |
| zero | missing or invalid | any | repair result file |
| non-zero | valid blocked result | any | record blocked state |
| non-zero | missing or invalid | any | repair or fail iteration |
