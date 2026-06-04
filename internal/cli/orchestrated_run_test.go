package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/runstate"
	"github.com/aki-0421/loop/internal/workflow"
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

func TestRoleAgentIdleTimeoutRestartsCodingAgentInSameAttempt(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nRun the role workflow with an idle restart.\n")
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
        LOOP_ROLE_TEST_AGENT_MODE: coding-idle-restart

run:
  maxIterations: 1
  maxTaskAttempts: 1
  maxRoleAgentRestarts: 1
  agentIdleTimeoutSeconds: 1

git:
  baseBranch: develop
  integration:
    mode: local_merge
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add idle restart fixture")
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "rolefake", JSON: true, NoColor: true}, []string{"task.md", "--goal", "The fake role workflow is complete."})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	if data := readText(t, filepath.Join(repo, "loop-fake-role-change.txt")); !strings.Contains(data, "resumed after idle timeout") {
		t.Fatalf("merged idle restart change missing:\n%s", data)
	}
	iterDir := latestIterationDir(t, repo, "0001")
	taskDir := filepath.Join(iterDir, "tasks", "0001")
	if got := countEventType(t, taskDir, "agent.started"); got != 2 {
		t.Fatalf("coding agent starts = %d, want timeout attempt + restart", got)
	}
	taskEvents := readText(t, filepath.Join(taskDir, "agent-events.jsonl"))
	for _, want := range []string{`"type":"agent.stalled"`, `"type":"agent.restart"`} {
		if !strings.Contains(taskEvents, want) {
			t.Fatalf("task events missing %s:\n%s", want, taskEvents)
		}
	}
	if strings.Contains(taskEvents, `"type":"task.attempt.discarded"`) {
		t.Fatalf("role restart should preserve the same task attempt:\n%s", taskEvents)
	}
}

func TestResumeRunRestartsReviewingIterationFromDurableArtifacts(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configText := `version: 1

agent:
  default: rolefake
  adapters:
    rolefake:
      command: ` + yamlSingleQuote(agentCommand) + `
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
`
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nResume a reviewing iteration.\n")
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), configText)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add resume fixture")

	runID := "resume-review-run"
	iterationID := "0001"
	runDir := filepath.Join(repo, ".loop", "runs", runID)
	iterDir := filepath.Join(runDir, "iterations", iterationID)
	taskDir := filepath.Join(iterDir, "tasks", "0001")
	worktree := filepath.Join(repo, ".loop", "worktrees", runID, iterationID, "iteration")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "worktree", "add", "-b", "wip/0001", worktree, "develop")
	mustWrite(t, filepath.Join(worktree, "resume-marker.txt"), "resume review\n")
	git(t, worktree, "add", "resume-marker.txt")
	git(t, worktree, "commit", "-m", "F: add resume marker")
	commitSHA := strings.TrimSpace(git(t, worktree, "rev-parse", "HEAD"))

	tree := workflow.TaskTree{
		SchemaVersion:  1,
		Summary:        "Resume reviewing iteration",
		GoalEvaluation: "The resume test has one completed task and needs review.",
		Tasks: []workflow.Task{{
			ID:          "fake-task",
			Title:       "Fake task",
			Description: "Create the resume marker.",
			Acceptance:  []string{"resume-marker.txt exists."},
		}},
	}
	if err := writeTaskTreeAudit(iterDir, tree); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskAudit(taskDir, tree.Tasks[0]); err != nil {
		t.Fatal(err)
	}
	if err := ensureEmptyFile(filepath.Join(iterDir, "agent-events.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := ensureEmptyFile(filepath.Join(taskDir, "agent-events.jsonl")); err != nil {
		t.Fatal(err)
	}
	result := workflow.TaskResult{
		SchemaVersion: 1,
		TaskID:        "fake-task",
		Status:        "completed",
		Summary:       "Fake task completed before the crash.",
	}
	resultData, err := workflow.MarshalIndent(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := artifactdb.WriteRoleHandoff(artifactdb.GlobalDBPathForIteration(iterDir), runID, iterationID, "task-result", "fake-task", string(resultData)); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskResultAudit(iterDir, taskDir, "fake-task", resultData); err != nil {
		t.Fatal(err)
	}
	if err := writeTaskMergeAudit(taskDir, taskMergeRecord{
		SchemaVersion:   1,
		TaskID:          "fake-task",
		Status:          "merged",
		Branch:          "task/0001-fake-task-attempt-1",
		IterationBranch: "wip/0001",
		TaskCommits:     []taskMergeCommit{{SHA: commitSHA, Subject: "F: add resume marker"}},
		MergeCommit:     taskMergeCommit{SHA: commitSHA, Subject: "F: add resume marker"},
		MergedAt:        time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(iterDir, "prompt.md"), "# Task\n\nResume a reviewing iteration.\n")
	mustWrite(t, filepath.Join(iterDir, "effective-config.yaml"), configText)
	state := runstate.New(runID, "The resume fixture is complete.", "develop", "rolefake")
	state.CurrentIteration = iterationID
	state.Stage = runstate.StageReviewing
	state.Iterations = []runstate.IterationRecord{{
		IterationID:   iterationID,
		BranchInitial: "wip/0001",
		BranchCurrent: "wip/0001",
		Stage:         string(runstate.StageReviewing),
	}}
	if err := runstate.Write(filepath.Join(runDir, "run-state.json"), state); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandResume(ctx, globals{JSON: true, NoColor: true}, []string{runID})
	}); err != nil {
		t.Fatalf("loop resume: %v", err)
	}

	resumed := readLatestRunState(t, repo)
	if resumed.Stage != runstate.StageCompleted {
		t.Fatalf("resumed stage = %s, want completed", resumed.Stage)
	}
	if data := readText(t, filepath.Join(repo, "resume-marker.txt")); !strings.Contains(data, "resume review") {
		t.Fatalf("resume marker was not merged:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "review-result.json")); err != nil {
		t.Fatalf("review-result not written during resume: %v", err)
	}
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

func TestRoleOrchestratedReplansAfterExplicitDiscard(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nReplan after an explicit discard.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: rolediscard
  adapters:
    rolediscard:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "explicit-discard-replan"

run:
  maxIterations: 1

git:
  baseBranch: develop
  integration:
    mode: local_merge
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add role discard fixture")
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "rolediscard", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should replan after discard: %v", err)
	}

	if data := readText(t, filepath.Join(repo, "replacement.txt")); data != "replacement\n" {
		t.Fatalf("replacement.txt = %q, want replacement", data)
	}
	iterDir := latestIterationDir(t, repo, "0001")
	if got := countEventType(t, iterDir, "agent.started"); got != 3 {
		t.Fatalf("iteration agent.started count = %d, want two planners plus review", got)
	}
}

func TestRoleOrchestratedReplansAfterMaxAttemptExhaustion(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nReplan after task attempts are exhausted.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: roleexhaust
  adapters:
    roleexhaust:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "max-attempt-exhaustion-replan"

run:
  maxIterations: 1
  maxTaskAttempts: 1

git:
  baseBranch: develop
  integration:
    mode: local_merge
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add max attempt discard fixture")
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "roleexhaust", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should replan after max attempt exhaustion: %v", err)
	}

	if data := readText(t, filepath.Join(repo, "max-exhaustion-replacement.txt")); data != "replacement\n" {
		t.Fatalf("max-exhaustion-replacement.txt = %q, want replacement", data)
	}
	iterDir := latestIterationDir(t, repo, "0001")
	discardedResult := readText(t, filepath.Join(iterDir, "tasks", "0001", "task-result.json"))
	if !strings.Contains(discardedResult, `"status": "discarded"`) || !strings.Contains(discardedResult, `"discard_reason"`) {
		t.Fatalf("exhausted task should be marked discarded:\n%s", discardedResult)
	}
	replacementResult := readText(t, filepath.Join(iterDir, "tasks", "0002", "task-result.json"))
	if !strings.Contains(replacementResult, "max-attempt replacement") {
		t.Fatalf("replacement task result missing:\n%s", replacementResult)
	}
}

func TestRoleOrchestratedPRRepairAfterRenameUsesTrackedBranch(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nRepair a PR check after branch rename.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: roleprrepair
  adapters:
    roleprrepair:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "pr-rename-repair"

run:
  maxIterations: 1
  maxReviewFixCycles: 2

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      checksWatchTimeoutSeconds: 5
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add role PR repair fixture")
	addBareOrigin(t, repo)
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	ghChecks := filepath.Join(ghDir, "checks.count")
	writeFailOnceThenPassingFakeGH(t, ghDir, ghLog, ghChecks)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "roleprrepair", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should repair PR checks after branch rename: %v", err)
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted {
		t.Fatalf("run stage = %s, want completed", state.Stage)
	}
	if got := state.Iterations[0].BranchCurrent; got != "feat/role-pr-repair" {
		t.Fatalf("branch_current = %q, want renamed branch", got)
	}
	iterDir := filepath.Join(repo, ".loop", "runs", state.RunID, "iterations", "0001")
	repairMerge := readText(t, filepath.Join(iterDir, "tasks", "0002", "task-merge.json"))
	if !strings.Contains(repairMerge, `"iteration_branch": "feat/role-pr-repair"`) {
		t.Fatalf("repair task should merge into renamed branch:\n%s", repairMerge)
	}
	if strings.Contains(repairMerge, `"iteration_branch": "wip/0001"`) {
		t.Fatalf("repair task used stale initial branch:\n%s", repairMerge)
	}
	gh := readText(t, ghLog)
	if !strings.Contains(gh, "pr create ") || !strings.Contains(gh, "pr merge 1 --squash") {
		t.Fatalf("fake gh log missing PR create/merge:\n%s", gh)
	}
	assertBranchMissing(t, repo, "wip/0001")
	assertBranchMissing(t, repo, "feat/role-pr-repair")
}

func TestRoleOrchestratedParallelHumanReviewLeavesPendingPR(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nCreate a human-reviewed PR and keep moving.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: rolehumanpr
  adapters:
    rolehumanpr:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "pr-human-pending"

run:
  maxIterations: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      reviewMode: parallel_human_review
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add parallel human review fixture")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writePassingFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "rolehumanpr", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should leave a pending human-review PR: %v", err)
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted {
		t.Fatalf("run stage = %s, want completed", state.Stage)
	}
	if len(state.PendingPullRequests) != 1 {
		t.Fatalf("pending PRs = %#v, want one", state.PendingPullRequests)
	}
	pending := state.PendingPullRequests[0]
	if pending.Status != "waiting_for_human" || pending.Branch != "feat/human-pending-pr" {
		t.Fatalf("pending PR = %#v", pending)
	}
	if !containsString(pending.ChangedFiles, "pending-human-pr.txt") {
		t.Fatalf("pending changed files = %#v, want pending-human-pr.txt", pending.ChangedFiles)
	}
	iterDir := filepath.Join(repo, ".loop", "runs", state.RunID, "iterations", "0001")
	prStateText, err := artifactdb.Read(iterDir, "pr-state")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prStateText, `"status": "waiting_for_human"`) {
		t.Fatalf("pr-state should wait for human review:\n%s", prStateText)
	}
	gh := readText(t, ghLog)
	if !strings.Contains(gh, "pr create ") || !strings.Contains(gh, "pr checks 1") || strings.Contains(gh, "pr merge") {
		t.Fatalf("fake gh log should create/check but not merge:\n%s", gh)
	}
	assertBranchMissing(t, repo, "wip/0001")
	assertBranchMissing(t, repo, "feat/human-pending-pr")
	if _, err := os.Stat(filepath.Join(repo, "pending-human-pr.txt")); !os.IsNotExist(err) {
		t.Fatalf("pending PR work should not be on the checked-out base branch, stat err=%v", err)
	}
}

func TestRoleOrchestratedPlannerCanWaitForPendingPRs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	previousInterval := githubSleepPollInterval
	githubSleepPollInterval = time.Millisecond
	defer func() {
		githubSleepPollInterval = previousInterval
	}()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nCreate a human-reviewed PR, then wait when all remaining work is blocked.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: rolehumanprwait
  adapters:
    rolehumanprwait:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "pr-human-pending-then-wait"

run:
  maxIterations: 2

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      reviewMode: parallel_human_review
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add pending PR wait fixture")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	ghViews := filepath.Join(ghDir, "views.count")
	writePendingThenMergedFakeGH(t, ghDir, ghLog, ghViews)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "rolehumanprwait", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should wait for pending PR changes: %v", err)
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted {
		t.Fatalf("run stage = %s, want completed", state.Stage)
	}
	if len(state.PendingPullRequests) != 0 {
		t.Fatalf("pending PRs = %#v, want none after merged state is observed", state.PendingPullRequests)
	}
	if len(state.Iterations) != 2 {
		t.Fatalf("iterations = %d, want 2", len(state.Iterations))
	}
	secondTaskTree := readText(t, filepath.Join(repo, ".loop", "runs", state.RunID, "iterations", "0002", "task-tree.json"))
	if !strings.Contains(secondTaskTree, `"wait_for_pending_prs": true`) {
		t.Fatalf("second task tree should contain wait_for_pending_prs:\n%s", secondTaskTree)
	}
	events := readText(t, filepath.Join(repo, ".loop", "runs", state.RunID, "iterations", "0002", "agent-events.jsonl"))
	if !strings.Contains(events, "pr.human_review.pending_waiting") || !strings.Contains(events, "pr.human_review.pending_updated") {
		t.Fatalf("pending PR wait events missing:\n%s", events)
	}
	gh := readText(t, ghLog)
	if strings.Count(gh, "pr view 1 --json state,mergedAt,url") < 2 {
		t.Fatalf("fake gh log missing pending PR state polls:\n%s", gh)
	}
}

func TestRoleOrchestratedParallelHumanReviewRepairsFeedbackOnPendingPRBranch(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	originParent := t.TempDir()
	origin := filepath.Join(originParent, "origin.git")
	git(t, originParent, "init", "--bare", "-b", "develop", origin)
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-u", "origin", "develop")
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nCreate a human-reviewed PR, then repair review feedback.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

agent:
  default: rolehumanfeedback
  adapters:
    rolehumanfeedback:
      command: `+yamlSingleQuote(agentCommand)+`
      args: [-test.run=TestHelperProcessRoleAgent, --]
      prompt: stdin
      env:
        LOOP_ROLE_TEST_AGENT: "1"
        LOOP_ROLE_TEST_AGENT_MODE: "pr-human-feedback-repair"

run:
  maxIterations: 2

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      reviewMode: parallel_human_review
`)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add human review feedback fixture")
	git(t, repo, "push")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	createdPath := filepath.Join(ghDir, "created")
	writeReviewFeedbackFakeGH(t, ghDir, ghLog, createdPath)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "rolehumanfeedback", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should repair pending PR feedback: %v", err)
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted {
		t.Fatalf("run stage = %s, want completed", state.Stage)
	}
	if len(state.Iterations) != 2 {
		t.Fatalf("iterations = %d, want 2", len(state.Iterations))
	}
	if state.Iterations[1].BranchCurrent != "feat/human-pending-pr" {
		t.Fatalf("second iteration branch = %q, want pending PR branch", state.Iterations[1].BranchCurrent)
	}
	if len(state.PendingPullRequests) != 1 {
		t.Fatalf("pending PRs = %#v, want one waiting PR", state.PendingPullRequests)
	}
	pending := state.PendingPullRequests[0]
	if pending.Status != "waiting_for_human" || pending.Branch != "feat/human-pending-pr" {
		t.Fatalf("pending PR after repair = %#v", pending)
	}
	if pending.FeedbackHandledAt != "2026-06-04T00:03:00Z" {
		t.Fatalf("feedback handled at = %q, want latest feedback timestamp", pending.FeedbackHandledAt)
	}
	if got := git(t, repo, "show", "origin/feat/human-pending-pr:review-feedback.txt"); got != "feedback repair\n" {
		t.Fatalf("remote PR branch repair file = %q", got)
	}
	secondTaskTree := readText(t, filepath.Join(repo, ".loop", "runs", state.RunID, "iterations", "0002", "task-tree.json"))
	if !strings.Contains(secondTaskTree, `"id": "human-pr-feedback"`) {
		t.Fatalf("second task tree missing PR feedback task:\n%s", secondTaskTree)
	}
	secondEvents := readText(t, filepath.Join(repo, ".loop", "runs", state.RunID, "iterations", "0002", "agent-events.jsonl"))
	if !strings.Contains(secondEvents, "pr.human_review.repair_started") {
		t.Fatalf("second events missing repair start:\n%s", secondEvents)
	}
	gh := readText(t, ghLog)
	if !strings.Contains(gh, "pr view 1 --json reviewDecision,latestReviews,comments,updatedAt") {
		t.Fatalf("fake gh log missing review feedback lookup:\n%s", gh)
	}
	if got := strings.Count(gh, "pr create "); got != 1 {
		t.Fatalf("repair flow should not create a second PR; pr create count = %d\n%s", got, gh)
	}
	if strings.Contains(gh, "pr merge") {
		t.Fatalf("human review repair should not merge:\n%s", gh)
	}
	assertBranchMissing(t, repo, "feat/human-pending-pr")
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
		case "explicit-discard-replan":
			iterDir := os.Getenv("LOOP_ITERATION_DIR")
			countFile := filepath.Join(iterDir, "planner-count.txt")
			_, firstErr := os.Stat(countFile)
			_ = os.WriteFile(countFile, []byte("seen\n"), 0o644)
			if os.IsNotExist(firstErr) {
				payload := `{
  "schema_version": 1,
  "summary": "Discard one task before replanning",
  "goal_evaluation": "Fake planner selected a task that will be discarded.",
  "tasks": [
    {
      "id": "discard-task",
      "title": "Discard task",
      "description": "Discard this task so the planner can revise the plan.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["The task is discarded with a reason."]
    }
  ]
}`
				return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
			}
			payload := `{
  "schema_version": 1,
  "summary": "Run replacement task",
  "goal_evaluation": "Fake planner replaced the discarded task.",
  "tasks": [
    {
      "id": "replacement-task",
      "title": "Replacement task",
      "description": "Create the replacement marker.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["replacement.txt contains the replacement value."]
    }
  ]
}`
			return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
		case "max-attempt-exhaustion-replan":
			iterDir := os.Getenv("LOOP_ITERATION_DIR")
			countFile := filepath.Join(iterDir, "planner-count.txt")
			_, firstErr := os.Stat(countFile)
			_ = os.WriteFile(countFile, []byte("seen\n"), 0o644)
			if os.IsNotExist(firstErr) {
				payload := `{
  "schema_version": 1,
  "summary": "Exhaust one task before replanning",
  "goal_evaluation": "Fake planner selected a task that will exhaust attempts.",
  "tasks": [
    {
      "id": "exhaust-task",
      "title": "Exhaust task",
      "description": "Exit without a handoff so attempts are exhausted.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["The task is discarded after attempts are exhausted."]
    }
  ]
}`
				return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
			}
			payload := `{
  "schema_version": 1,
  "summary": "Run max-attempt replacement task",
  "goal_evaluation": "Fake planner replaced the exhausted task.",
  "tasks": [
    {
      "id": "max-attempt-replacement",
      "title": "Max-attempt replacement task",
      "description": "Create the replacement marker.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["max-exhaustion-replacement.txt contains the replacement value."]
    }
  ]
}`
			return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
		case "pr-rename-repair":
			payload := `{
  "schema_version": 1,
  "summary": "Run PR rename repair workflow",
  "goal_evaluation": "Fake planner selected an initial task.",
  "tasks": [
    {
      "id": "initial-pr-task",
      "title": "Initial PR task",
      "description": "Create the initial PR marker.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["initial-pr.txt contains the initial value."]
    }
  ]
}`
			return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
		case "pr-human-pending", "pr-human-pending-then-wait", "pr-human-feedback-repair":
			if os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") == "pr-human-pending-then-wait" && os.Getenv("LOOP_ITERATION_ID") == "0002" {
				payload := `{
  "schema_version": 1,
  "summary": "Wait for pending human-review PRs",
  "goal_evaluation": "The pending PR reserves the remaining safe work.",
  "wait_for_pending_prs": true,
  "tasks": []
}`
				return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
			}
			if os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") == "pr-human-feedback-repair" && os.Getenv("LOOP_ITERATION_ID") == "0002" {
				if localBranchExists(ctx, gitx.Runner{Dir: os.Getenv("LOOP_WORKDIR")}, "wip/0002") {
					fmt.Fprintln(os.Stderr, "planner ran after wip/0002 was created")
					return 1
				}
				if err := commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"feedback", "1"}); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return exitCode(err)
				}
				payload := `{
  "schema_version": 1,
  "summary": "Address human PR feedback",
  "goal_evaluation": "Fake planner selected a task for pending PR review feedback.",
  "repair_pull_request": "1",
  "tasks": [
    {
      "id": "human-pr-feedback",
      "title": "Human PR feedback",
      "description": "Address the review feedback on the existing human-reviewed PR.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["review-feedback.txt contains the feedback repair value."]
    }
  ]
}`
				return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
			}
			payload := `{
  "schema_version": 1,
  "summary": "Run human-review PR workflow",
  "goal_evaluation": "Fake planner selected one human-review PR task.",
  "tasks": [
    {
      "id": "human-pr-task",
      "title": "Human PR task",
      "description": "Create a marker for a human-reviewed PR.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["pending-human-pr.txt contains the pending value."]
    }
  ]
}`
			return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
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
      "acceptance": ["shared.txt contains the first value."]
    },
    {
      "id": "second-task",
      "title": "Second task",
      "description": "Create the second shared file value.",
      "depends_on": [],
      "conflicts_with": [],
      "acceptance": ["shared.txt contains the second value."]
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
      "acceptance": ["retry.txt contains the second attempt value."]
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
      "acceptance": ["The fake workflow marker file exists."]
    }
  ]
}`
		return exitCode(commandHandoff(ctx, globals{}, []string{"write", "task-tree", "--value", payload}))
	case "coding":
		taskID := os.Getenv("LOOP_TASK_ID")
		workDir := os.Getenv("LOOP_WORKDIR")
		switch os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") {
		case "explicit-discard-replan":
			if taskID == "discard-task" {
				return exitCode(commandTask(ctx, globals{}, []string{"discard", "--reason", "The fake task is intentionally discarded."}))
			}
			if err := startRoleTaskTodo(ctx, taskID, "run replacement task"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := os.WriteFile(filepath.Join(workDir, "replacement.txt"), []byte("replacement\n"), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := completeRoleTaskTodo(ctx); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed "+taskID+"."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return exitCode(commandTask(ctx, globals{}, []string{"merge", "--type", "F", "complete", taskID}))
		case "max-attempt-exhaustion-replan":
			if taskID == "exhaust-task" {
				return 0
			}
			if err := startRoleTaskTodo(ctx, taskID, "run max-attempt replacement"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := os.WriteFile(filepath.Join(workDir, "max-exhaustion-replacement.txt"), []byte("replacement\n"), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := completeRoleTaskTodo(ctx); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed the max-attempt replacement."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return exitCode(commandTask(ctx, globals{}, []string{"merge", "--type", "F", "complete", taskID}))
		case "merge-conflict":
			value := "first\n"
			if taskID == "second-task" {
				value = "second\n"
			}
			if err := startRoleTaskTodo(ctx, taskID, "write "+taskID); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := os.WriteFile(filepath.Join(workDir, "shared.txt"), []byte(value), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := completeRoleTaskTodo(ctx); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed "+taskID+"."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := commandTask(ctx, globals{}, []string{"merge", "--type", "F", "complete", taskID}); err == nil {
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
				if err := startRoleTaskTodo(ctx, taskID, "retry unmerged task attempt"); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				if err := os.WriteFile(filepath.Join(workDir, "retry.txt"), []byte("first attempt\n"), 0o644); err != nil {
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
				completeErr := completeRoleTaskTodo(ctx)
				_ = os.Remove(hookPath)
				if completeErr == nil {
					fmt.Fprintln(os.Stderr, "first attempt commit unexpectedly succeeded")
					return 1
				}
				return 0
			}
			if err := startRoleTaskTodo(ctx, taskID, "retry unmerged task attempt"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := os.WriteFile(filepath.Join(workDir, "retry.txt"), []byte("second attempt\n"), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := completeRoleTaskTodo(ctx); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed the second attempt."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return exitCode(commandTask(ctx, globals{}, []string{"merge", "--type", "F", "complete", taskID}))
		case "coding-idle-restart":
			taskDir := os.Getenv("LOOP_TASK_DIR")
			restartFile := filepath.Join(taskDir, "idle-restart-count.txt")
			if _, err := os.Stat(restartFile); os.IsNotExist(err) {
				if err := os.WriteFile(restartFile, []byte("1\n"), 0o644); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				if err := startRoleTaskTodo(ctx, taskID, "resume after idle timeout"); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				if err := os.WriteFile(filepath.Join(workDir, "loop-fake-role-change.txt"), []byte("partial before idle timeout\n"), 0o644); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				time.Sleep(3 * time.Second)
				return 1
			}
			if err := os.WriteFile(filepath.Join(workDir, "loop-fake-role-change.txt"), []byte("resumed after idle timeout\n"), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := completeRoleTaskTodo(ctx); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent resumed after idle timeout."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return exitCode(commandTask(ctx, globals{}, []string{"merge", "--type", "F", "complete", taskID}))
		case "pr-rename-repair":
			filename := "initial-pr.txt"
			message := "run initial PR task"
			value := "initial\n"
			if taskID == "repair-pr-check" {
				filename = "repair-pr-check.txt"
				message = "repair PR check"
				value = "repair\n"
			}
			if err := startRoleTaskTodo(ctx, taskID, message); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := os.WriteFile(filepath.Join(workDir, filename), []byte(value), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := completeRoleTaskTodo(ctx); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed "+taskID+"."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return exitCode(commandTask(ctx, globals{}, []string{"merge", "--type", "F", "complete", taskID}))
		case "pr-human-pending", "pr-human-pending-then-wait", "pr-human-feedback-repair":
			message := "run human review PR task"
			filename := "pending-human-pr.txt"
			value := "pending\n"
			if taskID == "human-pr-feedback" {
				message = "run human review feedback repair"
				filename = "review-feedback.txt"
				value = "feedback repair\n"
			}
			if err := startRoleTaskTodo(ctx, taskID, message); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := os.WriteFile(filepath.Join(workDir, filename), []byte(value), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := completeRoleTaskTodo(ctx); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed the human-review PR task."); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return exitCode(commandTask(ctx, globals{}, []string{"merge", "--type", "F", "complete", taskID}))
		}
		if err := startRoleTaskTodo(ctx, taskID, "run fake role workflow"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.WriteFile(filepath.Join(workDir, "loop-fake-role-change.txt"), []byte("fake role change\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := completeRoleTaskTodo(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := writeRoleTaskResult(ctx, taskID, "Fake coding agent completed "+taskID+"."); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return exitCode(commandTask(ctx, globals{}, []string{"merge", "--type", "F", "complete", taskID}))
	case "review":
		if os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") == "pr-rename-repair" {
			return runPRRenameRepairReviewAgent(ctx)
		}
		if os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") == "pr-human-pending" || os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") == "pr-human-pending-then-wait" || os.Getenv("LOOP_ROLE_TEST_AGENT_MODE") == "pr-human-feedback-repair" {
			return runPRHumanPendingReviewAgent(ctx)
		}
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

func startRoleTaskTodo(ctx context.Context, taskID, message string) error {
	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--type", "F", "--title", "Complete " + taskID, "--acceptance", "The task acceptance criteria are met.", message}); err != nil {
		return err
	}
	return commandTask(ctx, globals{}, []string{"todo", "start", "1"})
}

func completeRoleTaskTodo(ctx context.Context) error {
	return commandTask(ctx, globals{}, []string{"todo", "complete", "1"})
}

func runPRRenameRepairReviewAgent(ctx context.Context) int {
	iterDir := os.Getenv("LOOP_ITERATION_DIR")
	reviewCount := filepath.Join(iterDir, "review-count.txt")
	_, firstErr := os.Stat(reviewCount)
	_ = os.WriteFile(reviewCount, []byte("seen\n"), 0o644)
	if os.IsNotExist(firstErr) {
		if err := commandBranch(ctx, globals{}, []string{"rename", "--kind", "feat", "role-pr-repair"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitCode(err)
		}
		if err := artifactdb.Write(iterDir, "pr-title", "Run role PR repair\n"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := artifactdb.Write(iterDir, "pr-body", "## Summary\n\nFake role PR repair.\n"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := commandPR(ctx, globals{}, []string{"create"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitCode(err)
		}
		if err := commandPR(ctx, globals{}, []string{"checks"}); err == nil {
			fmt.Fprintln(os.Stderr, "first fake PR checks unexpectedly passed")
			return 1
		}
		payload := `{
  "schema_version": 1,
  "status": "changes_requested",
  "summary": "Fake review requested a PR check repair.",
  "goal_evaluation": "The fake PR check failed after branch rename.",
  "findings": [
    {
      "id": "repair-pr-check",
      "task_id": "initial-pr-task",
      "title": "Repair PR check",
      "description": "Create a repair commit after the branch has been renamed.",
      "acceptance": ["The repair task merges into the renamed iteration branch."]
    }
  ]
}`
		return exitCode(commandHandoff(ctx, globals{}, []string{"write", "review-result", "--value", payload}))
	}
	if err := commandPR(ctx, globals{}, []string{"checks"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitCode(err)
	}
	if err := commandPR(ctx, globals{}, []string{"merge"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitCode(err)
	}
	payload := `{
  "schema_version": 1,
  "status": "approved",
  "summary": "Fake review approved the repaired PR.",
  "goal_evaluation": "The renamed PR branch was repaired and merged.",
  "goal_complete": true
}`
	return exitCode(commandHandoff(ctx, globals{}, []string{"write", "review-result", "--value", payload}))
}

func runPRHumanPendingReviewAgent(ctx context.Context) int {
	iterDir := os.Getenv("LOOP_ITERATION_DIR")
	repairPR := strings.TrimSpace(os.Getenv("LOOP_REPAIR_PR"))
	if repairPR == "" {
		if err := commandBranch(ctx, globals{}, []string{"rename", "--kind", "feat", "human pending PR"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitCode(err)
		}
	}
	if err := artifactdb.Write(iterDir, "pr-title", "Run human-review PR\n"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := artifactdb.Write(iterDir, "pr-body", "## Summary\n\nFake human-review PR.\n"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := commandPR(ctx, globals{}, []string{"create"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitCode(err)
	}
	if err := commandPR(ctx, globals{}, []string{"checks"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitCode(err)
	}
	payload := `{
  "schema_version": 1,
  "status": "approved",
  "summary": "Fake review left the PR waiting for human review.",
  "goal_evaluation": "The PR is ready for human review but not merged.",
  "goal_complete": true
}`
	return exitCode(commandHandoff(ctx, globals{}, []string{"write", "review-result", "--value", payload}))
}

func writeFailOnceThenPassingFakeGH(t *testing.T, dir, logPath, checksPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  exit 1
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  count=0
  if [ -f ` + shellQuote(checksPath) + ` ]; then
    count=$(cat ` + shellQuote(checksPath) + `)
  fi
  count=$((count + 1))
  echo "$count" > ` + shellQuote(checksPath) + `
  if [ "$count" -eq 1 ]; then
    echo "unit test failed after rename" >&2
    exit 1
  fi
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writePendingThenMergedFakeGH(t *testing.T, dir, logPath, viewsPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  if [ "$5" = "url" ]; then
    exit 1
  fi
  count=0
  if [ -f ` + shellQuote(viewsPath) + ` ]; then
    count=$(cat ` + shellQuote(viewsPath) + `)
  fi
  count=$((count + 1))
  echo "$count" > ` + shellQuote(viewsPath) + `
  if [ "$count" -lt 2 ]; then
    printf 'OPEN\t\thttps://github.com/acme/app/pull/1\n'
    exit 0
  fi
  printf 'MERGED\t2026-06-03T00:00:00Z\thttps://github.com/acme/app/pull/1\n'
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 1
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeReviewFeedbackFakeGH(t *testing.T, dir, logPath, createdPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  if [ "$5" = "url" ]; then
    exit 1
  fi
  if [ "$5" = "state,mergedAt,url" ]; then
    printf 'OPEN\t\thttps://github.com/acme/app/pull/1\n'
    exit 0
  fi
  if [ "$5" = "reviewDecision,latestReviews,comments,updatedAt" ]; then
cat <<'JSON'
{
  "reviewDecision": "CHANGES_REQUESTED",
  "updatedAt": "2026-06-04T00:03:00Z",
  "latestReviews": [
    {
      "state": "CHANGES_REQUESTED",
      "body": "Please add the feedback repair file.",
      "submittedAt": "2026-06-04T00:02:00Z",
      "url": "https://github.com/acme/app/pull/1#pullrequestreview-1",
      "author": {"login": "reviewer"}
    }
  ],
  "comments": [
    {
      "body": "Also rerun checks on the same PR branch.",
      "createdAt": "2026-06-04T00:03:00Z",
      "url": "https://github.com/acme/app/pull/1#issuecomment-1",
      "author": {"login": "pm"}
    }
  ]
}
JSON
    exit 0
  fi
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "created" > ` + shellQuote(createdPath) + `
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 1
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
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

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
