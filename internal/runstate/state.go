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
	StageValidating    Stage = "validating"
	StageIntegrating   Stage = "integrating"
	StageCompleted     Stage = "completed"
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
	BranchCurrent   string `json:"branch_current,omitempty"`
	BranchFinal     string `json:"branch_final,omitempty"`
	Stage           string `json:"stage"`
	ResultPath      string `json:"result_path,omitempty"`
	SummarySentence string `json:"summary_sentence,omitempty"`
	ShouldFullyStop bool   `json:"should_fully_stop,omitempty"`
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
	if !oneOf(string(state.Stage), "created", "branch_created", "agent_running", "validating", "integrating", "completed", "failed", "cancelled") {
		errs = append(errs, "stage is invalid")
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

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
