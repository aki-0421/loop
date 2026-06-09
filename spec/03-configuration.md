# Configuration

## Lookup order

`loop` builds the effective configuration from lowest to highest precedence:

1. Built-in defaults.
2. User config: `~/.config/loop/config.yaml`.
3. Repository config: `.loop/config.yaml`.
4. Environment variables prefixed with `LOOP_`.
5. Command flags.

The effective configuration is written to each iteration directory as `effective-config.yaml`.

The configuration contract is documented in this Markdown specification and summarized in `09-json-contracts.md`; loop does not maintain a separate JSON Schema file for config validation.

Repository config remains in `.loop/config.yaml`. Runtime data is stored outside the repository under `~/.loop/workspaces/<repo-id>/` by default. Set `LOOP_HOME` to override the `~/.loop` root.

## Example

`loop init` writes a minimal repository config. Built-in defaults supply everything else.

```yaml
version: 1
```

Only keep values that differ from the built-in defaults. For example:

```yaml
version: 1

agent:
  default: claude
  adapters:
    claude:
      command: claude

run:
  maxIterations: 3 # 0 or omitted means unlimited
  maxParallelTasks: 2
  maxTaskAttempts: 2

skills:
  sourceDir: .codex/skills

git:
  baseBranch: main # omit to use the branch at run start
```

## `language`

| Key | Type | Behavior |
| --- | --- | --- |
| `default` | string | Default language tag for generated human-readable content. Built-in value is `en`. |
| `allowSkillOverride` | boolean | Allows skills to request another output language for repository-specific workflows. |

JSON keys and contract values are always English identifiers.

## `agent`

`agent.default` is `codex`. Each adapter defines a command, arguments, prompt passing mode, and environment. The built-in Codex adapter uses `codex exec --json` because `loop` invokes agents non-interactively and consumes structured audit events. The built-in Claude Code adapter uses `claude -p {prompt} --verbose --output-format stream-json --dangerously-skip-permissions` so Claude Code result usage can be normalized into `agent.usage`.

Built-in known adapters:

| Adapter | Command shape | Usage source |
| --- | --- | --- |
| `codex` | `codex exec --json` | `turn.completed.usage` deltas and `token_count.info.total_token_usage` snapshots |
| `claude` | `claude -p {prompt} --verbose --output-format stream-json --dangerously-skip-permissions` | `result.usage` snapshots |

Prompt passing modes:

| Mode | Behavior |
| --- | --- |
| `stdin` | Pass the assembled prompt to standard input. |
| `arg` | Pass the prompt as one command argument. |

Adapter arguments may include placeholders:

- `{prompt}`: prompt text for `arg` mode.
- `{cwd}`: working directory.

Path-bearing prompt placeholders such as `{prompt_file}`, `{result_file}`, and `{iteration_dir}` are rejected. Agents should access iteration content through `loop iteration ...` and write role outputs through `loop handoff`.

## `run`

| Key | Type | Behavior |
| --- | --- | --- |
| `maxIterations` | integer | Maximum iterations; `0` means unlimited. |
| `maxParallelTasks` | integer | Maximum non-conflicting coding tasks to run at once. Built-in value is `2`. |
| `maxTaskAttempts` | integer | Attempts for a coding task before failing the iteration. Built-in value is `2`. |

## `skills`

`skills.sourceDir` is the preferred project skill directory. The default is `.agents/skills/`, matching agents that share project-level skills. `loop` also discovers existing project skill directories such as `.codex/skills/`, `.claude/skills/`, `.cline/skills/`, and `skills/` so prompt assembly can find repository skills. `loop init` delegates default skill installation to `npx skills` instead of directly copying skill files.

Sync modes:

| Mode | Behavior |
| --- | --- |
| `copy` | Copy skills when `loop skills sync` or explicit sync-on-run is enabled. |
| `symlink` | Symlink skills when the platform supports it. |
| `off` | Do not sync; reference discovered skill paths in prompts. |

## `git`

| Key | Type | Behavior |
| --- | --- | --- |
| `baseBranch` | string | Optional pinned integration target. When omitted, `loop run` uses the branch that was checked out when the command started. |

Branch naming is not configurable. The CLI creates each iteration in a Git worktree on a temporary branch named `wip/<iteration>`, accepts only its fixed branch kind preset for `loop branch rename`, slugifies the branch subject, and appends `-<iteration>` only when needed to avoid collisions.

## `git.integration`

Modes:

| Mode | Behavior |
| --- | --- |
| `local_merge` | Merge the approved iteration branch into the base branch locally using `git.integration.mergeMethod`. |
| `pr` | The merge agent creates, checks, and merges the pull request through loop-owned PR commands after QA review approval; the CLI verifies merged state before cleanup. |

Pull request mode is the built-in default and uses `gh` commands. In role-orchestrated mode, the merge agent reads the PR template, writes `pr-title` and `pr-body`, and then invokes `loop pr create`.

Iteration merge methods:

| Method | Behavior |
| --- | --- |
| `squash` | Built-in default. Local mode creates one squash commit on the base branch, and PR auto-merge uses `gh pr merge --squash`. The commit body includes the iteration's internal commits so task-level history remains inspectable after the branch is deleted. |
| `merge_commit` | Local mode uses `git merge --no-ff`, and PR auto-merge uses `gh pr merge --merge`. The merge commit subject uses `Complete change set: <summary>` so the boundary is visible in history. |

Pull request review modes:

| Mode | Behavior |
| --- | --- |
| `auto_merge` | Fully autonomous mode. The merge agent creates the PR, waits for checks, runs `loop pr merge`, and `merge-result.status=merged` requires `pr-state.status=merged`. |
| `parallel_human_review` | Trust-ramped parallel mode. The merge agent creates the PR and waits for checks, `loop pr checks` records `pr-state.status=waiting_for_human`, and the CLI preserves the remote PR for human review while continuing later iterations from the base branch. Runtime context includes pending PR titles, branches, and changed files so planners can avoid overlapping work. At iteration boundaries, the CLI removes merged or closed pending PRs, then planners inspect remaining PR feedback with `loop pr feedback <pr>` and can return `repair_pull_request` to resume that PR branch after planning; when no safe non-overlapping work remains, planners can return `wait_for_pending_prs=true` with no tasks to enter PR review wait mode. |
| `serial_human_review` | Strict manual mode. The merge agent creates the PR and waits for checks, `loop pr checks` records `pr-state.status=waiting_for_human`, and the CLI shows a PR review wait screen and polls every 5 minutes until a human merges the PR externally before cleanup or the next iteration. |

Interactive PR runs can change the active review policy with `r`, cycling through `auto merge`, `parallel review`, and `serial review`. The renderer displays the active policy at the end of the metrics line and locks the shortcut while PR integration is in progress.

Pull request check timing:

| Field | Built-in value | Behavior |
| --- | ---: | --- |
| `git.integration.pr.waitChecks` | `true` | Make `loop pr checks` and `loop pr merge` run `gh pr checks <pr> --watch`. |
| `git.integration.pr.checksRequiredOnly` | `false` | Pass `--required` so only required checks affect the wait. |
| `git.integration.pr.checksStartupDelaySeconds` | `5` | Wait after PR creation or fix push before asking GitHub for checks. |
| `git.integration.pr.checksDiscoveryTimeoutSeconds` | `60` | Keep polling when GitHub reports no checks for the PR branch. |
| `git.integration.pr.checksPollIntervalSeconds` | `5` | Delay between no-checks discovery polls and the `gh pr checks --watch --interval` value. |
| `git.integration.pr.checksWatchTimeoutSeconds` | `3600` | Maximum time for a reported pending check set to complete before the command fails. |
| `git.integration.pr.humanReview` | `false` | Compatibility boolean. When true and `reviewMode` is omitted, selects `serial_human_review`. |
| `git.integration.pr.reviewMode` | `auto_merge` | Pull request review policy: `auto_merge`, `parallel_human_review`, or `serial_human_review`. |

If no checks are reported after the discovery timeout, the check wait is treated as skipped.

`git.integration.pr.mergeWhenChecksPass` is accepted for compatibility with older configs, but role-orchestrated PR merges are initiated by the merge agent with `loop pr merge` after checks pass.

## `validation`

Configured validation commands run after the agent phase and before integration. They run from the iteration worktree.

Each command has:

| Field | Behavior |
| --- | --- |
| `name` | Human-readable label used in validation logs. |
| `run` | Shell command string to execute. |
| `required` | When true, a non-zero exit code fails validation and prevents integration. |

Validation commands use the user's default shell instead of hard-coded `sh` where possible. On Unix, the CLI reads `SHELL`; zsh, bash, fish, ksh, and csh-family shells run as login command shells, `nu` runs with `-l -c`, PowerShell-compatible shells run with `-Command`, and other shells run with `-c`. If `SHELL` is empty, the CLI falls back to `sh -c`. On Windows, the CLI uses `COMSPEC /C`, falling back to `cmd /C`.

## `logs`

| Key | Type | Behavior |
| --- | --- | --- |
| `dir` | string | Run log directory relative to the repository runtime store. The built-in value is `runs`. The legacy `.loop/runs` value is normalized to `runs` so runtime logs still stay outside the repository. Absolute paths are accepted as explicit overrides. |
| `retainRawAgentOutput` | boolean | Keep sanitized agent output events for audit. |
| `redactEnv` | boolean | Redact configured environment keys in human-readable logs. |

## Environment variables

| Variable | Behavior |
| --- | --- |
| `LOOP_AGENT` | Overrides `agent.default`. |
| `LOOP_CONFIG` | Overrides config path. |
| `LOOP_MAX_ITERATIONS` | Overrides run iteration limit; `0` means unlimited. |
| `LOOP_BASE_BRANCH` | Overrides base branch. |
| `LOOP_HOME` | Overrides the runtime storage root used instead of `~/.loop`. |
| `LOOP_NO_COLOR` | Disables color output. |
