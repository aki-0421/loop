package runstate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Stage string

const (
	StageCreated       Stage = "created"
	StageBranchCreated Stage = "branch_created"
	StageAgentRunning  Stage = "agent_running"
	StageRepairRunning Stage = "repair_running"
	StageValidating    Stage = "validating"
	StageIntegrating   Stage = "integrating"
	StageCompleted     Stage = "completed"
	StageBlocked       Stage = "blocked"
	StageFailed        Stage = "failed"
	StageCancelled     Stage = "cancelled"
)

type State struct {
	SchemaVersion    int               `json:"schema_version"`
	RunID            string            `json:"run_id"`
	Goal             string            `json:"goal,omitempty"`
	BaseBranch       string            `json:"base_branch"`
	Agent            string            `json:"agent"`
	CurrentIteration string            `json:"current_iteration"`
	Stage            Stage             `json:"stage"`
	Iterations       []IterationRecord `json:"iterations"`
}

type IterationRecord struct {
	IterationID     string `json:"iteration_id"`
	BranchInitial   string `json:"branch_initial"`
	BranchFinal     string `json:"branch_final,omitempty"`
	Stage           string `json:"stage"`
	ResultPath      string `json:"result_path,omitempty"`
	SummarySentence string `json:"summary_sentence,omitempty"`
	ShouldFullyStop bool   `json:"should_fully_stop,omitempty"`
}

type Result struct {
	SchemaVersion   int               `json:"schema_version"`
	Status          string            `json:"status"`
	SummarySentence string            `json:"summary_sentence"`
	ShouldFullyStop bool              `json:"should_fully_stop"`
	GoalEvaluation  string            `json:"goal_evaluation"`
	Branch          BranchResult      `json:"branch"`
	Commits         []CommitResult    `json:"commits"`
	Validation      ValidationResult  `json:"validation"`
	Artifacts       map[string]string `json:"artifacts"`
	Assumptions     []string          `json:"assumptions,omitempty"`
	BlockedReason   string            `json:"blocked_reason,omitempty"`
	Error           string            `json:"error,omitempty"`
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
	Status   string                    `json:"status"`
	Commands []ValidationCommandResult `json:"commands"`
}

type ValidationCommandResult struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Required   bool   `json:"required"`
	OutputPath string `json:"output_path,omitempty"`
}

func New(runID, goal, baseBranch, agent string) State {
	return State{
		SchemaVersion:    1,
		RunID:            runID,
		Goal:             goal,
		BaseBranch:       baseBranch,
		Agent:            agent,
		CurrentIteration: "0001",
		Stage:            StageCreated,
		Iterations:       []IterationRecord{},
	}
}

func NewRunID(now time.Time) (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), nil
}

func IterationID(n int) string {
	return fmt.Sprintf("%04d", n)
}

func Read(path string) (State, error) {
	var state State
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, ValidateState(state)
}

func Write(path string, state State) error {
	if err := ValidateState(state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return AtomicWrite(path, data, 0o644)
}

func ValidateState(state State) error {
	var errs []string
	if state.SchemaVersion != 1 {
		errs = append(errs, "schema_version must be 1")
	}
	if state.RunID == "" {
		errs = append(errs, "run_id is required")
	}
	if state.BaseBranch == "" {
		errs = append(errs, "base_branch is required")
	}
	if state.Agent == "" {
		errs = append(errs, "agent is required")
	}
	if state.CurrentIteration == "" {
		errs = append(errs, "current_iteration is required")
	}
	if !oneOf(string(state.Stage), "created", "branch_created", "agent_running", "repair_running", "validating", "integrating", "completed", "blocked", "failed", "cancelled") {
		errs = append(errs, "stage is invalid")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func ReadResult(path string) (Result, error) {
	var result Result
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	return result, ValidateResult(result)
}

func WriteResult(path string, result Result) error {
	if err := ValidateResult(result); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return AtomicWrite(path, data, 0o644)
}

func ValidateResult(result Result) error {
	var errs []string
	if result.SchemaVersion != 1 {
		errs = append(errs, "schema_version must be 1")
	}
	if !oneOf(result.Status, "completed", "no_change", "needs_repair", "blocked", "failed") {
		errs = append(errs, "status is invalid")
	}
	if strings.TrimSpace(result.SummarySentence) == "" {
		errs = append(errs, "summary_sentence is required")
	}
	if len(result.SummarySentence) > 120 {
		errs = append(errs, "summary_sentence must be at most 120 characters")
	}
	if strings.TrimSpace(result.GoalEvaluation) == "" {
		errs = append(errs, "goal_evaluation is required")
	}
	if strings.TrimSpace(result.Branch.InitialName) == "" {
		errs = append(errs, "branch.initial_name is required")
	}
	if result.Commits == nil {
		errs = append(errs, "commits is required")
	}
	for i, commit := range result.Commits {
		if strings.TrimSpace(commit.Message) == "" {
			errs = append(errs, fmt.Sprintf("commits[%d].message is required", i))
		}
	}
	if !oneOf(result.Validation.Status, "passed", "failed", "skipped", "partial") {
		errs = append(errs, "validation.status is invalid")
	}
	if result.Validation.Commands == nil {
		errs = append(errs, "validation.commands is required")
	}
	if result.Artifacts == nil {
		errs = append(errs, "artifacts is required")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func AtomicWriteJSON(path string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(path, append(data, '\n'), perm)
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
