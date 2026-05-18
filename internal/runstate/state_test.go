package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewRunIDFormatAndIterationID(t *testing.T) {
	id, err := NewRunID(time.Date(2026, 5, 17, 1, 2, 3, 0, time.FixedZone("JST", 9*60*60)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "20260516-160203-") || len(id) != len("20260516-160203-a1b2c3") {
		t.Fatalf("unexpected run id %q", id)
	}
	if got := IterationID(7); got != "0007" {
		t.Fatalf("IterationID(7) = %q", got)
	}
}

func TestWriteStateAtomicallyAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".loop", "runs", "r1", "run-state.json")
	state := New("r1", "done", "develop", "codex")
	state.CurrentIteration = "0001"

	if err := Write(path, state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary state file still exists: %v", err)
	}
	loaded, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunID != "r1" || loaded.Stage != StageCreated {
		t.Fatalf("unexpected loaded state: %+v", loaded)
	}
}

func TestAppendEventWritesJSONLWithTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-events.jsonl")
	log := EventLog{
		Path: path,
		Now:  func() time.Time { return time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC) },
	}
	if err := log.Append(Event{"type": "iteration.started", "iteration_id": "0001"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytesTrimSpace(data), &event); err != nil {
		t.Fatal(err)
	}
	if event["ts"] != "2026-05-17T00:00:00Z" || event["type"] != "iteration.started" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
