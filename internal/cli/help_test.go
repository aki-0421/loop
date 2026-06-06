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
	for _, notWant := range []string{"loop iteration", "loop commit", "loop branch", "loop role", "loop memory"} {
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
	if !strings.Contains(err.Error(), "available to agent processes through `loop help handoff write`") {
		t.Fatalf("error should explain agent-only help: %v", err)
	}
}

func TestAgentContextHelpCommandShowsCompactList(t *testing.T) {
	t.Setenv("LOOP_AGENT_CONTEXT", "1")
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, nil)
	})
	if err != nil {
		t.Fatalf("loop help: %v", err)
	}
	for _, want := range []string{
		"agent-help-v1\n",
		"context:LOOP_AGENT_CONTEXT=1 selects this agent-facing reference for `loop help`",
		"rule:read runtime and instruction with `loop iteration read runtime` and `loop iteration read instruction` before acting.",
		"rule:run `loop role instruction` for the current LOOP_ROLE-specific operating rules.",
		"rule:use `loop help`, `loop help issue`, `loop help task todo`, and `loop help handoff write`; read artifacts as `loop iteration read validation`.",
		"planner:write one AI sprint-sized task-tree",
		"coding:create task TODOs before edits; commit TODOs need one quoted final commit-message argument",
		"review:inspect diff, task results, validation, browser QA",
		"merge:after review approval",
		"cmd:loop branch rename",
		"cmd:loop handoff write task-tree|task-result|review-result|merge-result",
		"cmd:loop iteration path|read|write",
		"cmd:loop iteration read $artifact",
		"cmd:loop iteration write $artifact",
		"cmd:loop issue report",
		"cmd:loop pr create",
		"cmd:loop pr merge",
		"cmd:loop role instruction",
		"cmd:loop task merge",
		"artifacts:",
		"runtime:r:file",
		"task-tree:r:file",
		"detail:loop help <command...>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent help output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "  ") || strings.Contains(out, "\n\n") {
		t.Fatalf("agent help should avoid padding and blank lines:\n%s", out)
	}
	for _, notWant := range []string{"loop memory", "loop commit", "loop iteration todo", "loop iteration close", "todo:rw", "plan:rw"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("agent help should not expose %q:\n%s", notWant, out)
		}
	}
}

func TestAgentContextHelpCommandShowsReviewPRDetails(t *testing.T) {
	t.Setenv("LOOP_AGENT_CONTEXT", "1")
	for _, topic := range [][]string{
		{"branch", "rename"},
		{"pr", "create"},
		{"pr", "checks"},
		{"pr", "logs"},
		{"pr", "merge"},
		{"role", "instruction"},
		{"iteration", "write"},
	} {
		out, err := captureStdout(t, func() error {
			return commandHelp(context.Background(), globals{}, topic)
		})
		if err != nil {
			t.Fatalf("loop help %v: %v", topic, err)
		}
		if !strings.Contains(out, "cmd:loop "+strings.Join(topic, " ")) {
			t.Fatalf("help %v missing command detail:\n%s", topic, out)
		}
	}
}

func TestHumanHelpForAgentOnlyTopicsPointsToAgentHelp(t *testing.T) {
	for _, topic := range [][]string{
		{"branch"},
		{"branch", "rename"},
		{"iteration"},
		{"pr", "create"},
		{"pr", "checks"},
		{"pr", "logs"},
		{"pr", "merge"},
		{"role"},
		{"role", "instruction"},
		{"iteration", "write"},
	} {
		err := commandHelp(context.Background(), globals{}, topic)
		if err == nil {
			t.Fatalf("human help %v should reject agent-only topic", topic)
		}
		want := "loop help " + strings.Join(topic, " ")
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("human help %v should point to %q, got %v", topic, want, err)
		}
	}
}

func TestAgentContextHelpCommandShowsHandoffWriteSchemas(t *testing.T) {
	t.Setenv("LOOP_AGENT_CONTEXT", "1")
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"handoff", "write"})
	})
	if err != nil {
		t.Fatalf("loop help handoff write: %v", err)
	}
	for _, want := range []string{
		"cmd:loop handoff write task-tree|task-result|review-result|merge-result",
		"task-tree={schema_version:1,summary:string,goal_evaluation:string",
		"depends_on:[]",
		"conflicts_with:[]",
		"task-result={schema_version:1,task_id:string,status:completed|discarded|failed",
		"review-result={schema_version:1,status:approved|changes_requested|failed",
		"merge-result={schema_version:1,status:merged|waiting_for_human|pr_check_failed",
		"--file $path",
		"--value $json",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
}

func TestAgentContextHelpCommandShowsIterationArtifactsFromRegistry(t *testing.T) {
	t.Setenv("LOOP_AGENT_CONTEXT", "1")
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"iteration", "read"})
	})
	if err != nil {
		t.Fatalf("loop help iteration read: %v", err)
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

func TestAgentContextHelpCommandShowsIssueDetails(t *testing.T) {
	t.Setenv("LOOP_AGENT_CONTEXT", "1")
	for _, topic := range [][]string{
		{"issue", "ask"},
		{"issue", "report"},
	} {
		out, err := captureStdout(t, func() error {
			return commandHelp(context.Background(), globals{}, topic)
		})
		if err != nil {
			t.Fatalf("loop help %v: %v", topic, err)
		}
		if !strings.Contains(out, "--title $text") || !strings.Contains(out, "--body $text") {
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
		t.Setenv("LOOP_AGENT_CONTEXT", "1")
		return Run([]string{"--json", "help", "iteration", "read"})
	})
	if err != nil {
		t.Fatalf("loop --json help iteration read: %v", err)
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
