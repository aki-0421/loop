package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestAgentOwnedPRChecksRepairAndMergeInSingleAgentInvocation(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: repairtest
  adapters:
    repairtest:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_MODE: pr_owned_repair

run:
  repairAttempts: 0

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      mergeWhenChecksPass: true
      deleteBranch: true
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add loop pr repair fixture")
	git(t, repo, "push", "origin", "develop")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	ghChecks := filepath.Join(ghDir, "checks.count")
	writeRepairingFakeGH(t, ghDir, ghLog, ghChecks)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "repairtest", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	logBytes, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatal(err)
	}
	gh := string(logBytes)
	if got := strings.Count(gh, "pr create "); got != 1 {
		t.Fatalf("pr create count = %d, log:\n%s", got, gh)
	}
	if got := strings.Count(gh, "pr checks 1 --watch"); got != 3 {
		t.Fatalf("pr checks count = %d, log:\n%s", got, gh)
	}
	if got := strings.Count(gh, "pr merge 1 --squash"); got != 1 {
		t.Fatalf("pr merge count = %d, log:\n%s", got, gh)
	}
	if strings.Contains(gh, "--delete-branch") {
		t.Fatalf("loop pr merge should not ask gh to delete branches from an iteration worktree:\n%s", gh)
	}

	runID, err := latestRun(filepath.Join(repo, ".loop", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	iterDir := filepath.Join(repo, ".loop", "runs", runID, "iterations", "0001")
	events, err := os.ReadFile(filepath.Join(iterDir, "agent-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(events), `"type":"agent.started"`); got != 1 {
		t.Fatalf("agent.started count = %d, events:\n%s", got, events)
	}
	promptBytes, err := os.ReadFile(filepath.Join(iterDir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(promptBytes)
	if prompt != "# Task\n\nMake the fake change.\n" {
		t.Fatalf("prompt.md should remain the instruction snapshot, got:\n%s", prompt)
	}
	prState, err := artifactdb.Read(iterDir, "pr-state")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prState, `"status": "merged"`) {
		t.Fatalf("pr-state should record merged PR:\n%s", prState)
	}
	stateBytes, err := os.ReadFile(filepath.Join(repo, ".loop", "runs", runID, "run-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Iterations []struct {
			ShouldFullyStop bool `json:"should_fully_stop"`
		} `json:"iterations"`
	}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Iterations) != 1 {
		t.Fatalf("iterations = %d, want 1", len(state.Iterations))
	}
	if state.Iterations[0].ShouldFullyStop {
		t.Fatal("PR check repair should not overwrite the original stop decision")
	}
	if _, err := os.Stat(filepath.Join(repo, ".loop", "worktrees", runID, "0001")); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed after agent-owned PR merge, err=%v", err)
	}
	assertBranchMissing(t, repo, "test/fake-agent")
}

func TestAgentOwnedPRMergeAllowsDetachedWorktreeAfterMerge(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: detachtest
  adapters:
    detachtest:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_MODE: pr_owned_simple

run:
  repairAttempts: 0

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      deleteBranch: true
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add detached pr merge fixture")
	git(t, repo, "push", "origin", "develop")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeDetachOnMergeFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "detachtest", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	runID, err := latestRun(filepath.Join(repo, ".loop", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".loop", "worktrees", runID, "0001")); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed after detached PR merge, err=%v", err)
	}
	assertBranchMissing(t, repo, "test/fake-agent")
}

func TestAgentOwnedPRMergeAllowsNonLoopSquashSubjectInResult(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: squashtest
  adapters:
    squashtest:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_MODE: pr_owned_non_loop_merge_result

run:
  repairAttempts: 0

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      deleteBranch: true
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add non-loop squash subject fixture")
	git(t, repo, "push", "origin", "develop")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writePassingFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "squashtest", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	}); err != nil {
		t.Fatalf("loop run should accept a merged PR result with a host-created squash subject: %v", err)
	}

	logBytes, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "pr merge 1 --squash") {
		t.Fatalf("expected PR merge, log:\n%s", logBytes)
	}
}

func TestAgentOwnedPRMergeFinalizesAfterPostMergeResultDrift(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: driftpr
  adapters:
    driftpr:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_MODE: pr_owned_empty_dirty_after_merge

run:
  repairAttempts: 0

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      deleteBranch: true
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add post merge drift fixture")
	git(t, repo, "push", "origin", "develop")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writePassingFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "driftpr", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	}); err != nil {
		t.Fatalf("loop run should finalize a merged PR even when post-merge result metadata drifts: %v", err)
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "develop" {
		t.Fatalf("current branch = %q, want develop", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "post-merge-dirty.txt")); !os.IsNotExist(err) {
		t.Fatalf("post-merge dirty file should be cleaned before the next iteration, err=%v", err)
	}
}

func TestAgentOwnedPRMergeDeletesOriginBranchAndPrunesTrackingRef(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: origincleanup
  adapters:
    origincleanup:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_MODE: pr_owned_simple

run:
  repairAttempts: 0

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      deleteBranch: true
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add origin pr branch cleanup fixture")
	git(t, repo, "push", "origin", "develop")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writePassingFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "origincleanup", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	assertBranchMissing(t, repo, "test/fake-agent")
	assertRemoteBranchMissing(t, repo, "test/fake-agent")
	assertRemoteTrackingBranchMissing(t, repo, "test/fake-agent")
}

func TestAgentOwnedPRPollsUntilChecksAppearBeforeMerge(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: prpolltest
  adapters:
    prpolltest:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_MODE: pr_owned_simple

run:
  repairAttempts: 0

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 1
      checksDiscoveryTimeoutSeconds: 10
      checksPollIntervalSeconds: 1
      mergeWhenChecksPass: true
      deleteBranch: false
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add loop pr polling fixture")
	git(t, repo, "push", "origin", "develop")
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	ghChecks := filepath.Join(ghDir, "checks.count")
	writeDelayedChecksFakeGH(t, ghDir, ghLog, ghChecks)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "prpolltest", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	logBytes, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatal(err)
	}
	gh := string(logBytes)
	if got := strings.Count(gh, "pr checks 1 --watch"); got != 4 {
		t.Fatalf("pr checks count = %d, log:\n%s", got, gh)
	}
	if got := strings.Count(gh, "pr merge 1 --squash"); got != 1 {
		t.Fatalf("pr merge count = %d, log:\n%s", got, gh)
	}
	if strings.Index(gh, "pr merge 1 --squash") < strings.LastIndex(gh, "pr checks 1 --watch") {
		t.Fatalf("merge ran before final checks poll:\n%s", gh)
	}
}

func TestLocalMergeModeIntegratesFakeAgent(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: localtest
  adapters:
    localtest:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"

git:
  baseBranch: develop
  integration:
    mode: local_merge
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add loop local merge fixture")

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "localtest", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, "loop-fake-change.txt")); err != nil {
		t.Fatalf("local merge did not leave fake change on base branch: %v", err)
	}
	log := git(t, repo, "log", "--oneline", "-1")
	if !strings.Contains(log, "Run fake agent behavior") {
		t.Fatalf("base branch was not squash merged with result summary:\n%s", log)
	}
	assertBranchMissing(t, repo, "test/fake-agent")
}

func TestRunRejectsCompletedResultWithoutBranchRename(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: localtest
  adapters:
    localtest:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_MODE: completed_unrenamed

run:
  repairAttempts: 0

git:
  baseBranch: develop
  integration:
    mode: local_merge
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add unrenamed branch fixture")
	withWorkingDir(t, repo)

	_, err = captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "localtest", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	})
	if err == nil {
		t.Fatal("expected run to reject completed result without branch rename")
	}
	if !strings.Contains(err.Error(), "loop branch rename") {
		t.Fatalf("error = %v", err)
	}
}

func TestPRModeRejectsCompletedResultBeforePRMerge(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake the fake change.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), fmt.Sprintf(`version: 1

agent:
  default: prunmerged
  adapters:
    prunmerged:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_MODE: pr_unmerged_result

run:
  repairAttempts: 0

git:
  baseBranch: develop
  integration:
    mode: pr
`, yamlSingleQuote(agentCommand)))
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add unmerged pr fixture")
	git(t, repo, "push", "origin", "develop")
	withWorkingDir(t, repo)

	_, err = captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "prunmerged", JSON: true, NoColor: true}, []string{"task.md", "--max-iterations", "1"})
	})
	if err == nil {
		t.Fatal("expected run to reject completed PR result before merge")
	}
	if !strings.Contains(err.Error(), "loop pr merge") {
		t.Fatalf("error should point to loop pr merge: %v", err)
	}
}

func TestPRChecksWritesArtifactAndConciseErrorLog(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      deleteBranch: false
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add pr checks fixture config")
	git(t, repo, "push", "origin", "develop")
	git(t, repo, "checkout", "-b", "test/fake-agent", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "F: add fake change")

	runDir := filepath.Join(repo, ".loop", "runs", "run", "iterations", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/fake-agent",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "1", Branch: "test/fake-agent", Base: "develop"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeFailingChecksFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	_, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"checks", "--iteration-dir", runDir})
	})
	if err == nil {
		t.Fatal("expected loop pr checks to fail")
	}
	checks, err := artifactdb.Read(runDir, "pr-checks")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(checks, `"status": "failed"`) || !strings.Contains(checks, "unit test failed: missing dependency") {
		t.Fatalf("pr-checks artifact missing failure details:\n%s", checks)
	}
	errorsLog := readText(t, filepath.Join(runDir, "errors.log"))
	if !strings.Contains(errorsLog, "see pr-checks artifact") {
		t.Fatalf("errors.log missing concise pointer:\n%s", errorsLog)
	}
	if strings.Contains(errorsLog, "Refreshing checks status") || strings.Contains(errorsLog, "unit test failed: missing dependency") {
		t.Fatalf("errors.log should not duplicate full check output:\n%s", errorsLog)
	}
}

func TestPRMergeRejectsInvalidIterationCommitBeforeHostMerge(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: false
      deleteBranch: false
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add pr invalid commit fixture config")
	git(t, repo, "push", "origin", "develop")
	git(t, repo, "checkout", "-b", "test/fake-agent", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "Add preview claim screen (#4)")

	runDir := filepath.Join(repo, ".loop", "runs", "run", "iterations", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/fake-agent",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "1", Branch: "test/fake-agent", Base: "develop", Title: "Invalid iteration commit"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writePassingFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	_, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"merge", "--iteration-dir", runDir})
	})
	if err == nil {
		t.Fatal("expected loop pr merge to reject the invalid iteration commit")
	}
	if !strings.Contains(err.Error(), "Add preview claim screen (#4)") || !strings.Contains(err.Error(), "must use <TYPE>: <message>") {
		t.Fatalf("error should explain the invalid iteration commit: %v", err)
	}
	if data, readErr := os.ReadFile(ghLog); readErr == nil && strings.Contains(string(data), "pr merge") {
		t.Fatalf("host merge should not run after invalid iteration commit:\n%s", data)
	}
}

func TestPRMergeFetchesMergedPRIntoMemory(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: false
      deleteBranch: false
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add pr memory fetch fixture config")
	git(t, repo, "checkout", "-b", "test/fake-agent", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "F: add fake change")

	runDir := filepath.Join(repo, ".loop", "runs", "run", "iterations", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/fake-agent",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "9", Branch: "test/fake-agent", Base: "develop", Title: "Add fake change"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeMergeAndFetchFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"merge", "--iteration-dir", runDir})
	}); err != nil {
		t.Fatalf("loop pr merge: %v", err)
	}
	hits, err := artifactdb.SearchPRMemory(filepath.Join(repo, ".loop", "loop.db"), artifactdb.PRMemorySearchOptions{Query: "merge fetch memory", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Number != 9 || hits[0].Record.State != "merged" {
		t.Fatalf("merged PR was not fetched into memory: %+v", hits)
	}
	log := readText(t, ghLog)
	if !strings.Contains(log, "pr merge 9 --squash") || !strings.Contains(log, "api graphql") {
		t.Fatalf("expected merge and fetch GraphQL calls:\n%s", log)
	}
}

func TestPRMergeRecoversAlreadyMergedRemoteState(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: false
      deleteBranch: false
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add already merged pr fixture config")
	git(t, repo, "checkout", "-b", "test/fake-agent", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "F: add fake change")

	runDir := filepath.Join(repo, ".loop", "runs", "run", "iterations", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/fake-agent",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "9", Branch: "test/fake-agent", Base: "develop", Title: "Add fake change"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeAlreadyMergedFetchFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"merge", "--iteration-dir", runDir})
	}); err != nil {
		t.Fatalf("loop pr merge should recover already-merged remote state: %v", err)
	}
	prState, err := artifactdb.Read(runDir, "pr-state")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prState, `"status": "merged"`) || !strings.Contains(prState, `"merged_at": "2026-05-20T01:00:00Z"`) {
		t.Fatalf("pr-state should record recovered merged PR:\n%s", prState)
	}
	log := readText(t, ghLog)
	if !strings.Contains(log, "pr merge 9 --squash") || !strings.Contains(log, "api graphql") {
		t.Fatalf("expected merge attempt and GraphQL recovery:\n%s", log)
	}
}

func TestHelperProcessFakeAgent(t *testing.T) {
	if os.Getenv("LOOP_TEST_FAKE_AGENT") != "1" {
		return
	}
	os.Exit(runTestFakeAgent())
}

func runTestFakeAgent() int {
	iterDir, err := resolveIterationDir(context.Background(), globals{}, "", os.Getenv("LOOP_RUN_ID"), os.Getenv("LOOP_ITERATION_ID"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	mode := testFakeAgentMode()
	switch mode {
	case "invalid_json":
		writeTestFakeRawResult(iterDir, "{invalid json\n")
		return 0
	case "dirty":
		_ = os.WriteFile(filepath.Join(getenvForTestAgent("LOOP_WORKDIR", "."), "loop-fake-dirty.txt"), []byte("dirty\n"), 0o644)
		writeTestFakeResult(iterDir, "completed", nil)
		return 0
	case "blocked":
		writeTestFakeResult(iterDir, "blocked", nil)
		return 1
	case "blocking_issue":
		_ = commandIssue(context.Background(), globals{JSON: true, NoColor: true}, []string{"ask", "--title", "Clarify blocking fixture", "--body", "Can this blocked fixture continue?", "--blocking"})
		writeTestFakeResult(iterDir, "blocked", nil)
		return 0
	case "no_change":
		writeTestFakeResult(iterDir, "no_change", nil)
		return 0
	case "needs_repair":
		writeTestFakeResult(iterDir, "needs_repair", nil)
		return 0
	case "validation_fix":
		workDir := getenvForTestAgent("LOOP_WORKDIR", ".")
		_ = commandBranch(context.Background(), globals{}, []string{"rename", "test/fake-agent"})
		changePath := filepath.Join(workDir, "validation-ok.txt")
		_ = os.WriteFile(changePath, []byte("validation repaired at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = gitForTestAgent(workDir, "add", "validation-ok.txt")
		_ = gitForTestAgent(workDir, "commit", "-m", "F: repair validation fixture")
		writeTestFakeResult(iterDir, "completed", testFakeCommit(workDir))
		return 0
	case "pr_owned_repair":
		return runTestFakeAgentOwnedPR(iterDir, true, "", false, false)
	case "pr_owned_simple":
		return runTestFakeAgentOwnedPR(iterDir, false, "", false, false)
	case "pr_owned_non_loop_merge_result":
		return runTestFakeAgentOwnedPR(iterDir, false, "Add preview claim screen (#4)", false, false)
	case "pr_owned_empty_dirty_after_merge":
		return runTestFakeAgentOwnedPR(iterDir, false, "", true, true)
	case "pr_unmerged_result":
		workDir := getenvForTestAgent("LOOP_WORKDIR", ".")
		_ = commandBranch(context.Background(), globals{}, []string{"rename", "test/fake-agent"})
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed without pr merge at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = gitForTestAgent(workDir, "add", "loop-fake-change.txt")
		_ = gitForTestAgent(workDir, "commit", "-m", "F: run fake agent behavior")
		writeTestFakeResult(iterDir, "completed", testFakeCommit(workDir))
		return 0
	case "completed_unrenamed":
		workDir := getenvForTestAgent("LOOP_WORKDIR", ".")
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed without branch rename at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = gitForTestAgent(workDir, "add", "loop-fake-change.txt")
		_ = gitForTestAgent(workDir, "commit", "-m", "F: run fake agent behavior")
		writeTestFakeResult(iterDir, "completed", testFakeCommit(workDir))
		return 0
	default:
		workDir := getenvForTestAgent("LOOP_WORKDIR", ".")
		_ = commandBranch(context.Background(), globals{}, []string{"rename", "test/fake-agent"})
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = gitForTestAgent(workDir, "add", "loop-fake-change.txt")
		_ = gitForTestAgent(workDir, "commit", "-m", "F: run fake agent behavior")
		writeTestFakeResult(iterDir, "completed", testFakeCommit(workDir))
		return 0
	}
}

func runTestFakeAgentOwnedPR(iterDir string, repair bool, resultCommitMessage string, omitResultCommit, dirtyAfterMerge bool) int {
	ctx := context.Background()
	workDir := getenvForTestAgent("LOOP_WORKDIR", ".")
	_ = commandBranch(ctx, globals{}, []string{"rename", "test/fake-agent"})
	changePath := filepath.Join(workDir, "loop-fake-change.txt")
	_ = os.WriteFile(changePath, []byte("fake agent completed at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
	_ = gitForTestAgent(workDir, "add", "loop-fake-change.txt")
	_ = gitForTestAgent(workDir, "commit", "-m", "F: run fake agent behavior")
	_ = artifactdb.Write(iterDir, "pr-title", "Run fake agent behavior\n")
	_ = artifactdb.Write(iterDir, "pr-body", "## Summary\n\nFake agent PR.\n")
	if err := commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"create"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"checks"}); err != nil {
		if !repair {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		repairPath := filepath.Join(workDir, "loop-fake-pr-repair.txt")
		_ = os.WriteFile(repairPath, []byte("fake pr repair at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = gitForTestAgent(workDir, "add", "loop-fake-pr-repair.txt")
		_ = gitForTestAgent(workDir, "commit", "-m", "C: repair fake pr checks")
		_ = artifactdb.Append(iterDir, "worklog", "Repaired fake PR checks after loop pr checks failed.\n")
		if err := commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"checks"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if err := commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"merge"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if dirtyAfterMerge {
		_ = os.WriteFile(filepath.Join(workDir, "post-merge-dirty.txt"), []byte("dirty after merge\n"), 0o644)
	}
	var resultCommit map[string]any
	if !omitResultCommit {
		resultCommit = testFakeCommit(workDir)
		if strings.TrimSpace(resultCommitMessage) != "" {
			resultCommit["message"] = resultCommitMessage
		}
	}
	writeTestFakeResult(iterDir, "completed", resultCommit)
	return 0
}

func testFakeAgentMode() string {
	sequence := strings.TrimSpace(os.Getenv("LOOP_FAKE_AGENT_SEQUENCE"))
	if sequence == "" {
		return getenvForTestAgent("LOOP_FAKE_AGENT_MODE", "completed")
	}
	var modes []string
	for _, item := range strings.Split(sequence, ",") {
		if mode := strings.TrimSpace(item); mode != "" {
			modes = append(modes, mode)
		}
	}
	if len(modes) == 0 {
		return getenvForTestAgent("LOOP_FAKE_AGENT_MODE", "completed")
	}
	index := nextTestFakeAgentInvocationIndex()
	if index >= len(modes) {
		index = len(modes) - 1
	}
	return modes[index]
}

func nextTestFakeAgentInvocationIndex() int {
	path := strings.TrimSpace(os.Getenv("LOOP_FAKE_AGENT_COUNT_FILE"))
	if path == "" {
		return 0
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := os.ReadFile(path)
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	_ = os.WriteFile(path, []byte(strconv.Itoa(count+1)+"\n"), 0o644)
	return count
}

func writeTestFakeResult(iterDir, status string, commit map[string]any) {
	validationStatus := "passed"
	if status == "blocked" {
		validationStatus = "skipped"
	}
	commits := []map[string]any{}
	if commit != nil {
		commits = append(commits, commit)
	}
	branch := testFakeBranchResult(status)
	result := map[string]any{
		"schema_version":    1,
		"status":            status,
		"summary_sentence":  "Run fake agent behavior",
		"should_fully_stop": status != "completed",
		"goal_evaluation":   "Fake agent produced a deterministic test result.",
		"branch":            branch,
		"commits":           commits,
		"validation":        map[string]any{"status": validationStatus, "commands": []map[string]any{}},
		"artifacts":         map[string]any{},
		"assumptions":       []string{},
		"blocked_reason":    "",
	}
	if status == "blocked" {
		result["blocked_reason"] = getenvForTestAgent("LOOP_FAKE_BLOCKED_REASON", "Fake agent blocked by requested mode.")
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	writeTestFakeRawResult(iterDir, string(append(data, '\n')))
}

func writeTestFakeRawResult(iterDir, resultJSON string) {
	runID, iterationID := artifactdb.ParseIterationDir(iterDir)
	_ = artifactdb.WriteResultHandoff(artifactdb.GlobalDBPathForIteration(iterDir), runID, iterationID, resultJSON)
}

func testFakeBranchResult(status string) map[string]any {
	workDir := getenvForTestAgent("LOOP_WORKDIR", ".")
	initial := getenvForTestAgent("LOOP_INITIAL_BRANCH", "wip/0001")
	current := currentBranchForTestAgent(workDir)
	if current == "" {
		current = runtimeCurrentBranchForTestAgent()
	}
	if current == "" {
		current = getenvForTestAgent("LOOP_CURRENT_BRANCH", initial)
	}
	kind := "test"
	slug := "fake-agent"
	final := ""
	if status == "completed" && current != "" && current != initial {
		final = current
		if parsedKind, parsedSlug, ok := splitTestBranchName(current); ok {
			kind = parsedKind
			slug = parsedSlug
		}
	}
	branch := map[string]any{"initial_name": initial, "kind": kind, "slug": slug}
	if final != "" {
		branch["final_name"] = final
	}
	return branch
}

func currentBranchForTestAgent(dir string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func runtimeCurrentBranchForTestAgent() string {
	iterDir, err := resolveIterationDir(context.Background(), globals{}, "", os.Getenv("LOOP_RUN_ID"), os.Getenv("LOOP_ITERATION_ID"))
	if err != nil {
		return ""
	}
	runtime, err := readRuntimeMap(iterDir)
	if err != nil {
		return ""
	}
	return runtimeString(runtime, "current_branch")
}

func splitTestBranchName(branch string) (string, string, bool) {
	kind, slug, ok := strings.Cut(strings.TrimSpace(branch), "/")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(slug) == "" {
		return "", "", false
	}
	return strings.TrimSpace(kind), strings.TrimSpace(slug), true
}

func gitForTestAgent(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

func testFakeCommit(dir string) map[string]any {
	cmd := exec.Command("git", "log", "-1", "--format=%H")
	cmd.Dir = dir
	out, err := cmd.Output()
	sha := ""
	if err == nil {
		sha = strings.TrimSpace(string(out))
	}
	return map[string]any{"sha": sha, "message": "F: run fake agent behavior"}
}

func getenvForTestAgent(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func addBareOrigin(t *testing.T, repo string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	cmd := exec.Command("git", "init", "--bare", origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare failed: %v\n%s", err, out)
	}
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-u", "origin", "develop")
}

func writeRepairingFakeGH(t *testing.T, dir, logPath, checksPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  count=0
  if [ -f ` + shellQuote(checksPath) + ` ]; then
    count=$(cat ` + shellQuote(checksPath) + `)
  fi
  count=$((count + 1))
  echo "$count" > ` + shellQuote(checksPath) + `
  if [ "$count" -eq 1 ]; then
    echo "unit test failed: missing dependency" >&2
    exit 1
  fi
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeDelayedChecksFakeGH(t *testing.T, dir, logPath, checksPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  count=0
  if [ -f ` + shellQuote(checksPath) + ` ]; then
    count=$(cat ` + shellQuote(checksPath) + `)
  fi
  count=$((count + 1))
  echo "$count" > ` + shellQuote(checksPath) + `
  if [ "$count" -lt 3 ]; then
    echo "no checks reported on the 'test/fake-agent' branch" >&2
    exit 1
  fi
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writePassingFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeMergeAndFetchFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

if [ "$1" = "api" ] && [ "$2" = "graphql" ]; then
cat <<'JSON'
{"data":{"repository":{"pullRequest":{"number":9,"url":"https://github.com/acme/app/pull/9","state":"MERGED","title":"Add merge fetch memory","body":"Merge fetch memory body.","updatedAt":"2026-05-20T00:00:00Z","mergedAt":"2026-05-20T01:00:00Z","repository":{"nameWithOwner":"acme/app"}}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeAlreadyMergedFetchFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  echo "Pull request acme/app#9 was already merged" >&2
  exit 1
fi

if [ "$1" = "api" ] && [ "$2" = "graphql" ]; then
cat <<'JSON'
{"data":{"repository":{"pullRequest":{"number":9,"url":"https://github.com/acme/app/pull/9","state":"MERGED","title":"Add merge fetch memory","body":"Merge fetch memory body.","updatedAt":"2026-05-20T00:00:00Z","mergedAt":"2026-05-20T01:00:00Z","repository":{"nameWithOwner":"acme/app"}}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeDetachOnMergeFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  git checkout --detach HEAD >/dev/null 2>&1
  git branch -D test/fake-agent >/dev/null 2>&1
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFailingChecksFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  echo "Refreshing checks status every 5 seconds. Press Ctrl+C to quit."
  echo "unit test failed: missing dependency" >&2
  exit 1
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteRuntimeArtifact(t *testing.T, iterDir string, value map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := artifactdb.Write(iterDir, "runtime", string(data)); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

func yamlSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
