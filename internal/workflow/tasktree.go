package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = 1

var taskIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type TaskTree struct {
	SchemaVersion  int    `json:"schema_version"`
	Summary        string `json:"summary"`
	GoalEvaluation string `json:"goal_evaluation"`
	GoalComplete   bool   `json:"goal_complete,omitempty"`
	Tasks          []Task `json:"tasks"`
}

type Task struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	DependsOn     []string `json:"depends_on"`
	ConflictsWith []string `json:"conflicts_with"`
	Acceptance    []string `json:"acceptance"`
}

type TaskResult struct {
	SchemaVersion int      `json:"schema_version"`
	TaskID        string   `json:"task_id"`
	Status        string   `json:"status"`
	Summary       string   `json:"summary"`
	DiscardReason string   `json:"discard_reason,omitempty"`
	Validation    []string `json:"validation,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

type ReviewResult struct {
	SchemaVersion  int             `json:"schema_version"`
	Status         string          `json:"status"`
	Summary        string          `json:"summary"`
	GoalComplete   bool            `json:"goal_complete,omitempty"`
	GoalEvaluation string          `json:"goal_evaluation"`
	Findings       []ReviewFinding `json:"findings,omitempty"`
}

type ReviewFinding struct {
	ID          string   `json:"id"`
	TaskID      string   `json:"task_id,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Acceptance  []string `json:"acceptance"`
}

type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid workflow handoff: " + strings.Join(e.Problems, "; ")
}

func DecodeTaskTree(data []byte) (TaskTree, error) {
	var tree TaskTree
	if err := decodeStrict(data, &tree); err != nil {
		return tree, err
	}
	if problems := ValidateTaskTree(tree); len(problems) > 0 {
		return tree, &ValidationError{Problems: problems}
	}
	return tree, nil
}

func DecodeTaskResult(data []byte) (TaskResult, error) {
	var result TaskResult
	if err := decodeStrict(data, &result); err != nil {
		return result, err
	}
	if problems := ValidateTaskResult(result); len(problems) > 0 {
		return result, &ValidationError{Problems: problems}
	}
	return result, nil
}

func DecodeReviewResult(data []byte) (ReviewResult, error) {
	var result ReviewResult
	if err := decodeStrict(data, &result); err != nil {
		return result, err
	}
	if problems := ValidateReviewResult(result); len(problems) > 0 {
		return result, &ValidationError{Problems: problems}
	}
	return result, nil
}

func ValidateTaskTree(tree TaskTree) []string {
	var problems []string
	if tree.SchemaVersion != SchemaVersion {
		problems = append(problems, "schema_version must be 1")
	}
	if strings.TrimSpace(tree.Summary) == "" {
		problems = append(problems, "summary is required")
	}
	if strings.TrimSpace(tree.GoalEvaluation) == "" {
		problems = append(problems, "goal_evaluation is required")
	}
	seen := map[string]Task{}
	for i, task := range tree.Tasks {
		prefix := fmt.Sprintf("tasks[%d]", i)
		problems = append(problems, validateTask(prefix, task)...)
		if seen[task.ID].ID != "" {
			problems = append(problems, prefix+".id duplicates "+task.ID)
		}
		if task.ID != "" {
			seen[task.ID] = task
		}
	}
	for _, task := range tree.Tasks {
		for _, dep := range task.DependsOn {
			if seen[dep].ID == "" {
				problems = append(problems, fmt.Sprintf("task %q depends on unknown task %q", task.ID, dep))
			}
			if dep == task.ID {
				problems = append(problems, fmt.Sprintf("task %q depends on itself", task.ID))
			}
		}
		for _, conflict := range task.ConflictsWith {
			if seen[conflict].ID == "" {
				problems = append(problems, fmt.Sprintf("task %q conflicts with unknown task %q", task.ID, conflict))
			}
			if conflict == task.ID {
				problems = append(problems, fmt.Sprintf("task %q conflicts with itself", task.ID))
			}
		}
	}
	if len(problems) == 0 {
		if cycle := dependencyCycle(tree.Tasks); len(cycle) > 0 {
			problems = append(problems, "task dependency cycle: "+strings.Join(cycle, " -> "))
		}
	}
	return problems
}

func ValidateTaskResult(result TaskResult) []string {
	var problems []string
	if result.SchemaVersion != SchemaVersion {
		problems = append(problems, "schema_version must be 1")
	}
	if !validTaskID(result.TaskID) {
		problems = append(problems, "task_id must start with a lowercase letter and contain only lowercase letters, digits, or hyphens")
	}
	if !oneOf(result.Status, "completed", "discarded", "failed") {
		problems = append(problems, "status must be completed, discarded, or failed")
	}
	if strings.TrimSpace(result.Summary) == "" {
		problems = append(problems, "summary is required")
	}
	if result.Status == "discarded" && strings.TrimSpace(result.DiscardReason) == "" {
		problems = append(problems, "discard_reason is required when status is discarded")
	}
	return problems
}

func ValidateReviewResult(result ReviewResult) []string {
	var problems []string
	if result.SchemaVersion != SchemaVersion {
		problems = append(problems, "schema_version must be 1")
	}
	if !oneOf(result.Status, "approved", "changes_requested", "failed") {
		problems = append(problems, "status must be approved, changes_requested, or failed")
	}
	if strings.TrimSpace(result.Summary) == "" {
		problems = append(problems, "summary is required")
	}
	if strings.TrimSpace(result.GoalEvaluation) == "" {
		problems = append(problems, "goal_evaluation is required")
	}
	for i, finding := range result.Findings {
		prefix := fmt.Sprintf("findings[%d]", i)
		if !validTaskID(finding.ID) {
			problems = append(problems, prefix+".id must start with a lowercase letter and contain only lowercase letters, digits, or hyphens")
		}
		if strings.TrimSpace(finding.Title) == "" {
			problems = append(problems, prefix+".title is required")
		}
		if strings.TrimSpace(finding.Description) == "" {
			problems = append(problems, prefix+".description is required")
		}
		if len(finding.Acceptance) == 0 {
			problems = append(problems, prefix+".acceptance must have at least one item")
		}
	}
	return problems
}

func ExecutionWaves(tasks []Task, maxParallel int) ([][]Task, error) {
	if maxParallel <= 0 {
		maxParallel = 1
	}
	tree := TaskTree{SchemaVersion: SchemaVersion, Summary: "schedule", GoalEvaluation: "schedule", Tasks: tasks}
	if problems := ValidateTaskTree(tree); len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	remaining := map[string]Task{}
	done := map[string]bool{}
	for _, task := range tasks {
		remaining[task.ID] = task
	}
	var waves [][]Task
	for len(remaining) > 0 {
		var ready []Task
		for _, task := range remaining {
			if depsDone(task, done) {
				ready = append(ready, task)
			}
		}
		sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
		if len(ready) == 0 {
			return nil, errors.New("no schedulable tasks remain")
		}
		var wave []Task
		for _, task := range ready {
			if len(wave) >= maxParallel {
				break
			}
			if conflictsAny(task, wave) {
				continue
			}
			wave = append(wave, task)
		}
		for _, task := range wave {
			done[task.ID] = true
			delete(remaining, task.ID)
		}
		waves = append(waves, wave)
	}
	return waves, nil
}

func FindingTask(finding ReviewFinding) Task {
	return Task{
		ID:          finding.ID,
		Title:       finding.Title,
		Description: finding.Description,
		Acceptance:  finding.Acceptance,
	}
}

func MarshalIndent(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func decodeStrict(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

func validateTask(prefix string, task Task) []string {
	var problems []string
	if !validTaskID(task.ID) {
		problems = append(problems, prefix+".id must start with a lowercase letter and contain only lowercase letters, digits, or hyphens")
	}
	if strings.TrimSpace(task.Title) == "" {
		problems = append(problems, prefix+".title is required")
	}
	if strings.TrimSpace(task.Description) == "" {
		problems = append(problems, prefix+".description is required")
	}
	if len(task.Acceptance) == 0 {
		problems = append(problems, prefix+".acceptance must have at least one item")
	}
	return problems
}

func validTaskID(id string) bool {
	return taskIDPattern.MatchString(strings.TrimSpace(id))
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func depsDone(task Task, done map[string]bool) bool {
	for _, dep := range task.DependsOn {
		if !done[dep] {
			return false
		}
	}
	return true
}

func conflictsAny(task Task, wave []Task) bool {
	for _, other := range wave {
		if conflicts(task, other) || conflicts(other, task) {
			return true
		}
	}
	return false
}

func conflicts(a, b Task) bool {
	for _, conflict := range a.ConflictsWith {
		if conflict == b.ID {
			return true
		}
	}
	return false
}

func dependencyCycle(tasks []Task) []string {
	graph := map[string][]string{}
	for _, task := range tasks {
		graph[task.ID] = append([]string(nil), task.DependsOn...)
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var stack []string
	var visit func(string) []string
	visit = func(id string) []string {
		if visiting[id] {
			for i, item := range stack {
				if item == id {
					return append(append([]string(nil), stack[i:]...), id)
				}
			}
			return []string{id, id}
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		stack = append(stack, id)
		for _, dep := range graph[id] {
			if cycle := visit(dep); len(cycle) > 0 {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, task := range tasks {
		if cycle := visit(task.ID); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}
