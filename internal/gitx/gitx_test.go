package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepoRootAndCleanIgnoresRuntime(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	root, err := RepoRoot(ctx, filepath.Join(repo, "sub"))
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	gotRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != wantRoot {
		t.Fatalf("root = %q, want %q", gotRoot, wantRoot)
	}

	mustWrite(t, filepath.Join(repo, ".loop", "runs", "r1", "worklog.md"), "runtime")
	mustWrite(t, filepath.Join(repo, ".loop", "loop.db"), "global memory")
	mustWrite(t, filepath.Join(repo, ".loop", "loop.db-wal"), "global memory wal")
	mustWrite(t, filepath.Join(repo, "feature.txt"), "dirty")

	result, err := (Runner{Dir: repo}).CheckClean(ctx, CleanOptions{IgnoreRuntime: true})
	if err != nil {
		t.Fatalf("CheckClean: %v", err)
	}
	if result.Clean {
		t.Fatal("tree reported clean with a non-runtime dirty file")
	}
	if len(result.Dirty) != 1 || result.Dirty[0].Path != "feature.txt" {
		t.Fatalf("dirty = %#v, want only feature.txt", result.Dirty)
	}

	os.Remove(filepath.Join(repo, "feature.txt"))
	result, err = (Runner{Dir: repo}).CheckClean(ctx, CleanOptions{IgnoreRuntime: true})
	if err != nil {
		t.Fatalf("CheckClean runtime-only: %v", err)
	}
	if !result.Clean {
		t.Fatalf("runtime-only dirty files should be ignored: %#v", result.Dirty)
	}
}

func TestBranchNamesAndCollisions(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	r := Runner{Dir: repo}

	if got := InitialBranchName(7); got != "wip/0007" {
		t.Fatalf("InitialBranchName = %q", got)
	}
	final, err := FinalBranchName("feat", "Add Usage Report Command!")
	if err != nil {
		t.Fatalf("FinalBranchName: %v", err)
	}
	if final != "feat/add-usage-report-command" {
		t.Fatalf("final = %q", final)
	}

	if _, err := FinalBranchName("", "feature/add usage report command"); err == nil {
		t.Fatal("feature alias should not be accepted")
	}

	if err := r.CreateBranch(ctx, final, "HEAD"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	got, err := r.UniqueBranchName(ctx, final, "0007")
	if err != nil {
		t.Fatalf("UniqueBranchName: %v", err)
	}
	if got != "feat/add-usage-report-command-0007" {
		t.Fatalf("unique = %q", got)
	}

	if err := r.RenameBranch(ctx, final, "fix/renamed"); err != nil {
		t.Fatalf("RenameBranch: %v", err)
	}
	exists, err := r.BranchExists(ctx, "fix/renamed")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if !exists {
		t.Fatal("renamed branch not found")
	}
	git(t, repo, "checkout", "main")
	if err := r.DeleteBranch(ctx, "fix/renamed", true); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
}

func TestListCommitsAndSquashMerge(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	r := Runner{Dir: repo}

	if err := r.CreateBranch(ctx, "wip/0001", "main"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	mustWrite(t, filepath.Join(repo, "change.txt"), "one")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "F: add change")

	commits, err := r.ListCommits(ctx, "main", "wip/0001")
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(commits) != 1 || commits[0].Subject != "F: add change" {
		t.Fatalf("commits = %#v", commits)
	}

	if err := r.SquashMerge(ctx, "main", "wip/0001", "Add change", false); err != nil {
		t.Fatalf("SquashMerge: %v", err)
	}
	out := git(t, repo, "log", "--format=%s", "-1")
	if out != "Add change\n" {
		t.Fatalf("merge commit subject = %q", out)
	}
}

func TestMainBranchReadsRemoteHead(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	r := Runner{Dir: repo}
	git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	got, err := r.MainBranch(ctx)
	if err != nil {
		t.Fatalf("MainBranch: %v", err)
	}
	if got != "main" {
		t.Fatalf("main branch = %q, want main", got)
	}
}

func TestMainBranchDoesNotFallBackToLocalMain(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	r := Runner{Dir: repo}

	if got, err := r.MainBranch(ctx); err == nil {
		t.Fatalf("MainBranch = %q, want inference error without remote HEAD", got)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "loop@example.test")
	git(t, dir, "config", "user.name", "Loop Test")
	mustWrite(t, filepath.Join(dir, "README.md"), "hello\n")
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-m", "F: initial")
	return dir
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
