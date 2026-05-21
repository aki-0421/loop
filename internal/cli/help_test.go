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
		"loop memory search",
		"Run `loop help <command>` for details.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
	for _, notWant := range []string{"loop iteration", "loop commit", "loop branch"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("human help should hide %q:\n%s", notWant, out)
		}
	}
}

func TestHelpCommandRejectsAgentOnlyDetail(t *testing.T) {
	err := commandHelp(context.Background(), globals{}, []string{"iteration", "result"})
	if err == nil {
		t.Fatal("human help should reject agent-only topics")
	}
	if !strings.Contains(err.Error(), "loop help agent iteration result") {
		t.Fatalf("error should point to agent help: %v", err)
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
		"cmd:loop branch rename",
		"cmd:loop commit --type type message;",
		"cmd:loop iteration plan",
		"cmd:loop iteration result",
		"cmd:loop iteration todo",
		"cmd:loop issue report",
		"cmd:loop memory search",
		"artifacts:",
		"plan:rw:file",
		"detail:loop help agent <command...>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent help output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "  ") || strings.Contains(out, "\n\n") {
		t.Fatalf("agent help should avoid padding and blank lines:\n%s", out)
	}
}

func TestAgentHelpCommandShowsIterationResultDetails(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"agent", "iteration", "result"})
	})
	if err != nil {
		t.Fatalf("loop help agent iteration result: %v", err)
	}
	for _, want := range []string{
		"cmd:loop iteration result",
		"--summary text",
		"--should-stop bool",
		"--goal-evaluation text",
		"summary:Build valid iteration result JSON",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
}

func TestAgentHelpCommandShowsIterationPlanAndTodoDetails(t *testing.T) {
	planOut, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"agent", "iteration", "plan"})
	})
	if err != nil {
		t.Fatalf("loop help agent iteration plan: %v", err)
	}
	for _, want := range []string{
		"cmd:loop iteration plan",
		"sub:loop iteration plan read",
		"loop iteration plan template",
		"Keep TODOs in `loop iteration todo`",
	} {
		if !strings.Contains(planOut, want) {
			t.Fatalf("plan help output missing %q:\n%s", want, planOut)
		}
	}

	todoOut, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"agent", "iteration", "todo"})
	})
	if err != nil {
		t.Fatalf("loop help agent iteration todo: %v", err)
	}
	for _, want := range []string{
		"cmd:loop iteration todo",
		"sub:loop iteration todo complete",
		"After the plan selects the implementation scope",
		"`<type>` is one of F, T, R, D, S, V, or C",
		"F=features, fixes, or user-visible behavior",
		"same `--type <type> <message>` shape as `loop commit`",
		"Complete an item only after its matching commit exists",
	} {
		if !strings.Contains(todoOut, want) {
			t.Fatalf("todo help output missing %q:\n%s", want, todoOut)
		}
	}
}

func TestAgentHelpCommandShowsTodoTypeDetailsForTypedCommands(t *testing.T) {
	for _, topic := range [][]string{
		{"agent", "iteration", "todo", "insert"},
		{"agent", "iteration", "todo", "edit"},
	} {
		out, err := captureStdout(t, func() error {
			return commandHelp(context.Background(), globals{}, topic)
		})
		if err != nil {
			t.Fatalf("loop help %v: %v", topic, err)
		}
		for _, want := range []string{
			"`<type>` is one of F, T, R, D, S, V, or C",
			"F=features, fixes, or user-visible behavior",
			"--type type",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("todo typed command help %v missing %q:\n%s", topic, want, out)
			}
		}
	}
}

func TestAgentHelpCommandShowsBranchRenameKinds(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"agent", "branch", "rename"})
	})
	if err != nil {
		t.Fatalf("loop help agent branch rename: %v", err)
	}
	for _, want := range []string{
		"cmd:loop branch rename",
		"desc:`<kind>` is one of feat, fix, refactor, docs, test, style, build, ci, or chore.",
		"--kind kind",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
}

func TestAgentHelpCommandShowsCommitContract(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return commandHelp(context.Background(), globals{}, []string{"agent", "commit"})
	})
	if err != nil {
		t.Fatalf("loop help agent commit: %v", err)
	}
	for _, want := range []string{
		"cmd:loop commit --type type message",
		"desc:`<type>` is one of F, T, R, D, S, V, or C",
		"F=features, fixes, or user-visible behavior",
		"loop commit --type F add weather app shell",
		"--type type",
		"do not run `git add` or `git commit` directly",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
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
		"plan:rw:file",
		"pr-template:r:repo",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
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
