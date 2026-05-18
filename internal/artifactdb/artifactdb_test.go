package artifactdb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAppendReadAndMirror(t *testing.T) {
	root := t.TempDir()
	iterDir := filepath.Join(root, ".loop", "runs", "run-1", "iterations", "0001")

	if err := Write(iterDir, "summary", "Added password"); err != nil {
		t.Fatal(err)
	}
	if err := Append(iterDir, "summary", " reset tests.\n"); err != nil {
		t.Fatal(err)
	}
	got, err := Read(iterDir, "summary")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Added password reset tests.\n" {
		t.Fatalf("summary = %q", got)
	}
	if _, err := os.Stat(filepath.Join(iterDir, LocalDBName)); err != nil {
		t.Fatalf("local db missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".loop", GlobalDBName)); err != nil {
		t.Fatalf("global db missing: %v", err)
	}

	hits, err := SearchGlobal(filepath.Join(root, ".loop", GlobalDBName), SearchOptions{Query: "password reset", RunID: "run-1", Artifact: "summary", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].IterationID != "0001" || hits[0].Content != got {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}

func TestRebuildGlobalUsesIterationDBOnly(t *testing.T) {
	root := t.TempDir()
	iterDir := filepath.Join(root, ".loop", "runs", "run-1", "iterations", "0001")
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(iterDir, "summary.md"), []byte("file artifact should be ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if count, err := RebuildGlobalFromRuns(filepath.Join(root, ".loop", "runs")); err != nil {
		t.Fatal(err)
	} else if count != 0 {
		t.Fatalf("indexed %d file records, want 0", count)
	}

	if err := Write(iterDir, "summary", "Stored in sqlite.\n"); err != nil {
		t.Fatal(err)
	}
	if count, err := RebuildGlobalFromRuns(filepath.Join(root, ".loop", "runs")); err != nil {
		t.Fatal(err)
	} else if count != 1 {
		t.Fatalf("indexed %d records, want 1", count)
	}
}
