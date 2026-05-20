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

func TestCreateIssueQuestionEnsuresLabelsAndStoresIssue(t *testing.T) {
	ctx := context.Background()
	repo := newGitHubMemoryRepo(t)
	logPath := filepath.Join(t.TempDir(), "gh.log")
	ghPath := fakeIssueCreateGH(t, logPath)
	runsDir := filepath.Join(repo, ".loop", "runs")
	record, err := CreateIssueQuestion(ctx, IssueQuestionOptions{
		WorkDir:     repo,
		RunsDir:     runsDir,
		Title:       "Clarify retention policy",
		Body:        "Which records are authoritative?",
		Blocking:    true,
		RunID:       "run-1",
		IterationID: "0001",
		GHPath:      ghPath,
	})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if record.Number != 12 || record.Kind != "issue" || !strings.Contains(record.Labels, LabelBlocking) {
		t.Fatalf("record = %+v, want blocking issue #12", record)
	}
	hits, err := artifactdb.SearchGitHubContext(artifactdb.GlobalDBPathFromRunsPath(runsDir), artifactdb.GitHubContextSearchOptions{Query: "authoritative", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Number != 12 {
		t.Fatalf("stored issue search hits = %+v", hits)
	}
	log := readFileForMemoryTest(t, logPath)
	if strings.Count(log, "createLabel") != 2 || !strings.Contains(log, "blocking: true") || !strings.Contains(log, "loop:question") {
		t.Fatalf("issue creation did not ensure labels and metadata:\n%s", log)
	}
}

func TestSyncIssuesAndGitHubUpdatesStoreIssueAndPRComments(t *testing.T) {
	ctx := context.Background()
	repo := newGitHubMemoryRepo(t)
	ghPath := fakeGitHubContextGH(t)
	runsDir := filepath.Join(repo, ".loop", "runs")
	full, err := SyncIssues(ctx, SyncOptions{WorkDir: repo, RunsDir: runsDir, Full: true, GHPath: ghPath})
	if err != nil {
		t.Fatalf("sync issues: %v", err)
	}
	if !full.Full || full.Fetched != 2 {
		t.Fatalf("full context result = %+v, want issue and comment", full)
	}
	updates, err := SyncGitHubUpdates(ctx, SyncOptions{WorkDir: repo, RunsDir: runsDir, GHPath: ghPath})
	if err != nil {
		t.Fatalf("sync updates: %v", err)
	}
	if updates.Fetched != 2 {
		t.Fatalf("updates = %+v, want closed issue and PR comment", updates)
	}
	hits, err := artifactdb.SearchGitHubContext(artifactdb.GlobalDBPathFromRunsPath(runsDir), artifactdb.GitHubContextSearchOptions{Query: "reviewer answer", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Kind != "pr-comment" || hits[0].Record.Number != 6 {
		t.Fatalf("PR comment search hits = %+v", hits)
	}
}

func newGitHubMemoryRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	return dir
}

func fakeIssueCreateGH(t *testing.T, logPath string) string {
	t.Helper()
	return fakeGHScript(t, `#!/bin/sh
echo "$@" >> "`+logPath+`"
args="$*"
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
{"data":{"createIssue":{"issue":{"__typename":"Issue","number":12,"url":"https://github.com/acme/app/issues/12","state":"OPEN","title":"Clarify retention policy","body":"Which records are authoritative?","updatedAt":"2026-05-20T00:00:00Z","closedAt":null,"author":{"login":"bot"},"labels":{"nodes":[{"name":"loop:question"},{"name":"loop:blocking"}]},"repository":{"nameWithOwner":"acme/app"}}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
exit 0
fi
exit 1
`)
}

func fakeGitHubContextGH(t *testing.T) string {
	t.Helper()
	return fakeGHScript(t, `#!/bin/sh
args="$*"
if echo "$args" | grep -q 'is:pr updated:>='; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"__typename":"PullRequest","number":6,"url":"https://github.com/acme/app/pull/6","state":"MERGED","title":"Add reviewable change","body":"PR body.","updatedAt":"2026-05-20T03:00:00Z","closedAt":"2026-05-20T03:00:00Z","mergedAt":"2026-05-20T03:00:00Z","author":{"login":"dev"},"labels":{"nodes":[]},"repository":{"nameWithOwner":"acme/app"},"comments":{"nodes":[{"id":"pc1","url":"https://github.com/acme/app/pull/6#issuecomment-1","body":"reviewer answer accepted","updatedAt":"2026-05-20T03:00:00Z","author":{"login":"reviewer"}}]}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T04:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:issue updated:>='; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"__typename":"Issue","number":4,"url":"https://github.com/acme/app/issues/4","state":"CLOSED","title":"Clarify cache authority","body":"Closed as confirmed.","updatedAt":"2026-05-20T02:00:00Z","closedAt":"2026-05-20T02:00:00Z","author":{"login":"pm"},"labels":{"nodes":[{"name":"loop:question"},{"name":"loop:blocking"}]},"repository":{"nameWithOwner":"acme/app"},"comments":{"nodes":[]}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T04:00:00Z","cost":1}}}
JSON
exit 0
fi
if echo "$args" | grep -q 'is:issue'; then
cat <<'JSON'
{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"__typename":"Issue","number":4,"url":"https://github.com/acme/app/issues/4","state":"OPEN","title":"Clarify cache authority","body":"Is GitHub authoritative?","updatedAt":"2026-05-20T00:00:00Z","closedAt":null,"author":{"login":"pm"},"labels":{"nodes":[{"name":"loop:question"},{"name":"loop:blocking"}]},"repository":{"nameWithOwner":"acme/app"},"comments":{"nodes":[{"id":"ic1","url":"https://github.com/acme/app/issues/4#issuecomment-1","body":"Waiting for owner answer.","updatedAt":"2026-05-20T00:30:00Z","author":{"login":"dev"}}]}}]},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T04:00:00Z","cost":1}}}
JSON
exit 0
fi
exit 1
`)
}

func readFileForMemoryTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
