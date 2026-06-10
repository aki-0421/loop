# JSON Contracts

## Contract Source

This Markdown file is the source of truth for JSON and YAML contracts. Do not maintain duplicate `schemas/*.schema.json` files. Runtime validation is implemented with Go struct decoding, known-field YAML decoding, and contract-specific validators.

## Role Handoffs

Role-orchestrated runs use DB-backed handoffs written with `loop handoff`.

### Task Tree

Required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Current value is `1`. |
| `summary` | string | One-sentence AI sprint-sized PR summary. |
| `goal_evaluation` | string | Current view of the CLI goal. |
| `goal_complete` | boolean | Optional. True only when a CLI goal exists and the planner believes no more work remains after integration. |
| `wait_for_pending_prs` | boolean | Optional. Only valid with an empty `tasks` array. In `parallel_human_review` mode, this asks the CLI to wait for pending human-review PR changes when no safe non-overlapping implementation work remains. |
| `repair_pull_request` | string | Optional. PR number or URL from `pending_pull_requests`. When set, `tasks` describe repair work for that existing human-review PR branch. The CLI checks out the PR branch only after the planner handoff is accepted. |
| `tasks` | array | Coding tasks with dependencies and conflicts. |

Task fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | Lowercase letters, digits, and hyphens, starting with a letter. |
| `title` | string | Short task title. |
| `description` | string | Instructions for the coding agent. |
| `depends_on` | array | Task IDs that must complete first. |
| `conflicts_with` | array | Task IDs that must not run concurrently. |
| `acceptance` | array | Concrete acceptance checks. |

The CLI rejects duplicate IDs, unknown dependencies or conflicts, self-dependencies, self-conflicts, dependency cycles, invalid IDs, `wait_for_pending_prs` combined with tasks, `repair_pull_request` without tasks, `repair_pull_request` combined with `wait_for_pending_prs`, and unknown fields. Planner tasks must not include commit metadata.

### Task Result

Required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Current value is `1`. |
| `task_id` | string | Completed task ID. |
| `status` | enum | `completed`, `discarded`, or `failed`. |
| `summary` | string | Outcome summary. |
| `discard_reason` | string | Required when `status=discarded`; explains why the planner must revise or replace the task. |
| `validation` | array | Optional validation notes. |
| `notes` | array | Optional additional notes. |

### Review Result

Required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Current value is `1`. |
| `status` | enum | `approved`, `changes_requested`, or `failed`. |
| `summary` | string | Review summary. |
| `goal_complete` | boolean | Optional goal completion decision. |
| `goal_evaluation` | string | Explanation of the goal decision. |
| `findings` | array | Repair-task findings when changes are requested. |

Each finding has `id`, optional `task_id`, `title`, `description`, and `acceptance`. Findings must be specific enough for the CLI to create repair tasks.

QA review agents may also record findings incrementally with `loop review finding add`. Recorded findings are merged into the final `review-result` after the review agent exits. If at least one recorded finding exists, the CLI treats the review as `changes_requested` and starts repair tasks even when the final handoff omits inline `findings`.

### Merge Result

Required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Current value is `1`. |
| `status` | enum | `merged`, `waiting_for_human`, `pr_check_failed`, `blocked`, or `failed`. |
| `summary` | string | Merge lifecycle summary. |
| `pr` | string | Optional PR URL or number. |
| `branch` | string | Optional PR branch. |
| `findings` | array | Repair-task findings when `status=pr_check_failed`. |

`pr_check_failed` requires at least one finding. Findings use the same shape as review findings and must be specific enough for the CLI to create repair tasks. PR check failures are reported here, not as review `changes_requested`.

## Task TODOs

`loop task todo` writes `tasks/<sequence>/task-todo.json`. Coding agents must create initial TODOs before editing the task worktree. Pending TODOs can be inserted, moved, removed, or cancelled. Done, active, and cancelled TODOs are fixed; later additions and moves must stay after the last fixed TODO. TODOs are processed serially. `commit` TODOs are staged with `loop task todo stage <n>` and completed by `loop task todo complete <n>`, which creates the task-branch commit from staged changes before marking the item done. `no_commit` TODOs skip staging and are marked done by `loop task todo complete <n>` only when the task worktree has no repository changes. Cancelled TODOs are skipped by serial start, stage, completion, and merge checks.

TODO file fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Current value is `1`. |
| `task_id` | string | Coding task ID. |
| `items` | array | Ordered task-local TODO items. |

TODO item fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `status` | enum | `pending`, `active`, `done`, or `cancelled`. |
| `work_type` | enum | `commit` or `no_commit`. Missing values in older files are treated as `commit`. |
| `type` | enum | Required for `commit`; commit type `F`, `T`, `R`, `D`, `S`, `V`, or `C`. |
| `title` | string | Short TODO title. |
| `acceptance` | array | TODO-specific success criteria. |
| `commit_message` | string | Required for `commit`; lowercase imperative commit body. |
| `commit_sha` | string | Required when a `commit` TODO is done; created by `loop task todo complete`. |
| `commit_subject` | string | Required when a `commit` TODO is done; final `<TYPE>: <message>` subject. |
| `cancelled_at` | string | Required when cancelled; UTC RFC3339 timestamp. |

## Task Merge Audit

`loop task merge` writes `tasks/<sequence>/task-merge.json` after the completed task branch is incorporated into the iteration branch.

Required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Current value is `1`. |
| `task_id` | string | Completed task ID. |
| `status` | enum | `merged`. |
| `branch` | string | Task branch that was merged. |
| `iteration_branch` | string | Iteration branch that received the task branch merge. |
| `task_commits` | array | Task-branch TODO commits as `sha` and `subject`. |
| `merge_commit` | object | The iteration branch merge commit as `sha` and `subject`; subjects use `Complete work: <task title>`. |
| `merged_at` | string | UTC timestamp when the merge completed. |

## Pending Pull Requests

When `git.integration.pr.reviewMode=parallel_human_review`, approved PRs that are waiting for external human review remain in `run-state.json` under `pending_pull_requests` and are copied into later runtime artifacts.

Pending PR fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `pr` | string | PR number or URL returned by GitHub. |
| `branch` | string | PR head branch. |
| `base` | string | Integration base branch. |
| `title` | string | PR title when known. |
| `run_id` | string | Run that created the PR. |
| `iteration_id` | string | Iteration that created the PR. |
| `changed_files` | array | Files changed by the PR branch relative to the base branch. Later planners use this to avoid overlapping work. |
| `created_at` | string | UTC timestamp recorded by `loop pr create`. |
| `status` | enum | `waiting_for_human` while the PR is pending. |
| `review_decision` | string | Latest GitHub review decision when known. |
| `review_feedback` | string | Compact summary of the latest actionable review decision or PR comments. |
| `review_feedback_at` | string | Timestamp of the latest actionable feedback. |
| `feedback_handled_at` | string | Latest feedback timestamp already incorporated by a repair iteration. |

## Iteration Close

Role-orchestrated runs use role handoffs instead of terminal iteration close JSON. The iteration close contract remains for command-level validation.

Required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Schema version. Current value is `1`. |
| `action` | enum | `merge` or `skip_merge`. |
| `summary_sentence` | string | Required for `merge`; one English sentence used for the merge subject. |
| `skip_merge_reason` | string | Required for `skip_merge`; explains why this branch should not be incorporated. |
| `sleep_until_github_update` | boolean | Optional for `skip_merge`; true means pause in GitHub sleep mode before the next iteration and requires `should_fully_stop=false`. |
| `should_fully_stop` | boolean | Whether the run goal is fully satisfied. |
| `goal_evaluation` | string | Short explanation of the stop decision. |
| `branch` | object | Initial and tracked branch metadata for the iteration. |
| `commits` | array | Commits created through `loop commit` during the iteration. |
| `validation` | object | Validation commands and results. |
| `artifacts` | object | Logical names for active plan, TODO, and PR text files. |

The close JSON rejects unknown fields. `merge` requires `summary_sentence`, disallows `sleep_until_github_update=true`, and requires validation status `passed` or `skipped`. `skip_merge` requires `skip_merge_reason`. When `sleep_until_github_update=true`, `action` must be `skip_merge` and `should_fully_stop` must be `false`.

`branch` fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `initial_name` | string | Required initial numbered branch created by loop. |
| `kind` | string | Optional tracked branch kind derived from `loop branch rename`. |
| `slug` | string | Optional tracked branch slug derived from `loop branch rename`. |
| `final_name` | string | Optional tracked branch name after rename. Required for merge by the builder. |

`commits` item fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `sha` | string | Optional commit SHA. |
| `message` | string | Required commit subject. |

`validation` fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `status` | enum | `passed`, `failed`, `skipped`, or `partial`. |
| `commands` | array | Command log entries. |

`validation.commands` item fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | Required validation command label. |
| `command` | string | Required shell command string. |
| `exit_code` | integer | Process exit code. |
| `required` | boolean | Whether a non-zero exit code blocks integration. |
| `output_path` | string | Optional path to captured output. |

`artifacts` fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `plan` | string | Optional logical name for the active plan artifact. |
| `todo` | string | Optional logical name for the active TODO artifact. |
| `pr_title` | string | Optional logical name for the pull request title artifact. |
| `pr_body` | string | Optional logical name for the pull request body artifact. |

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
  --goal-evaluation "The selected implementation scope is complete; token refresh coverage remains." \
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

Run state fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Required schema version. Current value is `1`. |
| `run_id` | string | Required stable run identifier. |
| `goal` | string | Optional run goal supplied by the user. |
| `base_branch` | string | Required integration branch. |
| `agent` | string | Required agent adapter name. |
| `current_iteration` | string | Required current iteration id. |
| `stage` | enum | Required run stage. |
| `iterations` | array | Required iteration records. |

Stages:

- `created`
- `branch_created`
- `agent_running`
- `validating`
- `integrating`
- `completed`
- `failed`
- `cancelled`

Iteration record fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `iteration_id` | string | Required numbered iteration id. |
| `branch_initial` | string | Required initial branch name. |
| `branch_current` | string | Optional current tracked branch name. |
| `branch_final` | string | Optional final branch name after integration. |
| `stage` | string | Required iteration stage. |
| `result_path` | string | Optional result path. Current runs leave it empty because close handoffs are stored in the runtime `loop.db`. |
| `summary_sentence` | string | Optional merge summary. |
| `should_fully_stop` | boolean | Optional final stop decision from the iteration close. |

## Config Contract

Configuration fields and defaults are documented in `03-configuration.md`. `.loop/config.yaml` and user config are merged with the built-in defaults, decoded with known fields enabled, normalized, and validated by the Go implementation.

Validation requirements:

| Area | Requirement |
| --- | --- |
| `version` | Required value `1`. |
| `language.default` | Required string. |
| `agent.default` | Required string with a matching `agent.adapters.<name>` entry. |
| `agent.adapters.*.command` | Required string. |
| `agent.adapters.*.prompt` | Must be `stdin` or `arg`. |
| `agent.adapters.*.args` | Must not contain `{prompt_file}`, `{result_file}`, or `{iteration_dir}`. |
| `run.maxIterations` | Must be `0` or greater. |
| `skills.sourceDir` | Required string. |
| `skills.targets.*.mode` | Must be `copy`, `symlink`, or `off`. |
| `git.integration.mode` | Must be `local_merge` or `pr`. |
| `git.integration.mergeMethod` | Must be `squash` or `merge_commit`. |
| `git.integration.pr.checksStartupDelaySeconds` | Must be non-negative. |
| `git.integration.pr.checksDiscoveryTimeoutSeconds` | Must be non-negative. |
| `git.integration.pr.checksPollIntervalSeconds` | Must be non-negative. |
| `git.integration.pr.checksWatchTimeoutSeconds` | Must be positive. |
| `logs.dir` | Required string. Relative values are resolved under the repository runtime store in `~/.loop/workspaces/<repo-id>/`; the legacy `.loop/runs` value is normalized to `runs`. |
