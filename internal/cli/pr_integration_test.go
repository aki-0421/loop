package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestPRChecksFailureLaunchesRepairAgentBeforeRetry(t *testing.T) {
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

run:
  repairAttempts: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      mergeWhenChecksPass: false
      deleteBranch: false
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
	if got := strings.Count(gh, "pr checks 1 --watch"); got != 2 {
		t.Fatalf("pr checks count = %d, log:\n%s", got, gh)
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
	if got := strings.Count(string(events), `"type":"agent.started"`); got != 2 {
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
	audit, err := artifactdb.Read(iterDir, "agent-prompt-audit")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Pull Request Check Repair Contract",
		"perform a web search",
		"unit test failed: missing dependency",
	} {
		if !strings.Contains(audit, want) {
			t.Fatalf("repair prompt audit missing %q:\n%s", want, audit)
		}
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
  worktree: false
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
  worktree: false
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
	mode := os.Getenv("LOOP_FAKE_AGENT_MODE")
	if mode == "" {
		mode = "completed"
	}
	switch mode {
	case "invalid_json":
		_ = artifactdb.Write(iterDir, "result", "{invalid json\n")
		return 0
	case "dirty":
		_ = os.WriteFile(filepath.Join(getenvForTestAgent("LOOP_WORKDIR", "."), "loop-fake-dirty.txt"), []byte("dirty\n"), 0o644)
		writeTestFakeResult(iterDir, "completed", nil)
		return 0
	case "blocked":
		writeTestFakeResult(iterDir, "blocked", nil)
		return 1
	case "no_change":
		writeTestFakeResult(iterDir, "no_change", nil)
		return 0
	case "completed_unrenamed":
		workDir := getenvForTestAgent("LOOP_WORKDIR", ".")
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed without branch rename at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = gitForTestAgent(workDir, "add", "loop-fake-change.txt")
		_ = gitForTestAgent(workDir, "commit", "-m", "F: run fake agent behavior")
		_ = artifactdb.Write(iterDir, "summary", "# Iteration Summary\n\n- Fake agent completed without renaming.\n")
		writeTestFakeResult(iterDir, "completed", testFakeCommit(workDir))
		return 0
	default:
		workDir := getenvForTestAgent("LOOP_WORKDIR", ".")
		_ = commandBranch(context.Background(), globals{}, []string{"rename", "test/fake-agent"})
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = gitForTestAgent(workDir, "add", "loop-fake-change.txt")
		_ = gitForTestAgent(workDir, "commit", "-m", "F: run fake agent behavior")
		_ = artifactdb.Write(iterDir, "summary", "# Iteration Summary\n\n- Fake agent completed.\n")
		writeTestFakeResult(iterDir, "completed", testFakeCommit(workDir))
		return 0
	}
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
	result := map[string]any{
		"schema_version":    1,
		"status":            status,
		"summary_sentence":  "Run fake agent behavior",
		"should_fully_stop": status != "completed",
		"goal_evaluation":   "Fake agent produced a deterministic test result.",
		"branch":            map[string]any{"initial_name": "wip/0001", "kind": "test", "slug": "fake-agent", "final_name": "test/fake-agent"},
		"commits":           commits,
		"validation":        map[string]any{"status": validationStatus, "commands": []map[string]any{}},
		"artifacts":         map[string]any{"summary": "summary"},
		"assumptions":       []string{},
		"blocked_reason":    "",
	}
	if status == "blocked" {
		result["blocked_reason"] = "Fake agent blocked by requested mode."
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	_ = artifactdb.Write(iterDir, "result", string(append(data, '\n')))
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

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

func yamlSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
