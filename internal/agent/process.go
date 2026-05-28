package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aki-0421/loop/internal/runstate"
)

type PromptMode string

const (
	PromptStdin PromptMode = "stdin"
	PromptArg   PromptMode = "arg"
)

const processCancelWaitDelay = 10 * time.Second

type ProcessAdapter struct {
	AdapterName string
	Command     string
	Args        []string
	PromptMode  PromptMode
	Env         map[string]string
}

func (a ProcessAdapter) Name() string {
	if a.AdapterName != "" {
		return a.AdapterName
	}
	return a.Command
}

func (a ProcessAdapter) Prepare(ctx context.Context, req PrepareRequest) (*PreparedAgent, error) {
	if a.Command == "" {
		return nil, errors.New("agent: command is required")
	}
	args := a.expandArgs(req)
	env := map[string]string{}
	for k, v := range a.Env {
		env[k] = v
	}
	for k, v := range req.Environment {
		env[k] = v
	}
	return &PreparedAgent{Name: a.Name(), Command: a.Command, Args: args, Env: env}, nil
}

func (a ProcessAdapter) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	prepared, err := a.Prepare(ctx, PrepareRequest{
		WorkDir: req.WorkDir, PromptText: req.PromptText, Environment: req.Env, Timeout: req.Timeout,
	})
	if err != nil {
		return nil, err
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	if err := os.MkdirAll(req.IterationDir, 0o755); err != nil {
		return nil, err
	}
	eventLog := runstate.EventLog{Path: req.EventLogPath}
	eventMetadata := copyEventMetadata(req.EventMetadata)
	started := time.Now().UTC()
	appendAgentEvent(eventLog, req.OnEvent, startedEvent(prepared.Command, prepared.Args, req.PromptText), eventMetadata)

	cmd := exec.CommandContext(ctx, prepared.Command, prepared.Args...)
	cmd.Dir = req.WorkDir
	cmd.Env = mergeEnv(os.Environ(), prepared.Env)
	if a.mode() == PromptStdin {
		cmd.Stdin = strings.NewReader(req.PromptText)
	}
	configureCommandCancel(cmd)
	stdoutCapture := newStreamCapture(eventLog, "stdout", req.OnEvent, eventMetadata)
	stderrCapture := newStreamCapture(eventLog, "stderr", req.OnEvent, eventMetadata)
	cmd.Stdout = stdoutCapture
	cmd.Stderr = stderrCapture

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	waitErr := cmd.Wait()
	stdoutCapture.Flush()
	stderrCapture.Flush()
	for _, err := range []error{stdoutCapture.Err(), stderrCapture.Err()} {
		if err != nil && waitErr == nil {
			waitErr = err
		}
	}

	exitCode := 0
	if waitErr != nil {
		exitCode = 1
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			waitErr = ctx.Err()
		}
	}
	finished := time.Now().UTC()
	appendAgentEvent(eventLog, req.OnEvent, runstate.Event{"type": "agent.exited", "exit_code": exitCode}, eventMetadata)
	if waitErr != nil {
		appendErrorLog(req.ErrorsLogPath, fmt.Sprintf("agent exited with code %d after %s: %s", exitCode, finished.Sub(started).Round(time.Millisecond), errorString(waitErr)))
	}
	result := &RunResult{
		ExitCode: exitCode, StartedAt: started, FinishedAt: finished,
		EventPath: req.EventLogPath, Err: waitErr,
	}
	return result, waitErr
}

func (a ProcessAdapter) expandArgs(req PrepareRequest) []string {
	args := make([]string, len(a.Args))
	for i, arg := range a.Args {
		arg = strings.ReplaceAll(arg, "{prompt}", req.PromptText)
		arg = strings.ReplaceAll(arg, "{cwd}", req.WorkDir)
		args[i] = arg
	}
	return args
}

func (a ProcessAdapter) mode() PromptMode {
	if a.PromptMode == "" {
		return PromptStdin
	}
	return a.PromptMode
}

type streamCapture struct {
	mu                   sync.Mutex
	eventLog             runstate.EventLog
	stream               string
	onEvent              func(runstate.Event)
	eventMetadata        map[string]any
	pending              []byte
	err                  error
	pendingCommandStarts map[string]time.Time
}

func newStreamCapture(eventLog runstate.EventLog, stream string, onEvent func(runstate.Event), eventMetadata map[string]any) *streamCapture {
	return &streamCapture{
		eventLog:             eventLog,
		stream:               stream,
		onEvent:              onEvent,
		eventMetadata:        copyEventMetadata(eventMetadata),
		pendingCommandStarts: map[string]time.Time{},
	}
}

func (c *streamCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = append(c.pending, p...)
	for {
		i := bytesIndexByte(c.pending, '\n')
		if i < 0 {
			break
		}
		line := string(c.pending[:i])
		line = strings.TrimSuffix(line, "\r")
		c.emit(line)
		c.pending = c.pending[i+1:]
	}
	return len(p), nil
}

func (c *streamCapture) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		return
	}
	line := strings.TrimSuffix(string(c.pending), "\r")
	c.emit(line)
	c.pending = nil
}

func (c *streamCapture) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *streamCapture) emit(text string) {
	if c.err != nil {
		return
	}
	summary := summarizeAgentOutput(c.stream, text)
	now := time.Now().UTC()
	if summary.ScreenText != "" && c.onEvent != nil {
		c.onEvent(withEventMetadata(runstate.Event{
			"type":   "agent.stream",
			"stream": c.stream,
			"text":   summary.ScreenText,
			"ts":     now.Format(time.RFC3339),
		}, c.eventMetadata))
	}
	for _, event := range summary.AuditEvents {
		out, keep := c.rewriteCommandLifecycle(event, now)
		if !keep {
			continue
		}
		event = out
		event = withEventMetadata(event, c.eventMetadata)
		if c.eventLog.Path != "" {
			if err := c.eventLog.Append(event); err != nil {
				c.err = err
				return
			}
		} else if _, ok := event["ts"]; !ok {
			event["ts"] = now.Format(time.RFC3339)
		}
		if c.onEvent != nil {
			c.onEvent(event)
		}
	}
}

func (c *streamCapture) rewriteCommandLifecycle(event runstate.Event, now time.Time) (runstate.Event, bool) {
	typ, _ := event["type"].(string)
	if typ != "agent.command.lifecycle" {
		return event, true
	}
	phase, _ := event["phase"].(string)
	key := commandLifecycleKey(event)
	if key == "" {
		key = c.stream + "\x00" + strings.TrimSpace(fmt.Sprint(event["command"])) + "\x00" + strings.TrimSpace(stringifyArgs(event["args"]))
	}
	switch phase {
	case "started":
		c.pendingCommandStarts[key] = now
		return nil, false
	case "completed":
		startedAt, ok := c.pendingCommandStarts[key]
		if ok {
			delete(c.pendingCommandStarts, key)
		}
		out := runstate.Event{
			"type":    "agent.command",
			"stream":  c.stream,
			"command": strings.TrimSpace(fmt.Sprint(event["command"])),
		}
		if args := eventArgsSlice(event["args"]); len(args) > 0 {
			out["args"] = args
		}
		if ok {
			ms := int(now.Sub(startedAt) / time.Millisecond)
			if ms < 0 {
				ms = 0
			}
			out["duration_ms"] = ms
		}
		if status := strings.TrimSpace(fmt.Sprint(event["status"])); status != "" && status != "<nil>" {
			out["status"] = status
		}
		if exitCode, ok := event["exit_code"]; ok {
			out["exit_code"] = exitCode
		}
		return out, true
	default:
		return nil, false
	}
}

func commandLifecycleKey(event runstate.Event) string {
	if id := strings.TrimSpace(fmt.Sprint(event["item_id"])); id != "" && id != "<nil>" {
		return "item:" + id
	}
	return ""
}

func eventArgsSlice(v any) []string {
	switch typed := v.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s == "" || s == "<nil>" {
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

func appendAgentEvent(log runstate.EventLog, onEvent func(runstate.Event), event runstate.Event, metadata map[string]any) {
	event = withEventMetadata(event, metadata)
	if log.Path != "" {
		_ = log.Append(event)
	} else if _, ok := event["ts"]; !ok {
		event["ts"] = time.Now().UTC().Format(time.RFC3339)
	}
	if onEvent != nil {
		onEvent(event)
	}
}

func copyEventMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]any, len(metadata))
	for k, v := range metadata {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func withEventMetadata(event runstate.Event, metadata map[string]any) runstate.Event {
	if len(metadata) == 0 || event == nil {
		return event
	}
	out := make(runstate.Event, len(event)+len(metadata))
	for k, v := range event {
		out[k] = v
	}
	for k, v := range metadata {
		out[k] = v
	}
	return out
}

func bytesIndexByte(b []byte, target byte) int {
	for i, c := range b {
		if c == target {
			return i
		}
	}
	return -1
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := append([]string{}, base...)
	for k, v := range extra {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

func appendErrorLog(path, message string) {
	message = strings.TrimSpace(message)
	if path == "" || message == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), message)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
