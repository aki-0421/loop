package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestRunSnapshotsInstructionPromptWithoutPersistingSourcePath(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	instructionPath := filepath.Join(repo, "secret", "private-task.md")
	instructionText := "# Secret Task\n\nDo not leak this path.\n"
	mustWrite(t, instructionPath, instructionText)

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{JSON: true, NoColor: true}, []string{instructionPath, "--dry-run", "--goal", "ship it"})
	}); err != nil {
		t.Fatalf("loop run --dry-run: %v", err)
	}

	runID, err := latestRun(filepath.Join(repo, ".loop", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	iterDir := filepath.Join(repo, ".loop", "runs", runID, "iterations", "0001")
	promptBytes, err := os.ReadFile(filepath.Join(iterDir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(promptBytes) != instructionText {
		t.Fatalf("prompt.md = %q, want instruction snapshot %q", promptBytes, instructionText)
	}
	if strings.Contains(string(promptBytes), "Loop Instruction") || strings.Contains(string(promptBytes), "Run Goal") || strings.Contains(string(promptBytes), instructionPath) {
		t.Fatalf("prompt.md leaked harness or path content:\n%s", promptBytes)
	}

	instructionOut, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"read", "instruction", "--iteration-dir", iterDir})
	})
	if err != nil {
		t.Fatalf("read instruction: %v", err)
	}
	if instructionOut != instructionText {
		t.Fatalf("instruction read = %q, want %q", instructionOut, instructionText)
	}
	_, err = captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"path", "instruction", "--iteration-dir", iterDir})
	})
	if err == nil || !strings.Contains(err.Error(), "path is not exposed") {
		t.Fatalf("instruction path should be hidden, got err=%v", err)
	}

	runtimeText, err := artifactdb.Read(iterDir, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	var runtime map[string]any
	if err := json.Unmarshal([]byte(runtimeText), &runtime); err != nil {
		t.Fatal(err)
	}
	if got := runtime["base_branch"]; got != "develop" {
		t.Fatalf("runtime base_branch = %#v, want develop from command start branch", got)
	}
	for _, key := range []string{"instruction_file", "instruction_path", "instruction_rel"} {
		if _, ok := runtime[key]; ok {
			t.Fatalf("runtime leaked %s: %#v", key, runtime)
		}
	}
	if strings.Contains(runtimeText, instructionPath) || strings.Contains(runtimeText, "private-task.md") {
		t.Fatalf("runtime leaked instruction path:\n%s", runtimeText)
	}

	stateBytes, err := os.ReadFile(filepath.Join(repo, ".loop", "runs", runID, "run-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateBytes), "instruction_path") || strings.Contains(string(stateBytes), instructionPath) || strings.Contains(string(stateBytes), "private-task.md") {
		t.Fatalf("run-state leaked instruction path:\n%s", stateBytes)
	}

	statusOut, err := captureStdout(t, func() error {
		return commandStatus(ctx, globals{JSON: true, NoColor: true}, []string{runID})
	})
	if err != nil {
		t.Fatalf("loop status: %v", err)
	}
	if strings.Contains(statusOut, "instruction") || strings.Contains(statusOut, "private-task.md") || strings.Contains(statusOut, instructionPath) {
		t.Fatalf("status leaked instruction path:\n%s", statusOut)
	}
}

func TestAgentReceivesBootstrapWithoutInstructionPathOrPromptPath(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	captureDir := t.TempDir()
	instructionPath := filepath.Join(repo, "secret", "private-task.md")
	mustWrite(t, instructionPath, "# Secret Task\n\nDo not leak this path.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: capture
  adapters:
    capture:
      command: %s
      args: [-test.run=TestHelperProcessCaptureAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_CAPTURE_AGENT: "1"
        LOOP_TEST_CAPTURE_DIR: %s

run:
  maxIterations: 1

git:
  baseBranch: develop
`, yamlSingleQuote(agentCommand), yamlSingleQuote(captureDir)))
	git(t, repo, "add", "secret/private-task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add capture fixture")

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "capture", JSON: true, NoColor: true}, []string{instructionPath})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	stdinBytes, err := os.ReadFile(filepath.Join(captureDir, "stdin.txt"))
	if err != nil {
		t.Fatal(err)
	}
	envBytes, err := os.ReadFile(filepath.Join(captureDir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	combined := string(stdinBytes) + "\n" + string(envBytes)
	for _, notWant := range []string{
		instructionPath,
		"private-task.md",
		"LOOP_INSTRUCTION_FILE",
		"LOOP_INSTRUCTION_PATH",
		"LOOP_INSTRUCTION_REL",
		"LOOP_ITERATION_DIR",
		"prompt.md",
	} {
		if strings.Contains(combined, notWant) {
			t.Fatalf("agent context leaked %q:\n%s", notWant, combined)
		}
	}
	if !strings.Contains(string(stdinBytes), "Use the `loop` skill") {
		t.Fatalf("agent stdin did not include bootstrap:\n%s", stdinBytes)
	}
}

func TestHelperProcessCaptureAgent(t *testing.T) {
	if os.Getenv("LOOP_TEST_CAPTURE_AGENT") != "1" {
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	captureDir := os.Getenv("LOOP_TEST_CAPTURE_DIR")
	_ = os.MkdirAll(captureDir, 0o755)
	_ = os.WriteFile(filepath.Join(captureDir, "stdin.txt"), stdin, 0o644)
	_ = os.WriteFile(filepath.Join(captureDir, "env.txt"), []byte(strings.Join(os.Environ(), "\n")), 0o644)
	result := `{
  "schema_version": 1,
  "status": "no_change",
  "summary_sentence": "Capture agent context",
  "should_fully_stop": true,
  "goal_evaluation": "Captured agent context for test.",
  "branch": {"initial_name": "wip/0001", "kind": "test", "slug": "capture-agent"},
  "commits": [],
  "validation": {"status": "skipped", "commands": []},
  "artifacts": {"summary": "summary"},
  "assumptions": []
}
`
	iterDir, err := resolveIterationDir(context.Background(), globals{}, "", os.Getenv("LOOP_RUN_ID"), os.Getenv("LOOP_ITERATION_ID"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runID, iterationID := artifactdb.ParseIterationDir(iterDir)
	_ = artifactdb.WriteResultHandoff(artifactdb.GlobalDBPathForIteration(iterDir), runID, iterationID, result)
	os.Exit(0)
}
