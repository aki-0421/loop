package validation

import (
	"context"
	"os"
	"path/filepath"
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
