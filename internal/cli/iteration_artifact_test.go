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
		return commandIteration(ctx, globals{}, []string{"write", "--iteration-dir", dir, "pr-body", "--value", "one\n"})
	}); err != nil {
		t.Fatalf("write pr-body: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"append", "pr-body", "--iteration-dir", dir, "--value", "two\n"})
	}); err != nil {
		t.Fatalf("append pr-body: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"read", "pr-body", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("read pr-body: %v", err)
	}
	if out != "one\ntwo\n" {
		t.Fatalf("pr-body content = %q", out)
	}
}

func TestWriteRuntimeArtifact(t *testing.T) {
	dir := t.TempDir()
	paths := pathSet{
		Runtime:         filepath.Join(dir, "runtime.json"),
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
	if got["goal"] != "ship it" || got["integration_mode"] != "pr" || got["pull_request_mode"] != true {
		t.Fatalf("runtime = %#v", got)
	}
	for _, key := range []string{"instruction_file", "instruction_path", "instruction_rel"} {
		if _, ok := got[key]; ok {
			t.Fatalf("runtime should not include %s: %#v", key, got)
		}
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

func TestIterationCommandRejectsGenericPlanTodoWrites(t *testing.T) {
	for _, args := range [][]string{
		{"write", "--iteration-dir", t.TempDir(), "plan", "--value", "nope"},
		{"append", "--iteration-dir", t.TempDir(), "plan", "--value", "nope"},
		{"write", "--iteration-dir", t.TempDir(), "todo", "--value", "- [ ] nope\n"},
		{"append", "--iteration-dir", t.TempDir(), "todo", "--value", "- [ ] nope\n"},
	} {
		err := commandIteration(context.Background(), globals{}, args)
		if err == nil {
			t.Fatalf("commandIteration(%v) succeeded, want dedicated command error", args)
		}
		if !strings.Contains(err.Error(), "dedicated commands") {
			t.Fatalf("commandIteration(%v) error = %q", args, err)
		}
	}
}

func TestIterationPlanCommandTemplateWriteRead(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	template, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"plan", "template"})
	})
	if err != nil {
		t.Fatalf("plan template: %v", err)
	}
	if !strings.Contains(template, "# Iteration Plan") || strings.Contains(template, "## TODO") {
		t.Fatalf("unexpected plan template:\n%s", template)
	}

	if _, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"plan", "write", "--iteration-dir", dir, "--value", "filled plan\n"})
	}); err != nil {
		t.Fatalf("plan write: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"plan", "read", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("plan read: %v", err)
	}
	if out != "filled plan\n" {
		t.Fatalf("plan read = %q", out)
	}

	jsonOut, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{JSON: true}, []string{"plan", "read", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("plan read json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("plan json should parse: %v\n%s", err, jsonOut)
	}
	if payload["artifact"] != "plan" || payload["content"] != "filled plan\n" {
		t.Fatalf("plan json = %#v", payload)
	}
}

func TestIterationTodoCommandMutatesOneItemAtATime(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	if _, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"todo", "insert", "--iteration-dir", dir, "--type", "F", "add", "feature", "shell"})
	}); err != nil {
		t.Fatalf("todo insert first: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"todo", "insert", "--iteration-dir", dir, "--after", "0", "--type", "T", "add", "feature", "tests"})
	}); err != nil {
		t.Fatalf("todo insert at top: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"todo", "edit", "--iteration-dir", dir, "2", "--type", "F", "add", "revised", "feature", "shell"})
	}); err != nil {
		t.Fatalf("todo edit: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"todo", "complete", "--iteration-dir", dir, "1"})
	}); err != nil {
		t.Fatalf("todo complete: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"todo", "list", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("todo list: %v", err)
	}
	want := "1. [x] T: add feature tests\n2. [ ] F: add revised feature shell\n"
	if out != want {
		t.Fatalf("todo list = %q, want %q", out, want)
	}

	raw, err := artifactdb.Read(dir, "todo")
	if err != nil {
		t.Fatal(err)
	}
	if raw != "- [x] T: add feature tests\n- [ ] F: add revised feature shell\n" {
		t.Fatalf("stored todo = %q", raw)
	}

	jsonOut, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{JSON: true}, []string{"todo", "list", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("todo list json: %v", err)
	}
	var payload struct {
		Artifact string `json:"artifact"`
		Items    []struct {
			Index   int    `json:"index"`
			Status  string `json:"status"`
			Text    string `json:"text"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("todo json should parse: %v\n%s", err, jsonOut)
	}
	if payload.Artifact != "todo" || len(payload.Items) != 2 || payload.Items[0].Index != 1 || payload.Items[0].Status != "done" || payload.Items[0].Type != "T" || payload.Items[1].Text != "F: add revised feature shell" || payload.Items[1].Message != "add revised feature shell" {
		t.Fatalf("todo json = %#v", payload)
	}
}

func TestIterationTodoCommandRejectsInvalidTextAndIndexes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"todo", "insert", "--iteration-dir", dir},
		{"todo", "insert", "--iteration-dir", dir, "F", "add", "feature"},
		{"todo", "insert", "--iteration-dir", dir, "--type", "nope", "add", "feature"},
		{"todo", "insert", "--iteration-dir", dir, "--type", "F", "Add", "feature"},
		{"todo", "insert", "--iteration-dir", dir, "--type", "F", "add", "feature."},
		{"todo", "edit", "--iteration-dir", dir, "1", "--type", "F", "add", "missing"},
		{"todo", "complete", "--iteration-dir", dir, "1"},
		{"todo", "complete", "--iteration-dir", dir},
	} {
		err := commandIteration(ctx, globals{}, args)
		if err == nil {
			t.Fatalf("commandIteration(%v) succeeded, want error", args)
		}
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

func TestIterationCommandReadsInstructionFromPromptSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("# Task\n\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{}, []string{"read", "instruction", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("read instruction: %v", err)
	}
	if !strings.Contains(out, "Do the thing.") {
		t.Fatalf("instruction output = %q", out)
	}

	jsonOut, err := captureStdout(t, func() error {
		return commandIteration(ctx, globals{JSON: true}, []string{"read", "instruction", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("read instruction json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["path"]; ok {
		t.Fatalf("instruction json leaked path: %#v", payload)
	}
	if payload["content"] != "# Task\n\nDo the thing.\n" {
		t.Fatalf("instruction json content = %#v", payload)
	}
}

func TestIterationCommandPathUsesArtifactName(t *testing.T) {
	dir := t.TempDir()
	if err := artifactdb.Write(dir, "runtime", `{"integration_mode":"local_merge"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{"path", "pr_body", "--iteration-dir", dir})
	})
	if err != nil {
		t.Fatalf("path pr_body error = %v", err)
	}
	if strings.TrimSpace(out) != filepath.Join(dir, "pr-body.md") {
		t.Fatalf("path pr_body output = %q", out)
	}

	out, err = captureStdout(t, func() error {
		return commandIteration(context.Background(), globals{}, []string{"path", "prompt", "--iteration-dir", dir})
	})
	if err == nil || !strings.Contains(err.Error(), "path is not exposed") {
		t.Fatalf("path prompt error = %v, output = %q", err, out)
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
	if err := artifactdb.Write(iterDir, "pr-body", "stored PR body\n"); err != nil {
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
		return commandIteration(context.Background(), globals{ConfigPath: ".loop/config.yaml", Agent: "codex"}, []string{"read", "pr-body", "--run", "run-1", "--iteration", "0002"})
	})
	if err != nil {
		t.Fatalf("read by run and iteration: %v", err)
	}
	if strings.TrimSpace(out) != "stored PR body" {
		t.Fatalf("pr-body output = %q", out)
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
