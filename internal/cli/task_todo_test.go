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
	if err := commandTask(ctx, globals{}, []string{"todo", "stage", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo stage: %v", err)
	}
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

func TestTaskTodoCompleteRequiresStagedChanges(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, _ := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Add marker", "--acceptance", "marker.txt exists.", "add", "marker"}); err != nil {
		t.Fatalf("todo add: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	mustWrite(t, filepath.Join(repo, "marker.txt"), "marker\n")

	err := commandTask(ctx, globals{}, []string{"todo", "complete", "1", "--iteration-dir", iterDir, "--task", "fake-task"})
	if err == nil || !strings.Contains(err.Error(), "run loop task todo stage <n> first") {
		t.Fatalf("complete without staged changes error = %v", err)
	}
}

func TestTaskTodoNoCommitCompletesWithoutCommit(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, taskDir := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)
	before := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--work-type", "no_commit", "--title", "Run validation", "--acceptance", "The validation command passes."}); err != nil {
		t.Fatalf("todo add no_commit: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	err := commandTask(ctx, globals{}, []string{"todo", "stage", "1", "--iteration-dir", iterDir, "--task", "fake-task"})
	if err == nil || !strings.Contains(err.Error(), "work_type is no_commit") {
		t.Fatalf("stage no_commit error = %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "complete", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo complete no_commit: %v", err)
	}
	after := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("no_commit TODO should not create a commit: before=%s after=%s", before, after)
	}
	todos := readTaskTodoForTest(t, taskDir)
	if todos.Items[0].WorkType != "no_commit" || todos.Items[0].Status != "done" || todos.Items[0].CommitSHA != "" {
		t.Fatalf("no_commit TODO item = %#v", todos.Items[0])
	}
}

func TestTaskTodoNoCommitRejectsRepositoryChanges(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, _ := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--work-type", "no_commit", "--title", "Inspect output", "--acceptance", "The output has been inspected."}); err != nil {
		t.Fatalf("todo add no_commit: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	mustWrite(t, filepath.Join(repo, "unexpected.txt"), "unexpected\n")

	err := commandTask(ctx, globals{}, []string{"todo", "complete", "1", "--iteration-dir", iterDir, "--task", "fake-task"})
	if err == nil || !strings.Contains(err.Error(), "no_commit task TODO cannot complete with repository changes") {
		t.Fatalf("complete dirty no_commit error = %v", err)
	}
}

func TestTaskTodoStageShowsAndRemovesCommitCandidates(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, _ := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Add marker", "--acceptance", "marker.txt exists.", "add", "marker"}); err != nil {
		t.Fatalf("todo add: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	mustWrite(t, filepath.Join(repo, "marker.txt"), "marker\n")
	mustWrite(t, filepath.Join(repo, "build.log"), "temporary build output\n")

	out, err := captureStdout(t, func() error {
		return commandTask(ctx, globals{}, []string{"todo", "stage", "1", "--iteration-dir", iterDir, "--task", "fake-task"})
	})
	if err != nil {
		t.Fatalf("todo stage: %v", err)
	}
	for _, want := range []string{"staged files:", "A marker.txt", "A build.log"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stage output missing %q:\n%s", want, out)
		}
	}

	out, err = captureStdout(t, func() error {
		return commandTask(ctx, globals{}, []string{"todo", "stage", "1", "--iteration-dir", iterDir, "--task", "fake-task", "--remove", "build.log"})
	})
	if err != nil {
		t.Fatalf("todo stage remove: %v", err)
	}
	if strings.Contains(out, "A build.log") {
		t.Fatalf("removed build log should not remain staged:\n%s", out)
	}
	if !strings.Contains(out, "?? build.log") {
		t.Fatalf("removed build log should be reported as unstaged:\n%s", out)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "complete", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo complete: %v", err)
	}
	if got := strings.TrimSpace(git(t, repo, "show", "--name-only", "--format=", "HEAD")); got != "marker.txt" {
		t.Fatalf("committed files = %q, want marker.txt", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "build.log")); err != nil {
		t.Fatalf("build log should remain in the worktree: %v", err)
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

func TestTaskTodoAddAndMoveAfterActiveTodo(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, taskDir := setupTaskTodoIteration(t)
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
	mustWrite(t, filepath.Join(repo, "marker.txt"), "marker\n")
	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--after", "1", "--type", "D", "--title", "Document marker", "--acceptance", "marker docs exist.", "document", "marker"}); err != nil {
		t.Fatalf("todo add after active: %v", err)
	}
	err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--after", "0", "--type", "D", "--title", "Document before marker", "--acceptance", "marker docs exist.", "document", "before", "marker"})
	if err == nil || !strings.Contains(err.Error(), "cannot insert before fixed task TODO 1") {
		t.Fatalf("add before active error = %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "move", "3", "--iteration-dir", iterDir, "--task", "fake-task", "--after", "1"}); err != nil {
		t.Fatalf("move pending after active: %v", err)
	}
	err = commandTask(ctx, globals{}, []string{"todo", "move", "2", "--iteration-dir", iterDir, "--task", "fake-task", "--after", "0"})
	if err == nil || !strings.Contains(err.Error(), "cannot insert before fixed task TODO 1") {
		t.Fatalf("move before active error = %v", err)
	}

	todos := readTaskTodoForTest(t, taskDir)
	if got := []string{todos.Items[0].Title, todos.Items[1].Title, todos.Items[2].Title}; strings.Join(got, "|") != "Add marker|Add marker tests|Document marker" {
		t.Fatalf("task TODO order = %#v", got)
	}
}

func TestTaskTodoRemoveOnlyPendingTodos(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, taskDir := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Add marker", "--acceptance", "marker.txt exists.", "add", "marker"}); err != nil {
		t.Fatalf("todo add first: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "D", "--title", "Document marker", "--acceptance", "marker docs exist.", "document", "marker"}); err != nil {
		t.Fatalf("todo add second: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "remove", "2", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("remove pending todo: %v", err)
	}
	err := commandTask(ctx, globals{}, []string{"todo", "remove", "1", "--iteration-dir", iterDir, "--task", "fake-task"})
	if err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("remove active todo error = %v", err)
	}
	todos := readTaskTodoForTest(t, taskDir)
	if len(todos.Items) != 1 || todos.Items[0].Title != "Add marker" {
		t.Fatalf("task TODO items after remove = %#v", todos.Items)
	}
}

func TestTaskTodoCancelActiveDiscardsChangesAndSkipsToNextTodo(t *testing.T) {
	ctx := context.Background()
	repo, iterDir, taskDir := setupTaskTodoIteration(t)
	withWorkingDir(t, repo)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Add marker", "--acceptance", "marker.txt exists.", "add", "marker"}); err != nil {
		t.Fatalf("todo add first: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "D", "--title", "Document marker", "--acceptance", "marker docs exist.", "document", "marker"}); err != nil {
		t.Fatalf("todo add second: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start: %v", err)
	}
	mustWrite(t, filepath.Join(repo, "marker.txt"), "marker\n")
	mustWrite(t, filepath.Join(repo, "build.log"), "temporary build output\n")
	if err := commandTask(ctx, globals{}, []string{"todo", "stage", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo stage: %v", err)
	}
	err := commandTask(ctx, globals{}, []string{"todo", "cancel", "1", "--iteration-dir", iterDir, "--task", "fake-task"})
	if err == nil || !strings.Contains(err.Error(), "--discard-changes") {
		t.Fatalf("cancel active without discard error = %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "cancel", "1", "--iteration-dir", iterDir, "--task", "fake-task", "--discard-changes"}); err != nil {
		t.Fatalf("cancel active with discard: %v", err)
	}
	for _, path := range []string{"marker.txt", "build.log"} {
		if _, err := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(err) {
			t.Fatalf("%s should be discarded, stat error = %v", path, err)
		}
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "2", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("start after cancelled todo: %v", err)
	}
	todos := readTaskTodoForTest(t, taskDir)
	if todos.Items[0].Status != "cancelled" || todos.Items[0].CancelledAt == "" || todos.Items[1].Status != "active" {
		t.Fatalf("task TODO statuses after cancel/start = %#v", todos.Items)
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

func TestTaskMergePreservesTaskCommitHistory(t *testing.T) {
	ctx := context.Background()
	_, iterDir, taskDir, iterationWorktree, taskWorktree := setupTaskMergeWorktrees(t)
	withWorkingDir(t, taskWorktree)

	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "--title", "Add marker", "--acceptance", "marker.txt exists.", "add", "marker"}); err != nil {
		t.Fatalf("todo add marker: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start marker: %v", err)
	}
	mustWrite(t, filepath.Join(taskWorktree, "marker.txt"), "marker\n")
	if err := commandTask(ctx, globals{}, []string{"todo", "stage", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo stage marker: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "complete", "1", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo complete marker: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "add", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "T", "--title", "Add marker test", "--acceptance", "marker_test.txt exists.", "add", "marker", "test"}); err != nil {
		t.Fatalf("todo add marker test: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "start", "2", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo start marker test: %v", err)
	}
	mustWrite(t, filepath.Join(taskWorktree, "marker_test.txt"), "marker test\n")
	if err := commandTask(ctx, globals{}, []string{"todo", "stage", "2", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo stage marker test: %v", err)
	}
	if err := commandTask(ctx, globals{}, []string{"todo", "complete", "2", "--iteration-dir", iterDir, "--task", "fake-task"}); err != nil {
		t.Fatalf("todo complete marker test: %v", err)
	}

	todos := readTaskTodoForTest(t, taskDir)
	var todoSHAs []string
	for _, item := range todos.Items {
		todoSHAs = append(todoSHAs, item.CommitSHA)
	}
	if err := commandTask(ctx, globals{}, []string{"merge", "--iteration-dir", iterDir, "--task", "fake-task", "--type", "F", "complete", "fake", "task"}); err != nil {
		t.Fatalf("task merge: %v", err)
	}

	mergeSHA := strings.TrimSpace(git(t, iterationWorktree, "rev-parse", "HEAD"))
	parents := strings.Fields(git(t, iterationWorktree, "show", "-s", "--format=%P", "HEAD"))
	if len(parents) != 2 {
		t.Fatalf("task merge should create a merge commit, parents = %#v", parents)
	}
	for _, sha := range todoSHAs {
		if sha == "" {
			t.Fatal("TODO commit SHA should be recorded")
		}
		git(t, iterationWorktree, "merge-base", "--is-ancestor", sha, "HEAD")
	}
	log := git(t, iterationWorktree, "log", "--format=%s", "develop..HEAD")
	for _, want := range []string{"Task 1 done: Fake task", "F: add marker", "T: add marker test"} {
		if !strings.Contains(log, want) {
			t.Fatalf("iteration branch log missing %q:\n%s", want, log)
		}
	}
	record, err := readTaskMergeAudit(taskDir, "fake-task")
	if err != nil {
		t.Fatal(err)
	}
	if record.MergeCommit.SHA != mergeSHA || record.MergeCommit.Subject != "Task 1 done: Fake task" {
		t.Fatalf("merge record merge commit = %#v, want %s Task 1 done: Fake task", record.MergeCommit, mergeSHA)
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

func setupTaskMergeWorktrees(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	repo := newCleanupRepo(t)
	iterationWorktree := filepath.Join(repo, ".loop", "worktrees", "run-1", "0001", "iteration")
	taskWorktree := filepath.Join(repo, ".loop", "worktrees", "run-1", "0001", "tasks", "fake-task", "attempt-1")
	git(t, repo, "worktree", "add", "-b", "wip/0001", iterationWorktree, "develop")
	git(t, repo, "worktree", "add", "-b", "task/0001-fake-task-attempt-1", taskWorktree, "wip/0001")
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
  "workdir": "`+filepath.ToSlash(taskWorktree)+`",
  "iteration_worktree": "`+filepath.ToSlash(iterationWorktree)+`",
  "task_id": "fake-task",
  "task_dir": "`+filepath.ToSlash(taskDir)+`",
  "integration_mode": "local_merge",
  "pull_request_mode": false,
  "role_orchestrated": true
}
`); err != nil {
		t.Fatal(err)
	}
	return repo, iterDir, taskDir, iterationWorktree, taskWorktree
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
