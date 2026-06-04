package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/runstate"
)

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
