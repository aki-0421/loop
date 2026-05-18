package agent

import (
	"context"
	"time"

	"github.com/aki-0421/loop/internal/runstate"
)

type AgentAdapter interface {
	Name() string
	Prepare(ctx context.Context, req PrepareRequest) (*PreparedAgent, error)
	Run(ctx context.Context, req RunRequest) (*RunResult, error)
}

type PrepareRequest struct {
	WorkDir      string
	PromptText   string
	PromptFile   string
	ResultPath   string
	IterationDir string
	Harness      string
	UserContent  string
	Timeout      time.Duration
	Environment  map[string]string
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
	PromptFile    string
	PromptText    string
	ResultPath    string
	IterationDir  string
	EventLogPath  string
	StdoutLogPath string
	StderrLogPath string
	ErrorsLogPath string
	Timeout       time.Duration
	OnEvent       func(runstate.Event)
}

type RunResult struct {
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	StdoutPath string
	StderrPath string
	EventPath  string
	ResultPath string
	Err        error
}
