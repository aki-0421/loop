package pr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"pr merge 1 --squash --subject Add weather app foundation --body-file body.md --delete-branch",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
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
	if !strings.Contains(result.Stderr, "no checks reported") {
		t.Fatalf("stderr = %q", result.Stderr)
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
