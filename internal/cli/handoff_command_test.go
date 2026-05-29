package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/workflow"
)

func TestHandoffWriteTaskResultUsesTaskDirectoryAudit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	iterDir := filepath.Join(root, ".loop", "runs", "run-1", "iterations", "0001")
	taskDir := filepath.Join(iterDir, "tasks", "0001")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskData, err := workflow.MarshalIndent(workflow.Task{
		ID:            "fake-task",
		Title:         "Fake task",
		Description:   "Complete a fake task.",
		Acceptance:    []string{"The fake task is done."},
		CommitType:    "F",
		CommitMessage: "complete fake task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.json"), taskData, 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"schema_version":1,"task_id":"fake-task","status":"completed","summary":"Completed fake task."}`
	if _, err := captureStdout(t, func() error {
		return commandHandoff(ctx, globals{}, []string{"write", "task-result", "--iteration-dir", iterDir, "--task", "fake-task", "--value", payload})
	}); err != nil {
		t.Fatalf("handoff write task-result: %v", err)
	}

	resultPath := filepath.Join(taskDir, "task-result.json")
	if data := readText(t, resultPath); !strings.Contains(data, `"task_id": "fake-task"`) {
		t.Fatalf("task-result audit = %s", data)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "task-results")); !os.IsNotExist(err) {
		t.Fatalf("legacy task-results directory should not exist: %v", err)
	}
}

func TestHandoffWriteRemovesUntrackedRootSourceFile(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	taskDir := filepath.Join(iterDir, "tasks", "0001")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskData, err := workflow.MarshalIndent(workflow.Task{
		ID:            "fake-task",
		Title:         "Fake task",
		Description:   "Complete a fake task.",
		Acceptance:    []string{"The fake task is done."},
		CommitType:    "F",
		CommitMessage: "complete fake task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.json"), taskData, 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(repo, "task-result.json")
	mustWrite(t, sourcePath, `{"schema_version":1,"task_id":"fake-task","status":"completed","summary":"Completed fake task."}`)
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandHandoff(ctx, globals{}, []string{"write", "task-result", "--iteration-dir", iterDir, "--task", "fake-task", "--file", "task-result.json"})
	}); err != nil {
		t.Fatalf("handoff write task-result: %v", err)
	}

	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("untracked handoff source should be removed, err=%v", err)
	}
	if data := readText(t, filepath.Join(taskDir, "task-result.json")); !strings.Contains(data, `"task_id": "fake-task"`) {
		t.Fatalf("task-result audit = %s", data)
	}
}
