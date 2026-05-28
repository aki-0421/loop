package prompt

import (
	"strings"
	"testing"
)

func TestAssembleIncludesOnlyBootstrap(t *testing.T) {
	text := Assemble(Request{
		Language: "en",
	})
	for _, want := range []string{
		"Use the `loop` skill",
		"Use `loop iteration`, `loop memory`, `loop issue`, and `loop handoff` commands",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembled prompt missing %q:\n%s", want, text)
		}
	}
	for _, notWant := range []string{
		oldSkill("iteration"),
		oldSkill("memory"),
		oldSkill("commit"),
		oldSkill("pr-writer"),
		oldSkill("repair"),
		"LOOP_",
		"User Instruction (Verbatim)",
		"Source:",
		"Implement the feature.",
		"Run Goal",
		"Feature is complete.",
	} {
		if strings.Contains(text, notWant) {
			t.Fatalf("assembled prompt should omit %q:\n%s", notWant, text)
		}
	}
}

func TestAssembleMentionsPRWriterOnlyInPullRequestMode(t *testing.T) {
	normal := Assemble(Request{
		Language: "ja",
	})
	if strings.Contains(normal, oldSkill("pr-writer")) || strings.Contains(normal, "Pull request mode") || strings.Contains(normal, "LOOP_") {
		t.Fatalf("non-PR prompt should not mention PR-specific instructions:\n%s", normal)
	}

	text := Assemble(Request{
		Language:        "ja",
		PullRequestMode: true,
	})
	for _, want := range []string{
		"Pull request creation, check waiting, repairs, and merging are performed by the CLI",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("PR prompt missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, oldSkill("pr-writer")) || strings.Contains(text, "LOOP_") || strings.Contains(text, "Pull request mode") {
		t.Fatalf("PR prompt should not expose runtime transport details:\n%s", text)
	}
}

func oldSkill(suffix string) string {
	return "loop-" + suffix
}
