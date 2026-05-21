# JSON Contracts

## Iteration Close

The agent writes the master-DB terminal handoff through `loop iteration close`. There is no local `result` artifact and there is no public non-merge status enum.

Required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Schema version. Current value is `1`. |
| `action` | enum | `merge` or `skip_merge`. |
| `summary_sentence` | string | Required for `merge`; one English sentence used for squash commit or merge subject. |
| `skip_merge_reason` | string | Required for `skip_merge`; explains why this branch should not be incorporated. |
| `sleep_until_github_update` | boolean | Optional for `skip_merge`; true means pause in GitHub sleep mode before the next iteration and requires `should_fully_stop=false`. |
| `should_fully_stop` | boolean | Whether the run goal is fully satisfied. |
| `goal_evaluation` | string | Short explanation of the stop decision. |
| `branch` | object | Initial and tracked branch metadata for the iteration. |
| `commits` | array | Commits created through `loop commit` during the iteration. |
| `validation` | object | Validation commands and results. |
| `artifacts` | object | Logical names for active plan, TODO, and PR text files. |

Merge example:

```json
{
  "schema_version": 1,
  "action": "merge",
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
    "pr_title": "pr-title",
    "pr_body": "pr-body"
  },
  "assumptions": [
    "Used the existing test command from the repository Makefile."
  ]
}
```

Skip-merge example:

```json
{
  "schema_version": 1,
  "action": "skip_merge",
  "skip_merge_reason": "No repository change is appropriate after reviewing the requested behavior.",
  "should_fully_stop": true,
  "sleep_until_github_update": false,
  "goal_evaluation": "The existing implementation already satisfies the instruction.",
  "branch": {
    "initial_name": "wip/0001"
  },
  "commits": [],
  "validation": {
    "status": "skipped",
    "commands": []
  },
  "artifacts": {}
}
```

`branch.initial_name` is the numbered branch created by loop. `branch.final_name` is the tracked branch after the agent has run `loop branch rename`. Merge closes are rejected when the tracked branch still equals the initial branch. Skip-merge closes may leave `branch.final_name` empty.

Commit message validation happens when `loop commit` creates the commit. The terminal handoff reports commit SHAs and subjects; it must not be used as the first place a malformed commit message is discovered.

## Close Builder

`loop iteration close` writes the generated JSON to the master-DB terminal handoff and prints the JSON to stdout.

The command fills fields the CLI can determine safely:

- `schema_version=1`
- `action` from exactly one of `--merge` or `--skip-merge`
- `sleep_until_github_update` from `--sleep`
- `branch.initial_name` from runtime context
- `branch.final_name` from the tracked current branch after `loop branch rename`
- `commits` from the runtime base branch to `HEAD` for `--merge`
- `artifacts` for existing logical artifacts
- `branch.kind` and `branch.slug` from the tracked branch, commit intent, and summary when available

The agent must provide semantic fields:

```bash
loop iteration close --merge \
  --summary "Add password reset validation tests" \
  --should-stop false \
  --goal-evaluation "The selected slice is complete; token refresh coverage remains." \
  --validation-status passed

loop iteration close --skip-merge \
  --reason "No safe mergeable work remains after opening https://github.com/acme/app/issues/12." \
  --sleep \
  --should-stop false \
  --goal-evaluation "The next decision depends on Issue context."
```

`--validation-command` is repeatable and accepts `name|command|exit_code|required` or a JSON command object. If no validation command is supplied, `--validation-status` should be set explicitly, usually to `skipped`.

Before writing, the builder rejects mechanical contract problems it can detect. `--merge` requires a renamed branch, clean worktree, valid commits, passed or skipped validation, and a merged PR in PR mode. `--skip-merge` requires only a reason and does not require commits, branch rename, validation success, or success JSON.

## Stop Decision

The runtime artifact includes the goal when one is provided. The agent applies this rule:

```text
If the goal is fully satisfied after this iteration is integrated, set should_fully_stop to true. Otherwise set it to false and explain the remaining gap in goal_evaluation.
```

When no CLI goal is provided, `should_fully_stop=true` is invalid regardless of instruction, Issue, PR, or comment text. GitHub sleep is separate: the agent chooses it with `loop iteration close --skip-merge --sleep --should-stop false` when external Issue, PR, or comment updates are needed before more useful work can happen.

## Validation Status

Validation status values:

| Value | Meaning |
| --- | --- |
| `passed` | Required validation commands passed. |
| `failed` | A required validation command failed. |
| `skipped` | No validation command was available or selected. |
| `partial` | Optional commands failed but required commands passed. |

A `merge` close with `validation.status=failed` or `partial` is rejected before handoff.

## Run State

`run-state.json` records the resumable state:

```json
{
  "schema_version": 1,
  "run_id": "20260517-000000-a1b2c3",
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
- `validating`
- `integrating`
- `completed`
- `failed`
- `cancelled`

## Config Schema

`.loop/config.yaml` is validated by converting YAML to JSON-compatible values and applying `schemas/loop.config.schema.json`.
