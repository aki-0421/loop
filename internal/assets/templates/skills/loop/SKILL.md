---
name: loop
description: Execute one loop coding iteration using CLI-managed context, memory, commits, validation, PR text, repair, and result JSON.
version: 1
---

# loop

You are executing one iteration inside the `loop` harness.

## Purpose

- Complete one reviewer-sized iteration from the current instruction.
- One iteration is one pull-request-sized change, not one project or milestone.
- Integration settings are provided by `loop iteration read runtime`, including `integration_mode` and `pull_request_mode`.
- Write human-readable output in English unless repository context explicitly requires otherwise.
- Do not ask the user questions or wait for manual user actions; make explicit assumptions or return `blocked` when no safe path exists.
- Use repository evidence before assumptions, and record assumptions in the `result` artifact.
- Stop after the chosen slice is complete, validated, committed, and documented.

## Artifact CLI and memory

Use the `loop` CLI binary as the source of truth for runtime artifacts, memory, commits, and result generation.
Do not construct or edit paths under the iteration directory directly for artifacts; this does not restrict normal repository file edits.
If a `loop` command fails, read the error, fix the arguments or state, and rerun it.

## Iteration map

Use this section as the working order and section map for one loop iteration:

1. Establish context. See [Getting oriented](#getting-oriented).
2. Choose exactly one implementation slice, then write `plan` and `todo` before any repository file edits. See [Implementation scope planning](#implementation-scope-planning) and [TODO planning](#todo-planning).
3. Implement only that slice by editing normal repository files.
4. Validate the relevant behavior. See [Validation](#validation).
5. Commit each completed TODO immediately with `loop commit` before moving to unrelated work. See [Commit contract](#commit-contract).
6. Stop after the chosen slice is complete; do not begin a follow-up slice.
7. Write `worklog` and `summary`. See [Handoff artifacts](#handoff-artifacts).
8. If the result will be `completed`, rename the branch with `loop branch rename`. See [Branch naming](#branch-naming).
9. If pull request mode is enabled, write PR artifacts, create the PR, wait for checks, repair CI failures in this same context, and merge through `loop pr`. See [PR artifacts and merge](#pr-artifacts-and-merge).
10. Before writing `result`, confirm commits are complete and `git status --short` shows no changed files; commit complete work with `loop commit` or revert incomplete work.
11. Write the final `result` artifact only after the PR is merged in pull request mode. See [Repair and result JSON](#repair-and-result-json).

## Getting oriented

Start each iteration by collecting operational context and recent-work context. Keep this phase shallow; detailed codebase exploration happens during planning.

1. Run `loop help agent`.
2. Run `pwd`.
3. Run `git status --short --branch`.
4. Run `loop iteration read runtime` to load integration mode, pull request mode, branch context, configured validation, and goal.
5. Run `loop iteration read instruction` to load the current task text.
6. Run `loop memory recent --limit 30` to review recent iteration summaries.
7. If recent memory points to older relevant context, run `loop memory search <query>`.
8. If commit history exists, run `git log --oneline -20`.
9. Inspect broad repository landmarks only when needed to identify planning entry points: root files, package directories, README or index files, and test or CI entry points.
10. Carry forward the planning inputs: current goal, branch and mode, validation settings, recent changes, unfinished or broken work, constraints, and assumptions.
11. If required operational context is missing or unsafe to interpret, return `blocked` with the missing evidence.

## Implementation scope planning

Plan after orientation and before any repository file edits. Use targeted codebase exploration to choose exactly one reviewer-sized slice.

```bash
loop help agent iteration plan
loop iteration plan template
```

Use the CLI-owned template when writing the `plan` artifact.

1. Start from the concrete entry point named or implied by the instruction or recent work, such as a failing test, route, endpoint, command, job, schema, migration, package, or configuration boundary.
2. Identify the highest-risk affected path: unknown behavior, integration boundary, data contract, migration, generated artifact, shared abstraction, or failing validation.
3. Inspect only the evidence needed to understand that path: relevant code, tests, docs or specs, schemas, fixtures, callers, consumers, runtime errors, and validation entry points.
4. For shared models, types, configuration, repository interfaces, adapters, or other cross-cutting contracts, verify the shape against existing usage from at least one caller and one consumer when available.
5. Prefer a narrow vertical slice through the riskiest path over completing one layer across packages.
6. Use a horizontal slice when the task itself is horizontal or the contract is already proven, such as renaming one API, updating one generated schema, fixing one shared utility, repairing one CI rule, or changing one established interface.
7. Let observed behavior and existing contracts shape new abstractions; introduce shared abstractions only with an immediate caller, consumer, or validation path.
8. Cross package boundaries when needed to prove the selected behavior, and keep each cross-package change tied to that behavior.
9. Keep the slice small enough to answer one reviewer question. If it grows, shrink to a characterization, repair, migration step, or proof slice that is independently reviewable.
10. Separate scaffolding, product behavior, refactors, tests, CI, and documentation unless one directly proves the other inside the selected slice.
11. Write the `plan` with the selected slice, why it was chosen, evidence used, expected files or subsystems, validation strategy, assumptions, and `Out of Scope`.
12. When the original instruction is broad, `Out of Scope` must briefly name deferred specs, features, layers, packages, tooling, or docs without turning them into a roadmap.

## TODO planning

After the plan has fixed the work target, decompose only that selected slice into TODOs.

```bash
loop help agent iteration todo
```

Planning gate: stay in planning until both the `plan` and `todo` artifacts have been written. Before that point, only inspect, read, and reason; do not edit repository files, run formatting/codegen that writes files, validate with mutating commands, or call `loop commit`.

## Validation

Validation is available when configured in the effective config or discoverable from repository conventions such as package scripts, Makefile targets, or CI config. Prefer configured validation first, then the narrowest relevant repository command. If validation was already failing before your change, report that baseline and do not repair unrelated historical failures.

Validation commands must terminate. Do not run a persistent development server as a foreground command. If browser or HTTP validation requires a server, start it in the background, capture its PID, run the check, and kill the server before continuing. Record the server lifecycle in `worklog`.

## Commit contract

Before the first commit in an iteration, inspect the commit help:

```bash
loop help agent commit
loop commit --type F add password reset flow
```

Use `loop commit` for commits. Do not run `git add` or `git commit` directly. If `loop commit` rejects the type or message, read the error, fix it immediately, and retry before continuing.

Commit immediately after a TODO is complete and sufficiently validated. Mark a TODO complete only after the matching commit exists, unless it required no repository change. Do not accumulate independent TODOs and commit them at the end.

## Handoff artifacts

Every completed iteration must leave enough structured context for the next iteration to continue without guessing.

`worklog` should include:

- selected slice,
- important commands,
- validation commands and outcomes,
- files or subsystems changed,
- decisions and assumptions,
- reverted or deferred work.

`summary` should include:

- what changed,
- commit SHA and subject,
- validation status,
- remaining follow-up slices,
- next recommended slice,
- whether the original goal is complete.

If follow-up slices remain, the iteration can still be `completed`, but `should_fully_stop` must be `false`.

## PR artifacts and merge

If pull request mode is enabled, get template text with `loop iteration read pr-template`. The CLI returns the repository template when present and CLI-owned fallback text otherwise.

- Follow the template text returned by the CLI; do not assume built-in sections.
- Write `pr-title` with `loop iteration write pr-title`.
- Write `pr-body` with `loop iteration write pr-body`.
- Write PR title and body content in English by default.
- Preserve visible template headings and checklist labels.
- Fill visible template sections with the chosen implementation slice, validation results, review notes, and intentionally deferred work.
- Keep the title concise and action-oriented.
- Describe only what this PR changes.
- Mention deferred work briefly when the original instruction is broader than the chosen slice.
- Run `loop pr create` after PR title and body are ready.
- Run `loop pr checks`. If it fails, read `loop iteration read pr-checks`, fetch needed logs with `loop pr logs <job-url-or-id>`, repair in the same branch, validate locally, commit with `loop commit`, update worklog and PR body when useful, then rerun `loop pr checks`.
- Run `loop pr merge` only after checks pass. It performs configured validation, performs a final check wait, merges through `gh`, and records the merged PR state.
- Do not write a `completed` result in pull request mode until `loop pr merge` has succeeded.

## Branch naming

For completed work, inspect the branch rename help first, then rename the iteration branch before writing the result:

```bash
loop help agent branch rename
loop branch rename feat/add-password-reset-tests
loop branch rename --kind fix handle-empty-search-query
```

Use one of the kinds shown by the help output. If the rename command prints a collision-adjusted branch, keep using that printed branch. Do not run direct Git branch switch, rename, push, PR, or merge commands; use `loop pr` for pull request operations.

Skip branch rename only for `no_change`, `blocked`, or `failed` results.

## Repair and result JSON

Repair narrowly without broadening the slice. Use `blocked` when no safe path exists.

For `completed`, the branch must already be renamed with `loop branch rename`; otherwise the result command will fail and tell you to rename it. `branch.final_name` is filled from tracked runtime context.

Generate the final result through the CLI:

```bash
loop iteration result --write --status completed --summary "..." --should-stop false --goal-evaluation "..." --validation-status passed
```

Add `--validation-command`, `--assumption`, `--blocked-reason`, `--error`, or branch override flags only when needed. The result command owns the JSON shape and returns actionable errors for mechanical contract problems; fix its feedback and rerun it.

Set `should_fully_stop` to `true` only when repository evidence shows the original instruction goal is complete. When the selected slice is complete but deferred slices remain, set `status` to `completed`, set `should_fully_stop` to `false`, and explain the next slice in `goal_evaluation`.
