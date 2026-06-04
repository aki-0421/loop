package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/workflow"
)

func RunFakeAgentFromEnv() int {
	if strings.TrimSpace(os.Getenv("LOOP_ROLE")) != "" || os.Getenv("LOOP_RESULT_HANDOFF") == "role-db" {
		return runFakeRoleAgent()
	}
	mode := fakeModeFromEnv()
	iterationDir := os.Getenv("LOOP_ITERATION_DIR")
	if iterationDir != "" {
		_ = os.MkdirAll(iterationDir, 0o755)
	}

	switch mode {
	case "invalid_json":
		_ = writeFakeRawResultHandoff(iterationDir, "{invalid json\n")
		return 0
	case "dirty":
		_ = os.WriteFile(filepath.Join(getenv("LOOP_WORKDIR", "."), "loop-fake-dirty.txt"), []byte("dirty\n"), 0o644)
		writeFakeResult(iterationDir, "merge")
		return 0
	case "skip_merge":
		writeFakeResult(iterationDir, "skip_merge")
		return 0
	case "sleep":
		writeFakeResult(iterationDir, "skip_merge", true)
		return 0
	case "validation_fix":
		workDir := getenv("LOOP_WORKDIR", ".")
		_ = loopBranchRename(workDir, "test/fake-agent")
		changePath := filepath.Join(workDir, "validation-ok.txt")
		_ = os.WriteFile(changePath, []byte("validation repaired at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = git(workDir, "add", "validation-ok.txt")
		_ = git(workDir, "commit", "-m", "F: repair validation fixture")
		writeFakeResult(iterationDir, "merge", fakeCommit(workDir))
		return 0
	case "merge_unrenamed":
		workDir := getenv("LOOP_WORKDIR", ".")
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent merge close without branch rename at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = git(workDir, "add", "loop-fake-change.txt")
		_ = git(workDir, "commit", "-m", "F: run fake agent behavior")
		writeFakeResult(iterationDir, "merge", fakeCommit(workDir))
		return 0
	default:
		workDir := getenv("LOOP_WORKDIR", ".")
		_ = loopBranchRename(workDir, "test/fake-agent")
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent merge close at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = git(workDir, "add", "loop-fake-change.txt")
		_ = git(workDir, "commit", "-m", "F: run fake agent behavior")
		writeFakeResult(iterationDir, "merge", fakeCommit(workDir))
		return 0
	}
}

func runFakeRoleAgent() int {
	role := strings.TrimSpace(os.Getenv("LOOP_ROLE"))
	switch role {
	case "planner":
		tree := workflow.TaskTree{
			SchemaVersion:  workflow.SchemaVersion,
			Summary:        "Run fake role workflow",
			GoalEvaluation: "Fake planner selected one deterministic task.",
			Tasks: []workflow.Task{{
				ID:          "fake-task",
				Title:       "Fake task",
				Description: "Create a deterministic fake workflow change.",
				Acceptance:  []string{"The fake workflow marker file exists."},
			}},
		}
		return writeFakeRoleHandoff("task-tree", "", tree)
	case "coding":
		taskID := getenv("LOOP_TASK_ID", "fake-task")
		workDir := getenv("LOOP_WORKDIR", ".")
		if err := loopTaskCommand(workDir, "task", "todo", "add", "--type", "F", "--title", "Run fake role workflow", "--acceptance", "The fake workflow marker file exists.", "run", "fake", "role", "workflow"); err != nil {
			return 1
		}
		if err := loopTaskCommand(workDir, "task", "todo", "start", "1"); err != nil {
			return 1
		}
		_ = os.WriteFile(filepath.Join(workDir, "loop-fake-role-change.txt"), []byte("fake role change at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		if err := loopTaskCommand(workDir, "task", "todo", "stage", "1"); err != nil {
			return 1
		}
		if err := loopTaskCommand(workDir, "task", "todo", "complete", "1"); err != nil {
			return 1
		}
		result := workflow.TaskResult{
			SchemaVersion: workflow.SchemaVersion,
			TaskID:        taskID,
			Status:        "completed",
			Summary:       "Fake coding agent completed " + taskID + ".",
		}
		if code := writeFakeRoleHandoff("task-result", taskID, result); code != 0 {
			return code
		}
		if err := loopTaskCommand(workDir, "task", "merge", "--type", "F", "complete", taskID); err != nil {
			return 1
		}
		return 0
	case "review":
		result := workflow.ReviewResult{
			SchemaVersion:  workflow.SchemaVersion,
			Status:         "approved",
			Summary:        "Fake review approved the iteration.",
			GoalEvaluation: "Fake review did not evaluate a real goal.",
		}
		if strings.TrimSpace(os.Getenv("LOOP_RUN_GOAL")) != "" {
			result.GoalComplete = true
			result.GoalEvaluation = "The fake role workflow completed the supplied goal."
		}
		return writeFakeRoleHandoff("review-result", "", result)
	default:
		return 1
	}
}

func writeFakeRoleHandoff(kind, taskID string, payload any) int {
	data, err := workflow.MarshalIndent(payload)
	if err != nil {
		return 1
	}
	iterationDir := os.Getenv("LOOP_ITERATION_DIR")
	globalPath := artifactdb.GlobalDBPathForIteration(iterationDir)
	if globalPath == "" {
		return 1
	}
	runID := getenv("LOOP_RUN_ID", "")
	iterationID := getenv("LOOP_ITERATION_ID", "")
	if runID == "" || iterationID == "" {
		parsedRunID, parsedIterationID := artifactdb.ParseIterationDir(iterationDir)
		runID = firstFakeNonEmpty(runID, parsedRunID)
		iterationID = firstFakeNonEmpty(iterationID, parsedIterationID)
	}
	if err := artifactdb.WriteRoleHandoff(globalPath, runID, iterationID, kind, taskID, string(data)); err != nil {
		return 1
	}
	return 0
}

func fakeModeFromEnv() string {
	sequence := strings.TrimSpace(os.Getenv("LOOP_FAKE_AGENT_SEQUENCE"))
	if sequence == "" {
		return getenv("LOOP_FAKE_AGENT_MODE", "merge")
	}
	var modes []string
	for _, item := range strings.Split(sequence, ",") {
		if mode := strings.TrimSpace(item); mode != "" {
			modes = append(modes, mode)
		}
	}
	if len(modes) == 0 {
		return getenv("LOOP_FAKE_AGENT_MODE", "merge")
	}
	index := nextFakeInvocationIndex()
	if index >= len(modes) {
		index = len(modes) - 1
	}
	return modes[index]
}

func nextFakeInvocationIndex() int {
	path := strings.TrimSpace(os.Getenv("LOOP_FAKE_AGENT_COUNT_FILE"))
	if path == "" {
		if n, err := strconv.Atoi(strings.TrimLeft(getenv("LOOP_ITERATION_ID", "1"), "0")); err == nil && n > 0 {
			return n - 1
		}
		return 0
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := os.ReadFile(path)
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	_ = os.WriteFile(path, []byte(strconv.Itoa(count+1)+"\n"), 0o644)
	return count
}

func loopBranchRename(dir, branch string) error {
	return loopTaskCommand(dir, "branch", "rename", branch)
}

func loopTaskCommand(dir string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	return cmd.Run()
}

func writeFakeResult(iterationDir, action string, args ...any) {
	_ = writeFakeResultHandoff(iterationDir, action, args...)
}

func writeFakeResultHandoff(iterationDir, action string, args ...any) error {
	validationStatus := "passed"
	if action == "skip_merge" {
		validationStatus = "skipped"
	}
	sleepUntilGitHubUpdate := os.Getenv("LOOP_FAKE_AGENT_SLEEP") == "1"
	hasGoal := strings.TrimSpace(os.Getenv("LOOP_RUN_GOAL")) != ""
	commitList := make([]map[string]any, 0, len(args))
	for _, arg := range args {
		switch typed := arg.(type) {
		case bool:
			sleepUntilGitHubUpdate = typed
		case map[string]any:
			commit := typed
			if commit != nil {
				commitList = append(commitList, commit)
			}
		}
	}
	result := map[string]any{
		"schema_version":    1,
		"action":            action,
		"summary_sentence":  "Run fake agent behavior",
		"should_fully_stop": action != "merge" && !sleepUntilGitHubUpdate && hasGoal,
		"goal_evaluation":   "Fake agent produced a deterministic test result.",
		"branch":            fakeBranchResult(action),
		"commits":           commitList,
		"validation":        map[string]any{"status": validationStatus, "commands": []map[string]any{}},
		"artifacts":         map[string]any{},
		"assumptions":       []string{},
		"skip_merge_reason": "",
	}
	if action == "skip_merge" {
		result["skip_merge_reason"] = "Fake agent chose not to merge this iteration."
		if sleepUntilGitHubUpdate {
			result["sleep_until_github_update"] = true
		}
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return writeFakeRawResultHandoff(iterationDir, string(append(data, '\n')))
}

func writeFakeRawResultHandoff(iterationDir, resultJSON string) error {
	globalPath := artifactdb.GlobalDBPathForIteration(iterationDir)
	if globalPath == "" {
		return nil
	}
	runID := getenv("LOOP_RUN_ID", "")
	iterationID := getenv("LOOP_ITERATION_ID", "")
	if runID == "" || iterationID == "" {
		parsedRunID, parsedIterationID := artifactdb.ParseIterationDir(iterationDir)
		if runID == "" {
			runID = parsedRunID
		}
		if iterationID == "" {
			iterationID = parsedIterationID
		}
	}
	if runID == "" || iterationID == "" {
		return nil
	}
	return artifactdb.WriteResultHandoff(globalPath, runID, iterationID, resultJSON)
}

func fakeBranchResult(action string) map[string]any {
	initial := getenv("LOOP_INITIAL_BRANCH", "wip/0001")
	current := currentBranch(getenv("LOOP_WORKDIR", "."))
	if current == "" {
		current = getenv("LOOP_CURRENT_BRANCH", initial)
	}
	kind := "test"
	slug := "fake-agent"
	final := ""
	if action == "merge" && current != "" && current != initial {
		final = current
		if parsedKind, parsedSlug, ok := splitBranch(current); ok {
			kind = parsedKind
			slug = parsedSlug
		}
	}
	branch := map[string]any{"initial_name": initial, "kind": kind, "slug": slug}
	if final != "" {
		branch["final_name"] = final
	}
	return branch
}

func currentBranch(dir string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(bytesTrimSpace(out))
}

func splitBranch(branch string) (string, string, bool) {
	kind, slug, ok := strings.Cut(strings.TrimSpace(branch), "/")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(slug) == "" {
		return "", "", false
	}
	return strings.TrimSpace(kind), strings.TrimSpace(slug), true
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

func fakeCommit(dir string) map[string]any {
	cmd := exec.Command("git", "log", "-1", "--format=%H")
	cmd.Dir = dir
	out, err := cmd.Output()
	sha := ""
	if err == nil {
		sha = string(bytesTrimSpace(out))
	}
	return map[string]any{"sha": sha, "message": "F: run fake agent behavior"}
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 {
		last := b[len(b)-1]
		if last != ' ' && last != '\n' && last != '\t' && last != '\r' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func firstFakeNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
