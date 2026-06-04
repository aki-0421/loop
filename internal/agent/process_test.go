package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/runstate"
)

func TestProcessAdapterPersistsAuditEventsAndFilteredMessages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command uses sh")
	}
	dir := t.TempDir()
	adapter := ProcessAdapter{
		AdapterName: "test",
		Command:     "sh",
		Args: []string{"-c", strings.Join([]string{
			"cat >/dev/null",
			"echo 'secret file line that must not persist'",
			"printf '%s\\n' '{\"type\":\"agent_message\",\"message\":\"Inspecting repository shape before edits.\"}'",
			"printf '%s\\n' '{\"cmd\":\"cat README.md\"}'",
			"printf '%s\\n' '{\"type\":\"tool_call\",\"name\":\"read_file\",\"arguments\":{\"path\":\"internal/cli/renderer.go\"}}' >&2",
		}, "; ")},
		PromptMode: PromptStdin,
	}
	var streamed []runstate.Event
	result, err := adapter.Run(context.Background(), RunRequest{
		WorkDir:       dir,
		PromptText:    "prompt",
		IterationDir:  dir,
		EventLogPath:  filepath.Join(dir, "agent-events.jsonl"),
		EventMetadata: map[string]any{"agent_type": "coding", "task_id": "unit-task"},
		OnEvent: func(event runstate.Event) {
			streamed = append(streamed, event)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "agent.started")
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "agent.message")
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "Inspecting repository shape before edits.")
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "agent.command")
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "agent.file_read")
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "agent.exited")
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), `"agent_type":"coding"`)
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), `"task_id":"unit-task"`)
	assertFileNotContains(t, filepath.Join(dir, "agent-events.jsonl"), "secret file line")
	assertFileNotContains(t, filepath.Join(dir, "agent-events.jsonl"), "agent.stream")
	assertFileDoesNotExist(t, filepath.Join(dir, "agent.stdout.log"))
	assertFileDoesNotExist(t, filepath.Join(dir, "agent.stderr.log"))
	assertFileDoesNotExist(t, filepath.Join(dir, "agent-exit.json"))
	assertFileDoesNotExist(t, filepath.Join(dir, "errors.log"))
	if !hasEventType(streamed, "agent.stream") {
		t.Fatalf("expected non-persisted screen stream event, got %#v", streamed)
	}
	for _, event := range streamed {
		if event["agent_type"] != "coding" {
			t.Fatalf("streamed event missing agent metadata: %#v", event)
		}
	}
	for _, event := range streamed {
		if event["type"] == "agent.stream" && strings.Contains(fmt.Sprint(event["text"]), "cat README.md") {
			t.Fatalf("audit command should not also stream as screen text: %#v", streamed)
		}
	}
}

func TestProcessAdapterAlwaysMarksAgentContext(t *testing.T) {
	adapter := ProcessAdapter{
		AdapterName: "test",
		Command:     "agent-bin",
		Env:         map[string]string{AgentContextEnv: "0", "OTHER": "adapter"},
	}
	prepared, err := adapter.Prepare(context.Background(), PrepareRequest{
		WorkDir:     t.TempDir(),
		PromptText:  "prompt",
		Environment: map[string]string{AgentContextEnv: "false", "OTHER": "request"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := prepared.Env[AgentContextEnv]; got != AgentContextValue {
		t.Fatalf("%s = %q, want %q", AgentContextEnv, got, AgentContextValue)
	}
	if got := prepared.Env["OTHER"]; got != "request" {
		t.Fatalf("request environment should override adapter environment, got %q", got)
	}
}

func TestProcessAdapterFlushesAuditEventsBeforeExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command uses sh")
	}
	dir := t.TempDir()
	adapter := ProcessAdapter{
		AdapterName: "test",
		Command:     "sh",
		Args:        []string{"-c", "cat >/dev/null; printf '%s\\n' '{\"cmd\":\"cat README.md\"}'; sleep 1; printf '%s\\n' '{\"cmd\":\"cat later.md\"}'"},
		PromptMode:  PromptStdin,
	}
	done := make(chan error, 1)
	go func() {
		_, err := adapter.Run(context.Background(), RunRequest{
			WorkDir:      dir,
			PromptText:   "prompt",
			IterationDir: dir,
			EventLogPath: filepath.Join(dir, "agent-events.jsonl"),
		})
		done <- err
	}()

	waitForFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "README.md", 2*time.Second)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		t.Fatal("agent exited before streaming assertion could observe an in-flight event")
	default:
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "later.md")
}

func TestProcessAdapterBuildsCommandEventFromLifecycleWithDuration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command uses sh")
	}
	dir := t.TempDir()
	adapter := ProcessAdapter{
		AdapterName: "test",
		Command:     "sh",
		Args: []string{"-c", strings.Join([]string{
			"cat >/dev/null",
			"printf '%s\\n' '{\"type\":\"item.started\",\"item\":{\"id\":\"item_1\",\"type\":\"command_execution\",\"command\":\"pwd\",\"status\":\"in_progress\"}}'",
			"sleep 0.01",
			"printf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"id\":\"item_1\",\"type\":\"command_execution\",\"command\":\"pwd\",\"status\":\"completed\",\"exit_code\":0}}'",
		}, "; ")},
		PromptMode: PromptStdin,
	}
	_, err := adapter.Run(context.Background(), RunRequest{
		WorkDir:      dir,
		PromptText:   "prompt",
		IterationDir: dir,
		EventLogPath: filepath.Join(dir, "agent-events.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agent-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), `"type":"agent.command"`); got != 1 {
		t.Fatalf("agent.command count = %d, want 1:\n%s", got, data)
	}
	if got := strings.Count(string(data), `"type":"agent.command.started"`); got != 1 {
		t.Fatalf("agent.command.started count = %d, want 1:\n%s", got, data)
	}
	if !strings.Contains(string(data), `"duration_ms":`) {
		t.Fatalf("expected duration_ms in command event:\n%s", data)
	}
	if strings.Contains(string(data), "agent.command.lifecycle") {
		t.Fatalf("lifecycle events should not be persisted:\n%s", data)
	}
}

func TestProcessAdapterPersistsUsageEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command uses sh")
	}
	dir := t.TempDir()
	adapter := ProcessAdapter{
		AdapterName: "test",
		Command:     "sh",
		Args: []string{"-c", strings.Join([]string{
			"cat >/dev/null",
			"printf '%s\\n' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1200,\"cached_input_tokens\":300,\"output_tokens\":45}}'",
		}, "; ")},
		PromptMode: PromptStdin,
	}
	_, err := adapter.Run(context.Background(), RunRequest{
		WorkDir:      dir,
		PromptText:   "prompt",
		IterationDir: dir,
		EventLogPath: filepath.Join(dir, "agent-events.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agent-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"agent.usage"`) || !strings.Contains(string(data), `"input_tokens":1200`) {
		t.Fatalf("usage event was not persisted:\n%s", data)
	}
}

func TestProcessAdapterCancelsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command uses sh")
	}
	dir := t.TempDir()
	adapter := ProcessAdapter{
		AdapterName: "test",
		Command:     "sh",
		Args:        []string{"-c", "sleep 5 & wait"},
		PromptMode:  PromptStdin,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := adapter.Run(ctx, RunRequest{
		WorkDir:       dir,
		PromptText:    "prompt",
		IterationDir:  dir,
		EventLogPath:  filepath.Join(dir, "agent-events.jsonl"),
		ErrorsLogPath: filepath.Join(dir, "errors.log"),
	})
	if err == nil {
		t.Fatal("expected cancelled process to return an error")
	}
	assertFileContains(t, filepath.Join(dir, "errors.log"), "agent exited with code")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("process group was not cancelled promptly; elapsed=%s", elapsed)
	}
}

func TestProcessAdapterCancelsIdleProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command uses sh")
	}
	dir := t.TempDir()
	adapter := ProcessAdapter{
		AdapterName: "test",
		Command:     "sh",
		Args: []string{"-c", strings.Join([]string{
			"cat >/dev/null",
			"printf '%s\\n' '{\"type\":\"agent_message\",\"message\":\"starting long wait\"}'",
			"sleep 5",
		}, "; ")},
		PromptMode: PromptStdin,
	}
	started := time.Now()
	_, err := adapter.Run(context.Background(), RunRequest{
		WorkDir:       dir,
		PromptText:    "prompt",
		IterationDir:  dir,
		EventLogPath:  filepath.Join(dir, "agent-events.jsonl"),
		ErrorsLogPath: filepath.Join(dir, "errors.log"),
		IdleTimeout:   100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected idle timeout")
	}
	if !IsIdleTimeout(err) {
		t.Fatalf("error = %v, want idle timeout", err)
	}
	assertFileContains(t, filepath.Join(dir, "agent-events.jsonl"), "agent.stalled")
	assertFileContains(t, filepath.Join(dir, "errors.log"), "agent idle timeout")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("idle process was not cancelled promptly; elapsed=%s", elapsed)
	}
}

func TestFakeAgentWritesResultFromEnv(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".loop", "runs", "run-1", "iterations", "0001")
	t.Setenv("LOOP_FAKE_AGENT_MODE", "skip_merge")
	t.Setenv("LOOP_ITERATION_DIR", dir)
	t.Setenv("LOOP_RUN_ID", "run-1")
	t.Setenv("LOOP_ITERATION_ID", "0001")
	if code := RunFakeAgentFromEnv(); code != 0 {
		t.Fatalf("fake agent exit code = %d", code)
	}
	data, err := artifactdb.ReadResultHandoff(filepath.Join(root, ".loop", artifactdb.GlobalDBName), "run-1", "0001")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		t.Fatal(err)
	}
	if result["action"] != "skip_merge" {
		t.Fatalf("unexpected fake result: %+v", result)
	}
}

func assertFileDoesNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists, want absent", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}

func waitForFileContains(t *testing.T, path, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("%s did not contain %q before timeout; last read error: %v", path, want, err)
			}
			t.Fatalf("%s did not contain %q before timeout:\n%s", path, want, data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s missing %q:\n%s", path, want, data)
	}
}

func assertFileNotContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), want) {
		t.Fatalf("%s unexpectedly contained %q:\n%s", path, want, data)
	}
}

func hasEventType(events []runstate.Event, want string) bool {
	for _, event := range events {
		if event["type"] == want {
			return true
		}
	}
	return false
}
