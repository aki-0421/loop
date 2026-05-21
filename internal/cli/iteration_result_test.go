package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/validation"
)

func TestIterationResultCommandBuildsAndWritesResult(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001")
	git(t, repo, "branch", "-m", "wip/0001", "feat/add-result-helper")
	mustWrite(t, filepath.Join(repo, "result-helper.txt"), "done\n")
	git(t, repo, "add", "result-helper.txt")
	git(t, repo, "commit", "-m", "F: add result helper")

	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "feat/add-result-helper")
	if err := artifactdb.Write(iterDir, "plan", "plan\n"); err != nil {
		t.Fatal(err)
	}
	if err := artifactdb.Write(iterDir, "worklog", "worklog\n"); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{
			"result",
			"--iteration-dir", iterDir,
			"--write",
			"--summary", "Add result helper",
			"--should-stop", "false",
			"--goal-evaluation", "The selected slice is complete; follow-up work remains.",
			"--validation-command", "unit|go test ./...|0|true",
			"--assumption", "Used the configured base branch from runtime.",
		})
	})
	if err != nil {
		t.Fatalf("iteration result: %v", err)
	}
	data, err := artifactdb.ReadResultHandoff(filepath.Join(repo, ".loop", artifactdb.GlobalDBName), "run-1", "0001")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != strings.TrimSpace(data) {
		t.Fatalf("stdout should contain the written result JSON\nstdout:\n%s\nhandoff:\n%s", out, data)
	}
	result, err := validation.ValidateResultJSON([]byte(data))
	if err != nil {
		t.Fatalf("generated result should validate: %v\n%s", err, data)
	}
	if result.Branch.InitialName != "wip/0001" {
		t.Fatalf("initial branch = %q", result.Branch.InitialName)
	}
	if result.Branch.Kind != "feat" || result.Branch.Slug != "add-result-helper" || result.Branch.FinalName != "feat/add-result-helper" {
		t.Fatalf("branch proposal = %#v", result.Branch)
	}
	if len(result.Commits) != 1 || result.Commits[0].Message != "F: add result helper" {
		t.Fatalf("commits = %#v", result.Commits)
	}
	if result.Validation.Status != "passed" || len(result.Validation.Commands) != 1 {
		t.Fatalf("validation = %#v", result.Validation)
	}
	if result.Artifacts.Plan != "plan" || result.Artifacts.Worklog != "worklog" {
		t.Fatalf("artifacts = %#v", result.Artifacts)
	}
	if len(result.Assumptions) != 1 {
		t.Fatalf("assumptions = %#v", result.Assumptions)
	}
}

func TestIterationResultCommandPrintsResultJSON(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	git(t, repo, "branch", "-m", "wip/0001", "docs/document-result-helper")
	mustWrite(t, filepath.Join(repo, "result-helper.md"), "done\n")
	git(t, repo, "add", "result-helper.md")
	git(t, repo, "commit", "-m", "D: document result helper")

	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "docs/document-result-helper")

	out, err := captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{
			"result",
			"--iteration-dir", iterDir,
			"--summary", "Document result helper",
			"--should-stop", "true",
			"--goal-evaluation", "The requested documentation is complete.",
			"--validation-status", "skipped",
		})
	})
	if err != nil {
		t.Fatalf("iteration result: %v", err)
	}
	var result validation.IterationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output should be raw result JSON: %v\n%s", err, out)
	}
	if result.SchemaVersion != 1 || result.Status != "completed" || result.Branch.Kind != "docs" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(out, `"artifact"`) {
		t.Fatalf("stdout should not be wrapped command JSON:\n%s", out)
	}
}

func TestIterationResultCommandRequiresSemanticFields(t *testing.T) {
	err := commandIteration(context.Background(), globals{}, []string{
		"result",
		"--summary", "Missing stop decision",
		"--goal-evaluation", "Not enough fields.",
	})
	if err == nil {
		t.Fatal("expected missing --should-stop to fail")
	}
	if !strings.Contains(err.Error(), "--should-stop") {
		t.Fatalf("error = %v", err)
	}
}

func TestIterationResultCommandRejectsBranchOverrideFlags(t *testing.T) {
	err := commandIteration(context.Background(), globals{}, []string{
		"result",
		"--branch-final", "feat/old-override",
		"--summary", "Old branch override",
		"--should-stop", "true",
		"--goal-evaluation", "Branch metadata comes from loop runtime.",
	})
	if err == nil {
		t.Fatal("expected branch override flag to fail")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("error = %v", err)
	}
}

func TestIterationResultCommandRejectsCompletedWithoutCommits(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "feat/finish-empty-slice", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "feat/finish-empty-slice")

	err := commandIteration(context.Background(), globals{}, []string{
		"result",
		"--iteration-dir", iterDir,
		"--summary", "Finish empty slice",
		"--should-stop", "true",
		"--goal-evaluation", "The slice is complete.",
		"--validation-status", "skipped",
	})
	if err == nil {
		t.Fatal("expected completed result without commits to fail")
	}
	if !strings.Contains(err.Error(), "requires at least one commit") {
		t.Fatalf("error = %v", err)
	}
}

func TestIterationResultCommandRejectsCompletedWithoutBranchRename(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	mustWrite(t, filepath.Join(repo, "result-helper.txt"), "done\n")
	git(t, repo, "add", "result-helper.txt")
	git(t, repo, "commit", "-m", "F: add result helper")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "wip/0001")

	err := commandIteration(context.Background(), globals{}, []string{
		"result",
		"--iteration-dir", iterDir,
		"--summary", "Add result helper",
		"--should-stop", "false",
		"--goal-evaluation", "The selected slice is complete; follow-up work remains.",
		"--validation-status", "skipped",
	})
	if err == nil {
		t.Fatal("expected completed result without branch rename to fail")
	}
	if !strings.Contains(err.Error(), "loop branch rename") {
		t.Fatalf("error = %v", err)
	}
}

func TestIterationResultCommandAllowsNoChangeWithoutBranchRename(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "wip/0001")

	out, err := captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{
			"result",
			"--iteration-dir", iterDir,
			"--status", "no_change",
			"--summary", "Confirm no repository change is needed",
			"--should-stop", "true",
			"--goal-evaluation", "The requested behavior already exists.",
			"--validation-status", "skipped",
		})
	})
	if err != nil {
		t.Fatalf("no_change result: %v", err)
	}
	var result validation.IterationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Branch.FinalName != "" {
		t.Fatalf("final branch for no_change = %q", result.Branch.FinalName)
	}
}

func writeRuntimeForResultTest(t *testing.T, iterDir, repo, initialBranch, currentBranch string) {
	t.Helper()
	data, err := json.MarshalIndent(map[string]any{
		"base_branch":    "develop",
		"initial_branch": initialBranch,
		"current_branch": currentBranch,
		"branch_renamed": initialBranch != "" && currentBranch != "" && initialBranch != currentBranch,
		"workdir":        repo,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifactdb.Write(iterDir, "runtime", string(append(data, '\n'))); err != nil {
		t.Fatal(err)
	}
}
