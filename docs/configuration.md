# Configuration Guide

This guide covers common repository configuration. The full contract is in [the configuration specification](../spec/03-configuration.md).

`loop init` writes a small `.loop/config.yaml`. Built-in defaults supply normal agent, run, Git, validation, memory, and log settings. Keep repository config focused on values that differ from those defaults.

## Lookup Order

`loop` builds the effective configuration from lowest to highest precedence:

1. Built-in defaults.
2. User config: `~/.config/loop/config.yaml`.
3. Repository config: `.loop/config.yaml`.
4. Environment variables prefixed with `LOOP_`.
5. Command flags.

Each iteration writes its effective configuration to `effective-config.yaml` for audit.

## Autonomous Defaults

The built-in defaults are designed for autonomous AI sprint work:

- `agent.default` is `codex`.
- Pull request integration is the default mode.
- `run.maxIterations` is `0`, which means unlimited iterations.
- `run.maxParallelTasks` is `2`.
- `run.maxTaskAttempts` is `2`.
- `run.maxReviewFixCycles` is `3`.
- Validation commands are empty until the repository config adds them.

A minimal repository config can be only:

```yaml
version: 1
```

## Common Overrides

Limit a first run to one inspected sprint:

```yaml
version: 1

run:
  maxIterations: 1
```

Add repository validation:

```yaml
version: 1

validation:
  commands:
    - name: test
      run: make test
      required: true
```

Pin the base branch:

```yaml
version: 1

git:
  baseBranch: main
```

Use local merge mode for local-only repositories or tests:

```yaml
version: 1

git:
  integration:
    mode: local_merge
```

Customize the Codex adapter:

```yaml
version: 1

agent:
  adapters:
    codex:
      args:
        - exec
        - --json
        - -m
        - gpt-5.5
        - -c
        - model_reasoning_effort="high"
        - -c
        - approval_policy="never"
        - -s
        - danger-full-access
```

Use another agent adapter:

```yaml
version: 1

agent:
  default: claude
  adapters:
    claude:
      command: claude
      prompt: arg
```

## Run-Time Flags

Flags are useful when changing the run shape without editing repository config:

```sh
loop run task.md --pr --human-review --max-iterations 1
```

```sh
loop run task.md \
  --goal "The selected feature is implemented and validation passes" \
  --pr
```

Important behavior:

- `--max-iterations 0` means unlimited.
- `--goal` is a stop condition, not the only source of work.
- Without `--goal`, the run is open-ended and stops only on an iteration limit, interrupt, or terminal error.
- With `--goal`, the run stops only after a successfully integrated iteration satisfies the goal.

## Environment Variables

Common environment overrides:

| Variable | Behavior |
| --- | --- |
| `LOOP_AGENT` | Overrides `agent.default`. |
| `LOOP_CONFIG` | Overrides the config path. |
| `LOOP_MAX_ITERATIONS` | Overrides the run iteration limit; `0` means unlimited. |
| `LOOP_BASE_BRANCH` | Overrides the base branch. |
| `LOOP_NO_COLOR` | Disables color output. |

See [the CLI specification](../spec/02-cli.md) and [the configuration specification](../spec/03-configuration.md) for exhaustive details.
