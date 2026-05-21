package cli

import (
	"context"
	"strings"
	"testing"
)

func TestMemoryCommandsRequireExplicitLimit(t *testing.T) {
	repo := newCleanupRepo(t)
	withWorkingDir(t, repo)

	for _, args := range [][]string{
		{"recent"},
		{"search", "checkout"},
	} {
		err := commandMemory(context.Background(), globals{}, args)
		if err == nil || !strings.Contains(err.Error(), "--limit is required") {
			t.Fatalf("commandMemory(%v) error = %v, want required limit", args, err)
		}
	}

	for _, args := range [][]string{
		{"recent", "--limit", "0"},
		{"search", "checkout", "--limit", "0"},
	} {
		err := commandMemory(context.Background(), globals{}, args)
		if err == nil || !strings.Contains(err.Error(), "--limit must be positive") {
			t.Fatalf("commandMemory(%v) error = %v, want positive limit", args, err)
		}
	}
}
