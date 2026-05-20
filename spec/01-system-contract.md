# System Contract

## Purpose

`loop` wraps a coding agent CLI and gives it a repeatable harness:

1. Read a persistent Markdown instruction file.
2. Load recent GitHub PR, Issue, and comment context from the local `.loop/loop.db` cache.
3. Run the configured agent with a compact code-generated skill bootstrap.
4. Let the agent plan, edit, validate, and request commits for complete work.
5. Read the agent result JSON.
6. Repair incomplete work when possible.
7. Integrate the iteration through local squash merge, or verify that the agent merged the pull request through `loop pr`.
8. Repeat until the agent reports `should_fully_stop=true`, the iteration limit is reached, or a terminal error is recorded.

## Fully automated default

Runs are fully automated:

- `loop` never asks the user for confirmation during `run`.
- The agent must not ask the user questions directly.
- Important product, policy, or large blocking specification clarifications are asked through `loop issue ask`, which creates GitHub Issues in the repository.
- Missing information is handled by making a local, explicit assumption and continuing.
- After creating a clarification Issue, the agent continues unrelated work. If no safe independent work remains, the agent records a blocked result in JSON and references the blocking Issue URL.
- A blocked result that references an open `loop:blocking` Issue puts the current run process into in-memory sleep mode. Sleep mode polls GitHub Issue/PR updates and relaunches the agent in the same iteration when new context appears.
- Local merge, pull, cleanup, repair, and resume operations are performed by the CLI according to configuration. In pull request mode, the agent performs PR creation, check waiting, CI repair, and PR merge through `loop pr` commands.

## Language default

English is the default for all machine-generated human-readable output. This includes:

- Plan files.
- TODO files.
- Work logs.
- Summaries.
- Commit messages.
- Branch slugs.
- Pull request titles and bodies.
- Skill instructions installed by `loop init`.

A repository may override output language through configuration or custom skills. Even when another output language is configured, JSON keys, status values, command names, and schema fields stay in English.

## Thin wrapper rule

`loop` is not a coding model. It orchestrates an external agent command.

The CLI owns:

- Process launch.
- Prompt assembly.
- Iteration directories.
- JSON validation.
- Git, commit, and pull request commands.
- Branch creation, branch rename command validation/tracking, PR command validation/tracking, and cleanup.
- Repair retries before PR creation. Pull request check repair happens inside the agent context through `loop pr checks`, local edits, commits, and `loop pr merge`.
- Resume state.
- Exit codes.
- GitHub PR, Issue, and comment context synchronization.

The agent owns:

- Code edits.
- Plan content.
- TODO execution.
- Deciding when a TODO-sized unit is ready to commit.
- Validation command selection when not configured.
- Result JSON creation.
- Branch rename requests through `loop branch rename`.
- Pull request text generation.

## Skill-based customization

Repository skills define the repeatable behavior expected from the agent. `loop init` installs the default `loop` skill, but teams may edit, remove, or add skills. `loop` discovers existing project skill directories, uses `.agents/skills/` for new repositories by default, and avoids creating a duplicate `.loop/skills/` tree.

The CLI activates `loop` with a compact code-generated bootstrap. The user instruction file is task input only and is not responsible for activating mandatory loop behavior.

## Skill Bootstrap Injection

The skill bootstrap is implemented in Go under the prompt assembly package. It is not stored as a user-editable instruction template and is not created by `loop init`.

The CLI assembles each agent request in this order:

1. Non-user-editable instruction to use the `loop` skill.
2. Instruction to use `loop iteration`, `loop memory`, `loop issue`, and `loop commit` commands for runtime context, artifact reads or writes, GitHub clarification Issues, and commits.

When an agent adapter supports a system or developer message channel, the bootstrap can be sent through that channel. When an adapter only supports a single prompt stream, the bootstrap is passed as the prompt. The implementation must avoid duplicating the full skill contract in the prompt.

## Determinism boundary

`loop` must record enough state to explain what happened during each iteration:

- Effective configuration.
- Prompt file.
- Agent command line.
- Agent event stream.
- Git branch names.
- Commits produced by the agent.
- Validation commands and outputs.
- Result JSON.
- Integration action.
- Cleanup action.
