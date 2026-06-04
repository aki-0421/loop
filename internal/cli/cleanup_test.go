package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/runstate"
)

func TestIterationCleanupRemovesCancelledWorktreeAndBranch(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	runner := gitx.Runner{Dir: repo}
	worktree := filepath.Join(repo, ".loop", "worktrees", "run", "0001")
	git(t, repo, "worktree", "add", "-b", "wip/0001", worktree, "develop")
	mustWrite(t, filepath.Join(worktree, "dirty.txt"), "dirty\n")

	cleanup := iterationCleanup{
		Active:       true,
		RootRunner:   runner,
		BaseBranch:   "develop",
		Branch:       "wip/0001",
		WorktreePath: worktree,
	}
	if issues := cleanup.cleanup(ctx); len(issues) > 0 {
		t.Fatalf("cleanup issues: %v", issues)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, err=%v", err)
	}
	assertBranchMissing(t, repo, "wip/0001")
}

func TestIterationCleanupResetsCancelledBranchInMainWorktree(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	runner := gitx.Runner{Dir: repo}
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	mustWrite(t, filepath.Join(repo, "dirty.txt"), "dirty\n")

	cleanup := iterationCleanup{
		Active:     true,
		RootRunner: runner,
		BaseBranch: "develop",
		Branch:     "wip/0001",
		WorkDir:    repo,
	}
	if issues := cleanup.cleanup(ctx); len(issues) > 0 {
		t.Fatalf("cleanup issues: %v", issues)
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "develop" {
		t.Fatalf("current branch = %q, want develop", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "dirty.txt")); !os.IsNotExist(err) {
		t.Fatalf("dirty file should be removed, err=%v", err)
	}
	assertBranchMissing(t, repo, "wip/0001")
}

func TestIterationCleanupResetsUncommittedSquashMerge(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	runner := gitx.Runner{Dir: repo}
	baseHead, err := runner.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	mustWrite(t, filepath.Join(repo, "feature.txt"), "feature\n")
	git(t, repo, "add", "feature.txt")
	git(t, repo, "commit", "-m", "F: add feature")
	git(t, repo, "checkout", "develop")
	git(t, repo, "merge", "--squash", "wip/0001")

	cleanup := iterationCleanup{
		Active:            true,
		DirectIntegrating: true,
		RootRunner:        runner,
		BaseBranch:        "develop",
		BaseHead:          baseHead,
		Branch:            "wip/0001",
		WorkDir:           repo,
	}
	if issues := cleanup.cleanup(ctx); len(issues) > 0 {
		t.Fatalf("cleanup issues: %v", issues)
	}
	head, err := runner.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if head != baseHead {
		t.Fatalf("HEAD = %s, want base %s", head, baseHead)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("squash file should be removed, err=%v", err)
	}
	assertBranchMissing(t, repo, "wip/0001")
}

func TestIterationCleanupOnErrorRemovesTaskResourcesAndActiveTemp(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	runner := gitx.Runner{Dir: repo}
	iterationWorktree := filepath.Join(repo, ".loop", "worktrees", "run", "0001", "iteration")
	taskWorktree := filepath.Join(repo, ".loop", "worktrees", "run", "0001", "tasks", "retry-task", "attempt-1")
	git(t, repo, "worktree", "add", "-b", "wip/0001", iterationWorktree, "develop")
	git(t, repo, "worktree", "add", "-b", "task/0001-retry-task-attempt-1", taskWorktree, "develop")
	activeDir := filepath.Join(t.TempDir(), "loop-active")
	mustWrite(t, filepath.Join(activeDir, ".loop-active-temp"), "1\n")
	mustWrite(t, filepath.Join(activeDir, "runtime.json"), "{}\n")
	lockDir := filepath.Join(repo, ".loop", "locks", "run-0001-task-merge.lock")
	mustWrite(t, filepath.Join(lockDir, "state.json"), "{}\n")
	statePath := filepath.Join(repo, ".loop", "runs", "run", "run-state.json")
	state := runstate.New("run", "", "develop", "fake")
	state.Iterations = append(state.Iterations, runstate.IterationRecord{IterationID: "0001", BranchInitial: "wip/0001", Stage: string(runstate.StagePlanning)})
	cleanup := iterationCleanup{
		Active:            true,
		RootRunner:        runner,
		BaseBranch:        "develop",
		Branch:            "wip/0001",
		WorktreePath:      iterationWorktree,
		TaskWorktreesRoot: filepath.Join(repo, ".loop", "worktrees", "run", "0001", "tasks"),
		TaskBranchPrefix:  "task/0001-",
		LockDir:           lockDir,
		ActiveDir:         activeDir,
		EventLogPath:      filepath.Join(repo, ".loop", "runs", "run", "iterations", "0001", "agent-events.jsonl"),
	}
	retErr := errors.New("planner failed")

	cleanup.OnExit(ctx, statePath, &state, &retErr)

	for _, path := range []string{iterationWorktree, taskWorktree, lockDir, activeDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed, err=%v", path, err)
		}
	}
	assertBranchMissing(t, repo, "wip/0001")
	assertBranchMissing(t, repo, "task/0001-retry-task-attempt-1")
	if state.Stage != runstate.StageFailed || state.Iterations[0].Stage != string(runstate.StageFailed) {
		t.Fatalf("state = %#v, want failed cleanup state", state)
	}
	events := readText(t, filepath.Join(repo, ".loop", "runs", "run", "iterations", "0001", "agent-events.jsonl"))
	for _, want := range []string{"run.error_cleanup.started", "iteration.active_temp.cleanup.completed", "run.error_cleanup.completed"} {
		if !strings.Contains(events, want) {
			t.Fatalf("cleanup events missing %s:\n%s", want, events)
		}
	}
}

func TestRemoveWorktreeBeforePRIntegrationLeavesBranchDeletable(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	runner := gitx.Runner{Dir: repo}
	worktree := filepath.Join(repo, ".loop", "worktrees", "run", "0001")
	git(t, repo, "worktree", "add", "-b", "feat/pr-branch", worktree, "develop")
	mustWrite(t, filepath.Join(worktree, "build-output.txt"), "generated\n")

	cleanup := iterationCleanup{WorktreePath: worktree, WorkDir: worktree}
	if err := removeWorktreeBeforePRIntegration(ctx, runner, &cleanup, worktree, repo); err != nil {
		t.Fatalf("removeWorktreeBeforePRIntegration: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, err=%v", err)
	}
	if cleanup.WorktreePath != "" {
		t.Fatalf("cleanup worktree path = %q, want empty", cleanup.WorktreePath)
	}
	if cleanup.WorkDir != repo {
		t.Fatalf("cleanup work dir = %q, want %q", cleanup.WorkDir, repo)
	}
	assertBranchExists(t, repo, "feat/pr-branch")
	if err := runner.DeleteBranch(ctx, "feat/pr-branch", true); err != nil {
		t.Fatalf("branch should be deletable after worktree removal: %v", err)
	}
}

func TestCleanupDisposableIterationFilesPreservesAuditFiles(t *testing.T) {
	iterDir := t.TempDir()
	activeDir := filepath.Join(t.TempDir(), "loop-active")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(activeDir, ".loop-active-temp"), "1\n")
	active := []string{
		"runtime.json",
		"plan.md",
		"todo.md",
		"validation.md",
		"validation-output-test.log",
		"pr-title.txt",
		"pr-body.md",
		"agent-prompt-audit.md",
	}
	for _, name := range active {
		mustWrite(t, filepath.Join(activeDir, name), "active\n")
	}
	preserved := []string{
		"prompt.md",
		"effective-config.yaml",
		"agent-events.jsonl",
		"errors.log",
		"pr-state.json",
		"pr-checks.json",
		"pr-check-log.txt",
		"github-updates.md",
	}
	for _, name := range preserved {
		mustWrite(t, filepath.Join(iterDir, name), "audit\n")
	}

	if issues := cleanupDisposableIterationFiles(activeDir, filepath.Join(iterDir, "agent-events.jsonl")); len(issues) != 0 {
		t.Fatalf("cleanup issues: %v", issues)
	}
	if _, err := os.Stat(activeDir); !os.IsNotExist(err) {
		t.Fatalf("active temp dir should be removed, err=%v", err)
	}
	for _, name := range preserved {
		if _, err := os.Stat(filepath.Join(iterDir, name)); err != nil {
			t.Fatalf("%s should be preserved: %v", name, err)
		}
	}
	events, err := os.ReadFile(filepath.Join(iterDir, "agent-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), "iteration.active_temp.cleanup.completed") || !strings.Contains(string(events), "plan.md") {
		t.Fatalf("cleanup event missing:\n%s", events)
	}
}

func TestPromptPathsPlaceDisposableArtifactsInActiveTempDir(t *testing.T) {
	iterDir := filepath.Join(t.TempDir(), ".loop", "runs", "run-1", "iterations", "0001")
	activeDir := filepath.Join(os.TempDir(), "loop-test-active")
	paths := promptPathsWithActive(iterDir, activeDir)
	for _, path := range []string{paths.Runtime, paths.Plan, paths.Todo, paths.Validation, paths.PRTitle, paths.PRBody} {
		if !strings.HasPrefix(path, activeDir+string(filepath.Separator)) {
			t.Fatalf("disposable path %q should be under active temp dir %q", path, activeDir)
		}
	}
	for _, path := range []string{paths.Prompt, paths.EffectiveConfig, paths.Events, paths.Errors} {
		if !strings.HasPrefix(path, iterDir+string(filepath.Separator)) {
			t.Fatalf("audit path %q should be under iteration dir %q", path, iterDir)
		}
	}
}

func TestRemoveWorktreeBeforePRIntegrationToleratesAlreadyRemovedPath(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	runner := gitx.Runner{Dir: repo}
	worktree := filepath.Join(repo, ".loop", "worktrees", "run", "0001")
	git(t, repo, "worktree", "add", "-b", "feat/pr-branch", worktree, "develop")
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}

	cleanup := iterationCleanup{WorktreePath: worktree, WorkDir: worktree}
	if err := removeWorktreeBeforePRIntegration(ctx, runner, &cleanup, worktree, repo); err != nil {
		t.Fatalf("removeWorktreeBeforePRIntegration should tolerate an already removed path: %v", err)
	}
	if cleanup.WorktreePath != "" {
		t.Fatalf("cleanup worktree path = %q, want empty", cleanup.WorktreePath)
	}
	if cleanup.WorkDir != repo {
		t.Fatalf("cleanup work dir = %q, want %q", cleanup.WorkDir, repo)
	}
}

func TestPreparePRMergeChecksOutBaseSoCurrentBranchCanBeDeleted(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	runner := gitx.Runner{Dir: repo}
	git(t, repo, "checkout", "-b", "feat/pr-branch", "develop")

	cleanup := iterationCleanup{WorkDir: repo}
	if err := preparePRMerge(ctx, runner, &cleanup, "", repo, "develop"); err != nil {
		t.Fatalf("preparePRMerge: %v", err)
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "develop" {
		t.Fatalf("current branch = %q, want develop", got)
	}
	if cleanup.WorkDir != repo {
		t.Fatalf("cleanup work dir = %q, want %q", cleanup.WorkDir, repo)
	}
	if err := runner.DeleteBranch(ctx, "feat/pr-branch", true); err != nil {
		t.Fatalf("branch should be deletable after base checkout: %v", err)
	}
}

func TestEnsureIterationBranchRejectsAgentBranchSwitch(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	runner := gitx.Runner{Dir: repo}
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	git(t, repo, "checkout", "-b", "feat/agent-renamed")

	err := ensureIterationBranch(ctx, runner, "wip/0001")
	if err == nil {
		t.Fatal("ensureIterationBranch should reject branch switches")
	}
	if !strings.Contains(err.Error(), "loop branch rename") {
		t.Fatalf("error = %q", err)
	}
}

func newCleanupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, ".home"))
	git(t, dir, "init", "-b", "develop")
	git(t, dir, "config", "user.email", "loop@example.test")
	git(t, dir, "config", "user.name", "Loop Test")
	mustWrite(t, filepath.Join(dir, "README.md"), "hello\n")
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-m", "F: initial")
	return dir
}

func assertBranchMissing(t *testing.T, repo, branch string) {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Fatalf("branch %s should be deleted", branch)
	}
}

func assertRemoteBranchMissing(t *testing.T, repo, branch string) {
	t.Helper()
	cmd := exec.Command("git", "ls-remote", "--exit-code", "--heads", "origin", branch)
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Fatalf("remote branch %s should be deleted from origin", branch)
	}
}

func assertRemoteTrackingBranchMissing(t *testing.T, repo, branch string) {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Fatalf("remote-tracking branch origin/%s should be pruned", branch)
	}
}

func assertBranchExists(t *testing.T, repo, branch string) {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		t.Fatalf("branch %s should exist: %v", branch, err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
