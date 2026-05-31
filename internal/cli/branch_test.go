package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/runstate"
)

func TestBranchRenameTracksRuntime(t *testing.T) {
	ctx := context.Background()
	repo, iterDir := setupBranchRenameIteration(t)
	withWorkingDir(t, repo)

	out, err := captureStdout(t, func() error {
		return commandBranch(ctx, globals{}, []string{"rename", "feat/add-user-profile", "--iteration-dir", iterDir})
	})
	if err != nil {
		t.Fatalf("branch rename: %v", err)
	}
	if strings.TrimSpace(out) != "feat/add-user-profile" {
		t.Fatalf("output = %q", out)
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "feat/add-user-profile" {
		t.Fatalf("current branch = %q", got)
	}
	runtime := readRuntimeForTest(t, iterDir)
	if runtime["initial_branch"] != "wip/0001" || runtime["iteration_branch"] != "feat/add-user-profile" || runtime["current_branch"] != "feat/add-user-profile" || runtime["branch_renamed"] != true {
		t.Fatalf("runtime = %#v", runtime)
	}
	state, err := runstate.Read(filepath.Join(repo, ".loop", "runs", "run-1", "run-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Iterations[0].BranchCurrent != "feat/add-user-profile" {
		t.Fatalf("branch_current = %q", state.Iterations[0].BranchCurrent)
	}
	events := readText(t, filepath.Join(iterDir, "agent-events.jsonl"))
	if !strings.Contains(events, `"type":"git.branch.renamed"`) {
		t.Fatalf("rename event missing:\n%s", events)
	}
}

func TestBranchRenameUsesKindFlagAndAllowsRepeatedRenames(t *testing.T) {
	ctx := context.Background()
	repo, iterDir := setupBranchRenameIteration(t)
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandBranch(ctx, globals{}, []string{"rename", "--kind", "feat", "add", "user", "profile", "--iteration-dir", iterDir})
	}); err != nil {
		t.Fatalf("first rename: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return commandBranch(ctx, globals{}, []string{"rename", "--kind", "fix", "handle", "branch", "feedback", "--iteration-dir", iterDir})
	})
	if err != nil {
		t.Fatalf("second rename: %v", err)
	}
	if strings.TrimSpace(out) != "fix/handle-branch-feedback" {
		t.Fatalf("output = %q", out)
	}
	assertBranchMissing(t, repo, "feat/add-user-profile")
	assertBranchExists(t, repo, "fix/handle-branch-feedback")
}

func TestBranchRenameAppliesCollisionSuffix(t *testing.T) {
	ctx := context.Background()
	repo, iterDir := setupBranchRenameIteration(t)
	withWorkingDir(t, repo)
	git(t, repo, "branch", "feat/existing", "develop")

	out, err := captureStdout(t, func() error {
		return commandBranch(ctx, globals{}, []string{"rename", "feat/existing", "--iteration-dir", iterDir})
	})
	if err != nil {
		t.Fatalf("branch rename: %v", err)
	}
	if strings.TrimSpace(out) != "feat/existing-0001" {
		t.Fatalf("output = %q", out)
	}
	assertBranchExists(t, repo, "feat/existing")
	assertBranchExists(t, repo, "feat/existing-0001")
}

func TestBranchRenameRejectsFeatureAlias(t *testing.T) {
	ctx := context.Background()
	repo, iterDir := setupBranchRenameIteration(t)
	withWorkingDir(t, repo)

	err := commandBranch(ctx, globals{}, []string{"rename", "feature/add-user-profile", "--iteration-dir", iterDir})
	if err == nil {
		t.Fatal("expected feature alias to fail")
	}
	if !strings.Contains(err.Error(), `branch kind "feature" is not allowed`) {
		t.Fatalf("error = %v", err)
	}
}

func TestBranchRenameRejectsInvalidKindImmediately(t *testing.T) {
	ctx := context.Background()
	repo, iterDir := setupBranchRenameIteration(t)
	withWorkingDir(t, repo)

	err := commandBranch(ctx, globals{}, []string{"rename", "bug/add-user-profile", "--iteration-dir", iterDir})
	if err == nil {
		t.Fatal("expected invalid kind to fail")
	}
	if !strings.Contains(err.Error(), "allowed kinds") {
		t.Fatalf("error = %v", err)
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "wip/0001" {
		t.Fatalf("current branch = %q", got)
	}
}

func TestBranchRenameRejectsDirectGitBranchChanges(t *testing.T) {
	ctx := context.Background()
	repo, iterDir := setupBranchRenameIteration(t)
	withWorkingDir(t, repo)
	git(t, repo, "checkout", "-b", "feat/direct-change")

	err := commandBranch(ctx, globals{}, []string{"rename", "fix/direct-change", "--iteration-dir", iterDir})
	if err == nil {
		t.Fatal("expected direct branch change to fail")
	}
	if !strings.Contains(err.Error(), "loop runtime tracks") {
		t.Fatalf("error = %v", err)
	}
}

func setupBranchRenameIteration(t *testing.T) (string, string) {
	t.Helper()
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	if err := artifactdb.Write(iterDir, "runtime", `{
  "run_id": "run-1",
  "iteration_id": "0001",
  "base_branch": "develop",
  "initial_branch": "wip/0001",
  "current_branch": "wip/0001",
  "branch_renamed": false,
  "workdir": "`+filepath.ToSlash(repo)+`",
  "integration_mode": "local_merge",
  "pull_request_mode": false
}
`); err != nil {
		t.Fatal(err)
	}
	state := runstate.New("run-1", "", "develop", "codex")
	state.Stage = runstate.StageAgentRunning
	state.Iterations = append(state.Iterations, runstate.IterationRecord{
		IterationID:   "0001",
		BranchInitial: "wip/0001",
		BranchCurrent: "wip/0001",
		Stage:         string(runstate.StageAgentRunning),
	})
	if err := runstate.Write(filepath.Join(repo, ".loop", "runs", "run-1", "run-state.json"), state); err != nil {
		t.Fatal(err)
	}
	return repo, iterDir
}

func readRuntimeForTest(t *testing.T, iterDir string) map[string]any {
	t.Helper()
	data, err := artifactdb.Read(iterDir, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	var runtime map[string]any
	if err := json.Unmarshal([]byte(data), &runtime); err != nil {
		t.Fatal(err)
	}
	return runtime
}
