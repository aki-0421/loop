package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/gitx"
)

func TestCopyWorktreeIncludedIgnoredPathsCopiesOnlyIncludedIgnoredPaths(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, ".gitignore"), ".env\n.env.*\nnode_modules/\nignored-not-included.txt\n")
	mustWrite(t, filepath.Join(repo, ".worktreeinclude"), ".env\n.env.*\nnode_modules/\nREADME.md\nignored-missing.txt\n")
	mustWrite(t, filepath.Join(repo, ".env"), "TOKEN=secret\n")
	mustWrite(t, filepath.Join(repo, ".env.local"), "TOKEN=local\n")
	mustWrite(t, filepath.Join(repo, "node_modules", "pkg", "index.js"), "module.exports = 1\n")
	mustWrite(t, filepath.Join(repo, "ignored-not-included.txt"), "ignored but not included\n")
	git(t, repo, "add", ".gitignore", ".worktreeinclude")
	git(t, repo, "commit", "-m", "C: add worktree include fixture")

	worktree := filepath.Join(repo, ".loop", "worktrees", "run", "0001", "iteration")
	git(t, repo, "worktree", "add", "-b", "wip/0001", worktree, "develop")
	events := filepath.Join(repo, ".loop", "runs", "run", "iterations", "0001", "agent-events.jsonl")

	if err := copyWorktreeIncludedIgnoredPaths(ctx, gitx.Runner{Dir: repo}, repo, worktree, events); err != nil {
		t.Fatal(err)
	}

	if got := readText(t, filepath.Join(worktree, ".env")); got != "TOKEN=secret\n" {
		t.Fatalf("copied .env = %q", got)
	}
	if got := readText(t, filepath.Join(worktree, ".env.local")); got != "TOKEN=local\n" {
		t.Fatalf("copied .env.local = %q", got)
	}
	if got := readText(t, filepath.Join(worktree, "node_modules", "pkg", "index.js")); got != "module.exports = 1\n" {
		t.Fatalf("copied node_modules file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(worktree, "ignored-not-included.txt")); !os.IsNotExist(err) {
		t.Fatalf("ignored but not included file should not be copied, err=%v", err)
	}
	if got := readText(t, filepath.Join(worktree, "README.md")); got != "hello\n" {
		t.Fatalf("tracked README should remain from checkout, got %q", got)
	}
	eventsText := readText(t, events)
	for _, want := range []string{`"type":"worktree.include_copied"`, `.env`, `.env.local`, `node_modules`} {
		if !strings.Contains(eventsText, want) {
			t.Fatalf("copy event missing %s:\n%s", want, eventsText)
		}
	}
}

func TestCopyWorktreeIncludedIgnoredPathsNoopsWithoutIncludeFile(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	worktree := filepath.Join(repo, ".loop", "worktrees", "run", "0001", "iteration")
	git(t, repo, "worktree", "add", "-b", "wip/0001", worktree, "develop")

	if err := copyWorktreeIncludedIgnoredPaths(ctx, gitx.Runner{Dir: repo}, repo, worktree, ""); err != nil {
		t.Fatal(err)
	}
}
