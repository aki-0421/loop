package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesMinimalConfigForDefaults(t *testing.T) {
	repo := newCleanupRepo(t)
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandInit(context.Background(), globals{}, []string{"--force", "--skills=false", "--agent", "codex", "--base", "main"})
	}); err != nil {
		t.Fatalf("loop init: %v", err)
	}

	text := readText(t, filepath.Join(repo, ".loop", "config.yaml"))
	for _, want := range []string{"version: 1", "baseBranch: main"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	for _, notWant := range []string{"maxIterations", "adapters", "validation", "memory", "logs", "worktree"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("config should omit %q:\n%s", notWant, text)
		}
	}
}

func TestInitRecordsNonDefaultAgentAndExistingSkillDir(t *testing.T) {
	repo := newCleanupRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".codex", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandInit(context.Background(), globals{}, []string{"--force", "--skills=false", "--agent", "claude", "--base", "develop"})
	}); err != nil {
		t.Fatalf("loop init: %v", err)
	}

	text := readText(t, filepath.Join(repo, ".loop", "config.yaml"))
	for _, want := range []string{"default: claude", "command: claude", "sourceDir: .codex/skills"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
