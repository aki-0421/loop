package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestRoleOrchestratedPRCreateRequiresRenameAndArtifacts(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  integration:
    mode: pr
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "C: configure pr mode")
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRolePRRuntimeForTest(t, iterDir, repo, "wip/0001")
	withWorkingDir(t, repo)

	err := commandPR(ctx, globals{}, []string{"create", "--iteration-dir", iterDir})
	if err == nil || !strings.Contains(err.Error(), "requires `loop branch rename`") {
		t.Fatalf("unrenamed role PR create error = %v", err)
	}

	git(t, repo, "branch", "-m", "feat/role-pr")
	writeRolePRRuntimeForTest(t, iterDir, repo, "feat/role-pr")
	err = commandPR(ctx, globals{}, []string{"create", "--iteration-dir", iterDir})
	if err == nil || !strings.Contains(err.Error(), "requires a pr-title artifact") {
		t.Fatalf("missing artifact role PR create error = %v", err)
	}
}

func TestPRChecksRejectsTaskRuntimeBeforePush(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "C: configure pr mode")
	git(t, repo, "checkout", "-b", "fix/pr-branch", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	activeDir := t.TempDir()
	writeTaskPRRuntimeForTest(t, activeDir, iterDir, repo, "task/0001-repair-attempt-1")
	if err := writePRState(iterDir, prState{SchemaVersion: 1, Status: "created", PR: "1", Branch: "fix/pr-branch", Base: "develop"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(artifactdb.ActiveIterationDirEnv, activeDir)
	t.Setenv("LOOP_RUN_ID", "run-1")
	t.Setenv("LOOP_ITERATION_ID", "0001")
	withWorkingDir(t, repo)

	err := commandPR(ctx, globals{}, []string{"checks", "--iteration-dir", iterDir})
	if err == nil {
		t.Fatal("expected task-runtime PR checks to fail")
	}
	if !strings.Contains(err.Error(), "pull request lifecycle commands must run from the iteration worktree") {
		t.Fatalf("error = %v", err)
	}
	if _, err := artifactdb.Read(iterDir, "pr-checks"); !errors.Is(err, artifactdb.ErrNotFound) {
		t.Fatalf("pr-checks should not be written before rejection: %v", err)
	}
}

func writeRolePRRuntimeForTest(t *testing.T, iterDir, repo, branch string) {
	t.Helper()
	if err := artifactdb.Write(iterDir, "runtime", `{
  "run_id": "run-1",
  "iteration_id": "0001",
  "base_branch": "develop",
  "initial_branch": "wip/0001",
  "current_branch": "`+branch+`",
  "workdir": "`+filepath.ToSlash(repo)+`",
  "integration_mode": "pr",
  "pull_request_mode": true,
  "role_orchestrated": true
}
`); err != nil {
		t.Fatal(err)
	}
}

func writeTaskPRRuntimeForTest(t *testing.T, activeDir, iterDir, repo, branch string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(activeDir, "runtime.json"), []byte(`{
  "run_id": "run-1",
  "iteration_id": "0001",
  "base_branch": "develop",
  "initial_branch": "wip/0001",
  "iteration_branch": "fix/pr-branch",
  "current_branch": "`+branch+`",
  "workdir": "`+filepath.ToSlash(repo)+`",
  "iteration_worktree": "`+filepath.ToSlash(repo)+`",
  "integration_mode": "pr",
  "pull_request_mode": true,
  "role_orchestrated": true,
  "task_id": "repair-task",
  "task_dir": "`+filepath.ToSlash(filepath.Join(iterDir, "tasks", "0001"))+`"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
}
