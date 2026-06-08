package cli

import (
	"context"
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
	captureDir := t.TempDir()
	instructionPath := filepath.Join(repo, "secret", "private-task.md")
	instructionText := "# Secret Task\n\nDo not leak this path.\n"
	mustWrite(t, instructionPath, instructionText)
	writeCapturePlannerConfig(t, repo, captureDir, 1)
	git(t, repo, "add", "secret/private-task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add prompt snapshot fixture")

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
		return commandRun(ctx, globals{Agent: "capture", JSON: true, NoColor: true}, []string{instructionPath, "--goal", "ship it"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	runID, err := latestRun(testRunsDir(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	iterDir := testIterationDir(t, repo, runID, "0001")
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

	stateBytes, err := os.ReadFile(filepath.Join(testRunDir(t, repo, runID), "run-state.json"))
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
	writeCapturePlannerConfig(t, repo, captureDir, 1)
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
	stdinText := string(stdinBytes)
	envText := string(envBytes)
	combined := stdinText + "\n" + envText
	for _, notWant := range []string{
		instructionPath,
		"private-task.md",
		"LOOP_INSTRUCTION_FILE",
		"LOOP_INSTRUCTION_PATH",
		"LOOP_INSTRUCTION_REL",
		"prompt.md",
	} {
		if strings.Contains(combined, notWant) {
			t.Fatalf("agent context leaked %q:\n%s", notWant, combined)
		}
	}
	for _, want := range []string{
		"LOOP_ITERATION_DIR=",
		artifactdb.ActiveIterationDirEnv + "=",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf("agent env missing %q:\n%s", want, envText)
		}
	}
	if !strings.Contains(stdinText, "Use the `loop` skill") {
		t.Fatalf("agent stdin did not include bootstrap:\n%s", stdinBytes)
	}
}

func writeCapturePlannerConfig(t *testing.T, repo, captureDir string, maxIterations int) {
	t.Helper()
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
        LOOP_CAPTURE_AGENT: "1"
        LOOP_CAPTURE_DIR: %s

run:
  maxIterations: %d

git:
  baseBranch: develop
`, yamlSingleQuote(agentCommand), yamlSingleQuote(captureDir), maxIterations))
}

func TestHelperProcessCaptureAgent(t *testing.T) {
	if os.Getenv("LOOP_CAPTURE_AGENT") != "1" {
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	captureDir := os.Getenv("LOOP_CAPTURE_DIR")
	_ = os.MkdirAll(captureDir, 0o755)
	_ = os.WriteFile(filepath.Join(captureDir, "stdin.txt"), stdin, 0o644)
	_ = os.WriteFile(filepath.Join(captureDir, "env.txt"), []byte(strings.Join(os.Environ(), "\n")), 0o644)
	if os.Getenv("LOOP_ROLE") != "planner" {
		os.Exit(1)
	}
	result := `{
  "schema_version": 1,
  "summary": "Capture planner context.",
  "goal_evaluation": "Captured planner context for test.",
  "tasks": []
}
`
	iterDir, err := resolveIterationDir(context.Background(), globals{}, "", os.Getenv("LOOP_RUN_ID"), os.Getenv("LOOP_ITERATION_ID"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runID, iterationID := artifactdb.ParseIterationDir(iterDir)
	_ = artifactdb.WriteRoleHandoff(artifactdb.GlobalDBPathForIteration(iterDir), runID, iterationID, "task-tree", "", result)
	os.Exit(0)
}
