package workflow

import "testing"

func TestValidateTaskTreeRejectsInvalidDependencies(t *testing.T) {
	tree := validTree()
	tree.Tasks[0].DependsOn = []string{"missing"}
	if problems := ValidateTaskTree(tree); len(problems) == 0 {
		t.Fatal("expected missing dependency to be rejected")
	}
}

func TestValidateTaskTreeRejectsCycles(t *testing.T) {
	tree := validTree()
	tree.Tasks = append(tree.Tasks, Task{
		ID:          "second",
		Title:       "Second",
		Description: "Second task.",
		DependsOn:   []string{"first"},
		Acceptance:  []string{"Second task is complete."},
	})
	tree.Tasks[0].DependsOn = []string{"second"}
	if problems := ValidateTaskTree(tree); len(problems) == 0 {
		t.Fatal("expected dependency cycle to be rejected")
	}
}

func TestExecutionWavesHonorsDependenciesConflictsAndParallelCap(t *testing.T) {
	tasks := []Task{
		{ID: "alpha", Title: "Alpha", Description: "Alpha.", Acceptance: []string{"Alpha."}},
		{ID: "beta", Title: "Beta", Description: "Beta.", ConflictsWith: []string{"gamma"}, Acceptance: []string{"Beta."}},
		{ID: "gamma", Title: "Gamma", Description: "Gamma.", Acceptance: []string{"Gamma."}},
		{ID: "delta", Title: "Delta", Description: "Delta.", DependsOn: []string{"alpha"}, Acceptance: []string{"Delta."}},
	}
	waves, err := ExecutionWaves(tasks, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(waves) < 2 {
		t.Fatalf("waves = %v, want at least 2 waves due dependency constraints", waves)
	}
	for _, wave := range waves {
		if len(wave) > 2 {
			t.Fatalf("wave size = %d, want max 2", len(wave))
		}
		if containsTask(wave, "beta") && containsTask(wave, "gamma") {
			t.Fatalf("conflicting tasks were scheduled together: %#v", wave)
		}
	}
	if !containsTask(waves[len(waves)-1], "delta") {
		t.Fatalf("dependent task should be scheduled after alpha, waves=%#v", waves)
	}
}

func TestDecodeTaskTreeRejectsUnknownFields(t *testing.T) {
	_, err := DecodeTaskTree([]byte(`{"schema_version":1,"summary":"x","goal_evaluation":"x","tasks":[],"extra":true}`))
	if err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestDecodeTaskTreeAllowsPendingPRWaitSignal(t *testing.T) {
	tree, err := DecodeTaskTree([]byte(`{"schema_version":1,"summary":"waiting","goal_evaluation":"pending PRs block safe work","wait_for_pending_prs":true,"tasks":[]}`))
	if err != nil {
		t.Fatalf("wait_for_pending_prs task tree should decode: %v", err)
	}
	if !tree.WaitForPendingPRs {
		t.Fatal("wait_for_pending_prs was not decoded")
	}
}

func TestDecodeTaskTreeAllowsPendingPRRepairSignal(t *testing.T) {
	tree, err := DecodeTaskTree([]byte(`{"schema_version":1,"summary":"repair","goal_evaluation":"pending PR needs repair","repair_pull_request":"1","tasks":[{"id":"repair-pr","title":"Repair PR","description":"Address feedback.","depends_on":[],"conflicts_with":[],"acceptance":["Feedback is addressed."]}]}`))
	if err != nil {
		t.Fatalf("repair_pull_request task tree should decode: %v", err)
	}
	if tree.RepairPullRequest != "1" {
		t.Fatalf("repair_pull_request = %q, want 1", tree.RepairPullRequest)
	}
}

func TestValidateTaskTreeRejectsPendingPRWaitWithTasks(t *testing.T) {
	tree := validTree()
	tree.WaitForPendingPRs = true
	if problems := ValidateTaskTree(tree); len(problems) == 0 {
		t.Fatal("expected wait_for_pending_prs with tasks to be rejected")
	}
}

func TestValidateTaskTreeRejectsPendingPRRepairWithoutTasks(t *testing.T) {
	tree := validTree()
	tree.RepairPullRequest = "1"
	tree.Tasks = nil
	if problems := ValidateTaskTree(tree); len(problems) == 0 {
		t.Fatal("expected repair_pull_request without tasks to be rejected")
	}
}

func TestValidateTaskResultRequiresDiscardReason(t *testing.T) {
	result := TaskResult{SchemaVersion: SchemaVersion, TaskID: "first", Status: "discarded", Summary: "Discarded."}
	if problems := ValidateTaskResult(result); len(problems) == 0 {
		t.Fatal("expected discarded task without reason to be rejected")
	}
	result.DiscardReason = "No safe path remains."
	if problems := ValidateTaskResult(result); len(problems) != 0 {
		t.Fatalf("valid discarded result rejected: %v", problems)
	}
}

func TestValidateMergeResultRequiresFindingsForPRCheckFailed(t *testing.T) {
	result := MergeResult{SchemaVersion: SchemaVersion, Status: "pr_check_failed", Summary: "Checks failed."}
	if problems := ValidateMergeResult(result); len(problems) == 0 {
		t.Fatal("expected pr_check_failed without findings to be rejected")
	}
	result.Findings = []ReviewFinding{{
		ID:          "repair-ci",
		Title:       "Repair CI",
		Description: "Fix the failing PR check.",
		Acceptance:  []string{"PR checks pass."},
	}}
	if problems := ValidateMergeResult(result); len(problems) != 0 {
		t.Fatalf("valid pr_check_failed result rejected: %v", problems)
	}
}

func TestDecodeMergeResultRejectsUnknownFields(t *testing.T) {
	_, err := DecodeMergeResult([]byte(`{"schema_version":1,"status":"merged","summary":"Merged.","extra":true}`))
	if err == nil {
		t.Fatal("expected unknown merge-result field to be rejected")
	}
}

func validTree() TaskTree {
	return TaskTree{
		SchemaVersion:  SchemaVersion,
		Summary:        "Valid tree",
		GoalEvaluation: "More work remains.",
		Tasks: []Task{{
			ID:          "first",
			Title:       "First",
			Description: "First task.",
			Acceptance:  []string{"First task is complete."},
		}},
	}
}

func containsTask(tasks []Task, id string) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}
