package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/gitx"
)

type todoArtifactItem struct {
	Status string
	Text   string
}

func commandIterationPlan(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop iteration plan <template|read|write> ...")}
	}
	switch args[0] {
	case "template":
		if len(args) != 1 {
			return codedError{2, fmt.Errorf("usage: loop iteration plan template")}
		}
		template := defaultPlanTemplate()
		return printResult(g, map[string]any{"artifact": "plan", "template": template}, template)
	case "read":
		return commandIterationPlanRead(ctx, g, args[1:])
	case "write":
		return commandIterationPlanWrite(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown iteration plan subcommand %q", args[0])}
	}
}

func commandIterationPlanRead(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("iteration plan read", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir, dirAlias, runID, iteration := addIterationLocatorFlags(fs)
	args = flagsFirst(args, iterationLocatorFlagNames())
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop iteration plan read [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	resolvedDir, err := resolveIterationDirRequiredFromFlags(ctx, g, *iterDir, *dirAlias, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	data, err := artifactdb.Read(resolvedDir, "plan")
	if err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"artifact": "plan", "content": data}, data)
}

func commandIterationPlanWrite(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("iteration plan write", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir, dirAlias, runID, iteration := addIterationLocatorFlags(fs)
	sourceFile := fs.String("file", "", "source file, or - for stdin")
	value := fs.String("value", "", "literal content")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true,
		"dir":           true,
		"run":           true,
		"iteration":     true,
		"file":          true,
		"value":         true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	valueSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "value" {
			valueSet = true
		}
	})
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop iteration plan write [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--file <path>|--value <text>]")}
	}
	resolvedDir, err := resolveIterationDirRequiredFromFlags(ctx, g, *iterDir, *dirAlias, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	data, err := readArtifactInput(*sourceFile, *value, valueSet)
	if err != nil {
		return codedError{2, err}
	}
	if err := artifactdb.Write(resolvedDir, "plan", string(data)); err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"artifact": "plan", "action": "write"}, "wrote plan\n")
}

func commandIterationTodo(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop iteration todo <list|insert|edit|complete> ...")}
	}
	switch args[0] {
	case "list":
		return commandIterationTodoList(ctx, g, args[1:])
	case "insert":
		return commandIterationTodoInsert(ctx, g, args[1:])
	case "edit":
		return commandIterationTodoEdit(ctx, g, args[1:])
	case "complete":
		return commandIterationTodoComplete(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown iteration todo subcommand %q", args[0])}
	}
}

func commandIterationTodoList(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("iteration todo list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir, dirAlias, runID, iteration := addIterationLocatorFlags(fs)
	args = flagsFirst(args, iterationLocatorFlagNames())
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop iteration todo list [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	resolvedDir, err := resolveIterationDirRequiredFromFlags(ctx, g, *iterDir, *dirAlias, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	items, err := readTodoArtifactItems(resolvedDir)
	if err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"artifact": "todo", "items": todoJSONItems(items)}, renderTodoList(items))
}

func commandIterationTodoInsert(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("iteration todo insert", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir, dirAlias, runID, iteration := addIterationLocatorFlags(fs)
	after := fs.Int("after", -1, "insert after 1-based todo index; 0 inserts at the top")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true,
		"dir":           true,
		"run":           true,
		"iteration":     true,
		"after":         true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	text, err := todoCommitSubject(ctx, g, fs.Args())
	if err != nil {
		return codedError{2, err}
	}
	resolvedDir, err := resolveIterationDirRequiredFromFlags(ctx, g, *iterDir, *dirAlias, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	items, err := readTodoArtifactItems(resolvedDir)
	if err != nil {
		return codedError{1, err}
	}
	insertAfter := *after
	if insertAfter == -1 {
		insertAfter = len(items)
	}
	if insertAfter < 0 || insertAfter > len(items) {
		return codedError{2, fmt.Errorf("--after index %d is out of range", *after)}
	}
	newItem := todoArtifactItem{Status: "pending", Text: text}
	items = append(items[:insertAfter], append([]todoArtifactItem{newItem}, items[insertAfter:]...)...)
	if err := writeTodoArtifactItems(resolvedDir, items); err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"artifact": "todo", "action": "insert", "index": insertAfter + 1, "item": todoJSONItem(insertAfter, newItem)}, fmt.Sprintf("inserted todo %d\n", insertAfter+1))
}

func commandIterationTodoEdit(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("iteration todo edit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir, dirAlias, runID, iteration := addIterationLocatorFlags(fs)
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true,
		"dir":           true,
		"run":           true,
		"iteration":     true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() < 3 {
		return codedError{2, fmt.Errorf("usage: loop iteration todo edit <n> [--iteration-dir <dir>|--run <run-id> --iteration <n>] <type> <message>")}
	}
	index, err := parseTodoIndex(fs.Arg(0))
	if err != nil {
		return codedError{2, err}
	}
	text, err := todoCommitSubject(ctx, g, fs.Args()[1:])
	if err != nil {
		return codedError{2, err}
	}
	resolvedDir, err := resolveIterationDirRequiredFromFlags(ctx, g, *iterDir, *dirAlias, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	items, err := readTodoArtifactItems(resolvedDir)
	if err != nil {
		return codedError{1, err}
	}
	if index < 1 || index > len(items) {
		return codedError{2, fmt.Errorf("todo index %d is out of range", index)}
	}
	items[index-1].Text = text
	if err := writeTodoArtifactItems(resolvedDir, items); err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"artifact": "todo", "action": "edit", "index": index, "item": todoJSONItem(index-1, items[index-1])}, fmt.Sprintf("edited todo %d\n", index))
}

func commandIterationTodoComplete(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("iteration todo complete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir, dirAlias, runID, iteration := addIterationLocatorFlags(fs)
	args = flagsFirst(args, iterationLocatorFlagNames())
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop iteration todo complete <n> [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	index, err := parseTodoIndex(fs.Arg(0))
	if err != nil {
		return codedError{2, err}
	}
	resolvedDir, err := resolveIterationDirRequiredFromFlags(ctx, g, *iterDir, *dirAlias, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	items, err := readTodoArtifactItems(resolvedDir)
	if err != nil {
		return codedError{1, err}
	}
	if index < 1 || index > len(items) {
		return codedError{2, fmt.Errorf("todo index %d is out of range", index)}
	}
	items[index-1].Status = "done"
	if err := writeTodoArtifactItems(resolvedDir, items); err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"artifact": "todo", "action": "complete", "index": index, "item": todoJSONItem(index-1, items[index-1])}, fmt.Sprintf("completed todo %d\n", index))
}

func addIterationLocatorFlags(fs *flag.FlagSet) (*string, *string, *string, *string) {
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	return iterDir, dirAlias, runID, iteration
}

func iterationLocatorFlagNames() map[string]bool {
	return map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true}
}

func resolveIterationDirFromFlags(ctx context.Context, g globals, iterationDir, dirAlias, runID, iteration string) (string, error) {
	if dirAlias != "" {
		iterationDir = dirAlias
	}
	return resolveIterationDir(ctx, g, iterationDir, runID, iteration)
}

func resolveIterationDirRequiredFromFlags(ctx context.Context, g globals, iterationDir, dirAlias, runID, iteration string) (string, error) {
	resolvedDir, err := resolveIterationDirFromFlags(ctx, g, iterationDir, dirAlias, runID, iteration)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resolvedDir) == "" {
		return "", errors.New("iteration directory is required; pass --iteration-dir, pass --run, or run inside an agent iteration")
	}
	return resolvedDir, nil
}

func defaultPlanTemplate() string {
	return strings.Join([]string{
		"# Iteration Plan",
		"",
		"## Purpose",
		"",
		"## Review Slice",
		"",
		"## Change Set",
		"",
		"## Verification",
		"",
		"## Out of Scope",
		"",
		"## Follow-up Slices",
		"",
		"## Risks and Notes",
		"",
	}, "\n")
}

func dedicatedArtifactWriteError(name string) error {
	switch name {
	case "plan":
		return fmt.Errorf("artifact %q uses dedicated commands; use `loop iteration plan template` and `loop iteration plan write`", name)
	case "todo":
		return fmt.Errorf("artifact %q uses dedicated commands; use `loop iteration todo insert`, `loop iteration todo edit`, or `loop iteration todo complete`", name)
	default:
		return fmt.Errorf("artifact %q uses dedicated commands", name)
	}
}

func readTodoArtifactItems(iterationDir string) ([]todoArtifactItem, error) {
	data, err := artifactdb.Read(iterationDir, "todo")
	if err != nil {
		if errors.Is(err, artifactdb.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return parseTodoArtifactItems(data), nil
}

func writeTodoArtifactItems(iterationDir string, items []todoArtifactItem) error {
	return artifactdb.Write(iterationDir, "todo", formatTodoArtifactItems(items))
}

func parseTodoArtifactItems(content string) []todoArtifactItem {
	var items []todoArtifactItem
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		status, text, ok := splitTodoLine(line)
		if !ok {
			continue
		}
		items = append(items, todoArtifactItem{Status: status, Text: text})
	}
	return items
}

func splitTodoLine(line string) (string, string, bool) {
	for _, marker := range []struct {
		Prefix string
		Status string
	}{
		{Prefix: "- [ ] ", Status: "pending"},
		{Prefix: "- [>] ", Status: "active"},
		{Prefix: "- [!] ", Status: "blocked"},
		{Prefix: "- [~] ", Status: "retrying"},
		{Prefix: "- [x] ", Status: "done"},
		{Prefix: "- [X] ", Status: "done"},
	} {
		if strings.HasPrefix(line, marker.Prefix) {
			text := strings.TrimSpace(strings.TrimPrefix(line, marker.Prefix))
			return marker.Status, text, text != ""
		}
	}
	return "", "", false
}

func formatTodoArtifactItems(items []todoArtifactItem) string {
	var b strings.Builder
	for _, item := range items {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		fmt.Fprintf(&b, "- [%s] %s\n", todoStatusMarker(item.Status), strings.TrimSpace(item.Text))
	}
	return b.String()
}

func renderTodoList(items []todoArtifactItem) string {
	var b strings.Builder
	for i, item := range items {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, todoStatusMarker(item.Status), item.Text)
	}
	return b.String()
}

func todoJSONItems(items []todoArtifactItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for i, item := range items {
		out = append(out, todoJSONItem(i, item))
	}
	return out
}

func todoJSONItem(index int, item todoArtifactItem) map[string]any {
	value := map[string]any{
		"index":  index + 1,
		"status": todoStatus(item.Status),
		"text":   item.Text,
	}
	if kind, message, ok := splitTodoSubject(item.Text); ok {
		value["type"] = kind
		value["message"] = message
	}
	return value
}

func todoStatus(status string) string {
	switch status {
	case "active", "blocked", "retrying", "done":
		return status
	default:
		return "pending"
	}
}

func todoStatusMarker(status string) string {
	switch status {
	case "active":
		return ">"
	case "blocked":
		return "!"
	case "retrying":
		return "~"
	case "done":
		return "x"
	default:
		return " "
	}
}

func todoCommitSubject(ctx context.Context, g globals, args []string) (string, error) {
	if len(args) < 2 {
		return "", errors.New("usage: loop iteration todo insert <type> <message>")
	}
	return buildLoopCommitSubject(args[0], strings.Join(args[1:], " "), todoCommitMessageMaxLength(ctx, g))
}

func todoCommitMessageMaxLength(ctx context.Context, g globals) int {
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return 0
	}
	cfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent, NoColor: g.NoColor}})
	if err != nil {
		return 0
	}
	return cfg.Git.Commits.MessageMaxLength
}

func splitTodoSubject(text string) (string, string, bool) {
	kind, message, ok := strings.Cut(strings.TrimSpace(text), ": ")
	if !ok || !isLoopCommitPrefix(kind) {
		return "", "", false
	}
	return kind, message, strings.TrimSpace(message) != ""
}

func parseTodoIndex(input string) (int, error) {
	index, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || index < 1 {
		return 0, fmt.Errorf("todo index must be a positive integer: %q", input)
	}
	return index, nil
}
