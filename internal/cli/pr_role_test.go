package cli

import (
	"context"
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
