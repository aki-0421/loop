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

The built-in `loop` skill consolidates the planner, coding, and review role conventions. It tells agents how to:

- Read runtime context with `loop iteration read runtime`.
- Read the user instruction with `loop iteration read instruction`.
- Create important clarification Issues with `loop issue ask`.
- Report concrete repository or harness improvement proposals with `loop issue report`; do not persist unsupported agent capability findings as Issues.
- Planner role: write `task-tree` with `loop handoff write task-tree`.
- Coding role: edit only the assigned task worktree, write `task-result` with `loop handoff write task-result --task "$LOOP_TASK_ID"`, and run `loop task merge` until the task is merged.
- Review role: inspect the iteration diff and write `review-result` with `loop handoff write review-result`.
- Avoid direct Git lifecycle commands, `loop commit`, `loop branch`, `loop pr`, and `loop iteration close`; the CLI owns those mechanics in role-orchestrated runs.

The skill includes compact JSON shapes for `task-tree`, `task-result`, and `review-result`, and points agents to `loop help agent handoff write` for current command flags and schema details.

The planner role treats one iteration as one AI sprint-sized PR: a coherent development goal that autonomous agents can complete in hours. It splits that sprint into coding-agent work packets for dependency ordering, conflict avoidance, validation, and parallel execution, not for review convenience.

The default skill keeps role behavior concise. It does not expose cached memory lookup commands to agents; GitHub context caching remains a CLI-owned internal capability. Commit intent comes from planner task metadata; pull request titles, bodies, checks, and pull request merges are handled by the CLI.

## Pull request templates

The CLI owns template lookup, fallback text, PR creation, check waiting, and merge behavior. Skills must not embed template bodies or run `gh` directly.

## Skill sync

`loop skills sync` copies or links the configured `skills.sourceDir` into configured agent targets. Sync is explicit by default; `syncOnRun` is disabled in the generated config.

The generated prompt is only a compact bootstrap. Detailed pull request template and fallback text should stay in CLI code rather than being copied into the prompt or default skill.

## Skill updates

`loop init` installs the default skill through `npx skills add aki-0421/loop --skill loop --agent <agent> --yes`. `loop skills install loop` uses the same `npx skills` path. `loop skills install <path>` remains a manual command for installing local skills and never overwrites repository-customized skills unless `--force` is supplied.
