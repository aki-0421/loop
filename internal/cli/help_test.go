package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHelpCommandListsCommands(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, nil)
	})
	if err != nil {
		t.Fatalf("loop help: %v", err)
	}
	for _, want := range []string{
		"Loop commands:",
		"loop run",
		"Run `loop help <command>` for details.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
	for _, notWant := range []string{"loop iteration", "loop commit", "loop branch", "loop memory"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("human help should hide %q:\n%s", notWant, out)
		}
	}
}

func TestHelpCommandRejectsAgentOnlyDetail(t *testing.T) {
	err := commandHelp(context.Background(), globals{}, []string{"handoff", "write"})
	if err == nil {
		t.Fatal("human help should reject agent-only topics")
	}
	if !strings.Contains(err.Error(), "loop help agent handoff write") {
		t.Fatalf("error should point to agent help: %v", err)
	}
}

func TestMemoryCommandIsRemoved(t *testing.T) {
	err := Run([]string{"memory", "search", "checkout", "--limit", "1"})
	if err == nil {
		t.Fatal("expected memory command to be unavailable")
	}
	if !strings.Contains(err.Error(), `unknown command "memory"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestAgentHelpCommandShowsCompactList(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"agent"})
	})
	if err != nil {
		t.Fatalf("loop help agent: %v", err)
	}
	for _, want := range []string{
		"agent-help-v1\n",
		"cmd:loop handoff write task-tree|task-result|review-result",
		"cmd:loop iteration read artifact",
		"cmd:loop issue report",
		"artifacts:",
		"runtime:r:file",
		"task-tree:r:file",
		"detail:loop help agent <command...>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent help output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "  ") || strings.Contains(out, "\n\n") {
		t.Fatalf("agent help should avoid padding and blank lines:\n%s", out)
	}
	for _, notWant := range []string{"loop memory", "loop commit", "loop branch", "loop pr", "loop iteration todo", "loop iteration close", "todo:rw", "plan:rw"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("agent help should not expose %q:\n%s", notWant, out)
		}
	}
}

func TestAgentHelpCommandShowsHandoffWriteSchemas(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"agent", "handoff", "write"})
	})
	if err != nil {
		t.Fatalf("loop help agent handoff write: %v", err)
	}
	for _, want := range []string{
		"cmd:loop handoff write task-tree|task-result|review-result",
		"task-tree={schema_version:1,summary:string,goal_evaluation:string",
		"depends_on:[]",
		"conflicts_with:[]",
		"commit_type:F|T|R|D|S|V|C",
		"task-result={schema_version:1,task_id:string,status:completed|failed|skipped",
		"review-result={schema_version:1,status:approved|changes_requested|failed",
		"--file path",
		"--value json",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
}

func TestAgentHelpCommandHidesLegacyLifecycleDetails(t *testing.T) {
	for _, topic := range [][]string{
		{"agent", "branch", "rename"},
		{"agent", "commit"},
		{"agent", "iteration", "close"},
		{"agent", "iteration", "plan"},
		{"agent", "iteration", "todo"},
		{"agent", "iteration", "todo", "insert"},
		{"agent", "pr", "create"},
	} {
		err := commandHelp(context.Background(), globals{}, topic)
		if err == nil {
			t.Fatalf("loop help %v should be hidden from role-agent help", topic)
		}
	}
}

func TestAgentHelpCommandShowsIterationArtifactsFromRegistry(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"agent", "iteration", "read"})
	})
	if err != nil {
		t.Fatalf("loop help agent iteration read: %v", err)
	}
	for _, want := range []string{
		"artifacts:",
		"runtime:r:file",
		"instruction:r:file",
		"validation:r:file",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
	for _, notWant := range []string{"todo:rw", "plan:rw"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("role-agent artifact help should not expose %q:\n%s", notWant, out)
		}
	}
}

func TestHelpCommandHidesLegacyLifecycleDetailsFromHumanHelp(t *testing.T) {
	for _, topic := range [][]string{
		{"branch", "rename"},
		{"commit"},
		{"iteration", "close"},
		{"iteration", "todo"},
		{"pr", "create"},
	} {
		err := commandHelp(context.Background(), globals{}, topic)
		if err == nil {
			t.Fatalf("loop help %v should be hidden from human help", topic)
		}
		if strings.Contains(err.Error(), "loop help agent") {
			t.Fatalf("hidden compatibility topic should not point to role-agent help: %v", err)
		}
	}
}

func TestAgentHelpCommandShowsIssueDetails(t *testing.T) {
	for _, topic := range [][]string{
		{"agent", "issue", "ask"},
		{"agent", "issue", "report"},
	} {
		out, err := captureStdout(t, func() error {
			return commandHelp(context.Background(), globals{}, topic)
		})
		if err != nil {
			t.Fatalf("loop help %v: %v", topic, err)
		}
		if !strings.Contains(out, "--title text") || !strings.Contains(out, "--body text") {
			t.Fatalf("issue help %v missing title/body flags:\n%s", topic, out)
		}
	}
}

func TestHelpCommandPrintsJSON(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return Run([]string{"--json", "help"})
	})
	if err != nil {
		t.Fatalf("loop --json help: %v", err)
	}
	var list struct {
		Commands []helpSummary `json:"commands"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("help JSON should parse: %v\n%s", err, out)
	}
	if len(list.Commands) == 0 {
		t.Fatalf("commands should not be empty: %#v", list)
	}
	for _, command := range list.Commands {
		if strings.HasPrefix(command.Command, "loop iteration") || command.Command == "loop commit" {
			t.Fatalf("human JSON help should hide agent-only command: %#v", command)
		}
	}

	out, err = captureStdout(t, func() error {
		return Run([]string{"--json", "help", "agent", "iteration", "read"})
	})
	if err != nil {
		t.Fatalf("loop --json help agent iteration read: %v", err)
	}
	var detail struct {
		Command   string         `json:"command"`
		Usage     string         `json:"usage"`
		Artifacts []helpArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("help detail JSON should parse: %v\n%s", err, out)
	}
	if detail.Command != "loop iteration read" || !strings.Contains(detail.Usage, "loop iteration read") || len(detail.Artifacts) == 0 {
		t.Fatalf("detail JSON = %#v", detail)
	}
}

func TestRunUnknownCommandMentionsHelp(t *testing.T) {
	err := Run([]string{"nope"})
	if err == nil {
		t.Fatal("unknown command should fail")
	}
	if !strings.Contains(err.Error(), "loop help") {
		t.Fatalf("error should mention loop help: %v", err)
	}
}

func TestLinterCommandIsRemoved(t *testing.T) {
	err := Run([]string{"linter", "document"})
	if err == nil {
		t.Fatal("removed linter command should fail")
	}
	if code, ok := ExitCode(err); !ok || code != 2 {
		t.Fatalf("linter removal error code = %d, %v; want 2", code, err)
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("error should report an unknown command: %v", err)
	}
}

func TestRunHelpAliases(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}} {
		out, err := captureStdout(t, func() error {
			return Run(args)
		})
		if err != nil {
			t.Fatalf("Run(%v): %v", args, err)
		}
		if !strings.Contains(out, "Loop commands:") {
			t.Fatalf("Run(%v) output = %q", args, out)
		}
	}
}
