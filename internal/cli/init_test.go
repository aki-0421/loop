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
		return commandInit(context.Background(), globals{}, []string{"--force", "--skills=false", "--agent", "codex"})
	}); err != nil {
		t.Fatalf("loop init: %v", err)
	}

	text := readText(t, filepath.Join(repo, ".loop", "config.yaml"))
	for _, want := range []string{"version: 1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	for _, notWant := range []string{"baseBranch", "maxIterations", "adapters", "validation", "memory", "logs", "worktree", "commits"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("config should omit %q:\n%s", notWant, text)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".loop", ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("loop init should not create .loop/.gitignore, err=%v", err)
	}
}

func TestInitWritesPinnedBaseWhenRequested(t *testing.T) {
	repo := newCleanupRepo(t)
	withWorkingDir(t, repo)

	if _, err := captureStdout(t, func() error {
		return commandInit(context.Background(), globals{}, []string{"--force", "--skills=false", "--agent", "codex", "--base", "main"})
	}); err != nil {
		t.Fatalf("loop init: %v", err)
	}

	text := readText(t, filepath.Join(repo, ".loop", "config.yaml"))
	if !strings.Contains(text, "baseBranch: main") {
		t.Fatalf("config missing pinned base:\n%s", text)
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

func TestInitInstallsDefaultSkillWithSkillsCLI(t *testing.T) {
	repo := newCleanupRepo(t)
	withWorkingDir(t, repo)
	var gotRoot string
	var gotArgs []string
	old := runSkillsCLIAdd
	runSkillsCLIAdd = func(_ context.Context, root string, args []string, quiet bool) error {
		gotRoot = root
		gotArgs = append([]string(nil), args...)
		if quiet {
			t.Fatal("skills CLI install should stream output for text mode")
		}
		mustWriteTestFile(t, filepath.Join(root, ".agents", "skills", "loop", "SKILL.md"), "---\nname: loop\n")
		mustWriteTestFile(t, filepath.Join(root, "skills-lock.json"), "{}\n")
		return nil
	}
	t.Cleanup(func() { runSkillsCLIAdd = old })

	if _, err := captureStdout(t, func() error {
		return commandInit(context.Background(), globals{}, []string{"--force", "--agent", "codex"})
	}); err != nil {
		t.Fatalf("loop init: %v", err)
	}

	if !samePath(t, gotRoot, repo) {
		t.Fatalf("skills CLI root = %s, want %s", gotRoot, repo)
	}
	wantArgs := "--yes skills add aki-0421/loop --skill loop --agent codex --yes"
	if strings.Join(gotArgs, " ") != wantArgs {
		t.Fatalf("skills CLI args = %q, want %q", strings.Join(gotArgs, " "), wantArgs)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents", "skills", "loop", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "skills-lock.json")); err != nil {
		t.Fatal(err)
	}
}

func TestInitSkipsSkillsCLIWhenSkillExistsWithoutForce(t *testing.T) {
	repo := newCleanupRepo(t)
	path := filepath.Join(repo, ".agents", "skills", "loop", "SKILL.md")
	mustWriteTestFile(t, path, "custom")
	withWorkingDir(t, repo)
	old := runSkillsCLIAdd
	runSkillsCLIAdd = func(context.Context, string, []string, bool) error {
		t.Fatal("skills CLI should not run when an existing skill is present without --force")
		return nil
	}
	t.Cleanup(func() { runSkillsCLIAdd = old })

	if _, err := captureStdout(t, func() error {
		return commandInit(context.Background(), globals{}, []string{"--agent", "codex"})
	}); err != nil {
		t.Fatalf("loop init: %v", err)
	}

	if got := readText(t, path); got != "custom" {
		t.Fatalf("existing skill was overwritten:\n%s", got)
	}
}

func TestInitMapsClaudeAdapterForSkillsCLI(t *testing.T) {
	repo := newCleanupRepo(t)
	withWorkingDir(t, repo)
	var gotArgs []string
	old := runSkillsCLIAdd
	runSkillsCLIAdd = func(_ context.Context, root string, args []string, _ bool) error {
		gotArgs = append([]string(nil), args...)
		mustWriteTestFile(t, filepath.Join(root, ".claude", "skills", "loop", "SKILL.md"), "---\nname: loop\n")
		return nil
	}
	t.Cleanup(func() { runSkillsCLIAdd = old })

	if _, err := captureStdout(t, func() error {
		return commandInit(context.Background(), globals{}, []string{"--force", "--agent", "claude"})
	}); err != nil {
		t.Fatalf("loop init: %v", err)
	}

	if !strings.Contains(strings.Join(gotArgs, " "), "--agent claude-code") {
		t.Fatalf("skills CLI args should target claude-code, got %q", strings.Join(gotArgs, " "))
	}
	text := readText(t, filepath.Join(repo, ".loop", "config.yaml"))
	if !strings.Contains(text, "sourceDir: .claude/skills") {
		t.Fatalf("config missing Claude skill source:\n%s", text)
	}
}

func TestSkillsInstallLoopUsesSkillsCLI(t *testing.T) {
	repo := newCleanupRepo(t)
	withWorkingDir(t, repo)
	var gotRoot string
	var gotArgs []string
	old := runSkillsCLIAdd
	runSkillsCLIAdd = func(_ context.Context, root string, args []string, quiet bool) error {
		gotRoot = root
		gotArgs = append([]string(nil), args...)
		if quiet {
			t.Fatal("skills CLI install should stream output for text mode")
		}
		mustWriteTestFile(t, filepath.Join(root, ".agents", "skills", "loop", "SKILL.md"), "---\nname: loop\n")
		return nil
	}
	t.Cleanup(func() { runSkillsCLIAdd = old })

	if _, err := captureStdout(t, func() error {
		return commandSkills(context.Background(), globals{}, []string{"install", "--force", "loop"})
	}); err != nil {
		t.Fatalf("loop skills install: %v", err)
	}

	if !samePath(t, gotRoot, repo) {
		t.Fatalf("skills CLI root = %s, want %s", gotRoot, repo)
	}
	wantArgs := "--yes skills add aki-0421/loop --skill loop --agent codex --yes"
	if strings.Join(gotArgs, " ") != wantArgs {
		t.Fatalf("skills CLI args = %q, want %q", strings.Join(gotArgs, " "), wantArgs)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents", "skills", "loop", "SKILL.md")); err != nil {
		t.Fatal(err)
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

func mustWriteTestFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	aEval, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatal(err)
	}
	bEval, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatal(err)
	}
	return aEval == bEval
}
