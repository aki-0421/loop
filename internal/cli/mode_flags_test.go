package cli

import (
	"context"
	"strings"
	"testing"
)

func TestRemovedRunModeFlagsAreRejected(t *testing.T) {
	for _, flagName := range []string{"--copilot", "--autonomous", "--plan", "--no-plan"} {
		t.Run(flagName, func(t *testing.T) {
			err := commandRun(context.Background(), globals{}, []string{"task.md", flagName})
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
				t.Fatalf("expected unknown flag error for %s, got %v", flagName, err)
			}
		})
	}
}

func TestRemovedResumeCopilotFlagIsRejected(t *testing.T) {
	err := commandResume(context.Background(), globals{}, []string{"run-1", "--copilot"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("expected unknown flag error for --copilot, got %v", err)
	}
}
