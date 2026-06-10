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
		"Use `loop iteration`, `loop issue`, `loop review`, and `loop handoff` commands",
		"Read artifacts as `loop iteration read validation`, `loop iteration read events`, or `loop iteration path events`.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembled prompt missing %q:\n%s", want, text)
		}
	}
	for _, notWant := range []string{
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
	if strings.Contains(normal, "Pull request mode") || strings.Contains(normal, "LOOP_") {
		t.Fatalf("non-PR prompt should not mention PR-specific instructions:\n%s", normal)
	}

	text := Assemble(Request{
		Language:        "ja",
		PullRequestMode: true,
	})
	for _, want := range []string{
		"QA review agents write review-result; merge agents own PR creation, checks, and merge through loop-owned PR commands after approval",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("PR prompt missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "LOOP_") || strings.Contains(text, "Pull request mode") {
		t.Fatalf("PR prompt should not expose runtime transport details:\n%s", text)
	}
}
