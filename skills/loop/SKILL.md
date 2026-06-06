---
name: loop
description: Execute one role inside the loop CLI orchestrator. Use when the CLI asks you to act as planner, coding, QA review, or merge agent and return strict handoff JSON through loop-owned commands.
version: 1
---

# loop

You are running inside the `loop` harness. The CLI owns worktrees, validation, cleanup, iteration state, task commits, branch lifecycle, pull requests, and role handoff storage.

Perform the role named in `LOOP_ROLE`, use loop-owned commands for role-owned lifecycle actions, write the required handoff, and exit.

Start by reading the role-scoped CLI instructions:

```bash
loop role instruction
```

Then read the current runtime context and user instruction:

```bash
loop iteration read runtime
loop iteration read instruction
```

Use role-scoped lifecycle commands: `loop task`, `loop branch`, `loop pr`, `loop issue`, and `loop handoff`. Role-orchestrated runs finish through role-specific handoffs plus the applicable task merge or PR commands.

Use `loop issue ask` only for important blocking product or policy questions; continue independent work when possible.

Write the required strict role handoff with `loop handoff write`.

Use `loop help`, `loop help issue`, `loop help task todo`, `loop help pr checks`, and `loop help handoff write` for command help.

Read named artifacts by passing the artifact name directly. Examples: `loop iteration read validation`, `loop iteration read events`, and `loop iteration path events`.

When adding task TODOs, commit TODOs need one quoted final commit-message argument after the flags. no_commit TODO syntax ends after the acceptance flags:

```bash
loop task todo add --work-type commit --type F --title "Add publish review route" --acceptance "The route renders the review workflow and focused coverage passes." "add publish review route"
loop task todo add --work-type no_commit --title "Run focused validation" --acceptance "The focused validation command passes."
```
