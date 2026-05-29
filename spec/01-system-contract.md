# System Contract

## Purpose

`loop` wraps a coding agent CLI and gives it a repeatable harness:

1. Read a persistent Markdown instruction file.
2. Load recent GitHub PR, Issue, and comment context from the local `.loop/loop.db` cache.
3. Run the configured adapter as planner, coding, and review agents with compact role prompts.
4. Validate the planner task tree, schedule coding tasks by dependency and conflict metadata, and run coding agents in CLI-created worktrees.
5. Require coding agents to create task-local TODOs before editing, complete each TODO as one CLI-created task-branch commit, then complete `loop task merge` to squash-merge the task branch into the iteration branch.
6. Require review agents to rename the iteration branch, create PR text from the template, create/check/merge the pull request through `loop pr`, or perform local merge mode when configured.
7. Repeat until role output reports `goal_complete=true` for a CLI-provided goal, the iteration limit is reached, or a terminal error is recorded.

## Fully automated default

Runs are fully automated:

- `loop` never asks the user for confirmation during `run`.
- Agents must not ask the user questions directly.
- Important product, policy, or large blocking specification clarifications are asked through `loop issue ask`, which creates GitHub Issues in the repository.
- Concrete repository or harness improvement proposals are reported through `loop issue report`. Unsupported agent capability findings are rediscovered each iteration and are not persisted as Issues.
- Missing information is handled by making a local, explicit assumption and continuing.
- After creating a clarification Issue, agents continue unrelated work when possible. The review or planner handoff reports when external context prevents useful progress.
- GitHub sleep mode polls GitHub Issue/PR updates and starts the next iteration when new context appears. In an interactive terminal, any keypress triggers an immediate fetch. Sleep mode is an explicit skip-merge choice and does not reuse the goal-completion stop decision.
- Local merge, pull, cleanup, and run-state inspection are performed by the CLI according to configuration. In pull request mode, review agents drive PR creation, check waiting, check failure handling, and PR merge through loop commands. `--human-review` pauses after PR creation for external post-hoc review or merge updates.

## Iteration Scope

One iteration produces one AI sprint-sized pull request. The planner should choose a coherent development goal that autonomous agents can complete in hours, comparable to a traditional agile sprint compressed into an autonomous run. It should not split work only to fit a review session. Post-hoc review happens after the autonomous work and is not a scope constraint.

## Language default

English is the default for all machine-generated human-readable output. This includes:

- Plan files.
- TODO files.
- Work logs.
- Summaries.
- Commit messages.
- Branch slugs.
- Pull request titles and bodies.
- Skill instructions installed during `loop init`.

A repository may override output language through configuration or custom skills. Even when another output language is configured, JSON keys, status values, command names, and contract fields stay in English.

## Thin wrapper rule

`loop` is not a coding model. It orchestrates an external agent command.

The CLI owns:

- Process launch.
- Prompt assembly.
- Iteration directories.
- JSON validation.
- Git, commit, and pull request commands.
- Branch creation, task worktree creation, commit creation, PR command validation/tracking, and cleanup.
- Run state.
- Exit codes.
- GitHub PR, Issue, and comment context synchronization.

The agent owns:

- Planner task-tree content.
- Code edits for assigned coding tasks.
- Task-result and review-result handoffs.
- Review findings that can become repair tasks.
- Validation command selection when not configured by the repository.

## Skill-based customization

Repository skills define the repeatable behavior expected from the agent. `loop init` installs the default `loop` skill by running `npx --yes skills add aki-0421/loop --skill loop --agent <agent> --yes`; it does not copy a local template. Teams may edit, remove, or add skills. `loop` discovers existing project skill directories and avoids creating a duplicate `.loop/skills/` tree.

The CLI activates `loop` with a compact code-generated bootstrap. The user instruction file is task input only and is not responsible for activating mandatory loop behavior.

## Skill Bootstrap Injection

The skill bootstrap is implemented in Go under the prompt assembly package. It is not stored as a user-editable instruction template and is not created by `loop init`.

The CLI assembles each agent request in this order:

1. Non-user-editable instruction to use the `loop` skill.
2. Instruction to use `loop iteration`, `loop issue`, and `loop handoff` commands for runtime context, GitHub Issues, and role handoffs.

When an agent adapter supports a system or developer message channel, the bootstrap can be sent through that channel. When an adapter only supports a single prompt stream, the bootstrap is passed as the prompt. The implementation must avoid duplicating the full skill contract in the prompt.

## Determinism boundary

`loop` must record enough state to explain what happened during each iteration:

- Effective configuration.
- Prompt file.
- Agent command line.
- Agent event stream.
- Git branch names.
- Commits produced by the CLI from completed task TODOs.
- Validation commands and outputs.
- Role handoff JSON.
- Integration action.
- Cleanup action.
