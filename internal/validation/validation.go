package validation

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/runstate"
)

type Result struct {
	Status   string
	Commands []runstate.ValidationCommandResult
	Markdown string
}

func Run(ctx context.Context, cwd, iterationDir string, commands []Command) (Result, error) {
	if len(commands) == 0 {
		md := "## Validation\n\nNo validation commands configured.\n"
		return Result{
			Status:   "skipped",
			Commands: []runstate.ValidationCommandResult{},
			Markdown: md,
		}, writeValidation(iterationDir, md)
	}
	var out Result
	var requiredFailed bool
	var optionalFailed bool
	var md strings.Builder
	md.WriteString("## Validation\n\n")
	for _, command := range commands {
		if strings.TrimSpace(command.Run) == "" {
			continue
		}
		exitCode, output := runShell(ctx, cwd, command.Run)
		name := command.Name
		if name == "" {
			name = command.Run
		}
		outputKey := sanitizeName(name)
		outputPath := "validation:" + outputKey
		if err := artifactdb.WriteValidationOutput(iterationDir, outputKey, string(output)); err != nil {
			return out, err
		}
		if exitCode != 0 {
			if command.Required {
				requiredFailed = true
			} else {
				optionalFailed = true
			}
		}
		out.Commands = append(out.Commands, runstate.ValidationCommandResult{
			Name:       name,
			Command:    command.Run,
			ExitCode:   exitCode,
			Required:   command.Required,
			OutputPath: outputPath,
		})
		status := "passed"
		if exitCode != 0 {
			status = "failed"
		}
		md.WriteString(fmt.Sprintf("- `%s`: %s (exit %d, required: %t)\n", command.Run, status, exitCode, command.Required))
	}
	switch {
	case requiredFailed:
		out.Status = "failed"
	case optionalFailed:
		out.Status = "partial"
	default:
		out.Status = "passed"
	}
	out.Markdown = md.String()
	return out, writeValidation(iterationDir, out.Markdown)
}

func runShell(ctx context.Context, cwd, command string) (int, []byte) {
	cmd := newShellCommand(ctx, command)
	cmd.Dir = cwd
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err == nil {
		return 0, buf.Bytes()
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), buf.Bytes()
	}
	buf.WriteString("\n")
	buf.WriteString(err.Error())
	buf.WriteString("\n")
	return 1, buf.Bytes()
}

func writeValidation(iterationDir, md string) error {
	return artifactdb.Write(iterationDir, "validation", md)
}

func sanitizeName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "command"
	}
	return out
}
