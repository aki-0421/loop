package artifactdb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAppendReadUsesIterationFilesOnly(t *testing.T) {
	root := t.TempDir()
	iterDir := filepath.Join(root, ".loop", "runs", "run-1", "iterations", "0001")

	if err := Write(iterDir, "pr-body", "Added password"); err != nil {
		t.Fatal(err)
	}
	if err := Append(iterDir, "pr-body", " reset tests.\n"); err != nil {
		t.Fatal(err)
	}
	got, err := Read(iterDir, "pr-body")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Added password reset tests.\n" {
		t.Fatalf("pr-body = %q", got)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "pr-body.md")); err != nil {
		t.Fatalf("pr-body file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "iteration.db")); !os.IsNotExist(err) {
		t.Fatalf("iteration db should not be created by runtime artifacts, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".loop", GlobalDBName)); !os.IsNotExist(err) {
		t.Fatalf("global db should not be created by runtime artifacts, err=%v", err)
	}
}

func TestActiveArtifactsUseTempDirFromEnvironment(t *testing.T) {
	root := t.TempDir()
	iterDir := filepath.Join(root, ".loop", "runs", "run-1", "iterations", "0001")
	activeDir := filepath.Join(t.TempDir(), "active")
	t.Setenv("LOOP_RUN_ID", "run-1")
	t.Setenv("LOOP_ITERATION_ID", "0001")
	t.Setenv(ActiveIterationDirEnv, activeDir)

	if err := Write(iterDir, "plan", "active plan\n"); err != nil {
		t.Fatal(err)
	}
	got, err := Read(iterDir, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if got != "active plan\n" {
		t.Fatalf("plan = %q", got)
	}
	if _, err := os.Stat(filepath.Join(activeDir, "plan.md")); err != nil {
		t.Fatalf("active plan file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "plan.md")); !os.IsNotExist(err) {
		t.Fatalf("iteration plan should not be written, err=%v", err)
	}

	if err := Write(iterDir, "pr-state", "{}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "pr-state.json")); err != nil {
		t.Fatalf("durable pr-state missing: %v", err)
	}
}

func TestRebuildGlobalFromRunsDoesNotIndexRuntimeArtifacts(t *testing.T) {
	root := t.TempDir()
	iterDir := filepath.Join(root, ".loop", "runs", "run-1", "iterations", "0001")
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(iterDir, "plan.md"), []byte("file artifact should be ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if count, err := RebuildGlobalFromRuns(filepath.Join(root, ".loop", "runs")); err != nil {
		t.Fatal(err)
	} else if count != 0 {
		t.Fatalf("indexed %d file records, want 0", count)
	}

	if err := Write(iterDir, "plan", "Stored in file artifacts.\n"); err != nil {
		t.Fatal(err)
	}
	if count, err := RebuildGlobalFromRuns(filepath.Join(root, ".loop", "runs")); err != nil {
		t.Fatal(err)
	} else if count != 0 {
		t.Fatalf("indexed %d records, want 0", count)
	}
	hits, err := SearchGlobal(filepath.Join(root, ".loop", GlobalDBName), SearchOptions{Query: "sqlite", RunID: "run-1", Artifact: "plan", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("runtime artifact search hits = %+v, want none", hits)
	}
}

func TestPRMemoryUpsertReplaceDeleteRecentAndSearch(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".loop", GlobalDBName)
	records := []PRMemoryRecord{
		{Repo: "acme/app", Number: 1, URL: "https://github.com/acme/app/pull/1", State: "open", Title: "Add password reset", Body: "Implements password reset tests.", UpdatedAt: "2026-05-18T00:00:00Z", FetchedAt: "2026-05-20T00:00:00Z"},
		{Repo: "acme/app", Number: 2, URL: "https://github.com/acme/app/pull/2", State: "merged", Title: "Handle token refresh", Body: "Refresh token repair.", UpdatedAt: "2026-05-19T00:00:00Z", MergedAt: "2026-05-19T01:00:00Z", FetchedAt: "2026-05-20T00:00:00Z"},
	}
	if err := ReplacePRMemory(dbPath, "acme/app", records, "2026-05-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	recent, err := RecentPRMemory(dbPath, "acme/app", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Number != 2 {
		t.Fatalf("recent = %+v, want PR #2", recent)
	}
	hits, err := SearchPRMemory(dbPath, PRMemorySearchOptions{Query: "password reset", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Number != 1 {
		t.Fatalf("search hits = %+v, want PR #1", hits)
	}
	upserted, deleted, err := ApplyPRMemorySync(dbPath, "acme/app", []PRMemoryRecord{
		{Repo: "acme/app", Number: 1, URL: "https://github.com/acme/app/pull/1", State: "CLOSED", Title: "Add password reset", Body: "Closed without merge.", UpdatedAt: "2026-05-20T01:00:00Z", FetchedAt: "2026-05-20T01:00:00Z"},
		{Repo: "acme/app", Number: 3, URL: "https://github.com/acme/app/pull/3", State: "OPEN", Title: "Add audit log", Body: "Audit trail.", UpdatedAt: "2026-05-20T01:00:00Z", FetchedAt: "2026-05-20T01:00:00Z"},
	}, "2026-05-20T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if upserted != 1 || deleted != 1 {
		t.Fatalf("upserted/deleted = %d/%d, want 1/1", upserted, deleted)
	}
	count, err := CountPRMemory(dbPath, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	hits, err = SearchPRMemory(dbPath, PRMemorySearchOptions{Query: "password", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("deleted PR remained searchable: %+v", hits)
	}
	lastSync, err := PRMemoryLastSync(dbPath, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if lastSync != "2026-05-20T01:00:00Z" {
		t.Fatalf("last sync = %q", lastSync)
	}
}

func TestGitHubContextUpsertReplaceRecentSearchAndBlockingIssues(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".loop", GlobalDBName)
	records := []GitHubContextRecord{
		{Repo: "acme/app", Kind: "issue", Number: 7, URL: "https://github.com/acme/app/issues/7", State: "open", Title: "Clarify retention", Body: "Should archived records be retained?", Author: "pm", Labels: "loop:question,loop:blocking", UpdatedAt: "2026-05-18T00:00:00Z", FetchedAt: "2026-05-20T00:00:00Z"},
		{Repo: "acme/app", Kind: "issue-comment", Number: 7, CommentID: "c1", URL: "https://github.com/acme/app/issues/7#issuecomment-1", State: "open", Title: "Clarify retention", Body: "Keep only GitHub-backed context.", Author: "owner", Labels: "loop:question,loop:blocking", UpdatedAt: "2026-05-19T00:00:00Z", FetchedAt: "2026-05-20T00:00:00Z"},
	}
	if err := ReplaceGitHubContext(dbPath, "acme/app", []string{"issue", "issue-comment"}, records, "2026-05-20T00:00:00Z", "github_context.issues.last_sync.acme/app"); err != nil {
		t.Fatal(err)
	}
	recent, err := RecentGitHubContext(dbPath, "acme/app", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Kind != "issue-comment" || recent[0].CommentID != "c1" {
		t.Fatalf("recent = %+v, want issue comment", recent)
	}
	hits, err := SearchGitHubContext(dbPath, GitHubContextSearchOptions{Query: "GitHub-backed", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Kind != "issue-comment" {
		t.Fatalf("search hits = %+v, want issue comment", hits)
	}
	open, err := OpenBlockingGitHubIssues(dbPath, "acme/app", []int{7})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].Number != 7 {
		t.Fatalf("open blocking issues = %+v, want #7", open)
	}
	upserted, err := ApplyGitHubContextSync(dbPath, "acme/app", []GitHubContextRecord{
		{Repo: "acme/app", Kind: "issue", Number: 7, URL: "https://github.com/acme/app/issues/7", State: "closed", Title: "Clarify retention", Body: "Closed as confirmed.", Author: "pm", Labels: "loop:question,loop:blocking", UpdatedAt: "2026-05-20T01:00:00Z", ClosedAt: "2026-05-20T01:00:00Z", FetchedAt: "2026-05-20T01:00:00Z"},
	}, "2026-05-20T01:00:00Z", "github_context.updates.last_sync.acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if upserted != 1 {
		t.Fatalf("upserted = %d, want 1", upserted)
	}
	open, err = OpenBlockingGitHubIssues(dbPath, "acme/app", []int{7})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("closed issue remained blocking: %+v", open)
	}
	lastSync, err := GitHubContextLastSync(dbPath, "github_context.updates.last_sync.acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if lastSync != "2026-05-20T01:00:00Z" {
		t.Fatalf("last sync = %q", lastSync)
	}
}

func TestGitHubMemoryUsesUnifiedRecordsTable(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".loop", GlobalDBName)
	if err := ReplacePRMemory(dbPath, "acme/app", []PRMemoryRecord{
		{Repo: "acme/app", Number: 1, URL: "https://github.com/acme/app/pull/1", State: "open", Title: "Unify records", Body: "Pull request body.", UpdatedAt: "2026-05-20T00:00:00Z", FetchedAt: "2026-05-20T00:00:00Z"},
	}, "2026-05-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceGitHubContext(dbPath, "acme/app", []string{"issue"}, []GitHubContextRecord{
		{Repo: "acme/app", Kind: "issue", Number: 2, URL: "https://github.com/acme/app/issues/2", State: "open", Title: "Decide retention", Body: "Issue body.", UpdatedAt: "2026-05-20T00:00:00Z", FetchedAt: "2026-05-20T00:00:00Z"},
	}, "2026-05-20T00:00:00Z", "github_context.issues.last_sync.acme/app"); err != nil {
		t.Fatal(err)
	}

	db, err := openGlobal(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tables := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type IN ('table', 'virtual table')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"github_records", "github_records_fts"} {
		if !tables[name] {
			t.Fatalf("%s table was not created; tables=%v", name, tables)
		}
	}
	for _, name := range []string{"pr_memory", "pr_memory_fts", "github_context", "github_context_fts"} {
		if tables[name] {
			t.Fatalf("legacy table %s should not be created; tables=%v", name, tables)
		}
	}
}
