package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRoleInstructionUsesCurrentRole(t *testing.T) {
	t.Setenv("LOOP_ROLE", "coding")
	t.Setenv("LOOP_TASK_ID", "implement-core")
	out, err := captureStdout(t, func() error {
		return commandRole(context.Background(), globals{}, []string{"instruction"})
	})
	if err != nil {
		t.Fatalf("loop role instruction: %v", err)
	}
	for _, want := range []string{
		"## Coding Role\n",
		"Coding agents must use `loop task todo`",
		"If you exit before `loop task merge` succeeds",
		"Complete only the assigned task from the prompt.",
		"loop task todo add",
		"one quoted final commit-message argument",
		"loop task merge --type F complete",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("role instruction missing %q:\n%s", want, out)
		}
	}
	for _, notWant := range []string{
		"## Shared Rules",
		"You are running inside the `loop` harness",
		"Read context with `loop iteration read runtime`",
		"## Planner Role",
		"## Review Role",
		"## Merge Role",
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("coding instruction should not include %q:\n%s", notWant, out)
		}
	}
}

func TestRoleInstructionMergeHumanReviewMode(t *testing.T) {
	t.Setenv("LOOP_ROLE", "merge")
	t.Setenv("LOOP_PR_MODE", "1")
	t.Setenv("LOOP_PR_REVIEW_MODE", "parallel_human_review")
	out, err := captureStdout(t, func() error {
		return commandRole(context.Background(), globals{}, []string{"instruction"})
	})
	if err != nil {
		t.Fatalf("loop role instruction: %v", err)
	}
	for _, want := range []string{
		"## Merge Role\n",
		"When `LOOP_PR_REVIEW_MODE=auto_merge`",
		"When `LOOP_PR_REVIEW_MODE=parallel_human_review` or `serial_human_review`",
		"Read `pr-state` only after `loop pr create`",
		"leave it waiting for human review",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("merge instruction missing %q:\n%s", want, out)
		}
	}
}

func TestRoleInstructionRequiresRole(t *testing.T) {
	err := commandRole(context.Background(), globals{}, []string{"instruction"})
	if err == nil || !strings.Contains(err.Error(), "LOOP_ROLE is required") {
		t.Fatalf("missing LOOP_ROLE error = %v", err)
	}
}

func TestRoleInstructionRejectsUnknownRole(t *testing.T) {
	t.Setenv("LOOP_ROLE", "observer")
	err := commandRole(context.Background(), globals{}, []string{"instruction"})
	if err == nil || !strings.Contains(err.Error(), "unsupported LOOP_ROLE") {
		t.Fatalf("unknown LOOP_ROLE error = %v", err)
	}
}

func TestRoleInstructionPrintsJSON(t *testing.T) {
	t.Setenv("LOOP_ROLE", "planner")
	out, err := captureStdout(t, func() error {
		return commandRole(context.Background(), globals{JSON: true}, []string{"instruction"})
	})
	if err != nil {
		t.Fatalf("loop --json role instruction: %v", err)
	}
	var got struct {
		Role        string `json:"role"`
		Instruction string `json:"instruction"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON output should parse: %v\n%s", err, out)
	}
	if got.Role != "planner" || !strings.Contains(got.Instruction, "## Planner Role") {
		t.Fatalf("JSON instruction = %#v", got)
	}
}
