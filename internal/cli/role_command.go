package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func commandRole(ctx context.Context, g globals, args []string) error {
	_ = ctx
	if len(args) != 1 || args[0] != "instruction" {
		return codedError{2, fmt.Errorf("usage: loop role instruction")}
	}
	rawRole := strings.TrimSpace(os.Getenv("LOOP_ROLE"))
	role := normalizeRole(rawRole)
	if role == "" {
		if rawRole != "" {
			return codedError{2, fmt.Errorf("unsupported LOOP_ROLE %q; expected planner, coding, review, or merge", rawRole)}
		}
		return codedError{2, fmt.Errorf("LOOP_ROLE is required for `loop role instruction`")}
	}
	text, err := renderRoleInstruction(role)
	if err != nil {
		return codedError{2, err}
	}
	return printResult(g, map[string]any{
		"role":        role,
		"instruction": text,
	}, text)
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "planner", "coding", "review", "merge":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return ""
	}
}

func renderRoleInstruction(role string) (string, error) {
	var b strings.Builder
	switch role {
	case "planner":
		b.WriteString(plannerRoleMarkdown())
	case "coding":
		b.WriteString(codingRoleMarkdown())
	case "review":
		b.WriteString(reviewRoleMarkdown())
	case "merge":
		b.WriteString(mergeRoleMarkdown())
	default:
		return "", fmt.Errorf("unsupported LOOP_ROLE %q; expected planner, coding, review, or merge", role)
	}
	return b.String(), nil
}

func plannerRoleMarkdown() string {
	return `## Planner Role

Explore the repository enough to plan the iteration. One iteration produces one PR, and that PR should be sized like an AI development sprint: the largest coherent goal suitable for an autonomous coding run while still producing an independently mergeable result. If the obvious next slice is only a narrow affordance, isolated implementation layer, or commit-sized change, expand to adjacent behavior that belongs to the same product or technical goal.

Write one task tree:

` + "```bash" + `
loop handoff write task-tree --file task-tree.json
` + "```" + `

The task tree contains ` + "`schema_version`" + `, ` + "`summary`" + `, ` + "`goal_evaluation`" + `, optional ` + "`goal_complete`" + `, optional ` + "`wait_for_pending_prs`" + `, and ` + "`tasks`" + `. Each task includes ` + "`id`" + `, ` + "`title`" + `, ` + "`description`" + `, ` + "`depends_on`" + `, ` + "`conflicts_with`" + `, and ` + "`acceptance`" + `. Commit intent comes from coding-agent task TODOs.

Task-tree shape:

` + "```json" + `
{
  "schema_version": 1,
  "summary": "One-sentence AI sprint-sized PR summary.",
  "goal_evaluation": "Current view of the CLI goal.",
  "goal_complete": false,
  "wait_for_pending_prs": false,
  "tasks": [
    {
      "id": "implement-core",
      "title": "Implement core behavior",
      "description": "Instructions for the coding agent.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["Concrete acceptance check."]
    }
  ]
}
` + "```" + `

Task rules:

- The task tree should cover the full sprint-level PR goal, not only a tiny isolated change.
- Prefer the fewest task boundaries that preserve autonomy, dependency ordering, conflict avoidance, validation, and safe parallelism.
- Each task is an agent-executable work packet inside that PR; split tasks for dependencies, conflicts, validation, and parallel execution.
- Each task should own a meaningful vertical outcome or substantial subsystem slice. Avoid splitting by implementation layer alone, such as separate model-only, route-only, and documentation-only tasks, unless the dependency or conflict boundary is real.
- A coding task should carry enough ownership for a meaningful local TODO sequence. Merge commit-sized microtasks into neighboring tasks.
- Keep documentation and validation inside the task that owns the behavior unless a final cross-cutting hardening task adds distinct value.
- Use the dependency graph to expose safe parallelism. When tasks must be serial, each serial step should still produce a meaningful integrated increment.
- Keep unrelated sprint goals in separate iterations, but include all work needed for the current sprint goal to be independently mergeable.
- If runtime context includes review-pending PRs and every safe implementation area is reserved by those PRs, return an empty ` + "`tasks`" + ` array with ` + "`wait_for_pending_prs: true`" + ` so the CLI waits for pending PR changes instead of creating overlapping work.
- IDs use lowercase letters, digits, and hyphens.
- ` + "`depends_on`" + ` defines required order.
- ` + "`conflicts_with`" + ` prevents parallel execution.
- Planner tasks describe task objective, task content, dependencies, conflicts, and acceptance criteria only.
- Commit metadata is rejected by the CLI.
`
}

func codingRoleMarkdown() string {
	return `## Coding Role

Coding agents must use ` + "`loop task todo`" + ` for task-local work. Commit TODOs create task-branch commits, no_commit TODOs record clean validation or inspection work, and ` + "`loop task merge`" + ` merges the completed task branch with a CLI-generated ` + "`Complete work: <task title>`" + ` merge commit. If you exit before ` + "`loop task merge`" + ` succeeds, the orchestrator treats the attempt as unmerged, discards that task branch/worktree, and may retry the task in a fresh coding session.

Complete only the assigned task from the prompt. Understand the task goal and success criteria, then inspect the repository before editing. Before implementation, create an initial task-local TODO list through ` + "`loop task todo add`" + `. Use ` + "`--work-type commit`" + ` for TODOs that create one task-branch commit, and ` + "`--work-type no_commit`" + ` for validation, inspection, handoff, or other work that leaves no repository changes. Commit TODO titles and commit messages must be specific to the assigned task. For commit TODOs, provide one quoted final commit-message argument after the flags. no_commit TODO syntax ends after the acceptance flags. Run task TODO mutation commands one at a time. If you need to adjust pending order before starting work, use ` + "`loop task todo add --after <n>`" + ` or ` + "`loop task todo move <n> --after <n>`" + `; ` + "`--after 0`" + ` places an item at the top. During implementation, you may add, move, remove, or cancel pending follow-up TODOs after the fixed done/active/cancelled boundary when you discover additional work such as documentation, tests, validation, or cleanup.

Process TODOs serially:

` + "```bash" + `
loop task todo add --work-type commit --type F --title "Add publish review route" --acceptance "The route renders the review workflow and focused coverage passes." "add publish review route"
loop task todo add --work-type no_commit --title "Run focused validation" --acceptance "The focused validation command passes."
loop task todo list
loop task todo start 1
# edit files for TODO 1 only
loop task todo stage 1
loop task todo complete 1
` + "```" + `

Inspect the ` + "`loop task todo stage`" + ` output before completing a commit TODO. If unrelated files appear, remove them from the index with ` + "`loop task todo stage <n> --remove <path>`" + ` or reset and explicitly add the intended paths with ` + "`loop task todo stage <n> --reset --add <path>`" + `. For no_commit TODOs, keep files unstaged and complete with a clean task worktree. Complete or cancel the current TODO before starting the next TODO.

Use ` + "`loop task todo cancel <n>`" + ` for pending TODOs that should remain in the audit trail as cancelled. To cancel an active TODO, run ` + "`loop task todo cancel <n> --discard-changes`" + `; this discards task worktree and index changes before marking the TODO cancelled.

When all TODOs are done, write the task result outside repository changes:

` + "```bash" + `
loop handoff write task-result --task "$LOOP_TASK_ID" --file "$LOOP_TASK_DIR/task-result.json"
` + "```" + `

Task-result shape:

` + "```json" + `
{
  "schema_version": 1,
  "task_id": "implement-core",
  "status": "completed",
  "summary": "What changed.",
  "validation": ["Focused check that ran."],
  "notes": []
}
` + "```" + `

After writing a completed task result, run:

` + "```bash" + `
loop task merge --type F complete "$LOOP_TASK_ID"
` + "```" + `

If the command reports conflicts, resolve them in the printed iteration worktree and run:

` + "```bash" + `
loop task merge --continue
` + "```" + `

Use ` + "`status: \"completed\"`" + ` only when the task implementation is ready to merge and all TODOs are complete. The task is not complete until ` + "`loop task merge`" + ` succeeds. Use ` + "`loop task discard --reason <reason>`" + ` when the task should be abandoned so the planner can revise the remaining plan. Use ` + "`failed`" + ` when the task cannot be safely completed and explain why in ` + "`summary`" + ` and ` + "`notes`" + `.
`
}

func reviewRoleMarkdown() string {
	return `## Review Role

Review the iteration branch after coding and validation. Inspect the diff, task tree, task results, validation evidence, browser/UI behavior when relevant, and cross-task acceptance criteria. Focus on whether the integrated code matches the planner's task tree and acceptance criteria, whether the coding-agent results accurately describe the implemented work, and whether code quality is acceptable.

Review scope is the ` + "`review-result`" + ` handoff. Branch rename, PR title/body artifacts, PR creation, PR checks, and PR merge belong to the merge role after QA approval. PR check failures belong in merge-result ` + "`pr_check_failed`" + ` findings.

` + "```bash" + `
loop handoff write review-result --file review-result.json
` + "```" + `

Review-result shape:

` + "```json" + `
{
  "schema_version": 1,
  "status": "approved",
  "summary": "Review conclusion.",
  "goal_evaluation": "Whether the integrated PR satisfies the CLI goal.",
  "goal_complete": false,
  "findings": []
}
` + "```" + `

Use ` + "`status: \"approved\"`" + ` only when the iteration goals are satisfied, code quality is acceptable, and the integrated branch satisfies the task tree and acceptance criteria. Use ` + "`changes_requested`" + ` with concrete implementation, QA, validation, or acceptance findings that can be converted into repair tasks. Set ` + "`goal_complete`" + ` only when a CLI goal exists and the integrated code would satisfy it after successful integration.
`
}

func mergeRoleMarkdown() string {
	return `## Merge Role

Handle PR lifecycle after QA approval. Use the task results and approved review evidence to write accurate PR title/body artifacts, then use loop-owned branch and PR commands.

Inspect ` + "`LOOP_PR_REVIEW_MODE`" + ` or ` + "`loop iteration read runtime`" + ` before choosing the merge path.

Read ` + "`pr-state`" + ` only after ` + "`loop pr create`" + `, ` + "`loop pr checks`" + `, or ` + "`loop pr merge`" + ` has written it. If ` + "`loop iteration read pr-state`" + ` reports it is missing before PR creation, continue the PR setup path.

When ` + "`LOOP_PR_REVIEW_MODE=auto_merge`" + `, rename the branch, read the PR template, write PR artifacts, create the PR, wait for checks, and merge:

` + "```bash" + `
loop branch rename --kind feat concise-branch-subject
loop iteration read pr-template
loop iteration write pr-title --value "Clear PR title"
loop iteration write pr-body --file pr-body.md
loop pr create
loop pr checks
loop pr merge
` + "```" + `

When ` + "`LOOP_PR_REVIEW_MODE=parallel_human_review`" + ` or ` + "`serial_human_review`" + `, prepare the PR, run checks, and leave it waiting for human review:

` + "```bash" + `
loop branch rename --kind feat concise-branch-subject
loop iteration read pr-template
loop iteration write pr-title --value "Clear PR title"
loop iteration write pr-body --file pr-body.md
loop pr create
loop pr checks
` + "```" + `

If ` + "`loop pr checks`" + ` fails, inspect ` + "`pr-checks`" + `, ` + "`pr-check-log`" + `, or ` + "`loop pr logs`" + ` as needed and write ` + "`merge-result`" + ` with ` + "`status: \"pr_check_failed\"`" + ` plus concrete repair findings.

` + "```bash" + `
loop handoff write merge-result --file merge-result.json
` + "```" + `

Merge-result shape:

` + "```json" + `
{
  "schema_version": 1,
  "status": "merged",
  "summary": "Merge lifecycle conclusion.",
  "pr": "https://github.com/org/repo/pull/123",
  "branch": "feat/example",
  "findings": []
}
` + "```" + `

Use ` + "`status: \"merged\"`" + ` only when ` + "`pr-state.status=merged`" + `. Use ` + "`status: \"waiting_for_human\"`" + ` only when human-review PR checks passed and ` + "`pr-state.status=waiting_for_human`" + `. Use ` + "`status: \"blocked\"`" + ` when PR lifecycle cannot continue without human intervention for a non-check reason. Use ` + "`status: \"failed\"`" + ` for unrecoverable merge-agent failures.
`
}
