package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestPRChecksWritesArtifactAndConciseErrorLog(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      deleteBranch: false
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add pr checks fixture config")
	git(t, repo, "push", "origin", "develop")
	git(t, repo, "checkout", "-b", "test/fake-agent", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "F: add fake change")

	runDir := testIterationDir(t, repo, "run", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/fake-agent",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "1", Branch: "test/fake-agent", Base: "develop"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeFailingChecksFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	_, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"checks", "--iteration-dir", runDir})
	})
	if err == nil {
		t.Fatal("expected loop pr checks to fail")
	}
	checks, err := artifactdb.Read(runDir, "pr-checks")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(checks, `"status": "failed"`) || !strings.Contains(checks, "unit test failed: missing dependency") {
		t.Fatalf("pr-checks artifact missing failure details:\n%s", checks)
	}
	errorsLog := readText(t, filepath.Join(runDir, "errors.log"))
	if !strings.Contains(errorsLog, "see pr-checks artifact") {
		t.Fatalf("errors.log missing concise pointer:\n%s", errorsLog)
	}
	if strings.Contains(errorsLog, "Refreshing checks status") || strings.Contains(errorsLog, "unit test failed: missing dependency") {
		t.Fatalf("errors.log should not duplicate full check output:\n%s", errorsLog)
	}
}

func TestPRCreateRecordsPushFailureBeforePRState(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add pr create push failure fixture config")
	git(t, repo, "checkout", "-b", "feat/push-failure", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "F: add push failure fixture")

	runDir := testIterationDir(t, repo, "run", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "feat/push-failure",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"role_orchestrated": true,
		"workdir":           repo,
	})
	if err := artifactdb.Write(runDir, "pr-title", "Push failure fixture\n"); err != nil {
		t.Fatal(err)
	}
	if err := artifactdb.Write(runDir, "pr-body", "## Summary\n\nPush failure fixture.\n"); err != nil {
		t.Fatal(err)
	}

	hookDir := t.TempDir()
	mustWrite(t, filepath.Join(hookDir, "pre-push"), "#!/bin/sh\necho 'apps/web/src/app/final-visual-route-contract.test.ts typecheck failed' >&2\nexit 1\n")
	if err := os.Chmod(filepath.Join(hookDir, "pre-push"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "config", "core.hooksPath", hookDir)
	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writePassingFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	_, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"create", "--iteration-dir", runDir})
	})
	if err == nil {
		t.Fatal("expected loop pr create to fail during branch push")
	}
	if !strings.Contains(err.Error(), "see `loop iteration read pr-checks`") {
		t.Fatalf("error should point to pr-checks artifact: %v", err)
	}
	checks, err := artifactdb.Read(runDir, "pr-checks")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(checks, `"status": "failed"`) || !strings.Contains(checks, `"branch": "feat/push-failure"`) || !strings.Contains(checks, "final-visual-route-contract.test.ts") {
		t.Fatalf("pr-checks artifact missing push failure details:\n%s", checks)
	}
	if _, ok, err := readPRState(runDir); err != nil || ok {
		t.Fatalf("pr-state should not exist after push failure: ok=%v err=%v", ok, err)
	}
	errorsLog := readText(t, filepath.Join(runDir, "errors.log"))
	if !strings.Contains(errorsLog, "pull request branch push failed for feat/push-failure; see pr-checks artifact") {
		t.Fatalf("errors.log missing concise push failure pointer:\n%s", errorsLog)
	}
	if data, err := os.ReadFile(ghLog); err == nil && strings.Contains(string(data), "pr create") {
		t.Fatalf("PR creation should not run after push failure:\n%s", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestPRChecksMarksHumanReviewPRWaiting(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: true
      waitChecks: true
      checksStartupDelaySeconds: 0
      checksDiscoveryTimeoutSeconds: 0
      checksPollIntervalSeconds: 1
      reviewMode: parallel_human_review
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add human review pr checks fixture")
	git(t, repo, "push", "origin", "develop")
	git(t, repo, "checkout", "-b", "test/human-review", "develop")
	mustWrite(t, filepath.Join(repo, "human-review.txt"), "change\n")
	git(t, repo, "add", "human-review.txt")
	git(t, repo, "commit", "-m", "F: add human review fixture")

	runDir := testIterationDir(t, repo, "run", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/human-review",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pr_review_mode":    "parallel_human_review",
		"pull_request_mode": true,
		"role_orchestrated": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "1", Branch: "test/human-review", Base: "develop"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writePassingFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"checks", "--iteration-dir", runDir})
	}); err != nil {
		t.Fatalf("loop pr checks: %v", err)
	}
	state, ok, err := readPRState(runDir)
	if err != nil || !ok {
		t.Fatalf("read pr-state: ok=%v err=%v", ok, err)
	}
	if state.Status != "waiting_for_human" {
		t.Fatalf("pr-state status = %q, want waiting_for_human", state.Status)
	}
	if err := commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"merge", "--iteration-dir", runDir}); err == nil || !strings.Contains(err.Error(), "human review mode") {
		t.Fatalf("loop pr merge error = %v, want human review rejection", err)
	}
}

func TestPRMergeRejectsInvalidIterationCommitBeforeHostMerge(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	addBareOrigin(t, repo)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: false
      deleteBranch: false
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add pr invalid commit fixture config")
	git(t, repo, "push", "origin", "develop")
	git(t, repo, "checkout", "-b", "test/fake-agent", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "Add preview claim screen (#4)")

	runDir := testIterationDir(t, repo, "run", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/fake-agent",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "1", Branch: "test/fake-agent", Base: "develop", Title: "Invalid iteration commit"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writePassingFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	_, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"merge", "--iteration-dir", runDir})
	})
	if err == nil {
		t.Fatal("expected loop pr merge to reject the invalid iteration commit")
	}
	if !strings.Contains(err.Error(), "Add preview claim screen (#4)") || !strings.Contains(err.Error(), "must use <TYPE>: <message>") {
		t.Fatalf("error should explain the invalid iteration commit: %v", err)
	}
	if data, readErr := os.ReadFile(ghLog); readErr == nil && strings.Contains(string(data), "pr merge") {
		t.Fatalf("host merge should not run after invalid iteration commit:\n%s", data)
	}
}

func TestPRMergeFetchesMergedPRIntoMemory(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: false
      deleteBranch: false
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add pr memory fetch fixture config")
	git(t, repo, "checkout", "-b", "test/fake-agent", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "F: add fake change")

	runDir := testIterationDir(t, repo, "run", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/fake-agent",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "9", Branch: "test/fake-agent", Base: "develop", Title: "Add fake change"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeMergeAndFetchFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"merge", "--iteration-dir", runDir})
	}); err != nil {
		t.Fatalf("loop pr merge: %v", err)
	}
	hits, err := artifactdb.SearchPRMemory(testStorage(t, repo).DBPath, artifactdb.PRMemorySearchOptions{Query: "merge fetch memory", Repo: "acme/app", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Record.Number != 9 || hits[0].Record.State != "merged" {
		t.Fatalf("merged PR was not fetched into memory: %+v", hits)
	}
	log := readText(t, ghLog)
	if !strings.Contains(log, "pr merge 9 --squash") || !strings.Contains(log, "api graphql") {
		t.Fatalf("expected merge and fetch GraphQL calls:\n%s", log)
	}
}

func TestPRMergeRecoversAlreadyMergedRemoteState(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1

git:
  baseBranch: develop
  integration:
    mode: pr
    pr:
      push: false
      waitChecks: false
      deleteBranch: false
`)
	git(t, repo, "add", ".loop/config.yaml")
	git(t, repo, "commit", "-m", "T: add already merged pr fixture config")
	git(t, repo, "checkout", "-b", "test/fake-agent", "develop")
	mustWrite(t, filepath.Join(repo, "change.txt"), "change\n")
	git(t, repo, "add", "change.txt")
	git(t, repo, "commit", "-m", "F: add fake change")

	runDir := testIterationDir(t, repo, "run", "0001")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteRuntimeArtifact(t, runDir, map[string]any{
		"run_id":            "run",
		"iteration_id":      "0001",
		"base_branch":       "develop",
		"initial_branch":    "wip/0001",
		"current_branch":    "test/fake-agent",
		"branch_renamed":    true,
		"integration_mode":  "pr",
		"pull_request_mode": true,
		"workdir":           repo,
	})
	if err := writePRState(runDir, prState{SchemaVersion: 1, Status: "created", PR: "9", Branch: "test/fake-agent", Base: "develop", Title: "Add fake change"}); err != nil {
		t.Fatal(err)
	}

	ghDir := t.TempDir()
	ghLog := filepath.Join(ghDir, "gh.log")
	writeAlreadyMergedFetchFakeGH(t, ghDir, ghLog)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandPR(ctx, globals{JSON: true, NoColor: true}, []string{"merge", "--iteration-dir", runDir})
	}); err != nil {
		t.Fatalf("loop pr merge should recover already-merged remote state: %v", err)
	}
	prState, err := artifactdb.Read(runDir, "pr-state")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prState, `"status": "merged"`) || !strings.Contains(prState, `"merged_at": "2026-05-20T01:00:00Z"`) {
		t.Fatalf("pr-state should record recovered merged PR:\n%s", prState)
	}
	log := readText(t, ghLog)
	if !strings.Contains(log, "pr merge 9 --squash") || !strings.Contains(log, "api graphql") {
		t.Fatalf("expected merge attempt and GraphQL recovery:\n%s", log)
	}
}

func addBareOrigin(t *testing.T, repo string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	cmd := exec.Command("git", "init", "--bare", origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare failed: %v\n%s", err, out)
	}
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-u", "origin", "develop")
}

func writeRepairingFakeGH(t *testing.T, dir, logPath, checksPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  count=0
  if [ -f ` + shellQuote(checksPath) + ` ]; then
    count=$(cat ` + shellQuote(checksPath) + `)
  fi
  count=$((count + 1))
  echo "$count" > ` + shellQuote(checksPath) + `
  if [ "$count" -eq 1 ]; then
    echo "unit test failed: missing dependency" >&2
    exit 1
  fi
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeDelayedChecksFakeGH(t *testing.T, dir, logPath, checksPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  count=0
  if [ -f ` + shellQuote(checksPath) + ` ]; then
    count=$(cat ` + shellQuote(checksPath) + `)
  fi
  count=$((count + 1))
  echo "$count" > ` + shellQuote(checksPath) + `
  if [ "$count" -lt 3 ]; then
    echo "no checks reported on the 'test/fake-agent' branch" >&2
    exit 1
  fi
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writePassingFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeMergeAndFetchFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  exit 0
fi

if [ "$1" = "api" ] && [ "$2" = "graphql" ]; then
cat <<'JSON'
{"data":{"repository":{"pullRequest":{"number":9,"url":"https://github.com/acme/app/pull/9","state":"MERGED","title":"Add merge fetch memory","body":"Merge fetch memory body.","updatedAt":"2026-05-20T00:00:00Z","mergedAt":"2026-05-20T01:00:00Z","repository":{"nameWithOwner":"acme/app"}}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeAlreadyMergedFetchFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  echo "Pull request acme/app#9 was already merged" >&2
  exit 1
fi

if [ "$1" = "api" ] && [ "$2" = "graphql" ]; then
cat <<'JSON'
{"data":{"repository":{"pullRequest":{"number":9,"url":"https://github.com/acme/app/pull/9","state":"MERGED","title":"Add merge fetch memory","body":"Merge fetch memory body.","updatedAt":"2026-05-20T00:00:00Z","mergedAt":"2026-05-20T01:00:00Z","repository":{"nameWithOwner":"acme/app"}}},"rateLimit":{"remaining":10,"resetAt":"2026-05-20T02:00:00Z","cost":1}}}
JSON
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeDetachOnMergeFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "1"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  echo "checks passed"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  git checkout --detach HEAD >/dev/null 2>&1
  git branch -D test/fake-agent >/dev/null 2>&1
  exit 0
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFailingChecksFakeGH(t *testing.T, dir, logPath string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> ` + shellQuote(logPath) + `

if [ "$1" = "--version" ]; then
  echo "gh version fake"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "checks" ]; then
  echo "Refreshing checks status every 5 seconds. Press Ctrl+C to quit."
  echo "unit test failed: missing dependency" >&2
  exit 1
fi

exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteRuntimeArtifact(t *testing.T, iterDir string, value map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := artifactdb.Write(iterDir, "runtime", string(data)); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

func yamlSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
