# Configuration

## Lookup order

`loop` builds the effective configuration from lowest to highest precedence:

1. Built-in defaults.
2. User config: `~/.config/loop/config.yaml`.
3. Repository config: `.loop/config.yaml`.
4. Environment variables prefixed with `LOOP_`.
5. Command flags.

The effective configuration is written to each iteration directory as `effective-config.yaml`.

## Example

`loop init` writes a minimal repository config. Built-in defaults supply everything else.

```yaml
version: 1

git:
  baseBranch: main
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
  baseBranch: main
```

## `language`

| Key | Type | Behavior |
| --- | --- | --- |
| `default` | string | Default language tag for generated human-readable content. Built-in value is `en`. |
| `allowSkillOverride` | boolean | Allows skills to request another output language for repository-specific workflows. |

JSON keys and schema values are always English identifiers.

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

## `git.branch`

`initialPattern` creates a numbered branch before the agent plans. The default is `wip/{iteration}`, for example `wip/0001`.

After plan creation, the agent proposes a final branch name in the `result` artifact. The agent keeps working on the initial branch; the CLI applies `finalPattern` and renames the branch before integration, for example:

```text
feat/add-token-refresh-tests
fix/handle-empty-profile-response
refactor/extract-auth-client
```

No built-in final branch pattern uses a `loop/` prefix. If the final name already exists, the CLI appends `conflictSuffix`.

## `git.commits`

`loop commit` uses this section when creating iteration commits.

| Field | Behavior |
| --- | --- |
| `requireAgentCommits` | Require at least one commit for completed iterations with repository changes. |
| `enforcePattern` | Reject iteration commits whose subjects do not follow the loop commit format. |
| `messageMaxLength` | Maximum length for the final `<TYPE>: <message>` subject. |

## `git.integration`

Modes:

| Mode | Behavior |
| --- | --- |
| `local_merge` | Squash merge the iteration branch into the base branch locally. |
| `pr` | Push the branch, create a pull request, wait for checks when configured, merge through `gh`, pull the base branch, and continue. |

Pull request mode uses `gh` commands. The CLI writes the generated title and body to files before invoking `gh`.

## Environment variables

| Variable | Behavior |
| --- | --- |
| `LOOP_AGENT` | Overrides `agent.default`. |
| `LOOP_CONFIG` | Overrides config path. |
| `LOOP_MAX_ITERATIONS` | Overrides run iteration limit; `0` means unlimited. |
| `LOOP_BASE_BRANCH` | Overrides base branch. |
| `LOOP_NO_COLOR` | Disables color output. |
