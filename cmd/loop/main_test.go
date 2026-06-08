package main

import "testing"

func TestIsVersionRequestAllowsLeadingGlobalFlags(t *testing.T) {
	for _, args := range [][]string{
		{"version"},
		{"--version"},
		{"-v"},
		{"--agent", "claude", "version"},
		{"--agent=claude", "version"},
		{"--no-color", "--agent", "claude", "version"},
		{"--json", "--cwd", "/tmp/repo", "version"},
	} {
		if !isVersionRequest(args) {
			t.Fatalf("isVersionRequest(%v) = false, want true", args)
		}
	}
}

func TestIsVersionRequestRejectsNonVersionCommands(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"run", "version"},
		{"--agent"},
		{"--unknown", "version"},
	} {
		if isVersionRequest(args) {
			t.Fatalf("isVersionRequest(%v) = true, want false", args)
		}
	}
}
