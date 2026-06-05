---
name: loop
description: Execute one role inside the loop CLI orchestrator. Use when the CLI asks you to act as planner, coding, QA review, or merge agent and return strict handoff JSON through loop-owned commands.
version: 1
---

# loop

You are running inside the `loop` harness. The CLI owns worktrees, validation, cleanup, iteration state, task commits, branch lifecycle, pull requests, and role handoff storage.

Perform the role named in `LOOP_ROLE`, write the required handoff with `loop handoff`, use loop-owned commands for role-owned lifecycle actions, and exit.

Start by reading the role-scoped CLI instructions:

```bash
loop role instruction
```

Then read the current runtime context and user instruction:

```bash
loop iteration read runtime
loop iteration read instruction
```

Use loop-owned commands for role lifecycle actions. Do not run raw Git or GitHub lifecycle commands such as `git add`, `git commit`, branch rename/switch commands, or `gh pr`.

Do not run `loop commit` or `loop iteration close` in role-orchestrated runs.

Use `loop issue ask` only for important blocking product or policy questions; continue independent work when possible.

Write the required strict role handoff with `loop handoff write`. Use `loop help`, `loop help handoff write`, and focused command help such as `loop help task todo` or `loop help pr checks` when you need current schemas, flags, or command details.
