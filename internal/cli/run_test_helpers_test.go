package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/runstate"
)

func readLatestRunState(t *testing.T, repo string) runstate.State {
	t.Helper()
	runsDir := testRunsDir(t, repo)
	runID, err := latestRun(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(runsDir, runID, "run-state.json"))
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
	runsDir := testRunsDir(t, repo)
	runID, err := latestRun(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(runsDir, runID, "iterations", iteration)
}

func testRunsDir(t *testing.T, repo string) string {
	t.Helper()
	return testStorage(t, repo).RunsDir
}

func testRunDir(t *testing.T, repo, runID string) string {
	t.Helper()
	return filepath.Join(testRunsDir(t, repo), runID)
}

func testIterationDir(t *testing.T, repo, runID, iteration string) string {
	t.Helper()
	return filepath.Join(testRunDir(t, repo, runID), "iterations", iteration)
}

func testStorage(t *testing.T, repo string) loopStoragePaths {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{CWD: repo, ConfigPath: filepath.Join(repo, ".loop", "config.yaml"), Overrides: config.Overrides{Agent: "codex"}})
	if err != nil {
		cfg = config.Defaults()
	}
	storage, err := loopStorageForRepo(context.Background(), repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return storage
}

func countEventType(t *testing.T, iterDir, eventType string) int {
	t.Helper()
	events := readText(t, filepath.Join(iterDir, "agent-events.jsonl"))
	return strings.Count(events, `"type":"`+eventType+`"`)
}
