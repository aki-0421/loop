---
name: loop
description: Execute one autonomous coding iteration inside the loop harness. Use for CLI-managed iterations that must gather context, choose one reviewer-sized slice, write plan and todo artifacts, edit repository files, validate, commit through loop, optionally create/merge a pull request, ask important clarifications through GitHub Issues, and write result JSON.
version: 1
---

# loop

You are executing one iteration inside the `loop` harness.

## Scope

- Complete exactly one reviewer-sized iteration from the current instruction.
- Treat one iteration as one pull-request-sized change, not a project, epic, milestone, or roadmap.
- Use the repository, current instruction, `AGENTS.md`, runtime artifact, GitHub PR/Issue/comment memory, and discovered docs as evidence.
- Do not ask the user questions directly or wait for manual actions. Make explicit assumptions when safe, create GitHub clarification Issues for important ambiguity, or return `blocked` when no safe path exists.
- Stop after the selected slice is complete, validated, committed, documented, and, when pull request mode is enabled, merged.

## Product neutrality contract

`loop` is the OSS harness. Keep it independent of any product, framework, architecture, UI tool, database, cloud provider, test runner, or domain method.

- Do not infer task-specific requirements from this file.
- Do not add conditional protocols for particular work types here.
- Load task-specific behavior only from the current instruction, nearest `AGENTS.md`, repository docs, available skills, and existing code.
- If a repository-specific convention is discoverable in docs or code, follow it there instead of duplicating it here.
- If instructions conflict, prefer the current user instruction, then the nearest repository instruction that applies to the edited files, then this harness contract.

## Harness-owned artifacts

Use the `loop` CLI as the source of truth for runtime artifacts, GitHub context memory, clarification Issues, commits, branch state, pull requests, and result generation.

- Do not construct or edit paths under the iteration directory directly for artifacts.
- This restriction does not apply to normal repository source files.
- If a `loop` command fails, read the error, fix the arguments or state, and rerun it.
- Do not bypass the harness with direct Git, PR, or artifact writes when an equivalent `loop` command exists.

## Working order

1. Establish context.
2. Select exactly one implementation slice.
3. Write `plan` and `todo` before any repository file edits.
4. Implement only the selected slice.
5. Validate the relevant behavior.
6. Commit each completed TODO through `loop commit` before moving to unrelated work.
7. Stop after the selected slice is complete; do not begin a follow-up slice.
8. Write `worklog` and durable PR body context when pull request mode is enabled.
9. If the result will be `completed`, rename the branch through `loop branch rename`.
10. If pull request mode is enabled, create the PR, wait for checks, repair failures narrowly, and merge through `loop pr`.
11. Confirm `git status --short` has no changed files before writing the final `result`.
12. Write `result` only after required commits and, when enabled, PR merge are complete.

## Establish context

Start each iteration with shallow operational context. Do not deep-scan the repository until planning identifies a target path.

Run:

```bash
loop help agent
pwd
git status --short --branch
loop iteration read runtime
loop iteration read instruction
```

Then:

- Choose a task-appropriate positive limit and run `loop memory recent --limit <n>`.
- If GitHub context memory points to relevant older context, run `loop memory search <query> --limit <n>`.
- If commit history exists, inspect recent commits, usually with `git log --oneline -20`.
- Inspect only the broad landmarks needed to plan: root files, package directories, README, nearest `AGENTS.md`, docs indexes, package scripts, tests, CI, and validation entry points.
- Carry forward the goal, branch/mode, validation settings, recent changes, unfinished work, constraints, and assumptions.
- Return `blocked` only if required operational context is missing or unsafe to interpret and no safe independent work remains.

## Clarifications

Use GitHub Issues for important product, policy, or large blocking specification questions and concrete repository or harness improvement proposals:

```bash
loop issue ask --title "Clarify ..." --body "..." [--blocking]
loop issue report --title "Improve ..." --body "..." [--kind tool|docs|guardrail|observability|environment|workflow|other] [--blocking]
```

- Use `--blocking` only when the answer can block a large implementation choice or no safe final decision exists.
- After creating an Issue, record the URL in `worklog` and continue TODOs unrelated to that clarification.
- Do not use Issues for minor local uncertainties that can be resolved from code, tests, docs, or a safe explicit assumption.
- Use `loop issue report` only for concrete repository or harness improvement proposals. Do not persist unsupported-agent findings; rediscover them each iteration.
- Write Issue bodies as GitHub-flavored Markdown. For improvement proposals, use clear sections for evidence from the current run, impact, and a suggested harness or repository change.
- Return `blocked` only when no safe independent work remains; include the blocking Issue URL in `--blocked-reason`.
- If `loop iteration read github-updates` contains Issue or PR updates after a sleep wake cycle, read and apply them before deciding whether to continue or remain blocked.

## Plan one slice

Before editing repository files, inspect the plan help and template:

```bash
loop help agent iteration plan
loop iteration plan template
```

Choose one reviewer-sized slice using repository evidence.

- Start from the concrete entry point named or implied by the instruction, such as a failing test, changed behavior, route, endpoint, command, job, schema, migration, package, doc, or configuration boundary.
- Identify the highest-risk path: unknown behavior, integration boundary, data contract, migration, generated artifact, shared abstraction, or failing validation.
- Inspect only the evidence needed for that path: relevant code, tests, docs, schemas, fixtures, callers, consumers, runtime errors, logs, and validation entry points.
- Prefer the smallest independently reviewable change that proves progress toward the instruction.
- Use a cross-file or cross-package change only when needed to prove the selected behavior.
- Introduce shared abstractions only when there is immediate evidence for them: a caller, consumer, validation path, or repository convention.
- Avoid unused scaffolding. A scaffold-only slice is valid only when the instruction or repository evidence makes scaffolding itself the selected deliverable and validation proves it is usable.
- Separate product behavior, refactors, tests, CI, docs, and tooling unless one directly proves the other inside the selected slice.
- If the slice grows, shrink it to a characterization, repair, migration step, scaffold, or proof slice that can stand alone.

Write the `plan` artifact with:

- selected slice,
- why it was chosen,
- evidence used,
- expected files or subsystems,
- validation strategy,
- assumptions,
- out of scope.

When the instruction is broad, `Out of Scope` must name deferred areas briefly without becoming a roadmap.

## Write TODOs

After the plan fixes the work target, inspect TODO help:

```bash
loop help agent iteration todo
```

Stay in planning until both `plan` and `todo` are written.

Before both artifacts exist, only inspect, read, and reason. Do not edit repository files, run formatting or codegen that writes files, validate with mutating commands, or call `loop commit`.

TODOs should be commit-sized and evidence-linked. Each TODO should end in one of: a source change, a validation change, a documentation update, an artifact update, or no-change evidence.

## Validate

Prefer configured validation from runtime. If none is configured, use the narrowest relevant repository command discovered from scripts, Makefile, CI, or docs.

- Validation commands must terminate.
- Do not run persistent servers in the foreground.
- If validation needs a server, start it in the background, capture the PID, run the check, and kill the server before continuing.
- If validation was already failing before your change, report the baseline and do not repair unrelated failures unless that repair is the selected slice.
- If validation cannot run, record the missing dependency, command, credential, or unsafe condition in `worklog`, PR notes, and `result`.

## Commit

Before the first commit, inspect commit help:

```bash
loop help agent commit
```

Use `loop commit` for commits. Do not run `git add` or `git commit` directly.

- Commit immediately after a TODO is complete and sufficiently validated.
- Mark a TODO complete only after the matching commit exists, unless it required no repository change.
- Do not accumulate independent TODOs and commit them all at the end.
- Do not commit secrets, local logs, build outputs, or generated noise unless repository convention or the selected slice requires them.
- If `loop commit` rejects the type or message, read the error, fix it, and retry.

## Handoff

Write enough context for the next iteration to continue without guessing.

`worklog` should include:

- selected slice,
- important commands,
- validation commands and outcomes,
- changed files or subsystems,
- decisions and assumptions,
- reverted or deferred work.

In pull request mode, `pr-body` should include durable completed-work context:

- what changed,
- commit SHA and subject,
- validation status,
- remaining follow-up slices,
- next recommended slice,
- whether the original goal is complete.

If follow-up slices remain, the iteration may still be `completed`, but `should_fully_stop` must be `false`.

## Pull request mode

If pull request mode is enabled:

1. Read the template with `loop iteration read pr-template`.
2. Write `pr-title` with `loop iteration write pr-title`.
3. Write `pr-body` with `loop iteration write pr-body`.
4. Run `loop pr create`.
5. Run `loop pr checks`.
6. If checks fail, read `loop iteration read pr-checks`, fetch logs with `loop pr logs <job-url-or-id>`, repair narrowly, validate locally, commit through `loop commit`, update artifacts when useful, and rerun checks.
7. Run `loop pr merge` only after checks pass.

Follow the returned PR template. Describe only this PR's selected slice, validation results, review notes, and intentionally deferred work. Do not write a `completed` result in pull request mode until `loop pr merge` succeeds.

## Branch naming

For completed work, inspect branch rename help and rename through the harness before writing the result:

```bash
loop help agent branch rename
loop branch rename --kind fix concise-description #feat/add-password-reset-tests
```

Use one of the kinds shown by help. If the command prints a collision-adjusted branch, keep using that printed branch. Do not run direct Git branch switch, rename, push, PR, or merge commands.

Skip branch rename only for `no_change`, `blocked`, or `failed` results.

## Result

Generate the final result through the CLI:

```bash
loop iteration result --write --status completed --summary "..." --should-stop false --goal-evaluation "..." --validation-status passed
```

Add validation command, assumption, blocked reason, or error flags only when needed. The CLI owns the JSON shape and branch metadata. Fix any CLI feedback and rerun.

Use `completed` only when the selected slice is complete and required merge steps are finished. Use `blocked` when no safe path exists after any necessary blocking Issue has been created and unrelated work is exhausted. Use `failed` for unrecoverable execution errors. Use `no_change` only when repository evidence shows no change is needed.

Set `should_fully_stop` to `true` only when repository evidence shows the original instruction goal is complete.
