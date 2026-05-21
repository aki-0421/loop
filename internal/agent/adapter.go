package agent

import (
	"context"
	"errors"
	"time"

	"github.com/aki-0421/loop/internal/runstate"
)

var ErrResultReceived = errors.New("agent stopped after result handoff")

type AgentAdapter interface {
	Name() string
	Prepare(ctx context.Context, req PrepareRequest) (*PreparedAgent, error)
	Run(ctx context.Context, req RunRequest) (*RunResult, error)
}

type PrepareRequest struct {
	WorkDir     string
	PromptText  string
	Timeout     time.Duration
	Environment map[string]string
}

type PreparedAgent struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

type RunRequest struct {
	WorkDir       string
	Env           map[string]string
	PromptText    string
	IterationDir  string
	EventLogPath  string
	ErrorsLogPath string
	Timeout       time.Duration
	OnEvent       func(runstate.Event)
}

type RunResult struct {
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	EventPath  string
	Err        error
}
