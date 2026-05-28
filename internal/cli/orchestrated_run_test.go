package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/agent"
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

func TestHelperProcessRoleAgent(t *testing.T) {
	if os.Getenv("LOOP_ROLE_TEST_AGENT") != "1" {
		return
	}
	os.Exit(agent.RunFakeAgentFromEnv())
}
