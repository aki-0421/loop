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
8. If pull request mode is enabled, write PR artifacts. See [PR artifacts](#pr-artifacts).
9. Before writing `result`, confirm commits are complete and `git status --short` shows no changed files; commit complete work with `loop commit` or revert incomplete work.
10. Write the final `result` artifact. See [Repair and result JSON](#repair-and-result-json).

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

Write `plan` before code edits; it is runtime memory only and must not be committed.

Planning gate: stay in planning until both `plan` and `todo` artifacts have been written. Before that point, only inspect, read, and reason; do not edit repository files, run formatting/codegen that writes files, validate with mutating commands, or call `loop commit`.

Use orientation findings when selecting the review slice. If memory, git history, or progress artifacts identify unfinished or broken work, prefer finishing or repairing that slice before starting new behavior.

Plan sections:

- Purpose
- Review Slice
- Change Set
- TODO
- Verification
- Out of Scope
- Follow-up Slices
- Risks and Notes, when relevant

`Review Slice` must name exactly one selected slice.

Before writing TODOs, draft the commit shape for the selected slice. Use the commit prefix taxonomy above as the partitioning checklist: decide which kinds of work are truly required, and split work whenever it needs a different prefix or a different clear commit subject.

`TODO` must contain only the current slice. Each checkbox must represent exactly one future `loop commit` invocation, except no-change confirmations. Write each checkbox with the intended commit type and short imperative subject, for example `- [ ] D: update loop planning guidance`. If implementation proves the type or subject wrong, update the TODO before committing.

Prefer one TODO and one commit for the chosen slice when possible. Use additional TODOs and commits only when the selected slice contains distinct commit-ready units, such as tightly coupled tests, docs, or configuration needed to prove the same slice.

Even for a tiny or no-change slice, write a `todo` artifact before leaving planning. Use one checkbox that names the confirmation or implementation unit.

Split TODOs by commit boundary, not by command sequence. A TODO is too broad if it needs more than one clear commit subject, if its subject would naturally contain "and", or if part of it can be reviewed, validated, reverted, or explained independently. Different prefixes are different TODOs unless one change directly proves the other and they must be reviewed and reverted together. When in doubt, split the TODOs and keep dependencies ordered.

`Out of Scope` is required when the instruction is broad. It should briefly name deferred specs, features, tooling, or docs without expanding them into a roadmap.

## Validation

Validation is available when configured in the effective config or discoverable from repository conventions such as package scripts, Makefile targets, or CI config. Prefer configured validation first, then the narrowest relevant repository command. If validation was already failing before your change, report that baseline and do not repair unrelated historical failures.

Validation commands must terminate. Do not run a persistent development server as a foreground command. If browser or HTTP validation requires a server, start it in the background, capture its PID, run the check, and kill the server before continuing. Record the server lifecycle in `worklog`.

## Commit contract

Use `loop commit` for commits. Do not run `git add` or `git commit` directly. The CLI stages repository changes, validates the message, creates the commit, and prints the resulting subject and SHA.

`loop commit` arguments:

```bash
loop commit <type> <short imperative message>
```

The CLI turns those arguments into this commit subject:

`<PREFIX>: <short imperative message>`

Prefixes:

- `F` feature or user-visible behavior change
- `T` tests or test utilities
- `R` refactor without intended behavior change
- `D` documentation
- `S` style or presentation
- `V` versioning, dependencies, licensing
- `C` configuration, build, lint, CI, tooling

Commit rules:

- Use English imperative mood.
- Keep the subject short and clear.
- Pass the prefix/type separately, for example `loop commit F add weather app shell`.
- Do not add a trailing period.
- Commit only complete, reviewable work.
- If `loop commit` rejects the message, read the error, fix the type or message immediately, and retry before continuing.

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

## PR artifacts

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

## Repair and result JSON

Repair narrowly without broadening the slice. Use `blocked` when no safe path exists.

Propose branch `kind` and `slug` in `result`. `branch.final_name` is only a proposed final name; the CLI may normalize it, add collision suffixes, and perform the actual branch rename. Do not rename, switch, push, create PRs, or merge branches.

Generate the final result through the CLI:

```bash
loop iteration result --write --status completed --summary "..." --should-stop false --goal-evaluation "..." --validation-status passed
```

Add `--validation-command`, `--assumption`, `--blocked-reason`, `--error`, or branch override flags only when needed. The result command owns the JSON shape and returns actionable errors for mechanical contract problems; fix its feedback and rerun it.

Set `should_fully_stop` to `true` only when repository evidence shows the original instruction goal is complete. When the selected slice is complete but deferred slices remain, set `status` to `completed`, set `should_fully_stop` to `false`, and explain the next slice in `goal_evaluation`.
