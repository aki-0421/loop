package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/workflow"
)

func TestTaskTodoCompleteCreatesCommit(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, taskDir := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Add marker", "--acceptance", "marker.txt exists.", "add", "marker", "file"}); err != nil {
		t.Fatalf("todo add: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	mustWrite(t, filepath.Join(repo, "marker.txt"), "marker\n")
	if err := commandTask(ctx, globals{}, []string{"todo", "complete", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo complete: %v", err)
	}

	if got := strings.TrimSpace(git(t, repo, "log", "-1", "--format=%s")); got != "F: add marker file" {
		t.Fatalf("commit subject = %q", got)
	}
	todos := readTaskTodoForTest(t, taskDir)
	if len(todos.Items) != 1 || todos.Items[0].Status != "done" || todos.Items[0].CommitSHA == "" {
		t.Fatalf("task TODO not completed with commit: %#v", todos)
	}
}

func TestTaskTodoRejectsNoChangeComplete(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, _ := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "T", "--title", "Add tests", "--acceptance", "tests exist.", "add", "tests"}); err != nil {
		t.Fatalf("todo add: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	err := commandTask(ctx, globals{}, []string{"todo", "complete", "1", "--iteration-dir", iterDir, "--task", "fake-task"})
	if err == nil || !strings.Contains(err.Error(), "no repository changes") {
		t.Fatalf("complete without changes error = %v", err)
	}
}

func TestTaskTodoAddAfterAndMoveReorderPendingTodos(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, taskDir := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Wire route", "--acceptance", "route delegates to the screen.", "wire", "route"}); err != nil {
		t.Fatalf("todo add first: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--after", "0", "--type", "T", "--title", "Add screen tests", "--acceptance", "screen tests cover the fixtures.", "add", "screen", "tests"}); err != nil {
		t.Fatalf("todo add at top: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "move", "2", "--iteration-dir", iterDir, "--task", "fake-task", "--after", "0"}); err != nil {
		t.Fatalf("todo move: %v", err)
	}

	todos := readTaskTodoForTest(t, taskDir)
	if len(todos.Items) != 2 {
		t.Fatalf("task TODO count = %d, want 2", len(todos.Items))
	}
	if todos.Items[0].Title != "Wire route" || todos.Items[1].Title != "Add screen tests" {
		t.Fatalf("task TODO order = %#v", todos.Items)
	}
}

func TestTaskTodoMoveRejectsStartedWork(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, _ := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Add marker", "--acceptance", "marker.txt exists.", "add", "marker"}); err != nil {
		t.Fatalf("todo add first: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "T", "--title", "Add marker tests", "--acceptance", "marker tests exist.", "add", "marker", "tests"}); err != nil {
		t.Fatalf("todo add second: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	err := commandTask(ctx, globals{}, []string{"todo", "move", "2", "--iteration-dir", iterDir, "--task", "fake-task", "--after", "0"})
	if err == nil || !strings.Contains(err.Error(), "cannot move task TODOs after task work has started") {
		t.Fatalf("move after work started error = %v", err)
	}
}

func TestTaskMergeRejectsIncompleteTodos(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, _ := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Add marker", "--acceptance", "marker.txt exists.", "add", "marker", "file"}); err != nil {
		t.Fatalf("todo add: %v", err)
	}
	err := commandTask(ctx, globals{}, []string{"merge", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "complete", "fake", "task"})
	if err == nil || !strings.Contains(err.Error(), "not done") {
		t.Fatalf("merge with incomplete TODO error = %v", err)
	}
}

func setupTaskTodoIteration(t *testing.T) (string, string, string) {
	t.Helper()
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	git(t, repo, "checkout", "-b", "task/0001-fake-task-attempt-1")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	taskDir := filepath.Join(iterDir, "tasks", "0001")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskData, err := workflow.MarshalIndent(workflow.Task{
		ID:          "fake-task",
		Title:       "Fake task",
		Description: "Complete a fake task.",
		Acceptance:  []string{"The fake task is done."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.json"), taskData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := artifactdb.Write(iterDir, "runtime", `{
  "run_id": "run-1",
  "iteration_id": "0001",
  "base_branch": "develop",
  "initial_branch": "wip/0001",
  "current_branch": "task/0001-fake-task-attempt-1",
  "workdir": "`+filepath.ToSlash(repo)+`",
  "iteration_worktree": "`+filepath.ToSlash(repo)+`",
  "task_id": "fake-task",
  "task_dir": "`+filepath.ToSlash(taskDir)+`",
  "integration_mode": "local_merge",
  "pull_request_mode": false,
  "role_orchestrated": true
}
`); err != nil {
		t.Fatal(err)
	}
	return repo, iterDir, taskDir
}

func readTaskTodoForTest(t *testing.T, taskDir string) taskTodoFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(taskDir, "task-todo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var todos taskTodoFile
	if err := json.Unmarshal(data, &todos); err != nil {
		t.Fatal(err)
	}
	return todos
}
