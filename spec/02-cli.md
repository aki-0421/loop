# CLI Specification

## Global behavior

```bash
loop <command> [flags]
```

Global flags:

| Flag | Default | Behavior |
| --- | --- | --- |
| `--config <path>` | `.loop/config.yaml` | Repository config path |
| `--agent <name>` | `codex` | Agent adapter to use |
| `--cwd <path>` | current directory | Repository root or subdirectory |
| `--log-level <level>` | `info` | `debug`, `info`, `warn`, `error` |
| `--json` | `false` | Print command result as JSON |
| `--no-color` | auto | Disable terminal color |
| `--help`, `-h` | n/a | Alias for `loop help` |

The CLI exits non-zero for invalid config, invalid instruction file, invalid result JSON, terminal agent failure, unrepaired dirty state, failed integration, or blocked work.

## `loop help`

Show the CLI-owned command reference.

```bash
loop help
loop help <command...>
loop help agent
loop help agent <command...>
```

`loop help` lists human-facing commands with summaries. `loop help <command...>` prints human-facing usage, description, flags, and subcommands. It does not list agent-only commands such as `loop commit` or `loop iteration`.

`loop help agent` is the compact agent-facing reference. It uses dense line-oriented text and includes agent-only commands plus command-owned reference data such as iteration artifact names. Agents should treat `loop help agent` and `loop help agent <command...>` as the source of truth instead of embedding long command lists. `loop --help` and `loop -h` are aliases for human-facing help.

## `loop init`

Create repository-local loop files.

```bash
loop init [flags]
```

Flags:

| Flag | Default | Behavior |
| --- | --- | --- |
| `--force` | `false` | Overwrite generated files that already exist |
| `--agent <name>` | `codex` | Set the default agent in generated config |
| `--skills` | `true` | Install the default skill into a discovered project skill directory |
| `--sync-agent-skills` | `false` | Explicitly sync skills into configured agent targets |
| `--base <branch>` | auto | Set base branch; if omitted, detect `develop`, then `main`, then current branch |

Generated tree:

```text
.loop/
  config.yaml
  .gitignore
.agents/skills/
  loop/SKILL.md
```

If `.agents/skills`, `.codex/skills`, `.claude/skills`, or another known agent skill directory already exists, `loop init` installs into the first discovered directory instead of creating a duplicate skill tree. `loop init` must not create `.loop/skills/` or `.loop/runs/`.

Generated `.loop/config.yaml` contains only repository-specific overrides. Built-in defaults supply normal agent, run, git, validation, memory, and log settings.

## `loop run`

Run the automated iteration loop.

```bash
loop run <instruction.md> [flags]
```

Flags:

| Flag | Default | Behavior |
| --- | --- | --- |
| `--goal <text>` | empty | Natural-language stop condition stored in runtime context |
| `--max-iterations <n>` | config value | Stop after `n` iterations; `0` means unlimited |
| `--pr` | config value | Use pull request integration instead of local squash merge |
| `--base <branch>` | config value | Base branch for integration |
| `--worktree` | config value | Run each iteration in a Git worktree |
| `--resume <run-id>` | empty | Resume an existing run |
| `--from-iteration <n>` | latest | Resume from a specific iteration |
| `--keep-branches <mode>` | config value | `always`, `failed`, `never` |
| `--keep-worktrees <mode>` | config value | `always`, `failed`, `never` |
| `--dry-run` | false | Build prompt and state files without launching the agent |

Runtime rules:

- `<instruction.md>` is required and must be supplied by the user.
- `loop init` does not create an instruction template.
- The instruction file is read at every iteration and copied to that iteration's `prompt.md`; edits to the source file affect later iterations only.
- The instruction file is task input only. Mandatory harness behavior is injected by CLI code, not by user-authored prompt text.
- If `--goal` is empty, the agent may still set `should_fully_stop=true` when no more useful work remains.
- The agent must write one `result` artifact per iteration.
- A completed iteration with changes is integrated before the next iteration starts.
- A completed iteration with `should_fully_stop=true` stops after integration.
- A no-change iteration with `should_fully_stop=true` stops without integration.

Example:

```bash
loop run docs/task.md \
  --goal "The authentication tests are implemented and the configured validation commands pass" \
  --pr
```

## `loop commit`

Create an iteration commit through the CLI. This is agent-facing and is shown by `loop help agent`, not by human-facing `loop help`.

```bash
loop commit <type> <message>
```

`<type>` is one of `F`, `T`, `R`, `D`, `S`, `V`, or `C`, with common lowercase aliases such as `feature`, `test`, `docs`, or `config` accepted. `<message>` is the short imperative subject body without the prefix. The CLI creates the final subject as `<TYPE>: <message>`.

Rules:

- The CLI stages repository changes, excluding ignored loop runtime files.
- The message body must start with a lowercase English letter and must not end with a period.
- The final subject must respect `git.commits.messageMaxLength`.
- Validation failures exit non-zero before any commit is created.
- On success, text output prints the short SHA and subject; JSON output prints `sha` and `message`.

## `loop resume`

Resume a stored run.

```bash
loop resume <run-id> [flags]
```

Flags:

| Flag | Default | Behavior |
| --- | --- | --- |
| `--from-iteration <n>` | latest incomplete | Resume from iteration `n` |
| `--repair` | true | Attempt repair before relaunching the iteration |

Resume loads `.loop/runs/<run-id>/run-state.json`, validates the current Git state, and continues from the recorded stage.

## `loop status`

Show the current or selected run.

```bash
loop status [run-id] [flags]
```

Output includes:

- Run id.
- Instruction path.
- Base branch.
- Current iteration.
- Current stage.
- Iteration branch.
- Last result status.
- Last summary sentence.
- Next action.
- Log directory.

## `loop logs`

Inspect run logs.

```bash
loop logs <run-id> [flags]
```

Flags:

| Flag | Default | Behavior |
| --- | --- | --- |
| `--iteration <n>` | latest | Select an iteration |
| `--follow` | false | Follow agent event stream |
| `--file <name>` | empty | Print a specific file from the iteration directory |

## `loop iteration`

Read and write named iteration artifacts through the CLI instead of requiring agents to construct file paths. These commands are agent-facing and are shown by `loop help agent`, not by human-facing `loop help`.

```bash
loop iteration path <artifact> [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop iteration read <artifact> [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop iteration write <artifact> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--file <path>|--value <text>]
loop iteration append <artifact> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--file <path>|--value <text>]
loop iteration result [--iteration-dir <dir>|--run <run-id> --iteration <n>] --summary <text> --should-stop <bool> --goal-evaluation <text> [flags]
```

If `--iteration-dir` is omitted, commands resolve the current agent iteration automatically. If `--run` is supplied, the CLI resolves `.loop/runs/<run-id>/iterations/<n>` using the configured log directory; `--iteration latest` selects the newest iteration.

Writable artifacts are `plan`, `todo`, `worklog`, `summary`, `result`, `pr-title`, and `pr-body`. Read-only artifacts include `runtime`, `instruction`, `prompt`, `effective-config`, `validation`, `events`, `stdout`, and `stderr`.

`pr-template` is a read-only artifact. `loop iteration read pr-template` prints the selected repository pull request template when present, otherwise it prints the CLI-owned fallback template. `loop iteration path pr-template` prints a path only when a repository template file exists.

`loop iteration result` builds valid iteration result JSON from CLI-owned runtime data and agent-supplied semantic fields. It prints JSON by default, or writes the `result` artifact with `--write`. The command infers `schema_version`, `branch.initial_name`, commits from `base_branch..HEAD`, logical artifact names, branch kind, and branch slug. Required semantic flags are `--summary`, `--should-stop true|false`, and `--goal-evaluation`. Agents pass `--validation-status` or repeat `--validation-command name|command|exit_code|required`; they override branch inference only with `--branch-kind`, `--branch-slug`, or `--branch-final`. The command rejects mechanical contract problems such as dirty completed work, missing commits for `completed`, branch mismatches, and missing blocked/failed reasons.

## `loop skills`

Manage repository skills.

```bash
loop skills list
loop skills install <name-or-path> [--force]
loop skills sync
loop skills doctor
```

Rules:

- Repository skills live in discovered agent skill directories. The default new directory is `.agents/skills/`.
- `sync` copies or symlinks skills into configured agent-specific paths only when explicitly requested.
- `doctor` validates `SKILL.md` front matter and required files.

## `loop memory`

Inspect and search iteration memory.

```bash
loop memory recent [--run <run-id>] [--limit 30]
loop memory search <query> [--run <run-id>] [--iteration <id>] [--artifact <name>] [--limit <n>]
loop memory compact [--run <run-id>]
```

`recent` returns the latest iteration summaries. `search` returns matching SQLite artifact excerpts. `compact` rebuilds the global SQLite search index from iteration databases.

## `loop doctor`

Check the environment.

```bash
loop doctor
```

Checks:

- Go binary can run.
- Git repository exists.
- Base branch exists.
- Config validates.
- Default agent command exists.
- Skill target is writable when skill sync is enabled.
- `gh` is available when pull request mode is enabled.
- Remote branch is configured when push or PR integration is enabled.
- Validation commands are executable.
