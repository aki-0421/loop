package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func TestIterationCommandWritesReadsAndAppendsArtifact(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	if _, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"write", "--iteration-dir", dir, "summary", "--value", "one\n"})
	}); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"append", "summary", "--iteration-dir", dir, "--value", "two\n"})
	}); err != nil {
		t.Fatalf("append summary: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"read", "summary", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if out != "one\ntwo\n" {
		t.Fatalf("summary content = %q", out)
	}
}

func TestWriteRuntimeArtifact(t *testing.T) {
	dir := t.TempDir()
	paths := pathSet{
		Runtime:         filepath.Join(dir, "runtime.json"),
		InstructionPath: filepath.Join(dir, "task.md"),
		InstructionRel:  "task.md",
		Goal:            "ship it",
		Language:        "en",
		IterationID:     "0001",
		BaseBranch:      "develop",
		CurrentBranch:   "wip/0001",
		IntegrationMode: "pr",
		PullRequestMode: true,
		WorkDir:         dir,
	}
	if err := writeRuntimeArtifact(paths); err != nil {
		t.Fatalf("writeRuntimeArtifact: %v", err)
	}
	data, err := artifactdb.Read(dir, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatal(err)
	}
	if got["instruction_rel"] != "task.md" || got["goal"] != "ship it" || got["integration_mode"] != "pr" || got["pull_request_mode"] != true {
		t.Fatalf("runtime = %#v", got)
	}
	if _, ok := got["mode"]; ok {
		t.Fatalf("runtime should not include removed mode: %#v", got)
	}
	if _, ok := got["plan_mode"]; ok {
		t.Fatalf("runtime should not include removed plan_mode: %#v", got)
	}
}

func TestIterationCommandRejectsReadOnlyArtifactWrites(t *testing.T) {
	err := commandIteration(context.Background(), globals{}, []string{"write", "--iteration-dir", t.TempDir(), "validation", "--value", "nope"})
	if err == nil {
		t.Fatal("write validation succeeded, want read-only error")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("error = %q", err)
	}
}

func TestIterationCommandReadsPullRequestTemplate(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, ".github", "PULL_REQUEST_TEMPLATE.md"), "## Summary\n\n## Review\n")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"read", "pr-template"})
	})
	if err != nil {
		t.Fatalf("read pr-template: %v", err)
	}
	if !strings.Contains(out, "## Review") {
		t.Fatalf("template output = %q", out)
	}
}

func TestIterationCommandReadsFallbackPullRequestTemplate(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"read", "pr-template"})
	})
	if err != nil {
		t.Fatalf("read fallback pr-template: %v", err)
	}
	for _, want := range []string{"## Summary", "## Verification"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fallback template missing %q:\n%s", want, out)
		}
	}

	_, err = captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"path", "pr-template"})
	})
	if err == nil || !strings.Contains(err.Error(), "pull request template not found") {
		t.Fatalf("path fallback pr-template error = %v", err)
	}
}

func TestIterationCommandReadsInstructionFromEnvironment(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	instruction := filepath.Join(dir, "task.md")
	if err := os.WriteFile(instruction, []byte("# Task\n\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOP_INSTRUCTION_FILE", instruction)

	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"read", "instruction"})
	})
	if err != nil {
		t.Fatalf("read instruction: %v", err)
	}
	if !strings.Contains(out, "Do the thing.") {
		t.Fatalf("instruction output = %q", out)
	}
}

func TestIterationCommandPathUsesArtifactName(t *testing.T) {
	dir := t.TempDir()
	if err := artifactdb.Write(dir, "runtime", `{"integration_mode":"local_merge"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	_, err := captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{"path", "pr_body", "--iteration-dir", dir})
	})
	if err == nil || !strings.Contains(err.Error(), "stored in the loop artifact database") {
		t.Fatalf("path pr_body error = %v", err)
	}

	out, err := captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{"path", "prompt", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("path prompt: %v", err)
	}
	if strings.TrimSpace(out) != filepath.Join(dir, "prompt.md") {
		t.Fatalf("path output = %q", out)
	}

	out, err = captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{"read", "runtime", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("read runtime: %v", err)
	}
	if !strings.Contains(out, "integration_mode") {
		t.Fatalf("runtime output = %q", out)
	}
}

func TestIterationCommandResolvesRunAndIteration(t *testing.T) {
	repo := newCleanupRepo(t)
	iterDir := filepath.Join(repo, ".loop", "runs", "run-1", "iterations", "0002")
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifactdb.Write(iterDir, "summary", "stored summary\n"); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	out, err := captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{ConfigPath: ".loop/config.yaml", Agent: "codex"}, []string{"read", "summary", "--run", "run-1", "--iteration", "0002"})
	})
	if err != nil {
		t.Fatalf("read by run and iteration: %v", err)
	}
	if strings.TrimSpace(out) != "stored summary" {
		t.Fatalf("summary output = %q", out)
	}
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	_ = r.Close()
	return string(out), runErr
}
