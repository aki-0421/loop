package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/gitx"
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
	active := []string{
		"runtime.json",
		"plan.md",
		"todo.md",
		"worklog.md",
		"validation.md",
		"validation-output-test.log",
		"pr-title.txt",
		"pr-body.md",
		"agent-prompt-audit.md",
	}
	for _, name := range active {
		mustWrite(t, filepath.Join(iterDir, name), "active\n")
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

	if issues := cleanupDisposableIterationFiles(iterDir, filepath.Join(iterDir, "agent-events.jsonl")); len(issues) != 0 {
		t.Fatalf("cleanup issues: %v", issues)
	}
	for _, name := range active {
		if _, err := os.Stat(filepath.Join(iterDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed, err=%v", name, err)
		}
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
	if !strings.Contains(string(events), "iteration.active_files.cleanup.completed") || !strings.Contains(string(events), "plan.md") {
		t.Fatalf("cleanup event missing:\n%s", events)
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
