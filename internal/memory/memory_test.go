package memory

import (
	"path/filepath"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestRecentAndSearchUsePullRequestMemory(t *testing.T) {
	root := t.TempDir()
	runsDir := filepath.Join(root, ".loop", "runs")
	dbPath := artifactdb.GlobalDBPathFromRunsPath(runsDir)
	if err := artifactdb.ReplacePRMemory(dbPath, "acme/app", []artifactdb.PRMemoryRecord{
		{Repo: "acme/app", Number: 1, URL: "https://github.com/acme/app/pull/1", State: "open", Title: "Add password reset", Body: "Added password reset tests.", UpdatedAt: "2026-05-18T00:00:00Z", FetchedAt: "2026-05-20T00:00:00Z"},
		{Repo: "acme/app", Number: 2, URL: "https://github.com/acme/app/pull/2", State: "merged", Title: "Handle token refresh", Body: "Handled token refresh errors.", UpdatedAt: "2026-05-19T00:00:00Z", MergedAt: "2026-05-19T01:00:00Z", FetchedAt: "2026-05-20T00:00:00Z"},
	}, "2026-05-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	recent, err := Recent(runsDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Number != 2 || recent[0].Summary != "Handle token refresh" {
		t.Fatalf("unexpected recent records: %+v", recent)
	}

	hits, err := Search(runsDir, "password reset", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Number != 1 || hits[0].Artifact != "pull-request" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}

func TestCompactReturnsPullRequestIndex(t *testing.T) {
	root := t.TempDir()
	runsDir := filepath.Join(root, ".loop", "runs")
	dbPath := artifactdb.GlobalDBPathFromRunsPath(runsDir)
	if err := artifactdb.UpsertPRMemory(dbPath, artifactdb.PRMemoryRecord{
		Repo: "acme/app", Number: 7, URL: "https://github.com/acme/app/pull/7", State: "merged", Title: "Add checkout validation", Body: "Checkout validation context.", UpdatedAt: "2026-05-18T00:00:00Z", MergedAt: "2026-05-18T01:00:00Z", FetchedAt: "2026-05-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	index, err := Compact(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if index.Version != 3 || len(index.Records) != 1 || index.Records[0].Number != 7 || len(index.Records[0].Keywords) == 0 {
		t.Fatalf("unexpected index: %+v", index)
	}
}
