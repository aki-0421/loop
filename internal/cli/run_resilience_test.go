package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/runstate"
)

func TestRunContinuesAcrossIterationsUntilAgentStops(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "completed,no_change",
		MaxIterations:  3,
		RepairAttempts: 1,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted {
		t.Fatalf("stage = %s, want completed", state.Stage)
	}
	if len(state.Iterations) != 2 {
		t.Fatalf("iterations = %d, want 2: %#v", len(state.Iterations), state.Iterations)
	}
	if state.Iterations[0].ShouldFullyStop {
		t.Fatalf("first completed iteration should allow the loop to continue: %#v", state.Iterations[0])
	}
	if !state.Iterations[1].ShouldFullyStop {
		t.Fatalf("second no-change iteration should stop the loop: %#v", state.Iterations[1])
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "develop" {
		t.Fatalf("current branch = %q, want develop", got)
	}
	assertBranchMissing(t, repo, "wip/0002")
	assertBranchMissing(t, repo, "test/fake-agent")
}

func TestRunRepairsInvalidResultAndIntegrates(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "invalid_json,completed",
		MaxIterations:  1,
		RepairAttempts: 1,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	iterDir := latestIterationDir(t, repo, "0001")
	if got := countEventType(t, iterDir, "agent.started"); got != 2 {
		t.Fatalf("agent.started count = %d, want 2", got)
	}
	if !strings.Contains(readText(t, filepath.Join(iterDir, "errors.log")), "result artifact missing or invalid") {
		t.Fatalf("errors.log did not record invalid-result repair:\n%s", readText(t, filepath.Join(iterDir, "errors.log")))
	}
	if _, err := os.Stat(filepath.Join(repo, "loop-fake-change.txt")); err != nil {
		t.Fatalf("repaired run did not integrate fake change: %v", err)
	}
}

func TestRunHonorsNeedsRepairResultBeforeValidation(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "needs_repair,completed",
		MaxIterations:  1,
		RepairAttempts: 1,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	iterDir := latestIterationDir(t, repo, "0001")
	if got := countEventType(t, iterDir, "agent.started"); got != 2 {
		t.Fatalf("agent.started count = %d, want 2", got)
	}
	if !strings.Contains(readText(t, filepath.Join(iterDir, "errors.log")), "agent requested repair") {
		t.Fatalf("errors.log did not record requested repair:\n%s", readText(t, filepath.Join(iterDir, "errors.log")))
	}
	if _, err := os.Stat(filepath.Join(repo, "loop-fake-change.txt")); err != nil {
		t.Fatalf("needs_repair flow did not integrate repaired change: %v", err)
	}
}

func TestRunRepairsValidationFailureAndIntegrates(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "completed,validation_fix",
		MaxIterations:  1,
		RepairAttempts: 1,
		ValidationYAML: `validation:
  commands:
    - name: repair-marker
      run: test -f validation-ok.txt
      required: true
`,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	iterDir := latestIterationDir(t, repo, "0001")
	if got := countEventType(t, iterDir, "agent.started"); got != 2 {
		t.Fatalf("agent.started count = %d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "validation-ok.txt")); err != nil {
		t.Fatalf("validation repair was not integrated: %v", err)
	}
	validation := readText(t, filepath.Join(iterDir, "errors.log"))
	if !strings.Contains(validation, "validation failed") {
		t.Fatalf("errors.log did not record validation repair cause:\n%s", validation)
	}
}

type resilienceOptions struct {
	Sequence       string
	MaxIterations  int
	RepairAttempts int
	ValidationYAML string
}

func writeResilienceFixture(t *testing.T, repo string, opts resilienceOptions) {
	t.Helper()
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake resilient loop progress.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(t.TempDir(), "agent.count")
	configText := fmt.Sprintf(`version: 1

agent:
  default: resilience
  adapters:
    resilience:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_SEQUENCE: %s
        LOOP_FAKE_AGENT_COUNT_FILE: %s

run:
  maxIterations: %d
  repairAttempts: %d

git:
  baseBranch: develop
  worktree: false
  integration:
    mode: local_merge
`, yamlSingleQuote(agentCommand), yamlSingleQuote(opts.Sequence), yamlSingleQuote(countFile), opts.MaxIterations, opts.RepairAttempts)
	if opts.ValidationYAML != "" {
		configText += "\n" + opts.ValidationYAML
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), configText)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add resilience fixture")
}

func readLatestRunState(t *testing.T, repo string) runstate.State {
	t.Helper()
	runID, err := latestRun(filepath.Join(repo, ".loop", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".loop", "runs", runID, "run-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state runstate.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func latestIterationDir(t *testing.T, repo, iteration string) string {
	t.Helper()
	runID, err := latestRun(filepath.Join(repo, ".loop", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repo, ".loop", "runs", runID, "iterations", iteration)
}

func countEventType(t *testing.T, iterDir, eventType string) int {
	t.Helper()
	events := readText(t, filepath.Join(iterDir, "agent-events.jsonl"))
	return strings.Count(events, `"type":"`+eventType+`"`)
}
