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
	if got := countEventType(t, iterDir, "agent.started"); got != 3 {
		t.Fatalf("agent.started count = %d, want planner+coder+reviewer", got)
	}
	assertBranchMissing(t, repo, "wip/0001")
}

func TestHelperProcessRoleAgent(t *testing.T) {
	if os.Getenv("LOOP_ROLE_TEST_AGENT") != "1" {
		return
	}
	os.Exit(agent.RunFakeAgentFromEnv())
}
