package doclint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAnalyzeReachabilityPlainPathsAndExclusions(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "AGENTS.md", "# Agents\n\nSee README.md, spec/00-index.md, and excluded/skip.md.\n")
	writeFile(t, repo, "README.md", "# Readme\n\nDocs: docs/guide.md and .github/PULL_REQUEST_TEMPLATE.md.\n")
	writeFile(t, repo, "spec/00-index.md", "# Index\n\n- 01-system-contract.md\n")
	writeFile(t, repo, "spec/01-system-contract.md", "# Contract\n")
	writeFile(t, repo, "docs/guide.md", "# Guide\n")
	writeFile(t, repo, ".github/PULL_REQUEST_TEMPLATE.md", "# PR\n")
	writeFile(t, repo, "excluded/skip.md", "# Excluded\n")
	writeFile(t, repo, "skills/local/SKILL.md", "# Skill\n")
	writeFile(t, repo, "nested/skills/SKILL.md", "# Nested skill\n")
	git(t, repo, "add", ".")

	report, err := Analyze(context.Background(), Options{
		Root:     repo,
		Entry:    "AGENTS.md",
		Excludes: []string{"excluded"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != "ok" || report.HasFindings() {
		t.Fatalf("report has findings: %+v", report)
	}
	for _, notWant := range []string{"excluded/skip.md", "skills/local/SKILL.md", "nested/skills/SKILL.md"} {
		if containsString(report.Tracked, notWant) {
			t.Fatalf("tracked should exclude %s: %+v", notWant, report.Tracked)
		}
	}
	for _, want := range []string{"AGENTS.md", "README.md", ".github/PULL_REQUEST_TEMPLATE.md", "docs/guide.md", "spec/00-index.md", "spec/01-system-contract.md"} {
		if !containsString(report.Reachable, want) {
			t.Fatalf("reachable missing %s: %+v", want, report.Reachable)
		}
	}
	if !reflect.DeepEqual(report.Excludes, []string{"excluded"}) {
		t.Fatalf("excludes = %+v", report.Excludes)
	}
}

func TestAnalyzeFindsUnreachableRequiredDocumentsAndInvalidReferences(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "AGENTS.md", strings.Join([]string{
		"# Agents",
		"",
		"See docs/live.md and docs/missing.md.",
		"Also [bad](docs/explicit-missing.md) and [outside](../outside.md).",
		"Ignore prompt.md and https://example.com/readme.md.",
		"Ignore ![diagram](docs/image.md).",
		"```",
		"docs/fenced-missing.md",
		"```",
		"",
	}, "\n"))
	writeFile(t, repo, "docs/live.md", "# Live\n")
	writeFile(t, repo, "docs/dead.md", "# Dead\n")
	git(t, repo, "add", ".")

	report, err := Analyze(context.Background(), Options{Root: repo, Entry: "AGENTS.md"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != "findings" {
		t.Fatalf("status = %q, want findings", report.Status)
	}
	if !reflect.DeepEqual(report.Unreachable, []string{"docs/dead.md"}) {
		t.Fatalf("unreachable required documents = %+v", report.Unreachable)
	}
	wantInvalid := []InvalidReference{
		{Source: "AGENTS.md", Line: 3, Target: "docs/missing.md", Reason: "not found"},
		{Source: "AGENTS.md", Line: 4, Target: "../outside.md", Reason: "outside repository"},
		{Source: "AGENTS.md", Line: 4, Target: "docs/explicit-missing.md", Reason: "not found"},
	}
	if !reflect.DeepEqual(report.InvalidReferences, wantInvalid) {
		t.Fatalf("invalid references = %+v", report.InvalidReferences)
	}
}

func TestAnalyzeLimitsUnreachableFindingsToRequiredReachablePatterns(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "AGENTS.md", "# Agents\n\nSee docs/live.md.\n")
	writeFile(t, repo, "docs/live.md", "# Live\n")
	writeFile(t, repo, "docs/optional.md", "# Optional\n")
	writeFile(t, repo, "spec/required.md", "# Required\n")
	writeFile(t, repo, "spec/excluded.md", "# Excluded\n")
	git(t, repo, "add", ".")

	report, err := Analyze(context.Background(), Options{
		Root:              repo,
		Entry:             "AGENTS.md",
		RequiredReachable: []string{"spec/*.md", "docs/live.md"},
		Excludes:          []string{"spec/excluded.md"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	wantRequired := []string{"docs/live.md", "spec/required.md"}
	if !reflect.DeepEqual(report.RequiredReachable, wantRequired) {
		t.Fatalf("required reachable = %+v, want %+v", report.RequiredReachable, wantRequired)
	}
	if !reflect.DeepEqual(report.Unreachable, []string{"spec/required.md"}) {
		t.Fatalf("unreachable required = %+v", report.Unreachable)
	}
	if containsString(report.Unreachable, "docs/optional.md") {
		t.Fatalf("optional document should not be reported unreachable: %+v", report.Unreachable)
	}
}

func TestAnalyzeRejectsInvalidExcludeValues(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "AGENTS.md", "# Agents\n")
	git(t, repo, "add", ".")

	for _, exclude := range []string{"", ".", "../docs", filepath.Join(repo, "docs")} {
		_, err := Analyze(context.Background(), Options{Root: repo, Entry: "AGENTS.md", Excludes: []string{exclude}})
		if err == nil {
			t.Fatalf("Analyze with exclude %q succeeded", exclude)
		}
		if !IsUsageError(err) {
			t.Fatalf("Analyze with exclude %q returned non-usage error: %v", exclude, err)
		}
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	return dir
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
