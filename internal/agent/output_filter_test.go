package agent

import "testing"

func TestSummarizeAgentOutputExtractsCodexUsage(t *testing.T) {
	summary := summarizeAgentOutput("stdout", `{"type":"turn.completed","usage":{"input_tokens":1200,"cached_input_tokens":300,"output_tokens":45}}`)

	if len(summary.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one usage event", summary.AuditEvents)
	}
	event := summary.AuditEvents[0]
	if event["type"] != "agent.usage" {
		t.Fatalf("event type = %v, want agent.usage", event["type"])
	}
	if event["input_tokens"] != 1200 || event["output_tokens"] != 45 || event["cache_read_tokens"] != 300 {
		t.Fatalf("unexpected token usage event: %#v", event)
	}
	if event["delta"] != true {
		t.Fatalf("codex turn usage should be emitted as delta: %#v", event)
	}
	if event["stream"] != "stdout" {
		t.Fatalf("stream = %v, want stdout", event["stream"])
	}
}

func TestSummarizeAgentOutputExtractsClaudeResultUsage(t *testing.T) {
	summary := summarizeAgentOutput("stdout", `{"type":"result","usage":{"input_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":2,"output_tokens":5}}`)

	if len(summary.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one usage event", summary.AuditEvents)
	}
	event := summary.AuditEvents[0]
	if event["type"] != "agent.usage" {
		t.Fatalf("event type = %v, want agent.usage", event["type"])
	}
	if event["input_tokens"] != 10 || event["output_tokens"] != 5 || event["cache_creation_tokens"] != 2 {
		t.Fatalf("unexpected token usage event: %#v", event)
	}
}

func TestSummarizeAgentOutputExtractsCommandLifecycle(t *testing.T) {
	started := summarizeAgentOutput("stdout", `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"sed -n '1,2p' README.md","status":"in_progress"}}`)
	if len(started.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one started lifecycle event", started.AuditEvents)
	}
	startEvent := started.AuditEvents[0]
	if startEvent["type"] != "agent.command.lifecycle" || startEvent["phase"] != "started" {
		t.Fatalf("unexpected started lifecycle event: %#v", startEvent)
	}

	completed := summarizeAgentOutput("stdout", `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"sed -n '1,2p' README.md","status":"completed","exit_code":0}}`)
	if len(completed.AuditEvents) < 2 {
		t.Fatalf("events = %#v, want completed lifecycle + file_read", completed.AuditEvents)
	}
	var hasLifecycle, hasRead bool
	for _, event := range completed.AuditEvents {
		if event["type"] == "agent.command.lifecycle" && event["phase"] == "completed" {
			hasLifecycle = true
		}
		if event["type"] == "agent.file_read" {
			hasRead = true
		}
	}
	if !hasLifecycle || !hasRead {
		t.Fatalf("missing expected completed lifecycle/file_read events: %#v", completed.AuditEvents)
	}
}
