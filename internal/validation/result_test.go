package validation

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateResultJSON(t *testing.T) {
	valid := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary_sentence": "Add checkout validation",
		"should_fully_stop": true,
		"goal_evaluation": "The requested goal is complete.",
		"branch": {"initial_name": "wip/0001"},
		"commits": [{"message": "T: add checkout validation"}],
		"validation": {"status": "passed", "commands": []},
		"artifacts": {"summary": "summary.md"}
	}`)
	if _, err := ValidateResultJSON(valid); err != nil {
		t.Fatal(err)
	}
	invalid := strings.Replace(string(valid), `"T: add checkout validation"`, `""`, 1)
	if _, err := ValidateResultJSON([]byte(invalid)); err == nil {
		t.Fatal("expected empty commit message to fail")
	}
}

func TestRunnerWritesMarkdownAndReturnsRequiredFailure(t *testing.T) {
	dir := t.TempDir()
	runner := Runner{WorkDir: dir, OutputPath: filepath.Join(dir, "validation.md")}
	results, err := runner.Run(context.Background(), []Command{
		{Name: "pass", Run: "printf ok", Required: true},
		{Name: "fail", Run: "printf nope && exit 2", Required: true},
	})
	if err == nil {
		t.Fatal("expected required failure")
	}
	if got := StatusFromResults(results); got != "failed" {
		t.Fatalf("status = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "validation.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "printf nope") {
		t.Fatalf("validation markdown missing command output:\n%s", data)
	}
}

func TestRunnerUsesUserDefaultShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SHELL is not the default shell source on Windows")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "shell-args.log")
	shellPath := filepath.Join(dir, "zsh")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_SHELL_LOG"
if [ "$1" = "-lc" ]; then
  exec /bin/sh -c "$2"
fi
exec /bin/sh "$@"
`
	if err := os.WriteFile(shellPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shellPath)
	t.Setenv("FAKE_SHELL_LOG", logPath)

	runner := Runner{WorkDir: dir}
	results, err := runner.Run(context.Background(), []Command{
		{Name: "shell", Run: "printf shell-ok", Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Output != "shell-ok" {
		t.Fatalf("results = %#v", results)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "-lc\nprintf shell-ok\n" {
		t.Fatalf("shell args = %q", got)
	}
}
