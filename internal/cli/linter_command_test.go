package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinterDocumentWarnsByDefault(t *testing.T) {
	repo := initLinterRepo(t)
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "# Agents\n")
	mustWrite(t, filepath.Join(repo, "docs", "dead.md"), "# Dead\n")
	git(t, repo, "add", ".")
	t.Chdir(repo)

	out, err := captureStdout(t, func() error {
		return commandLinter(context.Background(), globals{}, []string{"document"})
	})
	if err != nil {
		t.Fatalf("linter document should warn without failing: %v", err)
	}
	for _, want := range []string{"document linter warning", "unreachable required documents:", "docs/dead.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestLinterDocumentStrictFailsOnFindings(t *testing.T) {
	repo := initLinterRepo(t)
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "# Agents\n")
	mustWrite(t, filepath.Join(repo, "docs", "dead.md"), "# Dead\n")
	git(t, repo, "add", ".")
	t.Chdir(repo)

	out, err := captureStdout(t, func() error {
		return commandLinter(context.Background(), globals{}, []string{"document", "--strict"})
	})
	if err == nil {
		t.Fatal("strict linter should fail on findings")
	}
	if code, ok := ExitCode(err); !ok || code != 1 {
		t.Fatalf("strict error code = %d, %v; want 1", code, err)
	}
	if !strings.Contains(out, "docs/dead.md") {
		t.Fatalf("strict output should still include findings:\n%s", out)
	}
}

func TestLinterDocumentExcludesPathPrefixes(t *testing.T) {
	repo := initLinterRepo(t)
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "# Agents\n")
	mustWrite(t, filepath.Join(repo, "docs", "dead.md"), "# Dead\n")
	mustWrite(t, filepath.Join(repo, "notes", "dead.md"), "# Dead\n")
	mustWrite(t, filepath.Join(repo, "loop.json"), `{
  "version": 1,
  "linter": {
    "document": {
      "entry": "AGENTS.md",
      "requiredReachable": ["**/*.md"],
      "excludes": ["docs", "notes"]
    }
  }
}
`)
	git(t, repo, "add", ".")
	t.Chdir(repo)

	out, err := captureStdout(t, func() error {
		return commandLinter(context.Background(), globals{}, []string{"document", "--config", "loop.json"})
	})
	if err != nil {
		t.Fatalf("linter document with excludes: %v", err)
	}
	if strings.TrimSpace(out) != "document linter ok" {
		t.Fatalf("output = %q", out)
	}
}

func TestLinterDocumentRejectsUsageErrors(t *testing.T) {
	repo := initLinterRepo(t)
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "# Agents\n")
	mustWrite(t, filepath.Join(repo, "README.md"), "# Readme\n")
	mustWrite(t, filepath.Join(repo, "docs", "entry.md"), "# Entry\n")
	mustWrite(t, filepath.Join(repo, "plain.txt"), "not markdown\n")
	git(t, repo, "add", "AGENTS.md", "README.md", "docs/entry.md", "plain.txt")
	mustWrite(t, filepath.Join(repo, "UNTRACKED.md"), "# Untracked\n")
	t.Chdir(repo)

	configs := map[string]string{
		"nested.json":    `{"version":1,"linter":{"document":{"entry":"docs/entry.md"}}}`,
		"excluded.json":  `{"version":1,"linter":{"document":{"entry":"AGENTS.md","excludes":["AGENTS"]}}}`,
		"untracked.json": `{"version":1,"linter":{"document":{"entry":"UNTRACKED.md"}}}`,
		"plain.json":     `{"version":1,"linter":{"document":{"entry":"plain.txt"}}}`,
	}
	for name, content := range configs {
		mustWrite(t, filepath.Join(repo, name), content+"\n")
	}

	cases := [][]string{
		{"document", "AGENTS.md"},
		{"document", "--config", "nested.json"},
		{"document", "--config", "excluded.json"},
		{"document", "--config", "untracked.json"},
		{"document", "--config", "plain.json"},
	}
	for _, args := range cases {
		_, err := captureStdout(t, func() error {
			return commandLinter(context.Background(), globals{}, args)
		})
		if err == nil {
			t.Fatalf("commandLinter(%v) succeeded", args)
		}
		if code, ok := ExitCode(err); !ok || code != 2 {
			t.Fatalf("commandLinter(%v) code = %d, %v; want 2", args, code, err)
		}
	}
}

func TestLinterDocumentJSONShape(t *testing.T) {
	repo := initLinterRepo(t)
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "# Agents\n")
	mustWrite(t, filepath.Join(repo, "docs", "dead.md"), "# Dead\n")
	mustWrite(t, filepath.Join(repo, "loop.json"), `{"version":1,"linter":{"document":{"entry":"AGENTS.md","requiredReachable":["AGENTS.md"],"excludes":["docs"]}}}`)
	git(t, repo, "add", ".")
	t.Chdir(repo)

	out, err := captureStdout(t, func() error {
		return commandLinter(context.Background(), globals{JSON: true}, []string{"document", "--strict", "--config", "loop.json"})
	})
	if err != nil {
		t.Fatalf("linter document json: %v", err)
	}
	var value struct {
		Status            string   `json:"status"`
		Entry             string   `json:"entry"`
		Strict            bool     `json:"strict"`
		Config            string   `json:"config"`
		RequiredReachable []string `json:"required_reachable"`
		Excludes          []string `json:"excludes"`
		Unreachable       []string `json:"unreachable_required"`
		InvalidReferences []any    `json:"invalid_references"`
	}
	if err := json.Unmarshal([]byte(out), &value); err != nil {
		t.Fatalf("unmarshal json: %v\n%s", err, out)
	}
	if value.Status != "ok" || value.Entry != "AGENTS.md" || !value.Strict {
		t.Fatalf("unexpected json value: %+v", value)
	}
	if value.Config != "loop.json" {
		t.Fatalf("config = %q, want loop.json", value.Config)
	}
	if len(value.RequiredReachable) != 1 || value.RequiredReachable[0] != "AGENTS.md" {
		t.Fatalf("required reachable = %+v", value.RequiredReachable)
	}
	if len(value.Excludes) != 1 || value.Excludes[0] != "docs" {
		t.Fatalf("excludes = %+v", value.Excludes)
	}
	if len(value.Unreachable) != 0 || len(value.InvalidReferences) != 0 {
		t.Fatalf("json should have no findings: %+v", value)
	}
}

func initLinterRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	git(t, dir, "init", "-b", "main")
	return dir
}
