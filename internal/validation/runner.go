package validation

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Command struct {
	Name     string
	Run      string
	Required bool
	Timeout  time.Duration
}

type CommandResult struct {
	Name       string
	Command    string
	Required   bool
	ExitCode   int
	Output     string
	OutputPath string
	Duration   time.Duration
	Err        error
}

type EventAppender interface {
	Append(map[string]any) error
}

type Runner struct {
	WorkDir        string
	OutputPath     string
	EventAppender  EventAppender
	DefaultTimeout time.Duration
}

func (r Runner) Run(ctx context.Context, commands []Command) ([]CommandResult, error) {
	results := make([]CommandResult, 0, len(commands))
	var firstRequiredErr error
	for _, command := range commands {
		result := r.runOne(ctx, command)
		results = append(results, result)
		if r.EventAppender != nil {
			_ = r.EventAppender.Append(map[string]any{
				"type":      "validation.command.completed",
				"name":      result.Name,
				"command":   result.Command,
				"exit_code": result.ExitCode,
				"required":  result.Required,
			})
		}
		if result.Required && result.ExitCode != 0 && firstRequiredErr == nil {
			firstRequiredErr = result.Err
			if firstRequiredErr == nil {
				firstRequiredErr = fmt.Errorf("required validation command %q failed with exit code %d", result.Name, result.ExitCode)
			}
		}
	}
	if r.OutputPath != "" {
		if err := WriteValidationMarkdown(r.OutputPath, results); err != nil && firstRequiredErr == nil {
			firstRequiredErr = err
		}
	}
	return results, firstRequiredErr
}

func (r Runner) runOne(ctx context.Context, command Command) CommandResult {
	timeout := command.Timeout
	if timeout == 0 {
		timeout = r.DefaultTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	cmd := newShellCommand(ctx, command.Run)
	cmd.Dir = r.WorkDir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			err = ctx.Err()
		}
	}
	return CommandResult{
		Name:     command.Name,
		Command:  command.Run,
		Required: command.Required,
		ExitCode: exitCode,
		Output:   output.String(),
		Duration: time.Since(start),
		Err:      err,
	}
}

func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	shell, args := shellCommand(command)
	return exec.CommandContext(ctx, shell, args...)
}

func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		shell := os.Getenv("COMSPEC")
		if strings.TrimSpace(shell) == "" {
			shell = "cmd"
		}
		return shell, []string{"/C", command}
	}

	shell := os.Getenv("SHELL")
	if strings.TrimSpace(shell) == "" {
		return "sh", []string{"-c", command}
	}

	name := strings.TrimSuffix(strings.ToLower(filepath.Base(shell)), ".exe")
	switch name {
	case "bash", "zsh", "fish", "ksh", "mksh", "pdksh", "oksh", "csh", "tcsh":
		return shell, []string{"-lc", command}
	case "nu":
		return shell, []string{"-l", "-c", command}
	case "pwsh", "powershell":
		return shell, []string{"-Command", command}
	default:
		return shell, []string{"-c", command}
	}
}

func WriteValidationMarkdown(path string, results []CommandResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(FormatMarkdown(results)), 0o644)
}

func FormatMarkdown(results []CommandResult) string {
	var b strings.Builder
	b.WriteString("# Validation\n\n")
	if len(results) == 0 {
		b.WriteString("No validation commands configured.\n")
		return b.String()
	}
	for _, result := range results {
		status := "passed"
		if result.ExitCode != 0 {
			status = "failed"
		}
		fmt.Fprintf(&b, "## %s\n\n", result.Name)
		fmt.Fprintf(&b, "- Command: `%s`\n", result.Command)
		fmt.Fprintf(&b, "- Required: %t\n", result.Required)
		fmt.Fprintf(&b, "- Status: %s\n", status)
		fmt.Fprintf(&b, "- Exit code: %d\n", result.ExitCode)
		if result.Output != "" {
			b.WriteString("\n```text\n")
			b.WriteString(result.Output)
			if !strings.HasSuffix(result.Output, "\n") {
				b.WriteByte('\n')
			}
			b.WriteString("```\n")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func StatusFromResults(results []CommandResult) string {
	if len(results) == 0 {
		return "skipped"
	}
	optionalFailed := false
	for _, result := range results {
		if result.ExitCode != 0 {
			if result.Required {
				return "failed"
			}
			optionalFailed = true
		}
	}
	if optionalFailed {
		return "partial"
	}
	return "passed"
}
