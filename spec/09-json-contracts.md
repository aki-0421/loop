# JSON Contracts

## Iteration result

The agent writes the `result` artifact through `loop iteration result --write`. The lower-level `loop iteration write result` remains available for repair fallback when the result builder itself is unavailable.

Required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Schema version. Current value is `1`. |
| `status` | enum | `completed`, `no_change`, `needs_repair`, `blocked`, or `failed`. |
| `summary_sentence` | string | One English sentence used for squash commit or merge subject. |
| `should_fully_stop` | boolean | Whether the run goal is fully satisfied after this iteration. |
| `goal_evaluation` | string | Short explanation of the stop decision. |
| `branch` | object | Branch proposal used by the CLI for the final branch rename. |
| `commits` | array | Commits created through `loop commit` during the iteration. |
| `validation` | object | Validation commands and results. |
| `artifacts` | object | Paths to plan, TODO, summary, PR files, and logs. |

Example:

```json
{
  "schema_version": 1,
  "status": "completed",
  "summary_sentence": "Add password reset validation tests",
  "should_fully_stop": false,
  "goal_evaluation": "The requested test area improved, but token refresh coverage remains missing.",
  "branch": {
    "initial_name": "wip/0001",
    "kind": "test",
    "slug": "add-password-reset-validation-tests",
    "final_name": "test/add-password-reset-validation-tests"
  },
  "commits": [
    {
      "sha": "abc1234",
      "message": "T: add password reset validation tests"
    }
  ],
  "validation": {
    "status": "passed",
    "commands": [
      {
        "name": "test",
        "command": "make test",
        "exit_code": 0,
        "required": true
      }
    ]
  },
  "artifacts": {
    "plan": "plan",
    "todo": "todo",
    "summary": "summary",
    "pr_title": "pr-title",
    "pr_body": "pr-body"
  },
  "assumptions": [
    "Used the existing test command from the repository Makefile."
  ]
}
```

`branch.final_name` is a proposal or expected final name. The agent must not rename or switch branches. The CLI may normalize the proposal, append a collision suffix, and perform the actual branch rename before integration.

Commit message validation happens when `loop commit` creates the commit. The result artifact reports commit SHAs and subjects; it must not be used as the first place a malformed commit message is discovered.

## Result builder

`loop iteration result` prints valid iteration result JSON by default. With `--write`, it writes the generated JSON to the `result` artifact.

The command fills fields the CLI can determine safely:

- `schema_version=1`
- `branch.initial_name` from runtime context or the current Git branch
- `commits` from the runtime base branch to `HEAD`
- `artifacts` for existing logical artifacts
- `branch.kind` and `branch.slug` from commit intent and summary when not supplied

The agent must provide semantic fields:

```bash
loop iteration result --write \
  --status completed \
  --summary "Add password reset validation tests" \
  --should-stop false \
  --goal-evaluation "The selected slice is complete; token refresh coverage remains." \
  --validation-status passed
```

`--validation-command` is repeatable and accepts `name|command|exit_code|required` or a JSON command object. If no validation command is supplied, `--validation-status` should be set explicitly, usually to `skipped`.

Before writing, the builder rejects mechanical contract problems it can detect, including completed results with no commits, completed/no-change results with a dirty working tree, branch mismatches, blocked results without `--blocked-reason`, and failed results without `--error`.

## Stop decision

The runtime artifact includes the goal when one is provided. The agent applies this rule:

```text
If the goal is fully satisfied after this iteration is integrated, set should_fully_stop to true. Otherwise set it to false and explain the remaining gap in goal_evaluation.
```

When no goal is provided, the agent sets `should_fully_stop=true` only if it finds no useful next iteration under the instruction file.

## Validation status

Validation status values:

| Value | Meaning |
| --- | --- |
| `passed` | Required validation commands passed. |
| `failed` | A required validation command failed. |
| `skipped` | No validation command was available or selected. |
| `partial` | Optional commands failed but required commands passed. |

A `completed` result with `validation.status=failed` is not integrated.

## Run state

`run-state.json` records the resumable state:

```json
{
  "schema_version": 1,
  "run_id": "20260517-000000-a1b2c3",
  "instruction_path": "docs/task.md",
  "goal": "The feature is implemented and verified",
  "base_branch": "develop",
  "agent": "codex",
  "current_iteration": "0001",
  "stage": "agent_running",
  "iterations": []
}
```

Stages:

- `created`
- `branch_created`
- `agent_running`
- `repair_running`
- `validating`
- `integrating`
- `completed`
- `blocked`
- `failed`
- `cancelled`

## Config schema

`.loop/config.yaml` is validated by converting YAML to JSON-compatible values and applying `schemas/loop.config.schema.json`.
