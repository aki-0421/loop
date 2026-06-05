# Skills

## Directory format

Each skill is a directory containing `SKILL.md`.

```text
.agents/skills/
  loop/
    SKILL.md
```

The repository-published skill copy lives under:

```text
skills/
  loop/
    SKILL.md
```

This root `skills/<name>/SKILL.md` layout is compatible with external skill package CLIs that discover installable skills from repositories.

`SKILL.md` begins with YAML front matter:

```yaml
---
name: loop
description: Execute one role inside the loop CLI orchestrator and return a strict role handoff JSON.
version: 1
---
```

The body is Markdown. Built-in skills are English. Repository owners may customize them.

`loop` discovers existing project skill directories before choosing a source directory. The discovery order starts with the configured `skills.sourceDir`, then `.agents/skills/`, then known agent directories such as `.codex/skills/`, `.claude/skills/`, `.cline/skills/`, and `skills/`. For the default skill, `loop init` runs `npx skills add` and lets that CLI create the agent-specific skill directory. `loop init` must not directly copy the default `SKILL.md` from embedded templates, and it must not create `.loop/skills/`.

## Default skill

The built-in `loop` skill is a compact bootstrap, not the source of all role rules. It tells agents to:

- Read role-scoped operating instructions with `loop role instruction`.
- Read runtime context with `loop iteration read runtime`.
- Read the user instruction with `loop iteration read instruction`.
- Use loop-owned commands for lifecycle actions instead of raw Git or GitHub commands.
- Write strict role handoffs with `loop handoff write`.
- Use `loop help`, `loop help handoff write`, and focused command help for current schemas, flags, and command details.

Role-specific planner, coding, QA review, and merge instructions live behind the CLI-owned `loop role instruction` command. The command reads `LOOP_ROLE` and prints Markdown instructions for only the relevant role scope, avoiding skill context spent on full instructions for roles that are not currently executing.

Agent subprocesses receive `LOOP_AGENT_CONTEXT=1`, so `loop help` prints the agent-facing reference instead of the human command list. The agent-facing reference remains a command index and schema reference; `loop role instruction` is the role contract entry point.

The planner role treats one iteration as one AI sprint-sized PR: the largest coherent development goal suitable for an autonomous run while still producing an independently mergeable result. It splits that sprint into coding-agent work packets for dependency ordering, conflict avoidance, validation, and parallel execution. `loop role instruction` tells planners to prefer natural task boundaries, avoid layer-only splits, merge commit-sized microtasks into neighboring tasks, and keep documentation or validation with the behavior owner unless a cross-cutting hardening task adds distinct value.

The default skill keeps role behavior concise. It does not expose cached memory lookup commands to agents; GitHub context caching remains a CLI-owned internal capability. Planner tasks describe goals and acceptance criteria only. Commit intent comes from coding-agent task TODOs, QA review findings come from the review agent, and pull request titles, bodies, checks, and pull request merges are handled by the merge agent through loop commands.

## Pull request templates

The CLI owns template lookup and the underlying PR command implementation. `loop role instruction` instructs merge agents to read templates through `loop iteration read pr-template`, write `pr-title` and `pr-body`, use `loop pr` commands, and never run `gh` directly.

## Skill sync

`loop skills sync` copies or links the configured `skills.sourceDir` into configured agent targets. Sync is explicit by default; `syncOnRun` is disabled in the generated config.

The generated prompt is only a compact bootstrap. Detailed pull request template and fallback text should stay in CLI code rather than being copied into the prompt or default skill.

## Skill updates

`loop init` installs the default skill through `npx skills add aki-0421/loop --skill loop --agent <agent> --yes`. `loop skills install loop` uses the same `npx skills` path. `loop skills install <path>` remains a manual command for installing local skills and never overwrites repository-customized skills unless `--force` is supplied.
