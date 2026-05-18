package cli

import (
	"strings"
	"testing"
)

func TestMinimalInitConfigOmitsDefaultSections(t *testing.T) {
	data, err := minimalInitConfig("codex", "main", ".agents/skills")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
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

func TestMinimalInitConfigRecordsNonDefaultChoices(t *testing.T) {
	data, err := minimalInitConfig("claude", "develop", ".codex/skills")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"default: claude", "command: claude", "sourceDir: .codex/skills"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}
