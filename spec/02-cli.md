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
| `--skills` | `true` | Install the default skill through `npx skills` |
| `--sync-agent-skills` | `false` | Explicitly sync skills into configured agent targets |
| `--base <branch>` | unset | Pin the base branch instead of using the branch at run start |

Default generated tree for the built-in Codex adapter:

```text
.loop/
  config.yaml
  .gitignore
skills-lock.json
.agents/skills/
  loop/SKILL.md
```

With `--skills`, `loop init` runs:

```bash
npx --yes skills add aki-0421/loop --skill loop --agent <agent> --yes
```

`loop` maps built-in adapter names to the agent names expected by the skills CLI, such as `codex` to `codex` and `claude` to `claude-code`. If an existing discovered `loop` skill is present and `--force` is not set, `loop init` skips the `npx skills` invocation to avoid overwriting repository-customized skill content. `loop init` must not directly copy `SKILL.md` from embedded templates, and it must not create `.loop/skills/` or `.loop/runs/`.

Agent-specific skill directories may differ from the Codex default; for example, `--agent claude` installs through the skills CLI's `claude-code` target and records `.claude/skills` as `skills.sourceDir`.

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
| `--human-review` | config value | Compatibility shortcut for `--review-mode serial_human_review` |
| `--review-mode <mode>` | config value | PR review policy: `auto_merge`, `parallel_human_review`, or `serial_human_review` |
| `--base <branch>` | current branch at run start | Base branch for integration |
| `--resume <run-id>` | empty | Accepted for older scripts; use `loop resume <run-id>` |
| `--from-iteration <n>` | latest | Accepted for older scripts with `--resume`; currently ignored |
| `--keep-branches <mode>` | empty | Accepted for older scripts; cleanup is automatic |
| `--keep-worktrees <mode>` | empty | Accepted for older scripts; cleanup is automatic |

Runtime rules:

- `<instruction.md>` is required and must be supplied by the user.
- `loop init` does not create an instruction template.
- The instruction file is read at every iteration and copied to that iteration's `prompt.md`; edits to the source file affect later iterations only.
- The instruction file is task input only. Mandatory harness behavior is injected by CLI code, not by user-authored prompt text.
- If `--goal` is empty, `--should-stop true` is invalid regardless of instruction, Issue, PR, or comment text.
- The planner agent writes an AI sprint-level `task-tree` handoff for one coherent PR-sized development goal.
- The CLI schedules non-conflicting ready tasks and runs coding agents in task worktrees.
- Coding agents create task-local TODOs before editing, complete each TODO as one CLI-created task-branch commit, write `task-result` handoffs, run `loop task merge`, and resolve conflicts before exiting.
- The CLI runs validation, then the review agent writes a `review-result` handoff.
- Validation failures and review findings become repair tasks until approval or the configured fix-cycle limit.
- In PR mode with `reviewMode=auto_merge`, review agents rename the branch, create PR title/body artifacts from the template, create/check/merge the PR through loop commands, and approve only after `pr-state.status=merged`.
- With `reviewMode=parallel_human_review`, review agents create the PR and run checks, then approve only after `pr-state.status=waiting_for_human`; the CLI records the PR, branch, title, and changed files as pending review context, renders pending PR numbers and titles while continuing other work, cleans local worktrees and branches while preserving the remote PR branch, and starts the next iteration from the base branch. At iteration boundaries, the CLI removes merged or closed pending PRs before planning. Planners inspect remaining PR feedback with `loop pr feedback <pr>` and can return `repair_pull_request` with tasks so the CLI checks out that PR branch after planning. If no safe non-overlapping work remains, planners can return `wait_for_pending_prs=true` with an empty task tree so the CLI enters PR review wait mode.
- With `reviewMode=serial_human_review`, review agents create the PR and run checks, then approve only after `pr-state.status=waiting_for_human`; the CLI shows a PR review wait screen and polls every 5 minutes for an external human merge before cleanup or the next iteration. `--human-review` selects this mode.
- `goal_complete=true` can stop the run only after successful integration and only when `--goal` is non-empty.

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

Show the stored state for a run through the resume entry point.

```bash
loop resume <run-id> [flags]
```

Flags:

| Flag | Default | Behavior |
| --- | --- | --- |
| `--from-iteration <n>` | latest incomplete | Accepted for older scripts; currently ignored |

`loop resume` currently loads `.loop/runs/<run-id>/run-state.json` and prints the same run summary as `loop status <run-id>`. It does not relaunch agent, validation, or integration phases.

## `loop status`

Show the current or selected run.

```bash
loop status [run-id] [flags]
```

Output includes:

- Run id.
- Base branch.
- Current iteration.
- Current stage.
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
| `--follow` | false | Accepted for older scripts; currently ignored |
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

Writable artifacts are `plan`, `todo`, `task-tree`, `review-result`, `pr-title`, and `pr-body`. Read-only artifacts include `runtime`, `instruction`, `prompt`, `effective-config`, `validation`, `events`, `errors`, `github-updates`, `pr-state`, `pr-checks`, and `pr-check-log`. Role-orchestrated runs use planner, task, and review handoffs instead of terminal iteration close JSON.
PR command artifacts `pr-state`, `pr-checks`, and `pr-check-log` are read-only to the agent and written by `loop pr`. `github-updates` is written by the CLI when new GitHub Issue, PR, or comment diffs are observed at an iteration boundary or sleep wake cycle.

`plan` and `todo` use dedicated namespaces instead of generic bulk writes. `loop iteration plan template` prints the CLI-owned plan template, `loop iteration plan write` stores the filled plan, and `loop iteration plan read` reads it. After the plan selects the implementation scope, `loop iteration todo list` prints numbered TODOs; `insert --type <type> <message>`, `edit <n> --type <type> <message>`, and `complete <n>` mutate one TODO item at a time by the 1-based index shown by `list`. TODO `type` and `message` use the same validation as `loop commit`. Generic `loop iteration write plan`, `append plan`, `write todo`, and `append todo` are rejected with guidance to these commands.

`pr-template` is a read-only artifact. `loop iteration read pr-template` prints the selected repository pull request template when present, otherwise it prints the CLI-owned fallback template. `loop iteration path pr-template` prints a path only when a repository template file exists.

`loop iteration close` builds and writes valid terminal JSON from CLI-owned runtime data and agent-supplied semantic fields. Exactly one of `--merge` or `--skip-merge` is required. `--merge` requires `--summary`, `--should-stop true|false`, and `--goal-evaluation`; `--skip-merge` requires `--reason`, `--should-stop true|false`, and `--goal-evaluation`. `--should-stop true` is valid only when the run was started with a non-empty CLI `--goal`. With `--skip-merge`, agents may add `--sleep` to wait for GitHub updates before the next iteration; `--sleep` requires `--should-stop false`. Agents may pass `--validation-status` or repeat `--validation-command name|command|exit_code|required`; branch metadata is always taken from loop runtime and the tracked Git branch. The command rejects malformed merge closes before handoff, including dirty worktrees, missing commits, unrenamed branches, failed or partial validation, and unmerged PRs in PR mode. Skip-merge closes do not require commits, branch rename, validation success, or success JSON.

## `loop handoff`

Write and inspect role handoff JSON for role-orchestrated runs.

```bash
loop handoff write <task-tree|task-result|review-result> [--task <id>] [--file <path>|--value <json>]
loop handoff read <task-tree|task-result|review-result> [--task <id>]
loop handoff list [--kind <task-tree|task-result|review-result>]
```

`task-tree` is written by the planner. `task-result` is written by coding agents and requires `--task`. `review-result` is written by the review agent. The CLI validates each JSON payload and rejects unknown fields before storing the handoff in `.loop/loop.db` and writing durable audit copies in the iteration directory.

## `loop task`

Complete coding-task integration actions through agent-facing CLI commands.

```bash
loop task todo add --type <type> --title <title> --acceptance <text>... [--after <n>] <commit-message>
loop task todo list [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--task <id>]
loop task todo move <n> --after <n> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--task <id>]
loop task todo start <n> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--task <id>]
loop task todo complete <n> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--task <id>]
loop task discard --reason <reason> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--task <id>]
loop task merge --type <type> <summary> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--task <id>]
loop task merge --continue [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--task <id>]
```

Coding agents must create the task-local TODO list before implementation. `loop task todo add` accepts `--after <n>` for placement, with `--after 0` inserting at the top. `loop task todo move <n> --after <n>` can reorder pending TODOs before task work starts. `loop task todo add` and `move` are rejected after task work starts. `loop task todo start` enforces serial ordering. `loop task todo complete` stages current task worktree changes, creates the loop-formatted task-branch commit, records its SHA and subject in `tasks/<n>/task-todo.json`, and marks that TODO done. It rejects clean completions.

`loop task discard` writes a discarded `task-result` with a required reason so the planner can revise or rewrite the remaining plan.

`loop task merge` is used by coding agents after all task TODOs are complete and a completed `task-result` handoff exists. It waits for exclusive access to the iteration branch, squash-merges the task branch into the iteration branch with the supplied summary, writes `tasks/<n>/task-merge.json`, and exits non-zero if conflicts require agent resolution.

When conflicts occur, the command leaves the iteration worktree in the conflicted state and prints that path. The coding agent resolves conflicts there, then runs `loop task merge --continue` to stage the resolution, commit the squash merge, write the merge audit, and release the merge lock.

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
loop pr feedback <pr> [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop pr logs <job-url-or-id> [--iteration-dir <dir>|--run <run-id> --iteration <n>]
loop pr merge [--iteration-dir <dir>|--run <run-id> --iteration <n>]
```

`loop pr create` reads `pr-title` and `pr-body`, pushes the tracked branch when configured, creates or reuses the pull request, and writes `pr-state`. `loop pr checks` pushes current commits, waits for checks, writes `pr-checks`, and exits non-zero on failed checks with only a concise `errors.log` pointer. `loop pr feedback` fetches review decisions, latest reviews, comments, and updated time for planner decisions. `loop pr logs` writes `pr-check-log`. `loop pr merge` runs configured validation, performs a final check wait, merges through `gh`, and records `pr-state.status=merged`.

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
loop skills install <loop|path> [--force]
loop skills sync
loop skills doctor
```

Rules:

- Repository skills live in discovered agent skill directories. The default new directory is `.agents/skills/`.
- `install loop` delegates to `npx skills add aki-0421/loop --skill loop --agent <agent> --yes`; local paths are copied into `skills.sourceDir`.
- `sync` copies or symlinks skills into configured agent-specific paths only when explicitly requested.
- `doctor` validates `SKILL.md` front matter and required files.

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
