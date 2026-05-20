package memory

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestSyncFullAndIncremental(t *testing.T) {
	ctx := context.Background()
	repo := newGitHubMemoryRepo(t)
	ghPath := fakeMemoryGH(t, filepath.Join(t.TempDir(), "gh.log"))
	runsDir := filepath.Join(repo, ".loop", "runs")

	full, err := Sync(ctx, SyncOptions{WorkDir: repo, RunsDir: runsDir, Full: true, GHPath: ghPath})
	if err != nil {
		t.Fatalf("full sync: %v", err)
	}
	if !full.Full || full.Repo != "acme/app" || full.Fetched != 2 || full.Upserted != 2 {
		t.Fatalf("unexpected full sync result: %+v", full)
	}
	count, err := artifactdb.CountPRMemory(artifactdb.GlobalDBPathFromRunsPath(runsDir), "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count after full sync = %d, want 2", count)
	}

	incremental, err := Sync(ctx, SyncOptions{WorkDir: repo, RunsDir: runsDir, GHPath: ghPath})
	if err != nil {
		t.Fatalf("incremental sync: %v", err)
	}
	if incremental.Full || incremental.Fetched != 2 || incremental.Upserted != 1 || incremental.Deleted != 1 || incremental.Since == "" {
		t.Fatalf("unexpected incremental sync result: %+v", incremental)
	}
	hits, err := artifactdb.SearchPRMemory(artifactdb.GlobalDBPathFromRunsPath(runsDir), artifactdb.PRMemorySearchOptions{Query: "password", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("closed unmerged PR remained searchable: %+v", hits)
	}
	hits, err = artifactdb.SearchPRMemory(artifactdb.GlobalDBPathFromRunsPath(runsDir), artifactdb.PRMemorySearchOptions{Query: "audit", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Number != 3 {
		t.Fatalf("incremental open PR missing from search: %+v", hits)
	}
}

func TestSyncStopsBeforePaginatingWhenRateLimitIsTooLow(t *testing.T) {
	ctx := context.Background()
	repo := newGitHubMemoryRepo(t)
	ghPath := fakeRateLimitedMemoryGH(t)
	_, err := Sync(ctx, SyncOptions{WorkDir: repo, RunsDir: filepath.Join(repo, ".loop", "runs"), Full: true, GHPath: ghPath})
	var rateErr RateLimitError
	if !errors.As(err, &rateErr) {
		t.Fatalf("error = %v, want RateLimitError", err)
	}
	if rateErr.Remaining != 1 || rateErr.ResetAt == "" {
		t.Fatalf("rate error = %+v", rateErr)
	}
}

func TestFetchPullRequestUpsertsMergedPR(t *testing.T) {
	ctx := context.Background()
	repo := newGitHubMemoryRepo(t)
	ghPath := fakeFetchPRMemoryGH(t)
	runsDir := filepath.Join(repo, ".loop", "runs")
	record, err := FetchPullRequest(ctx, FetchOptions{WorkDir: repo, RunsDir: runsDir, Ref: "https://github.com/acme/app/pull/9", GHPath: ghPath})
	if err != nil {
		t.Fatalf("fetch PR: %v", err)
	}
	if record.Number != 9 || record.State != "merged" {
		t.Fatalf("record = %+v, want merged PR #9", record)
	}
	hits, err := artifactdb.SearchPRMemory(artifactdb.GlobalDBPathFromRunsPath(runsDir), artifactdb.PRMemorySearchOptions{Query: "single fetch", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Number != 9 {
		t.Fatalf("stored PR search hits = %+v", hits)
	}
}

func newGitHubMemoryRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func fakeMemoryGH(t *testing.T, logPath string) string {
	t.Helper()
	return fakeGHScript(t, `#!/bin/sh
echo "$@" >> "`+logPath+`"
args="$*"
if echo "$args" | grep -q 'updated:>='; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"number":1,"url":"https://github.com/acme/app/pull/1","state":"CLOSED","title":"Add password reset","body":"Closed without merge.","updatedAt":"2026-05-20T01:00:00Z","mergedAt":null,"repository":{"nameWithOwner":"acme/app"}},{"number":3,"url":"https://github.com/acme/app/pull/3","state":"OPEN","title":"Add audit log","body":"Audit trail for memory sync.","updatedAt":"2026-05-20T01:00:00Z","mergedAt":null,"repository":{"nameWithOwner":"acme/app"}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:open'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"number":1,"url":"https://github.com/acme/app/pull/1","state":"OPEN","title":"Add password reset","body":"Password reset implementation.","updatedAt":"2026-05-18T00:00:00Z","mergedAt":null,"repository":{"nameWithOwner":"acme/app"}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:merged'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"number":2,"url":"https://github.com/acme/app/pull/2","state":"MERGED","title":"Handle token refresh","body":"Token refresh implementation.","updatedAt":"2026-05-19T00:00:00Z","mergedAt":"2026-05-19T01:00:00Z","repository":{"nameWithOwner":"acme/app"}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
exit 1
`)
}

func fakeRateLimitedMemoryGH(t *testing.T) string {
	t.Helper()
	return fakeGHScript(t, `#!/bin/sh
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":true,"endCursor":"cursor-1"},"nodes":[]},"rateLimit":{"remaining":1,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
`)
}

func fakeFetchPRMemoryGH(t *testing.T) string {
	t.Helper()
	return fakeGHScript(t, `#!/bin/sh
cat <<'JSON'
{"data":{"repository":{"pullRequest":{"number":9,"url":"https://github.com/acme/app/pull/9","state":"MERGED","title":"Add single fetch memory","body":"Single fetch body.","updatedAt":"2026-05-20T00:00:00Z","mergedAt":"2026-05-20T01:00:00Z","repository":{"nameWithOwner":"acme/app"}}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
`)
}

func fakeGHScript(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
