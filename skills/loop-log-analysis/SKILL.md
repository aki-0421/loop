---
name: loop-log-analysis
description: Analyze loop CLI run logs and audit artifacts. Use when inspecting `.loop/runs`, `agent-events.jsonl`, task event logs, `errors.log`, validation output, PR check artifacts, token usage, stalled iterations, failed tasks, retries, cleanup, or any request to explain what happened during a loop run.
version: 1
---

# Loop Log Analysis

Use this skill to explain a loop run from its durable audit trail without relying on raw agent transcripts. Start from the run directory or repo root, identify the relevant iteration, summarize symptoms first, then cite the files or event types that support the conclusion.

## Quick Workflow

1. Locate the run and iteration.
   - If the user gives a run id, inspect `.loop/runs/<run-id>/`.
   - If the user gives an iteration, inspect `.loop/runs/<run-id>/iterations/<iteration>/`.
   - If they ask for "latest", choose the newest run id and newest iteration id by name unless `run-state.json` points elsewhere.
2. Run the analyzer when files are available locally:

```bash
python3 skills/loop-log-analysis/scripts/analyze_loop_logs.py . --run <run-id> --iteration latest
```

3. Read focused artifacts for any suspicious area the analyzer finds. Use `loop logs <run-id> --iteration <n> --file <name>` when the CLI should resolve artifact names, and read files directly for task-local artifacts.
4. Build the explanation around evidence:
   - Run state: `run-state.json`
   - Iteration events: `iterations/<n>/agent-events.jsonl`
   - Coding task events: `iterations/<n>/tasks/<seq>/agent-events.jsonl`
   - Failures: `errors.log`, non-zero `agent.exited`, failed `validation.command.completed`, `task.attempt.discarded`
   - Validation: `validation.md`, `validation-output-*.log`
   - Pull request mode: `pr-state.json`, `pr-checks.json`, `pr-check-log.txt`
5. Report raw uncertainty. If an artifact is missing because the run failed early or is still active, say which file is absent and what can still be inferred.

## Analysis Heuristics

- Treat `agent-events.jsonl` as append-only audit data. Use `agent.message` events for filtered agent progress messages. Do not look for raw stdout, stderr, diffs, or agent thinking logs; loop intentionally does not persist them.
- In role-orchestrated runs, planner and reviewer events live in the iteration event log, while coding-agent events live under each task directory.
- Prioritize event order by `ts`, but keep file-local order when timestamps are missing or equal.
- For token usage, add `agent.usage` events with `delta=true`. For usage snapshots without `delta`, use the largest snapshot within that agent process window after `agent.started`. Mark summaries as estimated if any event has `estimated=true`.
- A run can fail even after cleanup succeeds. Cleanup events explain what was removed; `errors.log` and the preceding failed event explain why cleanup was needed.
- A missing `errors.log` is normal for successful runs. A present `errors.log` should be summarized, not pasted wholesale.

## Output Shape

Use this order for user-facing summaries:

1. Status: run id, iteration, stage, and one-sentence outcome.
2. Cause: the most likely failure, stall, retry, or success signal.
3. Evidence: short bullets with artifact paths and event types.
4. Usage: input/output token totals when available, noting estimates.
5. Next action: the smallest useful command or file to inspect next.

Keep summaries concise and avoid exposing secrets from command lines or logs. If command text contains environment-like values, paraphrase rather than quote.

## Reference

Read `references/log-artifacts.md` when you need the durable artifact map, event semantics, or a deeper triage checklist.
