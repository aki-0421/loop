package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestRecentSummariesAndSearch(t *testing.T) {
	root := t.TempDir()
	oldPath := writeSummary(t, root, "20260517-000000-a1b2c3", "0001", "Added password reset tests.\n")
	writeSummary(t, root, "20260517-010000-b1c2d3", "0002", "Handled token refresh errors.\n")

	recent, err := Recent(filepath.Join(root, ".loop", "runs"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0] != "Handled token refresh errors.\n" {
		t.Fatalf("unexpected recent summaries: %+v", recent)
	}

	hits, err := Search(filepath.Join(root, ".loop", "runs"), "password reset", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != oldPath || hits[0].Artifact != "summary" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}

func TestRebuildIndex(t *testing.T) {
	root := t.TempDir()
	writeSummary(t, root, "20260517-000000-a1b2c3", "0001", "# Summary\n\n- Status: completed\n- Added checkout validation.\n")
	index, err := Compact(filepath.Join(root, ".loop", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Records) != 1 || index.Records[0].RunID == "" || len(index.Records[0].Keywords) == 0 {
		t.Fatalf("unexpected index: %+v", index)
	}
}

func writeSummary(t *testing.T, root, runID, iterationID, content string) string {
	t.Helper()
	iterDir := filepath.Join(root, ".loop", "runs", runID, "iterations", iterationID)
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifactdb.Write(iterDir, "summary", content); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, ".loop", "runs", runID, "iterations", iterationID, "summary")
}
