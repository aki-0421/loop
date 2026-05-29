package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/runstate"
)

func TestRoleOrchestratedLocalMergeUsesPlannerCoderReviewer(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nRun the role workflow.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: rolefake
  adapters:
    rolefake:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"

run:
  maxIterations: 1

git:
  baseBranch: develop
  integration:
    mode: local_merge
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add role workflow fixture")
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "rolefake", JSON: true, NoColor: true}, []string{"task.md", "--goal", "The fake role workflow is complete."})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	if data := readText(t, filepath.Join(repo, "loop-fake-role-change.txt")); !strings.Contains(data, "fake role change") {
		t.Fatalf("merged fake role change missing:\n%s", data)
	}
	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted || len(state.Iterations) != 1 {
		t.Fatalf("state = %#v, want one completed role iteration", state)
	}
	if !state.Iterations[0].ShouldFullyStop {
		t.Fatalf("role review should complete the supplied goal: %#v", state.Iterations[0])
	}
	iterDir := latestIterationDir(t, repo, "0001")
	for _, name := range []string{"task-tree.json", "review-result.json"} {
		if _, err := os.Stat(filepath.Join(iterDir, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
	if got := countEventType(t, iterDir, "agent.started"); got != 2 {
		t.Fatalf("iteration agent.started count = %d, want planner+reviewer", got)
	}
	iterationEvents := readText(t, filepath.Join(iterDir, "agent-events.jsonl"))
	for _, want := range []string{`"agent_type":"planner"`, `"agent_type":"review"`} {
		if !strings.Contains(iterationEvents, want) {
			t.Fatalf("iteration events missing %s:\n%s", want, iterationEvents)
		}
	}
	if strings.Contains(iterationEvents, `"agent_type":"coding"`) {
		t.Fatalf("coding events should not be written to iteration event log:\n%s", iterationEvents)
	}
	taskDir := filepath.Join(iterDir, "tasks", "0001")
	if data := readText(t, filepath.Join(taskDir, "task.json")); !strings.Contains(data, `"id": "fake-task"`) {
		t.Fatalf("task audit missing fake task:\n%s", data)
	}
	if data := readText(t, filepath.Join(taskDir, "task-result.json")); !strings.Contains(data, `"task_id": "fake-task"`) {
		t.Fatalf("task result audit missing fake task:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "task-results")); !os.IsNotExist(err) {
		t.Fatalf("legacy task-results directory should not exist: %v", err)
	}
	if got := countEventType(t, taskDir, "agent.started"); got != 1 {
		t.Fatalf("task agent.started count = %d, want coding agent", got)
	}
	taskEvents := readText(t, filepath.Join(taskDir, "agent-events.jsonl"))
	for _, want := range []string{`"agent_type":"coding"`, `"task_id":"fake-task"`} {
		if !strings.Contains(taskEvents, want) {
			t.Fatalf("task events missing %s:\n%s", want, taskEvents)
		}
	}
	assertBranchMissing(t, repo, "wip/0001")
}

func TestRoleOrchestratedCodingAgentResolvesTaskMergeConflict(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nRun two conflicting coding tasks.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: rolemerge
  adapters:
    rolemerge:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "merge-conflict"

run:
  maxIterations: 1
  maxParallelTasks: 2

git:
  baseBranch: develop
  integration:
    mode: local_merge
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add role merge conflict fixture")
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "rolemerge", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should resolve task merge conflict: %v", err)
	}

	if data := readText(t, filepath.Join(repo, "shared.txt")); data != "first\nsecond\n" {
		t.Fatalf("shared.txt = %q, want combined conflict resolution", data)
	}
	iterDir := latestIterationDir(t, repo, "0001")
	for _, taskDir := range []string{filepath.Join(iterDir, "tasks", "0001"), filepath.Join(iterDir, "tasks", "0002")} {
		if _, err := os.Stat(filepath.Join(taskDir, "task-merge.json")); err != nil {
			t.Fatalf("task merge audit missing in %s: %v", taskDir, err)
		}
	}
	mergedEvents := countEventType(t, filepath.Join(iterDir, "tasks", "0001"), "task.merge.completed") + countEventType(t, filepath.Join(iterDir, "tasks", "0002"), "task.merge.completed")
	if mergedEvents != 2 {
		t.Fatalf("completed task merge events = %d, want 2", mergedEvents)
	}
	assertBranchMissing(t, repo, "wip/0001")
}

func TestRoleOrchestratedCleansUpIterationOnPlannerError(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nFail during planning.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: rolefail
  adapters:
    rolefail:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "planner-error"

run:
  maxIterations: 1

git:
  baseBranch: develop
  integration:
    mode: local_merge
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add role planner error fixture")
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "rolefail", JSON: true, NoColor: true}, []string{"task.md"})
	}); err == nil {
		t.Fatal("loop run should fail when planner agent exits non-zero")
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageFailed {
		t.Fatalf("run stage = %s, want failed", state.Stage)
	}
	iterDir := filepath.Join(repo, ".loop", "runs", state.RunID, "iterations", "0001")
	if got := countEventType(t, iterDir, "run.error_cleanup.completed"); got != 1 {
		t.Fatalf("error cleanup events = %d, want 1", got)
	}
	if got := countEventType(t, iterDir, "iteration.active_temp.cleanup.completed"); got != 1 {
		t.Fatalf("active temp cleanup events = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".loop", "worktrees", state.RunID, "0001", "iteration")); !os.IsNotExist(err) {
		t.Fatalf("iteration worktree should be removed, err=%v", err)
	}
	assertBranchMissing(t, repo, "wip/0001")
}

func TestRoleOrchestratedDiscardsUnmergedTaskAttemptBeforeRetry(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nRetry an unmerged coding attempt.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: roleretry
  adapters:
    roleretry:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "discard-unmerged-retry"

run:
  maxIterations: 1
  maxTaskAttempts: 2

git:
  baseBranch: develop
  integration:
    mode: local_merge
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add role retry fixture")
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "roleretry", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should retry discarded task attempt: %v", err)
	}

	if data := readText(t, filepath.Join(repo, "retry.txt")); data != "second attempt\n" {
		t.Fatalf("retry.txt = %q, want second attempt", data)
	}
	iterDir := latestIterationDir(t, repo, "0001")
	taskDir := filepath.Join(iterDir, "tasks", "0001")
	if got := countEventType(t, taskDir, "agent.started"); got != 2 {
		t.Fatalf("task agent.started count = %d, want two attempts", got)
	}
	if got := countEventType(t, taskDir, "task.attempt.discarded"); got != 1 {
		t.Fatalf("discarded attempt events = %d, want 1", got)
	}
	if got := countEventType(t, taskDir, "task.merge.completed"); got != 1 {
		t.Fatalf("completed task merge events = %d, want 1", got)
	}
	if data := readText(t, filepath.Join(taskDir, "task-result.json")); !strings.Contains(data, "second attempt") {
		t.Fatalf("task result should come from retry:\n%s", data)
	}
	if data := readText(t, filepath.Join(taskDir, "task-merge.json")); !strings.Contains(data, `task/0001-retry-task-attempt-2`) {
		t.Fatalf("task merge should come from second attempt:\n%s", data)
	}
	assertBranchMissing(t, repo, "task/0001-retry-task-attempt-1")
	assertBranchMissing(t, repo, "task/0001-retry-task-attempt-2")
	assertBranchMissing(t, repo, "wip/0001")
}

func TestHelperProcessRoleAgent(t *testing.T) {
	if os.Getenv("LOOP_ROLE_TEST_AGENT") != "1" {
		return
	}
	os.Exit(runRoleTestAgent())
}

func runRoleTestAgent() int {
	ctx := context.Background()
	role := strings.TrimSpace(os.Getenv("LOOP_ROLE"))
	switch role {
	case "planner":
		switch os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") {
		case "planner-error":
			fmt.Fprintln(os.Stderr, "forced planner failure")
			return 1
		case "merge-conflict":
			payload := `{
  "schema_version": 1,
  "summary": "Run conflicting task merge workflow",
  "goal_evaluation": "Fake planner selected two conflicting tasks.",
  "tasks": [
    {
      "id": "first-task",
      "title": "First task",
      "description": "Create the first shared file value.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["shared.txt contains the first value."],
      "commit_type": "F",
      "commit_message": "write first shared value"
    },
    {
      "id": "second-task",
      "title": "Second task",
      "description": "Create the second shared file value.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["shared.txt contains the second value."],
      "commit_type": "F",
      "commit_message": "write second shared value"
    }
  ]
}`
			return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
		case "discard-unmerged-retry":
			payload := `{
  "schema_version": 1,
  "summary": "Retry one unmerged task attempt",
  "goal_evaluation": "Fake planner selected one retry task.",
  "tasks": [
    {
      "id": "retry-task",
      "title": "Retry task",
      "description": "Create a retry marker after one discarded attempt.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["retry.txt contains the second attempt value."],
      "commit_type": "F",
      "commit_message": "retry unmerged task attempt"
    }
  ]
}`
			return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
		}
		payload := `{
  "schema_version": 1,
  "summary": "Run fake role workflow",
  "goal_evaluation": "Fake planner selected one deterministic task.",
  "tasks": [
    {
      "id": "fake-task",
      "title": "Fake task",
      "description": "Create a deterministic fake workflow change.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["The fake workflow marker file exists."],
      "commit_type": "F",
      "commit_message": "run fake role workflow"
    }
  ]
}`
		return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
	case "coding":
		taskID := os.Getenv("LOOP_TASK_ID")
		workDir := os.Getenv("LOOP_WORKDIR")
		switch os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") {
		case "merge-conflict":
			value := "first\n"
			if taskID == "second-task" {
				value = "second\n"
			}
			if err := os.WriteFile(filepath.Join(workDir, "shared.txt"), []byte(value), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed "+taskID+"."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := commandTask(ctx, globals{}, []string{"merge"}); err == nil {
				return 0
			}
			iterationWorktree := os.Getenv("LOOP_ITERATION_WORKTREE")
			if err := os.WriteFile(filepath.Join(iterationWorktree, "shared.txt"), []byte("first\nsecond\n"), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return exitCode(commandTask(ctx, globals{}, []string{"merge", "--continue"}))
		case "discard-unmerged-retry":
			taskDir := os.Getenv("LOOP_TASK_DIR")
			attemptFile := filepath.Join(taskDir, "attempt-count.txt")
			if _, err := os.Stat(attemptFile); os.IsNotExist(err) {
				if err := os.WriteFile(attemptFile, []byte("1\n"), 0o644); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				if err := os.WriteFile(filepath.Join(workDir, "retry.txt"), []byte("first attempt\n"), 0o644); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent stopped before merging the first attempt."); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				hookPath, err := (gitx.Runner{Dir: workDir}).Run(ctx, "rev-parse", "--git-path", "hooks/pre-commit")
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				hookPath = strings.TrimSpace(hookPath)
				if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho forced pre-commit failure >&2\nexit 1\n"), 0o755); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				mergeErr := commandTask(ctx, globals{}, []string{"merge"})
				_ = os.Remove(hookPath)
				if mergeErr == nil {
					fmt.Fprintln(os.Stderr, "first attempt merge unexpectedly succeeded")
					return 1
				}
				return 0
			}
			if err := os.WriteFile(filepath.Join(workDir, "retry.txt"), []byte("second attempt\n"), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed the second attempt."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return exitCode(commandTask(ctx, globals{}, []string{"merge"}))
		}
		if err := os.WriteFile(filepath.Join(workDir, "loop-fake-role-change.txt"), []byte("fake role change\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed "+taskID+"."); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return exitCode(commandTask(ctx, globals{}, []string{"merge"}))
	case "review":
		payload := `{
  "schema_version": 1,
  "status": "approved",
  "summary": "Fake review approved the iteration.",
  "goal_evaluation": "The fake role workflow completed the supplied goal.",
  "goal_complete": true
}`
		return exitCode(commandHandoff(ctx, globals{}, []string{"write", "review-result", "--value", payload}))
	default:
		fmt.Fprintf(os.Stderr, "unknown role %q\n", role)
		return 1
	}
}

func writeRoleTaskResult(ctx context.Context, taskID, summary string) error {
	taskDir := os.Getenv("LOOP_TASK_DIR")
	payload := fmt.Sprintf(`{
  "schema_version": 1,
  "task_id": %q,
  "status": "completed",
  "summary": %q
}`, taskID, summary)
	path := filepath.Join(taskDir, "task-result-source.json")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		return err
	}
	return commandHandoff(ctx, globals{}, []string{"write", "task-result", "--task", taskID, "--file", path})
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	if code, ok := ExitCode(err); ok {
		return code
	}
	return 1
}
