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

func TestIterationCloseCommandBuildsAndWritesMergeResult(t *testing.T) {
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

	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{
			"close",
			"--iteration-dir", iterDir,
			"--merge",
			"--summary", "Add result helper",
			"--should-stop", "false",
			"--goal-evaluation", "The selected slice is complete; follow-up work remains.",
			"--validation-command", "unit|go test ./...|0|true",
			"--assumption", "Used the configured base branch from runtime.",
		})
	})
	if err != nil {
		t.Fatalf("iteration close: %v", err)
	}
	data, err := artifactdb.ReadResultHandoff(filepath.Join(repo, ".loop", artifactdb.GlobalDBName), "run-1", "0001")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != strings.TrimSpace(data) {
		t.Fatalf("stdout should contain the written close JSON\nstdout:\n%s\nhandoff:\n%s", out, data)
	}
	result, err := validation.ValidateResultJSON([]byte(data))
	if err != nil {
		t.Fatalf("generated result should validate: %v\n%s", err, data)
	}
	if result.Branch.InitialName != "wip/0001" {
		t.Fatalf("initial branch = %q", result.Branch.InitialName)
	}
	if result.Action != "merge" {
		t.Fatalf("action = %q", result.Action)
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
	if result.Artifacts.Plan != "plan" {
		t.Fatalf("artifacts = %#v", result.Artifacts)
	}
	if len(result.Assumptions) != 1 {
		t.Fatalf("assumptions = %#v", result.Assumptions)
	}
}

func TestIterationCloseCommandPrintsMergeResultJSON(t *testing.T) {
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
			"close",
			"--iteration-dir", iterDir,
			"--merge",
			"--summary", "Document result helper",
			"--should-stop", "true",
			"--goal-evaluation", "The requested documentation is complete.",
			"--validation-status", "skipped",
		})
	})
	if err != nil {
		t.Fatalf("iteration close: %v", err)
	}
	var result validation.IterationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output should be raw close JSON: %v\n%s", err, out)
	}
	if result.SchemaVersion != 1 || result.Action != "merge" || result.Branch.Kind != "docs" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(out, `"artifact"`) {
		t.Fatalf("stdout should not be wrapped command JSON:\n%s", out)
	}
}

func TestIterationCloseCommandRequiresSemanticFields(t *testing.T) {
	iterDir := filepath.Join(t.TempDir(), ".loop", "runs", "run-1", "iterations", "0001")
	err := commandIteration(context.Background(), globals{}, []string{
		"close",
		"--iteration-dir", iterDir,
		"--merge",
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

func TestIterationCloseCommandRejectsBranchOverrideFlags(t *testing.T) {
	err := commandIteration(context.Background(), globals{}, []string{
		"close",
		"--merge",
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

func TestIterationCloseCommandRejectsMergeWithoutCommits(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "feat/finish-empty-slice", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "feat/finish-empty-slice")

	err := commandIteration(context.Background(), globals{}, []string{
		"close",
		"--iteration-dir", iterDir,
		"--merge",
		"--summary", "Finish empty slice",
		"--should-stop", "true",
		"--goal-evaluation", "The slice is complete.",
		"--validation-status", "skipped",
	})
	if err == nil {
		t.Fatal("expected merge close without commits to fail")
	}
	if !strings.Contains(err.Error(), "requires at least one commit") {
		t.Fatalf("error = %v", err)
	}
}

func TestIterationCloseCommandRejectsMergeWithoutBranchRename(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	mustWrite(t, filepath.Join(repo, "result-helper.txt"), "done\n")
	git(t, repo, "add", "result-helper.txt")
	git(t, repo, "commit", "-m", "F: add result helper")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "wip/0001")

	err := commandIteration(context.Background(), globals{}, []string{
		"close",
		"--iteration-dir", iterDir,
		"--merge",
		"--summary", "Add result helper",
		"--should-stop", "false",
		"--goal-evaluation", "The selected slice is complete; follow-up work remains.",
		"--validation-status", "skipped",
	})
	if err == nil {
		t.Fatal("expected merge close without branch rename to fail")
	}
	if !strings.Contains(err.Error(), "loop branch rename") {
		t.Fatalf("error = %v", err)
	}
}

func TestIterationCloseCommandAllowsSkipMergeWithoutBranchRename(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "wip/0001")

	out, err := captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{
			"close",
			"--iteration-dir", iterDir,
			"--skip-merge",
			"--reason", "No repository change is appropriate.",
			"--should-stop", "true",
			"--goal-evaluation", "The requested behavior already exists.",
			"--validation-status", "skipped",
		})
	})
	if err != nil {
		t.Fatalf("skip-merge close: %v", err)
	}
	var result validation.IterationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Action != "skip_merge" || result.SkipMergeReason == "" {
		t.Fatalf("result = %#v", result)
	}
	if result.Branch.FinalName != "" {
		t.Fatalf("final branch for skip_merge = %q", result.Branch.FinalName)
	}
}

func TestIterationCloseCommandAllowsSkipMergeSleepWithoutStoppingGoal(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "wip/0001")

	out, err := captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{
			"close",
			"--iteration-dir", iterDir,
			"--skip-merge",
			"--sleep",
			"--reason", "Waiting for Issue context before more work is safe.",
			"--should-stop", "false",
			"--goal-evaluation", "The goal is not complete; more context is needed.",
			"--validation-status", "skipped",
		})
	})
	if err != nil {
		t.Fatalf("skip-merge sleep close: %v", err)
	}
	var result validation.IterationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !result.SleepUntilGitHubUpdate {
		t.Fatalf("sleep_until_github_update = false in %#v", result)
	}
	if result.ShouldFullyStop {
		t.Fatalf("should_fully_stop should remain independent from sleep")
	}
}

func TestIterationCloseCommandRejectsMergeSleep(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "wip/0001")

	err := commandIteration(context.Background(), globals{}, []string{
		"close",
		"--iteration-dir", iterDir,
		"--merge",
		"--sleep",
		"--summary", "Try invalid sleep",
		"--should-stop", "false",
		"--goal-evaluation", "Sleep is only available for skip-merge.",
		"--validation-status", "skipped",
	})
	if err == nil {
		t.Fatal("expected --merge --sleep to fail")
	}
	if !strings.Contains(err.Error(), "--sleep is only valid with --skip-merge") {
		t.Fatalf("error = %v", err)
	}
}

func TestIterationCloseCommandRejectsSleepStopTrue(t *testing.T) {
	repo := newCleanupRepo(t)
	git(t, repo, "checkout", "-b", "wip/0001", "develop")
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0001")
	writeRuntimeForResultTest(t, iterDir, repo, "wip/0001", "wip/0001")

	err := commandIteration(context.Background(), globals{}, []string{
		"close",
		"--iteration-dir", iterDir,
		"--skip-merge",
		"--sleep",
		"--reason", "Waiting for Issue context before more work is safe.",
		"--should-stop", "true",
		"--goal-evaluation", "The goal is complete.",
		"--validation-status", "skipped",
	})
	if err == nil {
		t.Fatal("expected --sleep --should-stop true to fail")
	}
	if !strings.Contains(err.Error(), "--sleep requires --should-stop false") {
		t.Fatalf("error = %v", err)
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
