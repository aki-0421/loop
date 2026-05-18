package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/aki-0421/loop/internal/runstate"
)

type outputSummary struct {
	ScreenText  string
	AuditEvents []runstate.Event
}

func summarizeAgentOutput(stream, text string) outputSummary {
	text = strings.TrimRight(text, "\r")
	events := auditEventsFromLine(stream, text)
	return outputSummary{
		ScreenText:  screenTextFromLine(text, events),
		AuditEvents: dedupeAuditEvents(events),
	}
}

func startedEvent(command string, args []string, promptText string) runstate.Event {
	return runstate.Event{
		"type":    "agent.started",
		"command": command,
		"args":    redactPromptArgs(args, promptText),
	}
}

func redactPromptArgs(args []string, promptText string) []string {
	redacted := make([]string, len(args))
	for i, arg := range args {
		if i > 0 && args[i-1] == "-c" {
			redacted[i] = "[script redacted]"
			continue
		}
		if promptText != "" && strings.Contains(arg, promptText) {
			redacted[i] = "[prompt redacted]"
			continue
		}
		if len(arg) > 240 {
			redacted[i] = arg[:200] + "...[truncated]"
			continue
		}
		redacted[i] = arg
	}
	return redacted
}

func auditEventsFromLine(stream, text string) []runstate.Event {
	var events []runstate.Event
	var decoded any
	if json.Unmarshal([]byte(text), &decoded) == nil {
		if top, ok := decoded.(map[string]any); ok {
			if lifecycle, ok := commandLifecycleEventsFromTopLevel(top); ok {
				events = append(events, lifecycle...)
			} else {
				events = append(events, auditEventsFromJSON(decoded)...)
			}
		} else {
			events = append(events, auditEventsFromJSON(decoded)...)
		}
	}
	if len(events) == 0 {
		if cmd, ok := extractPlainCommand(text); ok {
			events = append(events, commandEvent(cmd, nil))
			for _, path := range readPathsFromCommand(cmd) {
				events = append(events, fileReadEvent(path))
			}
		}
		for _, path := range extractPlainReadPaths(text) {
			events = append(events, fileReadEvent(path))
		}
	}
	for _, event := range events {
		if _, ok := event["stream"]; !ok {
			event["stream"] = stream
		}
	}
	return events
}

func commandLifecycleEventsFromTopLevel(obj map[string]any) ([]runstate.Event, bool) {
	lineType, _ := stringField(obj, "type")
	phase := ""
	switch lineType {
	case "item.started":
		phase = "started"
	case "item.completed":
		phase = "completed"
	default:
		return nil, false
	}
	item, ok := obj["item"].(map[string]any)
	if !ok {
		return nil, false
	}
	itemType, _ := stringField(item, "type")
	if itemType == "" {
		itemType, _ = stringField(item, "item_type")
	}
	if itemType != "command_execution" {
		return nil, false
	}
	command, _ := stringField(item, "command")
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, false
	}

	event := runstate.Event{
		"type":    "agent.command.lifecycle",
		"phase":   phase,
		"command": command,
	}
	if id, _ := stringField(item, "id"); strings.TrimSpace(id) != "" {
		event["item_id"] = strings.TrimSpace(id)
	}
	if args := stringSliceField(item, "args"); len(args) > 0 {
		event["args"] = args
	}
	if status, _ := stringField(item, "status"); strings.TrimSpace(status) != "" {
		event["status"] = strings.TrimSpace(status)
	}
	if exitCode, ok, _ := numberFieldAny(item, []string{"exit_code"}); ok {
		event["exit_code"] = exitCode
	}

	events := []runstate.Event{event}
	if phase == "completed" {
		full := command
		if args := stringifyArgs(event["args"]); strings.TrimSpace(args) != "" {
			full = strings.TrimSpace(full + " " + args)
		}
		for _, path := range readPathsFromCommand(full) {
			events = append(events, fileReadEvent(path))
		}
	}
	return events, true
}

func auditEventsFromJSON(v any) []runstate.Event {
	var events []runstate.Event
	var walk func(any, string, string)
	walk = func(value any, parentKey, ownerType string) {
		switch typed := value.(type) {
		case map[string]any:
			if typ, ok := stringField(typed, "type"); ok {
				ownerType = typ
			} else if typ, ok := stringField(typed, "event"); ok && ownerType == "" {
				ownerType = typ
			}
			events = append(events, auditEventsFromJSONObject(typed, parentKey, ownerType)...)
			for key, child := range typed {
				walk(child, key, ownerType)
			}
		case []any:
			for _, child := range typed {
				walk(child, parentKey, ownerType)
			}
		}
	}
	walk(v, "", "")
	return events
}

func auditEventsFromJSONObject(obj map[string]any, parentKey, ownerType string) []runstate.Event {
	var events []runstate.Event
	if event, ok := usageEventFromJSONObject(obj, parentKey, ownerType); ok {
		events = append(events, event)
	}
	if cmd, args, ok := commandFromJSONObject(obj); ok {
		events = append(events, commandEvent(cmd, args))
		for _, path := range readPathsFromCommand(strings.Join(append([]string{cmd}, args...), " ")) {
			events = append(events, fileReadEvent(path))
		}
	}
	if objectLooksLikeRead(obj) {
		for _, path := range pathValues(obj) {
			events = append(events, fileReadEvent(path))
		}
	}
	return events
}

func commandFromJSONObject(obj map[string]any) (string, []string, bool) {
	if cmd, ok := stringField(obj, "cmd"); ok {
		return cmd, nil, true
	}
	if cmd, ok := stringField(obj, "command"); ok && commandFieldLooksExecutable(obj, cmd) {
		return cmd, stringSliceField(obj, "args"), true
	}
	for _, nestedKey := range []string{"arguments", "input", "parameters"} {
		nested, ok := obj[nestedKey].(map[string]any)
		if !ok {
			continue
		}
		if cmd, args, ok := commandFromJSONObject(nested); ok {
			return cmd, args, true
		}
	}
	return "", nil, false
}

func commandFieldLooksExecutable(obj map[string]any, command string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	for _, key := range []string{"recipient_name", "name", "tool", "type"} {
		value, ok := stringField(obj, key)
		if !ok {
			continue
		}
		lower := strings.ToLower(value)
		if strings.Contains(lower, "exec") || strings.Contains(lower, "shell") || strings.Contains(lower, "command") {
			return true
		}
	}
	if _, ok := obj["args"]; ok {
		return true
	}
	return strings.Contains(command, " ") || strings.Contains(command, "/")
}

func objectLooksLikeRead(obj map[string]any) bool {
	for _, key := range []string{"recipient_name", "name", "tool", "type", "action"} {
		value, ok := stringField(obj, key)
		if !ok {
			continue
		}
		lower := strings.ToLower(value)
		if strings.Contains(lower, "read") || strings.Contains(lower, "open") || strings.Contains(lower, "view") {
			return true
		}
	}
	return false
}

func pathValues(obj map[string]any) []string {
	var paths []string
	for _, key := range []string{"path", "file", "file_path", "filepath", "filename", "target", "uri"} {
		value, ok := stringField(obj, key)
		if !ok {
			continue
		}
		if pathLooksSafe(value) {
			paths = append(paths, value)
		}
	}
	for _, nestedKey := range []string{"arguments", "input", "parameters"} {
		if nested, ok := obj[nestedKey].(map[string]any); ok {
			paths = append(paths, pathValues(nested)...)
		}
	}
	return paths
}

func commandEvent(command string, args []string) runstate.Event {
	event := runstate.Event{"type": "agent.command", "command": strings.TrimSpace(command)}
	if len(args) > 0 {
		event["args"] = args
	}
	return event
}

func fileReadEvent(path string) runstate.Event {
	return runstate.Event{"type": "agent.file_read", "path": strings.TrimSpace(path)}
}

func usageEventFromJSONObject(obj map[string]any, parentKey, ownerType string) (runstate.Event, bool) {
	lowerOwner := strings.ToLower(ownerType)
	isUsageObject := oneOfString(parentKey, "usage", "token_usage", "tokens") ||
		strings.Contains(lowerOwner, "usage") ||
		lowerOwner == "turn.completed" ||
		lowerOwner == "assistant" ||
		lowerOwner == "result" ||
		(lowerOwner == "status" && obj["used"] != nil)
	if !isUsageObject && !objectHasTokenFields(obj) {
		return nil, false
	}

	input, hasInput, inputName := numberFieldAny(obj, []string{
		"inputTokens", "input_tokens", "promptTokens", "prompt_tokens", "input",
	})
	if !hasInput {
		if used, ok, _ := numberFieldAny(obj, []string{"used"}); ok {
			input, hasInput = used, true
		}
	}
	output, hasOutput, _ := numberFieldAny(obj, []string{
		"outputTokens", "output_tokens", "completionTokens", "completion_tokens", "output",
	})
	cacheRead, hasCacheRead, cacheReadName := numberFieldAny(obj, []string{
		"cacheReadTokens", "cache_read_tokens", "cache_read_input_tokens", "cachedInputTokens", "cached_input_tokens",
	})
	cacheCreation, hasCacheCreation, _ := numberFieldAny(obj, []string{
		"cacheCreationTokens", "cache_creation_tokens", "cache_creation_input_tokens",
		"cacheWriteTokens", "cache_write_tokens",
	})
	if !hasInput && !hasOutput && !hasCacheRead && !hasCacheCreation {
		return nil, false
	}
	if hasCacheRead && cacheReadName == "cache_read_input_tokens" && inputName == "input_tokens" {
		input += cacheRead
	}
	event := runstate.Event{
		"type":                  "agent.usage",
		"input_tokens":          input,
		"output_tokens":         output,
		"cache_read_tokens":     cacheRead,
		"cache_creation_tokens": cacheCreation,
	}
	if strings.Contains(lowerOwner, "turn.completed") || strings.Contains(lowerOwner, "request-usage") {
		event["delta"] = true
	}
	if estimated, ok := boolField(obj, "estimated"); ok {
		event["estimated"] = estimated
	}
	return event, true
}

func objectHasTokenFields(obj map[string]any) bool {
	for _, key := range []string{
		"inputTokens", "input_tokens", "promptTokens", "prompt_tokens",
		"outputTokens", "output_tokens", "completionTokens", "completion_tokens",
		"cacheReadTokens", "cache_read_tokens", "cache_read_input_tokens", "cachedInputTokens", "cached_input_tokens",
		"cacheCreationTokens", "cache_creation_tokens", "cache_creation_input_tokens", "cacheWriteTokens", "cache_write_tokens",
	} {
		if _, ok := obj[key]; ok {
			return true
		}
	}
	return false
}

func numberFieldAny(obj map[string]any, keys []string) (int, bool, string) {
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			if typed >= 0 {
				return int(typed), true, key
			}
		case int:
			if typed >= 0 {
				return typed, true, key
			}
		case int64:
			if typed >= 0 {
				return int(typed), true, key
			}
		case json.Number:
			if n, err := typed.Int64(); err == nil && n >= 0 {
				return int(n), true, key
			}
		}
	}
	return 0, false, ""
}

func boolField(obj map[string]any, key string) (bool, bool) {
	value, ok := obj[key]
	if !ok {
		return false, false
	}
	typed, ok := value.(bool)
	return typed, ok
}

func oneOfString(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func dedupeAuditEvents(events []runstate.Event) []runstate.Event {
	seen := map[string]bool{}
	var deduped []runstate.Event
	for _, event := range events {
		key := fmt.Sprint(event["type"]) + "\x00" + fmt.Sprint(event["command"]) + "\x00" + fmt.Sprint(event["path"]) +
			"\x00" + fmt.Sprint(event["phase"]) + "\x00" + fmt.Sprint(event["item_id"]) + "\x00" + fmt.Sprint(event["status"]) +
			"\x00" + fmt.Sprint(event["exit_code"]) + "\x00" + fmt.Sprint(event["input_tokens"]) + "\x00" + fmt.Sprint(event["output_tokens"]) +
			"\x00" + fmt.Sprint(event["delta"])
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, event)
	}
	return deduped
}

func screenTextFromLine(text string, events []runstate.Event) string {
	if len(events) > 0 {
		return ""
	}
	var decoded any
	if json.Unmarshal([]byte(text), &decoded) == nil {
		if extracted := extractScreenTextFromJSON(decoded); extracted != "" {
			return extracted
		}
		return ""
	}
	candidate := strings.TrimSpace(stripANSI(text))
	if candidate == "" || looksLikeBulkContent(candidate) {
		return ""
	}
	return truncateDisplay(candidate, 180)
}

func extractScreenTextFromJSON(v any) string {
	switch typed := v.(type) {
	case map[string]any:
		if objectLooksLikeToolOutput(typed) {
			return ""
		}
		if objectLooksDisplayable(typed) {
			for _, key := range []string{"message", "summary", "delta", "text"} {
				if s, ok := stringField(typed, key); ok {
					s = strings.TrimSpace(stripANSI(s))
					if s != "" && !looksLikeBulkContent(s) {
						return truncateDisplay(s, 180)
					}
				}
			}
		}
		if content, ok := typed["content"]; ok {
			if s := extractScreenTextFromJSON(content); s != "" {
				return s
			}
		}
		for _, key := range sortedKeys(typed) {
			if s := extractScreenTextFromJSON(typed[key]); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range typed {
			if s := extractScreenTextFromJSON(child); s != "" {
				return s
			}
		}
	case string:
		s := strings.TrimSpace(stripANSI(typed))
		if s != "" && !looksLikeBulkContent(s) {
			return truncateDisplay(s, 180)
		}
	}
	return ""
}

func objectLooksDisplayable(obj map[string]any) bool {
	role, _ := stringField(obj, "role")
	if role == "assistant" {
		return true
	}
	for _, key := range []string{"type", "name"} {
		value, ok := stringField(obj, key)
		if !ok {
			continue
		}
		lower := strings.ToLower(value)
		if strings.Contains(lower, "message") || strings.Contains(lower, "reasoning") ||
			strings.Contains(lower, "thinking") || strings.Contains(lower, "thought") {
			return true
		}
	}
	_, hasMessage := obj["message"]
	_, hasText := obj["text"]
	_, hasDelta := obj["delta"]
	return hasMessage && (hasText || hasDelta)
}

func objectLooksLikeToolOutput(obj map[string]any) bool {
	for _, key := range []string{"type", "name", "role"} {
		value, ok := stringField(obj, key)
		if !ok {
			continue
		}
		lower := strings.ToLower(value)
		if strings.Contains(lower, "tool") || strings.Contains(lower, "exec") ||
			strings.Contains(lower, "command") || strings.Contains(lower, "function_call_output") ||
			strings.Contains(lower, "patch") || strings.Contains(lower, "diff") {
			return true
		}
	}
	return false
}

func formatAuditEvent(event runstate.Event) string {
	switch event["type"] {
	case "agent.command":
		args := stringifyArgs(event["args"])
		command, _ := event["command"].(string)
		if args != "" {
			return "command: " + strings.TrimSpace(command+" "+args)
		}
		return "command: " + command
	case "agent.file_read":
		path, _ := event["path"].(string)
		return "file_read: " + path
	default:
		return fmt.Sprintf("%s", event["type"])
	}
}

func stringifyArgs(v any) string {
	switch typed := v.(type) {
	case []string:
		return strings.Join(typed, " ")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func extractPlainCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(stripANSI(text))
	for _, prefix := range []string{"$ ", "> ", "command: ", "run: ", "exec: "} {
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			return strings.TrimSpace(trimmed[len(prefix):]), true
		}
	}
	return "", false
}

var plainReadPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:read|opened|opening|cat|viewing)\s+([A-Za-z0-9_./@{}:-]+\.[A-Za-z0-9_./@{}:-]+)`),
}

func extractPlainReadPaths(text string) []string {
	var paths []string
	for _, re := range plainReadPatterns {
		for _, match := range re.FindAllStringSubmatch(text, -1) {
			if len(match) > 1 && pathLooksSafe(match[1]) {
				paths = append(paths, match[1])
			}
		}
	}
	return paths
}

func readPathsFromCommand(command string) []string {
	fields := shellFields(command)
	if len(fields) == 0 {
		return nil
	}
	readCommands := map[string]bool{
		"cat": true, "sed": true, "awk": true, "nl": true, "head": true, "tail": true,
		"less": true, "more": true, "rg": true, "grep": true, "ls": true, "find": true,
	}
	base := filepath.Base(fields[0])
	if !readCommands[base] {
		return nil
	}
	var paths []string
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") || strings.Contains(field, "=") {
			continue
		}
		if pathLooksSafe(field) {
			paths = append(paths, field)
		}
	}
	return paths
}

func shellFields(s string) []string {
	var fields []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		fields = append(fields, b.String())
		b.Reset()
	}
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return fields
}

func pathLooksSafe(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "\n") || strings.Contains(value, "\x00") {
		return false
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return false
	}
	if strings.ContainsAny(value, "*?[]") {
		return false
	}
	if strings.HasPrefix(value, "-") {
		return false
	}
	return strings.Contains(value, "/") || strings.Contains(filepath.Base(value), ".")
}

func looksLikeBulkContent(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "diff --git") || strings.HasPrefix(trimmed, "@@") {
		return true
	}
	if strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") {
		return true
	}
	if regexp.MustCompile(`^L?\d+[:|]\s`).MatchString(trimmed) {
		return true
	}
	codePrefixes := []string{
		"package ", "import ", "func ", "type ", "const ", "var ", "class ", "def ",
		"return ", "if ", "for ", "while ", "switch ", "case ", "</", "<div", "<span",
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range codePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.HasPrefix(s, "  ") || strings.HasPrefix(s, "\t") {
		return true
	}
	return false
}

func stripANSI(s string) string {
	return regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`).ReplaceAllString(s, "")
}

func truncateDisplay(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	if max <= 14 {
		return string(runes[:max])
	}
	return string(runes[:max-14]) + "...[truncated]"
}

func stringField(obj map[string]any, key string) (string, bool) {
	value, ok := obj[key]
	if !ok {
		return "", false
	}
	if typed, ok := value.(string); ok {
		return typed, true
	}
	return "", false
}

func stringSliceField(obj map[string]any, key string) []string {
	value, ok := obj[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return out
	case string:
		return shellFields(typed)
	default:
		return nil
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
