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

The CLI exits non-zero for invalid config, invalid instruction file, invalid iteration close JSON, terminal agent failure, failed integration, or malformed merge-close state.

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
| `--base <branch>` | unset | Pin the base branch instead of using the branch at run start |

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
| `--base <branch>` | current branch at run start | Base branch for integration |
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
- The agent must close each iteration with exactly one of `loop iteration close --merge` or `loop iteration close --skip-merge`.
- A merge close is integrated before the next iteration starts.
- A merge close with `should_fully_stop=true` stops after integration.
- A skip-merge close never incorporates the iteration branch. It closes any unmerged PR, deletes iteration branches and worktrees, refreshes the target branch, and then continues or stops from `should_fully_stop`.
- In PR mode, `--merge` is accepted only after `loop pr merge` records a merged PR.
- A skip-merge close with `--sleep` and `--should-stop false` enters GitHub sleep mode after cleanup. Sleep mode waits for GitHub Issue, PR, or comment updates before launching the next iteration.

Example:

```bash
loop run docs/task.md \
  --goal "The authentication tests are implemented and the configured validation commands pass" \
  --pr
```

## `loop commit`

Create an iteration commit through the CLI. This is agent-facing and is shown by `loop help agent`, not by human-facing `loop help`. `loop help agent commit` is the compact source of truth for commit types, aliases, and message rules.

```bash
loop commit --type <type> <message>
```

`<type>` is one of `F`, `T`, `R`, `D`, `S`, `V`, or `C`, with common lowercase aliases such as `feature`, `test`, `docs`, or `config` accepted. `<message>` is the short imperative subject body without the prefix. The CLI creates the final subject as `<TYPE>: <message>`.

Rules:

- The CLI stages repository changes, excluding ignored loop runtime files.
- The message body must start with a lowercase English letter and must not end with a period.
- The final subject must be at most 72 characters.
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
- Last close action.
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
loop iteration plan <template|read|write> ...
loop iteration todo <list|insert|edit|complete> ...
loop iteration close (--merge|--skip-merge) [--iteration-dir <dir>|--run <run-id> --iteration <n>] --should-stop <bool> --goal-evaluation <text> [flags]
```

If `--iteration-dir` is omitted, commands resolve the current agent iteration automatically. If `--run` is supplied, the CLI resolves `.loop/runs/<run-id>/iterations/<n>` using the configured log directory; `--iteration latest` selects the newest iteration.

Writable artifacts are `plan`, `todo`, `pr-title`, and `pr-body`. Read-only artifacts include `runtime`, `instruction`, `prompt`, `effective-config`, `validation`, `events`, `stdout`, `stderr`, and `github-updates`. The terminal close is written through the master-DB result handoff, not a local artifact.
PR command artifacts `pr-state`, `pr-checks`, and `pr-check-log` are read-only to the agent and written by `loop pr`. `github-updates` is written by the CLI when new GitHub Issue, PR, or comment diffs are observed at an iteration boundary or sleep wake cycle.

`plan` and `todo` use dedicated namespaces instead of generic bulk writes. `loop iteration plan template` prints the CLI-owned plan template, `loop iteration plan write` stores the filled plan, and `loop iteration plan read` reads it. After the plan selects the review slice, `loop iteration todo list` prints numbered TODOs; `insert --type <type> <message>`, `edit <n> --type <type> <message>`, and `complete <n>` mutate one TODO item at a time by the 1-based index shown by `list`. TODO `type` and `message` use the same validation as `loop commit`. Generic `loop iteration write plan`, `append plan`, `write todo`, and `append todo` are rejected with guidance to these commands.

`pr-template` is a read-only artifact. `loop iteration read pr-template` prints the selected repository pull request template when present, otherwise it prints the CLI-owned fallback template. `loop iteration path pr-template` prints a path only when a repository template file exists.

`loop iteration close` builds and writes valid terminal JSON from CLI-owned runtime data and agent-supplied semantic fields. Exactly one of `--merge` or `--skip-merge` is required. `--merge` requires `--summary`, `--should-stop true|false`, and `--goal-evaluation`; `--skip-merge` requires `--reason`, `--should-stop true|false`, and `--goal-evaluation`. With `--skip-merge`, agents may add `--sleep` to wait for GitHub updates before the next iteration; `--sleep` requires `--should-stop false`. Agents may pass `--validation-status` or repeat `--validation-command name|command|exit_code|required`; branch metadata is always taken from loop runtime and the tracked Git branch. The command rejects malformed merge closes before handoff, including dirty worktrees, missing commits, unrenamed branches, failed or partial validation, and unmerged PRs in PR mode. Skip-merge closes do not require commits, branch rename, validation success, or success JSON.

## `loop branch`

Manage the current iteration branch through agent-facing CLI commands.

```bash
loop branch rename <kind>/<slug> [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop branch rename --kind <kind> <slug words...> [--iteration-dir <dir>|--run <run-id> --iteration <n>]
```

`loop branch rename` validates the branch kind against loop's fixed preset, slugifies the branch subject, applies the fixed iteration collision suffix when needed, renames the checked-out iteration branch, updates runtime context, and prints the actual branch name. Agents must use this command instead of direct Git branch switching or renaming. The command is rejected after integration has started.

## `loop pr`

Manage the current iteration pull request through agent-facing CLI commands.

```bash
loop pr create [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop pr checks [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop pr logs <job-url-or-id> [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop pr merge [--iteration-dir <dir>|--run <run-id> --iteration <n>]
```

`loop pr create` reads `pr-title` and `pr-body`, pushes the tracked branch when configured, creates or reuses the pull request, and writes `pr-state`. `loop pr checks` pushes current commits, waits for checks, writes `pr-checks`, and exits non-zero on failed checks with only a concise `errors.log` pointer. `loop pr logs` writes `pr-check-log`. `loop pr merge` runs configured validation, performs a final check wait, merges through `gh`, and records `pr-state.status=merged`.

## `loop issue`

Create GitHub clarification and improvement proposal Issues through agent-facing CLI commands.

```bash
loop issue ask --title <text> --body <text> [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop issue report --title <text> --body <text> [--kind <kind>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]
```

`loop issue ask` creates and applies the GitHub label `loop:question`, embeds loop run/iteration metadata in the Issue body, prints the Issue number and URL, and stores the Issue in the rebuildable GitHub context cache. Label creation failure is a hard command error.

Agents use this command only for important product, policy, or large specification ambiguity. After creating an Issue, the agent continues implementation that is unrelated to that clarification. The agent uses `loop iteration close --skip-merge --sleep` only when no safe mergeable work remains and GitHub updates are needed before continuing, and the reason should include the relevant Issue URL.

`loop issue report` creates and applies `loop:proposal` for concrete repository or harness improvement proposals. `--kind` is one of `tool`, `docs`, `guardrail`, `observability`, `environment`, `workflow`, or `other`. Issue bodies should be GitHub-flavored Markdown and include evidence, impact, and the proposed repository improvement. Unsupported agent capabilities are rediscovered each iteration and are not persisted as Issues.

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

Inspect cached GitHub PR, Issue, and comment context.

```bash
loop memory recent --limit <n> [--repo <owner/name>]
loop memory search <query> --limit <n> [--repo <owner/name>]
```

`recent` returns recent cached GitHub context records. `search` returns matching PR title/body, Issue title/body, and comment excerpts from the local `.loop/loop.db` cache without performing network access. Agents choose an explicit positive `--limit` for the current task; omitting it is an error. Output includes kind (`pr`, `issue`, `issue-comment`, or `pr-comment`), number, state, repository, title, URL, and excerpt. Memory refresh is automatic during `loop run` and after successful `loop pr merge`; there is no manual memory refresh command.

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
