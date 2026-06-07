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

func TestSummarizeAgentOutputExtractsCodexTokenCount(t *testing.T) {
	summary := summarizeAgentOutput("stdout", `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1200,"cached_input_tokens":300,"output_tokens":45,"reasoning_output_tokens":10,"total_tokens":1245},"last_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":5,"reasoning_output_tokens":1,"total_tokens":105},"model_context_window":258400}}}`)

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
	if event["total_tokens"] != 1245 || event["reasoning_output_tokens"] != 10 {
		t.Fatalf("unexpected token usage metadata: %#v", event)
	}
	if event["delta"] == true {
		t.Fatalf("codex token_count usage should be emitted as a snapshot: %#v", event)
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

func TestSummarizeAgentOutputExtractsCodexSessionAndToolEvents(t *testing.T) {
	session := summarizeAgentOutput("stdout", `{"type":"thread.started","thread_id":"0199a213-81c0-7800-8aa1-bbab2a035a53"}`)
	if len(session.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one session event", session.AuditEvents)
	}
	if event := session.AuditEvents[0]; event["type"] != "agent.session" || event["thread_id"] != "0199a213-81c0-7800-8aa1-bbab2a035a53" {
		t.Fatalf("unexpected session event: %#v", event)
	}

	tool := summarizeAgentOutput("stdout", `{"type":"item.started","item":{"id":"item_2","type":"mcp_tool_call","name":"mcp__github__search_issues","status":"in_progress","arguments":{"query":"is:open label:bug"}}}`)
	if len(tool.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one tool event", tool.AuditEvents)
	}
	event := tool.AuditEvents[0]
	if event["type"] != "agent.tool" || event["tool"] != "mcp__github__search_issues" || event["item_id"] != "item_2" {
		t.Fatalf("unexpected tool event: %#v", event)
	}
	if _, ok := event["arguments"]; ok {
		t.Fatalf("tool event must not persist raw arguments: %#v", event)
	}
}

func TestSummarizeAgentOutputExtractsClaudeToolUse(t *testing.T) {
	summary := summarizeAgentOutput("stdout", `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"make test"}}]}}`)

	var hasTool, hasCommand bool
	for _, event := range summary.AuditEvents {
		if event["type"] == "agent.tool" && event["tool"] == "Bash" && event["item_id"] == "toolu_1" {
			hasTool = true
		}
		if event["type"] == "agent.command" && event["command"] == "make test" {
			hasCommand = true
		}
	}
	if !hasTool || !hasCommand {
		t.Fatalf("missing Claude tool/command events: %#v", summary.AuditEvents)
	}
}

func TestSummarizeAgentOutputExtractsFileChangeWebSearchAndPlanEvents(t *testing.T) {
	fileChange := summarizeAgentOutput("stdout", `{"type":"item.completed","item":{"id":"item_3","type":"file_change","status":"completed","path":"internal/agent/output_filter.go","action":"edit","diff":"diff --git a/secret b/secret"}}`)
	if len(fileChange.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one file change event", fileChange.AuditEvents)
	}
	event := fileChange.AuditEvents[0]
	if event["type"] != "agent.file_change" || event["path"] != "internal/agent/output_filter.go" || event["action"] != "edit" {
		t.Fatalf("unexpected file change event: %#v", event)
	}
	if _, ok := event["diff"]; ok {
		t.Fatalf("file change event must not persist raw diff: %#v", event)
	}

	web := summarizeAgentOutput("stdout", `{"type":"item.completed","item":{"type":"web_search","query":"codex exec json events","status":"completed"}}`)
	if len(web.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one web search event", web.AuditEvents)
	}
	if event := web.AuditEvents[0]; event["type"] != "agent.web_search" || event["query"] != "codex exec json events" {
		t.Fatalf("unexpected web search event: %#v", event)
	}

	plan := summarizeAgentOutput("stdout", `{"type":"item.completed","item":{"type":"plan_update","plan":[{"status":"completed","step":"Inspect adapter contracts"},{"status":"in_progress","step":"Add normalized event extraction"},{"status":"pending","step":"Run tests"}]}}`)
	if len(plan.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one plan update event", plan.AuditEvents)
	}
	event = plan.AuditEvents[0]
	if event["type"] != "agent.plan_update" || event["completed_count"] != 1 || event["in_progress_count"] != 1 || event["pending_count"] != 1 {
		t.Fatalf("unexpected plan update event: %#v", event)
	}
	if event["active_step"] != "Add normalized event extraction" {
		t.Fatalf("active step = %v, want normalized extraction step", event["active_step"])
	}
}

func TestSummarizeAgentOutputPersistsAssistantMessage(t *testing.T) {
	summary := summarizeAgentOutput("stdout", `{"type":"agent_message","message":"Inspecting renderer behavior before editing."}`)

	if len(summary.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one message event", summary.AuditEvents)
	}
	event := summary.AuditEvents[0]
	if event["type"] != "agent.message" || event["text"] != "Inspecting renderer behavior before editing." {
		t.Fatalf("unexpected message event: %#v", event)
	}
	if summary.ScreenText != "" {
		t.Fatalf("persisted messages should not also be screen-only text: %q", summary.ScreenText)
	}
}

func TestSummarizeAgentOutputPersistsClaudeAssistantMessageOnce(t *testing.T) {
	summary := summarizeAgentOutput("stdout", `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"text","text":"I will run the focused tests next."}]}}`)

	if len(summary.AuditEvents) != 1 {
		t.Fatalf("events = %#v, want one deduped message event", summary.AuditEvents)
	}
	event := summary.AuditEvents[0]
	if event["type"] != "agent.message" || event["text"] != "I will run the focused tests next." {
		t.Fatalf("unexpected message event: %#v", event)
	}
}

func TestSummarizeAgentOutputDoesNotPersistReasoningText(t *testing.T) {
	summary := summarizeAgentOutput("stdout", `{"type":"agent_reasoning","text":"hidden chain of thought"}`)

	if len(summary.AuditEvents) != 0 {
		t.Fatalf("reasoning text should not be persisted: %#v", summary.AuditEvents)
	}
}

func TestSummarizeAgentOutputDoesNotPersistRawMessageContent(t *testing.T) {
	summary := summarizeAgentOutput("stdout", `{"type":"agent_message","message":"package main\n\nfunc main() {}"}`)

	if len(summary.AuditEvents) != 0 {
		t.Fatalf("raw code-like message content should not be persisted: %#v", summary.AuditEvents)
	}
}
