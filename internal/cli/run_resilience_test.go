package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/runstate"
)

func TestRunContinuesAcrossIterationsUntilAgentStops(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "completed,no_change",
		MaxIterations:  3,
		RepairAttempts: 1,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted {
		t.Fatalf("stage = %s, want completed", state.Stage)
	}
	if len(state.Iterations) != 2 {
		t.Fatalf("iterations = %d, want 2: %#v", len(state.Iterations), state.Iterations)
	}
	if state.Iterations[0].ShouldFullyStop {
		t.Fatalf("first completed iteration should allow the loop to continue: %#v", state.Iterations[0])
	}
	if !state.Iterations[1].ShouldFullyStop {
		t.Fatalf("second no-change iteration should stop the loop: %#v", state.Iterations[1])
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "develop" {
		t.Fatalf("current branch = %q, want develop", got)
	}
	assertBranchMissing(t, repo, "wip/0002")
	assertBranchMissing(t, repo, "test/fake-agent")
}

func TestRunKeepsDisposableArtifactsInGoTempDir(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "no_change",
		MaxIterations:  1,
		RepairAttempts: 0,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	iterDir := latestIterationDir(t, repo, "0001")
	for _, name := range []string{"runtime.json", "plan.md", "todo.md", "worklog.md", "validation.md", "pr-title.txt", "pr-body.md", "agent-prompt-audit.md"} {
		if _, err := os.Stat(filepath.Join(iterDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should not be written to iteration dir, err=%v", name, err)
		}
	}
	for _, name := range []string{"prompt.md", "effective-config.yaml", "agent-events.jsonl"} {
		if _, err := os.Stat(filepath.Join(iterDir, name)); err != nil {
			t.Fatalf("%s should remain in iteration dir: %v", name, err)
		}
	}

	activeDir := eventStringValue(t, iterDir, "iteration.active_temp.cleanup.completed", "active_dir")
	if activeDir == "" {
		t.Fatalf("cleanup event did not record active_dir")
	}
	if filepath.Clean(filepath.Dir(activeDir)) != filepath.Clean(os.TempDir()) {
		t.Fatalf("active dir = %q, want child of Go temp dir %q", activeDir, os.TempDir())
	}
	if _, err := os.Stat(activeDir); !os.IsNotExist(err) {
		t.Fatalf("active temp dir should be removed after cleanup, err=%v", err)
	}
}

func TestRunInitialSyncsGitHubPRMemoryBeforeFirstIteration(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "no_change",
		MaxIterations:  1,
		RepairAttempts: 0,
	})
	commitLoopRuntimeIgnore(t, repo)
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeInitialMemorySyncFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	log := readText(t, ghLog)
	if got := strings.Count(log, "api graphql"); got != 3 {
		t.Fatalf("initial memory sync GraphQL calls = %d, want 3:\n%s", got, log)
	}
	hits, err := artifactdb.SearchPRMemory(filepath.Join(repo, ".loop", "loop.db"), artifactdb.PRMemorySearchOptions{Query: "bootstrap memory", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Number != 4 {
		recent, _ := artifactdb.RecentPRMemory(filepath.Join(repo, ".loop", "loop.db"), "acme/app", 10)
		t.Fatalf("initial sync did not store PR memory: hits=%+v recent=%+v log=%s", hits, recent, log)
	}
}

func TestRunContinuesWhenIterationMemorySyncFailsAfterInitialSync(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "completed,no_change",
		MaxIterations:  2,
		RepairAttempts: 0,
	})
	commitLoopRuntimeIgnore(t, repo)
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeFailingIncrementalMemorySyncFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should continue after incremental memory sync failure: %v", err)
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted || len(state.Iterations) != 2 {
		t.Fatalf("state = %#v, want completed two-iteration run", state)
	}
	iterDir := latestIterationDir(t, repo, "0002")
	errorsLog := readText(t, filepath.Join(iterDir, "errors.log"))
	if !strings.Contains(errorsLog, "GitHub PR memory sync failed; using cached memory") {
		t.Fatalf("iteration 2 errors.log missing memory sync warning:\n%s", errorsLog)
	}
	if got := countEventType(t, iterDir, "agent.started"); got != 1 {
		t.Fatalf("agent should still run in iteration 2, started count = %d", got)
	}
}

func TestRunSleepsOnBlockingIssueAndWakesOnGitHubUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "blocking_issue,no_change",
		MaxIterations:  1,
		RepairAttempts: 0,
	})
	commitLoopRuntimeIgnore(t, repo)
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeBlockingIssueSleepFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOOP_FAKE_BLOCKED_REASON", "Waiting on https://github.com/acme/app/issues/44")
	previousSleep := githubSleepPoll
	previousInterval := githubSleepPollInterval
	githubSleepPoll = func(context.Context, time.Duration) error { return nil }
	githubSleepPollInterval = time.Millisecond
	defer func() {
		githubSleepPoll = previousSleep
		githubSleepPollInterval = previousInterval
	}()
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run should wake after blocking issue update: %v", err)
	}

	state := readLatestRunState(t, repo)
	if state.Stage != runstate.StageCompleted {
		t.Fatalf("stage = %s, want completed", state.Stage)
	}
	iterDir := latestIterationDir(t, repo, "0001")
	updates, err := artifactdb.Read(iterDir, "github-updates")
	if err != nil {
		t.Fatalf("github-updates artifact missing: %v", err)
	}
	if !strings.Contains(updates, "issue #44 closed") {
		t.Fatalf("github-updates missing closed issue:\n%s", updates)
	}
	if got := countEventType(t, iterDir, "agent.started"); got != 2 {
		t.Fatalf("agent should relaunch in same iteration after wake, started count = %d", got)
	}
	log := readText(t, ghLog)
	if !strings.Contains(log, "createIssue") || !strings.Contains(log, "is:issue updated:>=") {
		t.Fatalf("fake gh log missing issue creation or sleep polling:\n%s", log)
	}
}

func TestRunRepairsInvalidResultAndIntegrates(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "invalid_json,completed",
		MaxIterations:  1,
		RepairAttempts: 1,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	iterDir := latestIterationDir(t, repo, "0001")
	if got := countEventType(t, iterDir, "agent.started"); got != 2 {
		t.Fatalf("agent.started count = %d, want 2", got)
	}
	if !strings.Contains(readText(t, filepath.Join(iterDir, "errors.log")), "result handoff missing or invalid") {
		t.Fatalf("errors.log did not record invalid-result repair:\n%s", readText(t, filepath.Join(iterDir, "errors.log")))
	}
	if _, err := os.Stat(filepath.Join(repo, "loop-fake-change.txt")); err != nil {
		t.Fatalf("repaired run did not integrate fake change: %v", err)
	}
}

func TestRunHonorsNeedsRepairResultBeforeValidation(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "needs_repair,completed",
		MaxIterations:  1,
		RepairAttempts: 1,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	iterDir := latestIterationDir(t, repo, "0001")
	if got := countEventType(t, iterDir, "agent.started"); got != 2 {
		t.Fatalf("agent.started count = %d, want 2", got)
	}
	if !strings.Contains(readText(t, filepath.Join(iterDir, "errors.log")), "agent requested repair") {
		t.Fatalf("errors.log did not record requested repair:\n%s", readText(t, filepath.Join(iterDir, "errors.log")))
	}
	if _, err := os.Stat(filepath.Join(repo, "loop-fake-change.txt")); err != nil {
		t.Fatalf("needs_repair flow did not integrate repaired change: %v", err)
	}
}

func TestRunRepairsValidationFailureAndIntegrates(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	writeResilienceFixture(t, repo, resilienceOptions{
		Sequence:       "completed,validation_fix",
		MaxIterations:  1,
		RepairAttempts: 1,
		ValidationYAML: `validation:
  commands:
    - name: repair-marker
      run: test -f validation-ok.txt
      required: true
`,
	})
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandRun(ctx, globals{Agent: "resilience", JSON: true, NoColor: true}, []string{"task.md"})
	}); err != nil {
		t.Fatalf("loop run: %v", err)
	}

	iterDir := latestIterationDir(t, repo, "0001")
	if got := countEventType(t, iterDir, "agent.started"); got != 2 {
		t.Fatalf("agent.started count = %d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "validation-ok.txt")); err != nil {
		t.Fatalf("validation repair was not integrated: %v", err)
	}
	validation := readText(t, filepath.Join(iterDir, "errors.log"))
	if !strings.Contains(validation, "validation failed") {
		t.Fatalf("errors.log did not record validation repair cause:\n%s", validation)
	}
}

type resilienceOptions struct {
	Sequence       string
	MaxIterations  int
	RepairAttempts int
	ValidationYAML string
}

func writeResilienceFixture(t *testing.T, repo string, opts resilienceOptions) {
	t.Helper()
	mustWrite(t, filepath.Join(repo, "task.md"), "# Task\n\nMake resilient loop progress.\n")
	agentCommand, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(t.TempDir(), "agent.count")
	configText := fmt.Sprintf(`version: 1

agent:
  default: resilience
  adapters:
    resilience:
      command: %s
      args: [-test.run=TestHelperProcessFakeAgent, --]
      prompt: stdin
      env:
        LOOP_TEST_FAKE_AGENT: "1"
        LOOP_FAKE_AGENT_SEQUENCE: %s
        LOOP_FAKE_AGENT_COUNT_FILE: %s

run:
  maxIterations: %d
  repairAttempts: %d

git:
  baseBranch: develop
  integration:
    mode: local_merge
`, yamlSingleQuote(agentCommand), yamlSingleQuote(opts.Sequence), yamlSingleQuote(countFile), opts.MaxIterations, opts.RepairAttempts)
	if opts.ValidationYAML != "" {
		configText += "\n" + opts.ValidationYAML
	}
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), configText)
	git(t, repo, "add", "task.md", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add resilience fixture")
}

func readLatestRunState(t *testing.T, repo string) runstate.State {
	t.Helper()
	runID, err := latestRun(filepath.Join(repo, ".loop", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".loop", "runs", runID, "run-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state runstate.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func latestIterationDir(t *testing.T, repo, iteration string) string {
	t.Helper()
	runID, err := latestRun(filepath.Join(repo, ".loop", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repo, ".loop", "runs", runID, "iterations", iteration)
}

func countEventType(t *testing.T, iterDir, eventType string) int {
	t.Helper()
	events := readText(t, filepath.Join(iterDir, "agent-events.jsonl"))
	return strings.Count(events, `"type":"`+eventType+`"`)
}

func eventStringValue(t *testing.T, iterDir, eventType, key string) string {
	t.Helper()
	events := readText(t, filepath.Join(iterDir, "agent-events.jsonl"))
	for _, line := range strings.Split(events, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid event line %q: %v", line, err)
		}
		if event["type"] == eventType {
			value, _ := event[key].(string)
			return value
		}
	}
	t.Fatalf("event %q not found in:\n%s", eventType, events)
	return ""
}

func commitLoopRuntimeIgnore(t *testing.T, repo string) {
	t.Helper()
	mustWrite(t, filepath.Join(repo, ".loop", ".gitignore"), "runs/\nworktrees/\ntmp/\nlocks/\nloop.db\n*.db\n*.db-wal\n*.db-shm\n*.log\n")
	git(t, repo, "add", ".loop/.gitignore")
	git(t, repo, "commit", "-m", "T: ignore loop runtime files")
}

func writeInitialMemorySyncFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "gh"), `#!/bin/sh
echo "$@" >> "`+logPath+`"
args="$*"
if echo "$args" | grep -q 'is:issue'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"__typename":"Issue","number":2,"url":"https://github.com/acme/app/issues/2","state":"OPEN","title":"Clarify bootstrap behavior","body":"Initial repository issue context.","updatedAt":"2026-05-20T00:00:00Z","closedAt":null,"author":{"login":"octo"},"labels":{"nodes":[]},"repository":{"nameWithOwner":"acme/app"},"comments":{"nodes":[]}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:open'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"number":4,"url":"https://github.com/acme/app/pull/4","state":"OPEN","title":"Add bootstrap memory","body":"Bootstrap memory from GitHub PRs.","updatedAt":"2026-05-20T00:00:00Z","mergedAt":null,"repository":{"nameWithOwner":"acme/app"}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:merged'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
exit 1
`)
	if err := os.Chmod(filepath.Join(dir, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFailingIncrementalMemorySyncFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "gh"), `#!/bin/sh
echo "$@" >> "`+logPath+`"
args="$*"
if echo "$args" | grep -q 'updated:>='; then
  echo "temporary GraphQL failure" >&2
  exit 1
fi
if echo "$args" | grep -q 'is:issue'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"__typename":"Issue","number":2,"url":"https://github.com/acme/app/issues/2","state":"OPEN","title":"Clarify bootstrap behavior","body":"Initial repository issue context.","updatedAt":"2026-05-20T00:00:00Z","closedAt":null,"author":{"login":"octo"},"labels":{"nodes":[]},"repository":{"nameWithOwner":"acme/app"},"comments":{"nodes":[]}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:open'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"number":4,"url":"https://github.com/acme/app/pull/4","state":"OPEN","title":"Add bootstrap memory","body":"Bootstrap memory from GitHub PRs.","updatedAt":"2026-05-20T00:00:00Z","mergedAt":null,"repository":{"nameWithOwner":"acme/app"}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:merged'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
exit 1
`)
	if err := os.Chmod(filepath.Join(dir, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeBlockingIssueSleepFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	pollState := filepath.Join(dir, "poll.count")
	mustWrite(t, filepath.Join(dir, "gh"), `#!/bin/sh
echo "$@" >> "`+logPath+`"
args="$*"
if echo "$args" | grep -q 'is:issue updated:>='; then
  count=0
  if [ -f "`+pollState+`" ]; then count=$(cat "`+pollState+`"); fi
  next=$((count + 1))
  echo "$next" > "`+pollState+`"
  if [ "$count" = "0" ]; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
    exit 0
  fi
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"__typename":"Issue","number":44,"url":"https://github.com/acme/app/issues/44","state":"CLOSED","title":"Clarify blocking fixture","body":"Closed as confirmed.","updatedAt":"2026-05-20T01:00:00Z","closedAt":"2026-05-20T01:00:00Z","author":{"login":"pm"},"labels":{"nodes":[{"name":"loop:question"},{"name":"loop:blocking"}]},"repository":{"nameWithOwner":"acme/app"},"comments":{"nodes":[]}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:pr updated:>='; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'repository(owner:'; then
cat <<'JSON'
{"data":{"repository":{"id":"repo-id","labels":{"nodes":[]}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'createLabel'; then
if echo "$args" | grep -q 'loop:blocking'; then
cat <<'JSON'
{"data":{"createLabel":{"label":{"id":"blocking-label","name":"loop:blocking"}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
cat <<'JSON'
{"data":{"createLabel":{"label":{"id":"question-label","name":"loop:question"}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'createIssue'; then
cat <<'JSON'
{"data":{"createIssue":{"issue":{"__typename":"Issue","number":44,"url":"https://github.com/acme/app/issues/44","state":"OPEN","title":"Clarify blocking fixture","body":"Can this blocked fixture continue?","updatedAt":"2026-05-20T00:30:00Z","closedAt":null,"author":{"login":"bot"},"labels":{"nodes":[{"name":"loop:question"},{"name":"loop:blocking"}]},"repository":{"nameWithOwner":"acme/app"}}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:issue'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:open'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:merged'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
exit 1
`)
	if err := os.Chmod(filepath.Join(dir, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
}
