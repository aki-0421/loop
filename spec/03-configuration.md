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

`agent.default` is `codex`. Each adapter defines a command, arguments, prompt passing mode, and environment. The built-in Codex adapter uses `codex exec --json` because `loop` invokes agents non-interactively and consumes structured audit events.

Prompt passing modes:

| Mode | Behavior |
| --- | --- |
| `stdin` | Pass the assembled prompt to standard input. |
| `arg` | Pass the prompt as one command argument. |

Adapter arguments may include placeholders:

- `{prompt}`: prompt text for `arg` mode.
- `{cwd}`: working directory.

Path-bearing prompt placeholders such as `{prompt_file}`, `{result_file}`, and `{iteration_dir}` are rejected. Agents should access iteration content through `loop iteration ...` commands.

## `skills`

`skills.sourceDir` is the preferred project skill directory. The default is `.agents/skills/`, matching agents that share project-level skills. `loop` also discovers existing project skill directories such as `.codex/skills/`, `.claude/skills/`, `.cline/skills/`, and `skills/` so init and prompt assembly do not create redundant copies.

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
| `local_merge` | Squash merge the iteration branch into the base branch locally. |
| `pr` | Require the agent to create, check, fix when needed, and merge the pull request through `loop pr`; then refresh the base branch and continue. |

Pull request mode uses `gh` commands. The CLI writes the generated title and body to files before invoking `gh`.

Pull request check timing:

| Field | Built-in value | Behavior |
| --- | ---: | --- |
| `git.integration.pr.waitChecks` | `true` | Make `loop pr checks` and `loop pr merge` run `gh pr checks <pr> --watch`. |
| `git.integration.pr.checksRequiredOnly` | `false` | Pass `--required` so only required checks affect the wait. |
| `git.integration.pr.checksStartupDelaySeconds` | `5` | Wait after PR creation or fix push before asking GitHub for checks. |
| `git.integration.pr.checksDiscoveryTimeoutSeconds` | `60` | Keep polling when GitHub reports no checks for the PR branch. |
| `git.integration.pr.checksPollIntervalSeconds` | `5` | Delay between no-checks discovery polls and the `gh pr checks --watch --interval` value. |
| `git.integration.pr.checksWatchTimeoutSeconds` | `3600` | Maximum time for a reported pending check set to complete before the command fails. |

If no checks are reported after the discovery timeout, the check wait is treated as skipped.

`git.integration.pr.mergeWhenChecksPass` is accepted for compatibility with older configs, but PR merges are initiated by `loop pr merge`.

## `validation`

Configured validation commands run after the agent phase and before integration. They run from the iteration worktree.

Each command has:

| Field | Behavior |
| --- | --- |
| `name` | Human-readable label used in validation logs. |
| `run` | Shell command string to execute. |
| `required` | When true, a non-zero exit code fails validation and prevents integration. |

Validation commands use the user's default shell instead of hard-coded `sh` where possible. On Unix, the CLI reads `SHELL`; zsh, bash, fish, ksh, and csh-family shells run as login command shells, `nu` runs with `-l -c`, PowerShell-compatible shells run with `-Command`, and other shells run with `-c`. If `SHELL` is empty, the CLI falls back to `sh -c`. On Windows, the CLI uses `COMSPEC /C`, falling back to `cmd /C`.

## Environment variables

| Variable | Behavior |
| --- | --- |
| `LOOP_AGENT` | Overrides `agent.default`. |
| `LOOP_CONFIG` | Overrides config path. |
| `LOOP_MAX_ITERATIONS` | Overrides run iteration limit; `0` means unlimited. |
| `LOOP_BASE_BRANCH` | Overrides base branch. |
| `LOOP_NO_COLOR` | Disables color output. |
