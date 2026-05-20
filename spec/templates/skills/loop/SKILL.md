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
2. Choose exactly one reviewer-sized slice, then write `plan` and `todo` before any repository file edits. See [Review scope and planning](#review-scope-and-planning).
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

At the start of every iteration, establish context first:

1. List agent commands with `loop help agent`.
2. Run `pwd`.
3. Run `git status --short --branch`.
4. Run `loop iteration read runtime` to load integration mode, pull request mode, branch context, and goal.
5. Run `loop iteration read instruction` to load the current task text.
6. Run `loop memory recent --limit 30` to review recent iteration summaries.
7. If recent memory suggests relevant older context, search it with `loop memory search <query>`.
8. Inspect recent commits with `git log --oneline -20` when history exists.
9. Inspect the repository structure narrowly.
10. For large specs or docs, read the index, README, overview, or table of contents first.
11. Read only the spec files needed to understand the instruction and immediate context.

Capture unfinished or broken work found during orientation as planning input.

## Review scope and planning

Keep the iteration small enough that a reviewer can understand the intent, design, risk, and validation without reconstructing the whole project.

Use these PR-slicing heuristics:

- One PR should answer one reviewer question.
- Prefer small batches; small reviews move faster, get deeper review, merge easier, and roll back easier.
- Choose vertical only when it is tiny; otherwise split by boundary first, then UI, then polish.
- Keep the branch short-lived and avoid accumulating unrelated decisions.
- Separate scaffolding, product behavior, refactors, tests, CI, and documentation unless one directly proves the other.
- A review slice should be independently useful, or explicitly prepare the next independently useful slice.
- If the diff is becoming hard to summarize in one sentence, shrink the slice.
- If validation requires unrelated systems, shrink to the nearest testable boundary.
- If dependent work is needed, stack it as follow-up slices instead of merging it into this PR.
- Prefer boring, reversible changes over impressive completeness.

Write the `plan` and `todo` artifacts before code edits; they are runtime memory only and must not be committed.

Planning gate: stay in planning until both artifacts have been written. Before that point, only inspect, read, and reason; do not edit repository files, run formatting/codegen that writes files, validate with mutating commands, or call `loop commit`.

Use orientation findings when selecting the review slice. If memory, git history, or progress artifacts identify unfinished or broken work, prefer finishing or repairing that slice before starting new behavior.

Inspect the CLI-owned planning help and template before writing artifacts:

```bash
loop help agent iteration plan
loop help agent iteration todo
loop iteration plan template
```

Use the plan template to choose exactly one selected review slice first. After the plan has fixed the work target, use the TODO workflow described by the help output to decompose that slice into commit-sized tasks. TODOs must contain only the current slice, and each TODO uses the same type and message shape as `loop commit`, for example `loop iteration todo insert F add password reset flow`.

`Out of Scope` is required when the instruction is broad. It should briefly name deferred specs, features, tooling, or docs without expanding them into a roadmap.

## Validation

Validation is available when configured in the effective config or discoverable from repository conventions such as package scripts, Makefile targets, or CI config. Prefer configured validation first, then the narrowest relevant repository command. If validation was already failing before your change, report that baseline and do not repair unrelated historical failures.

Validation commands must terminate. Do not run a persistent development server as a foreground command. If browser or HTTP validation requires a server, start it in the background, capture its PID, run the check, and kill the server before continuing. Record the server lifecycle in `worklog`.

## Commit contract

Before the first commit in an iteration, inspect the commit help:

```bash
loop help agent commit
loop commit F add password reset flow
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
- Fill visible template sections with the chosen review slice, validation results, review notes, and intentionally deferred work.
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
