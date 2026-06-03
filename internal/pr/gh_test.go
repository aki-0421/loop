package pr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGHWrapperCreateChecksMerge(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "gh.log")
	ghPath := fakeGH(t, logPath, 0)
	r := Runner{Dir: dir, GHPath: ghPath}
	titlePath := filepath.Join(dir, "title.md")
	if err := os.WriteFile(titlePath, []byte("Add x\n\nignored body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	create, err := r.Create(ctx, CreateOptions{Base: "main", Head: "feat/x", TitleFile: titlePath, BodyFile: "body.md"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(create.Stdout, "https://example.test/pr/1") {
		t.Fatalf("create stdout = %q", create.Stdout)
	}
	if _, err := r.Checks(ctx, "1", true); err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if _, err := r.Merge(ctx, MergeOptions{PR: "1", Subject: "Add weather app foundation", BodyFile: "body.md", DeleteBranch: true}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	for _, want := range []string{
		"--version",
		"pr create --base main --head feat/x --title Add x --body-file body.md",
		"pr checks 1 --watch",
		"pr merge 1 --squash --subject Add weather app foundation (#1) --body-file body.md --delete-branch",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func TestMergeArgsPreservesPRLinkInSubject(t *testing.T) {
	tests := []struct {
		name    string
		pr      string
		subject string
		want    string
	}{
		{
			name:    "numeric pr",
			pr:      "42",
			subject: "Add usage report",
			want:    "Add usage report (#42)",
		},
		{
			name:    "github pull url",
			pr:      "https://github.com/acme/app/pull/42",
			subject: "Add usage report",
			want:    "Add usage report (#42)",
		},
		{
			name:    "owner repo hash",
			pr:      "acme/app#42",
			subject: "Add usage report",
			want:    "Add usage report (#42)",
		},
		{
			name:    "already linked",
			pr:      "42",
			subject: "Add usage report (#42)",
			want:    "Add usage report (#42)",
		},
		{
			name:    "unparseable pr",
			pr:      "feat/usage-report",
			subject: "Add usage report",
			want:    "Add usage report",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := MergeArgs(MergeOptions{PR: tt.pr, Subject: tt.subject})
			got := ""
			for i, arg := range args {
				if arg == "--subject" && i+1 < len(args) {
					got = args[i+1]
				}
			}
			if got != tt.want {
				t.Fatalf("subject = %q, want %q; args = %#v", got, tt.want, args)
			}
		})
	}
}

func TestGHWrapperChecksUsesWatchOptions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "gh.log")
	r := Runner{Dir: dir, GHPath: fakeGH(t, logPath, 0), ChecksIntervalSeconds: 3, ChecksRequiredOnly: true, ChecksTimeout: time.Minute}

	if _, err := r.Checks(ctx, "1", true); err != nil {
		t.Fatalf("Checks: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "pr checks 1 --required --watch --interval 3") {
		t.Fatalf("checks command did not include required/watch options:\n%s", logBytes)
	}
}

func TestGHWrapperViewStateParsesTSV(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "gh.log")
	r := Runner{Dir: dir, GHPath: fakeScript(t, `#!/bin/sh
echo "$@" >> "`+logPath+`"
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  printf 'MERGED\t2026-06-03T00:00:00Z\thttps://example.test/pr/1\n'
  exit 0
fi
exit 1
`)}

	state, _, err := r.ViewState(ctx, "1")
	if err != nil {
		t.Fatalf("ViewState: %v", err)
	}
	if state.State != "MERGED" || state.MergedAt != "2026-06-03T00:00:00Z" || state.URL != "https://example.test/pr/1" {
		t.Fatalf("state = %#v", state)
	}
}

func TestGHWrapperChecksFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r := Runner{Dir: dir, GHPath: fakeGH(t, filepath.Join(dir, "gh.log"), 1)}
	if _, err := r.Checks(ctx, "1", true); err == nil {
		t.Fatal("Checks succeeded, want failure")
	}
}

func TestGHWrapperChecksNoChecksReportedIsNotFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r := Runner{Dir: dir, GHPath: fakeScript(t, `#!/bin/sh
echo "no checks reported on the 'feat/x' branch" >&2
exit 1
`)}
	result, err := r.Checks(ctx, "https://github.com/example/repo/pull/1", true)
	if err != nil {
		t.Fatalf("Checks returned error for no checks reported: %v", err)
	}
	if !result.NoChecks {
		t.Fatal("Checks should mark no checks as a skipped check discovery result")
	}
	if !strings.Contains(result.Stderr, "no checks reported") {
		t.Fatalf("stderr = %q", result.Stderr)
	}
}

func TestGHWrapperChecksPendingExitCodeIsNotFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r := Runner{Dir: dir, GHPath: fakeScript(t, `#!/bin/sh
echo "checks pending" >&2
exit 8
`)}
	result, err := r.Checks(ctx, "1", true)
	if err != nil {
		t.Fatalf("Checks returned error for pending checks: %v", err)
	}
	if !result.Pending {
		t.Fatal("Checks should mark exit code 8 as pending")
	}
	if result.ExitCode != 8 {
		t.Fatalf("exit code = %d, want 8", result.ExitCode)
	}
}

func TestGHWrapperLogsParsesJobURL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "gh.log")
	r := Runner{Dir: dir, GHPath: fakeGH(t, logPath, 0)}

	if _, err := r.Logs(ctx, "https://github.com/acme/app/actions/runs/12345/job/67890"); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "run view 12345 --job 67890 --log") {
		t.Fatalf("logs command was not recorded correctly:\n%s", logBytes)
	}
}

func TestGHWrapperGraphQLUsesSerialVariables(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "gh.log")
	r := Runner{Dir: dir, GHPath: fakeGH(t, logPath, 0)}

	if _, err := r.GraphQL(ctx, "query { viewer { login } }", map[string]string{"name": "app", "owner": "acme"}); err != nil {
		t.Fatalf("GraphQL: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "api graphql -f query=query { viewer { login } } -F name=app -F owner=acme") {
		t.Fatalf("graphql command was not recorded correctly:\n%s", logBytes)
	}
}

func TestParseJobRef(t *testing.T) {
	runID, jobID := ParseJobRef("https://github.com/acme/app/actions/runs/12345/job/67890")
	if runID != "12345" || jobID != "67890" {
		t.Fatalf("ParseJobRef URL = %q, %q", runID, jobID)
	}
	runID, jobID = ParseJobRef("67890")
	if runID != "" || jobID != "67890" {
		t.Fatalf("ParseJobRef id = %q, %q", runID, jobID)
	}
	runID, jobID = ParseJobRef("https://github.com/acme/app/actions/runs/12345/job/67890?pr=4")
	if runID != "12345" || jobID != "67890" {
		t.Fatalf("ParseJobRef query URL = %q, %q", runID, jobID)
	}
}

func fakeGH(t *testing.T, logPath string, checksExit int) string {
	t.Helper()
	script := `#!/bin/sh
echo "$@" >> "` + logPath + `"
if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "https://example.test/pr/1"
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  exit ` + string(rune('0'+checksExit)) + `
fi
exit 0
`
	return fakeScript(t, script)
}

func fakeScript(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
