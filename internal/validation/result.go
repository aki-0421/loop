package validation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type IterationResult struct {
	SchemaVersion   int              `json:"schema_version"`
	Status          string           `json:"status"`
	SummarySentence string           `json:"summary_sentence"`
	ShouldFullyStop bool             `json:"should_fully_stop"`
	GoalEvaluation  string           `json:"goal_evaluation"`
	Branch          BranchResult     `json:"branch"`
	Commits         []CommitResult   `json:"commits"`
	Validation      ValidationResult `json:"validation"`
	Artifacts       ArtifactResult   `json:"artifacts"`
	Assumptions     []string         `json:"assumptions,omitempty"`
	BlockedReason   string           `json:"blocked_reason,omitempty"`
	Error           string           `json:"error,omitempty"`
}

type BranchResult struct {
	InitialName string `json:"initial_name"`
	Kind        string `json:"kind,omitempty"`
	Slug        string `json:"slug,omitempty"`
	FinalName   string `json:"final_name,omitempty"`
}

type CommitResult struct {
	SHA     string `json:"sha,omitempty"`
	Message string `json:"message"`
}

type ValidationResult struct {
	Status   string                 `json:"status"`
	Commands []ValidationCommandLog `json:"commands"`
}

type ValidationCommandLog struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Required   bool   `json:"required"`
	OutputPath string `json:"output_path,omitempty"`
}

type ArtifactResult struct {
	Plan    string `json:"plan,omitempty"`
	Todo    string `json:"todo,omitempty"`
	Summary string `json:"summary,omitempty"`
	Worklog string `json:"worklog,omitempty"`
	PRTitle string `json:"pr_title,omitempty"`
	PRBody  string `json:"pr_body,omitempty"`
}

type ResultValidationError struct {
	Problems []string
}

func (e *ResultValidationError) Error() string {
	return "invalid iteration result: " + fmt.Sprint(e.Problems)
}

func ValidateResultFile(path string) (*IterationResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ValidateResultJSON(b)
}

func ValidateResultJSON(data []byte) (*IterationResult, error) {
	var result IterationResult
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return nil, err
	}
	problems := validateResult(result)
	if len(problems) > 0 {
		return nil, &ResultValidationError{Problems: problems}
	}
	normalizeArtifactNames(&result.Artifacts)
	return &result, nil
}

func validateResult(r IterationResult) []string {
	var problems []string
	if r.SchemaVersion != 1 {
		problems = append(problems, "schema_version must be 1")
	}
	if !oneOf(r.Status, "completed", "no_change", "needs_repair", "blocked", "failed") {
		problems = append(problems, "status must be completed, no_change, needs_repair, blocked, or failed")
	}
	if r.SummarySentence == "" {
		problems = append(problems, "summary_sentence is required")
	}
	if len(r.SummarySentence) > 120 {
		problems = append(problems, "summary_sentence must be at most 120 characters")
	}
	if r.GoalEvaluation == "" {
		problems = append(problems, "goal_evaluation is required")
	}
	if r.Branch.InitialName == "" {
		problems = append(problems, "branch.initial_name is required")
	}
	for i, c := range r.Commits {
		if strings.TrimSpace(c.Message) == "" {
			problems = append(problems, fmt.Sprintf("commits[%d].message is required", i))
		}
	}
	if !oneOf(r.Validation.Status, "passed", "failed", "skipped", "partial") {
		problems = append(problems, "validation.status must be passed, failed, skipped, or partial")
	}
	for i, c := range r.Validation.Commands {
		if c.Name == "" {
			problems = append(problems, fmt.Sprintf("validation.commands[%d].name is required", i))
		}
		if c.Command == "" {
			problems = append(problems, fmt.Sprintf("validation.commands[%d].command is required", i))
		}
	}
	if r.Status == "completed" && r.Validation.Status == "failed" {
		problems = append(problems, "completed results cannot have failed validation")
	}
	return problems
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func normalizeArtifactNames(artifacts *ArtifactResult) {
	artifacts.Plan = logicalArtifactName(artifacts.Plan)
	artifacts.Todo = logicalArtifactName(artifacts.Todo)
	artifacts.Summary = logicalArtifactName(artifacts.Summary)
	artifacts.Worklog = logicalArtifactName(artifacts.Worklog)
	artifacts.PRTitle = logicalArtifactName(artifacts.PRTitle)
	artifacts.PRBody = logicalArtifactName(artifacts.PRBody)
}

func logicalArtifactName(value string) string {
	switch value {
	case "plan.md":
		return "plan"
	case "todo.md":
		return "todo"
	case "summary.md":
		return "summary"
	case "worklog.md":
		return "worklog"
	case "pr-title.txt":
		return "pr-title"
	case "pr-body.md":
		return "pr-body"
	default:
		return value
	}
}

func EnsureResultExists(path string) error {
	if path == "" {
		return errors.New("result path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
}
