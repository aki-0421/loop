package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/runstate"
	"github.com/aki-0421/loop/internal/workflow"
)

type taskMergeContext struct {
	IterationDir      string
	RunID             string
	IterationID       string
	TaskID            string
	TaskDir           string
	TaskWorktree      string
	TaskBranch        string
	IterationBranch   string
	IterationWorktree string
	StorageRoot       string
	Task              workflow.Task
}

type taskMergeCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

type taskMergeRecord struct {
	SchemaVersion   int               `json:"schema_version"`
	TaskID          string            `json:"task_id"`
	Status          string            `json:"status"`
	Branch          string            `json:"branch"`
	IterationBranch string            `json:"iteration_branch"`
	TaskCommits     []taskMergeCommit `json:"task_commits"`
	MergeCommit     taskMergeCommit   `json:"merge_commit"`
	MergedAt        string            `json:"merged_at"`
}

type taskTodoFile struct {
	SchemaVersion int            `json:"schema_version"`
	TaskID        string         `json:"task_id"`
	Items         []taskTodoItem `json:"items"`
}

type taskTodoItem struct {
	Status        string   `json:"status"`
	WorkType      string   `json:"work_type"`
	Type          string   `json:"type,omitempty"`
	Title         string   `json:"title"`
	Acceptance    []string `json:"acceptance"`
	CommitMessage string   `json:"commit_message,omitempty"`
	CommitSHA     string   `json:"commit_sha,omitempty"`
	CommitSubject string   `json:"commit_subject,omitempty"`
	StartedAt     string   `json:"started_at,omitempty"`
	CompletedAt   string   `json:"completed_at,omitempty"`
	CancelledAt   string   `json:"cancelled_at,omitempty"`
}

type taskMergeLock struct {
	SchemaVersion     int               `json:"schema_version"`
	RunID             string            `json:"run_id"`
	IterationID       string            `json:"iteration_id"`
	TaskID            string            `json:"task_id"`
	Branch            string            `json:"branch"`
	IterationBranch   string            `json:"iteration_branch"`
	IterationWorktree string            `json:"iteration_worktree"`
	TaskCommits       []taskMergeCommit `json:"task_commits"`
	Subject           string            `json:"subject"`
	Status            string            `json:"status"`
	ConflictPaths     []string          `json:"conflict_paths,omitempty"`
	CreatedAt         string            `json:"created_at"`
	UpdatedAt         string            `json:"updated_at"`
}

type stagedFile struct {
	Status string
	Path   string
	Orig   string
}

type taskTodoStageSummary struct {
	Staged   []stagedFile
	Unstaged []gitx.StatusEntry
}

func commandTask(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop task <todo|merge|discard> ...")}
	}
	switch args[0] {
	case "todo":
		return commandTaskTodo(ctx, g, args[1:])
	case "merge":
		return commandTaskMerge(ctx, g, args[1:])
	case "discard":
		return commandTaskDiscard(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown task subcommand %q", args[0])}
	}
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ", ")
}

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("value must not be empty")
	}
	*f = append(*f, value)
	return nil
}

func commandTaskTodo(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop task todo <add|list|move|remove|cancel|stage|start|complete> ...")}
	}
	switch args[0] {
	case "add":
		return commandTaskTodoAdd(ctx, g, args[1:])
	case "cancel":
		return commandTaskTodoCancel(ctx, g, args[1:])
	case "list":
		return commandTaskTodoList(ctx, g, args[1:])
	case "move":
		return commandTaskTodoMove(ctx, g, args[1:])
	case "remove":
		return commandTaskTodoRemove(ctx, g, args[1:])
	case "stage":
		return commandTaskTodoStage(ctx, g, args[1:])
	case "start":
		return commandTaskTodoStart(ctx, g, args[1:])
	case "complete":
		return commandTaskTodoComplete(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown task todo subcommand %q", args[0])}
	}
}

func commandTaskTodoAdd(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task todo add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	workTypeFlag := fs.String("work-type", "commit", "TODO work type: commit or no_commit")
	kind := fs.String("type", "", "commit type")
	title := fs.String("title", "", "TODO title")
	after := fs.Int("after", -1, "insert after 1-based TODO index; 0 inserts at the top")
	var acceptance stringListFlag
	fs.Var(&acceptance, "acceptance", "acceptance criterion; repeatable")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true, "dir": true, "run": true, "iteration": true,
		"task": true, "work-type": true, "type": true, "title": true, "after": true, "acceptance": true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	workType, err := normalizeTaskTodoWorkType(*workTypeFlag)
	if err != nil {
		return codedError{2, err}
	}
	if strings.TrimSpace(*title) == "" || len(acceptance) == 0 {
		return codedError{2, fmt.Errorf("usage: loop task todo add [--work-type <commit|no_commit>] --type <type> --title <title> --acceptance <text>... [--after <n>] <commit-message>")}
	}
	if workType == "commit" && (strings.TrimSpace(*kind) == "" || fs.NArg() < 1) {
		return codedError{2, fmt.Errorf("usage: loop task todo add --work-type commit --type <type> --title <title> --acceptance <text>... [--after <n>] <commit-message>")}
	}
	if workType == "no_commit" && (strings.TrimSpace(*kind) != "" || fs.NArg() > 0) {
		return codedError{2, fmt.Errorf("no_commit task TODOs must not include --type or a commit message")}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	insertAfter := *after
	if insertAfter == -1 {
		insertAfter = len(todos.Items)
	}
	if insertAfter < 0 || insertAfter > len(todos.Items) {
		return codedError{2, fmt.Errorf("--after index %d is out of range", *after)}
	}
	if err := requireTaskTodoInsertAllowed(todos.Items, insertAfter); err != nil {
		return codedError{2, err}
	}
	item := taskTodoItem{
		Status:     "pending",
		WorkType:   workType,
		Title:      strings.TrimSpace(*title),
		Acceptance: append([]string(nil), acceptance...),
	}
	event := runstate.Event{"type": "task.todo.added", "task_id": mergeCtx.TaskID, "index": insertAfter + 1, "title": item.Title, "work_type": item.WorkType}
	if workType == "commit" {
		subject, err := buildLoopCommitSubject(*kind, strings.Join(fs.Args(), " "), loopCommitMessageMaxLength)
		if err != nil {
			return codedError{2, err}
		}
		prefix, _, _ := strings.Cut(subject, ": ")
		item.Type = prefix
		item.CommitMessage = strings.Join(fs.Args(), " ")
		event["commit_subject"] = subject
	}
	todos.Items = append(todos.Items[:insertAfter], append([]taskTodoItem{item}, todos.Items[insertAfter:]...)...)
	if err := writeTaskTodoFile(mergeCtx, todos); err != nil {
		return codedError{1, err}
	}
	index := insertAfter + 1
	appendTaskMergeEvent(mergeCtx, event)
	return printResult(g, map[string]any{"task": mergeCtx.TaskID, "index": index, "todo": taskTodoJSONItem(index, item)}, fmt.Sprintf("added task todo %d\n", index))
}

func commandTaskTodoList(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task todo list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop task todo list [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"task": mergeCtx.TaskID, "items": taskTodoJSONItems(todos.Items)}, renderTaskTodoList(todos.Items))
}

func commandTaskTodoMove(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task todo move", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	after := fs.Int("after", -1, "move after 1-based TODO index; 0 moves to the top")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true, "after": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 1 || *after == -1 {
		return codedError{2, fmt.Errorf("usage: loop task todo move <n> --after <n> [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	index, err := parseTodoIndex(fs.Arg(0))
	if err != nil {
		return codedError{2, err}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	newIndex, err := moveTaskTodoItem(&todos, index, *after)
	if err != nil {
		return codedError{2, err}
	}
	if err := writeTaskTodoFile(mergeCtx, todos); err != nil {
		return codedError{1, err}
	}
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.todo.moved", "task_id": mergeCtx.TaskID, "index": index, "after": *after, "new_index": newIndex})
	return printResult(g, map[string]any{"task": mergeCtx.TaskID, "index": newIndex, "todo": taskTodoJSONItem(newIndex, todos.Items[newIndex-1])}, fmt.Sprintf("moved task todo %d to %d\n", index, newIndex))
}

func commandTaskTodoRemove(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task todo remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop task todo remove <n> [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	index, err := parseTodoIndex(fs.Arg(0))
	if err != nil {
		return codedError{2, err}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	removed, err := removeTaskTodoItem(&todos, index)
	if err != nil {
		return codedError{2, err}
	}
	if err := writeTaskTodoFile(mergeCtx, todos); err != nil {
		return codedError{1, err}
	}
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.todo.removed", "task_id": mergeCtx.TaskID, "index": index, "title": removed.Title})
	return printResult(g, map[string]any{"task": mergeCtx.TaskID, "index": index, "removed": taskTodoJSONItem(index, removed)}, fmt.Sprintf("removed task todo %d\n", index))
}

func commandTaskTodoStart(ctx context.Context, g globals, args []string) error {
	return updateTaskTodoStatus(ctx, g, args, "start")
}

func commandTaskTodoCancel(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task todo cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	discardChanges := fs.Bool("discard-changes", false, "discard task worktree and index changes before cancelling an active TODO")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop task todo cancel <n> [--discard-changes] [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	index, err := parseTodoIndex(fs.Arg(0))
	if err != nil {
		return codedError{2, err}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	if err := cancelTaskTodoItem(ctx, mergeCtx.TaskWorktree, &todos, index, *discardChanges); err != nil {
		return codedError{2, err}
	}
	if err := writeTaskTodoFile(mergeCtx, todos); err != nil {
		return codedError{1, err}
	}
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.todo.cancelled", "task_id": mergeCtx.TaskID, "index": index, "discard_changes": *discardChanges})
	return printResult(g, map[string]any{"task": mergeCtx.TaskID, "index": index, "todo": taskTodoJSONItem(index, todos.Items[index-1])}, fmt.Sprintf("cancelled task todo %d\n", index))
}

func commandTaskTodoStage(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task todo stage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	reset := fs.Bool("reset", false, "unstage all staged files before applying other stage changes")
	var adds stringListFlag
	var removes stringListFlag
	fs.Var(&adds, "add", "path to stage; repeatable")
	fs.Var(&removes, "remove", "path to unstage; repeatable")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true,
		"add": true, "remove": true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop task todo stage <n> [--add <path>]... [--remove <path>]... [--reset] [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	index, err := parseTodoIndex(fs.Arg(0))
	if err != nil {
		return codedError{2, err}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	if err := requireStageableTaskTodo(todos, index); err != nil {
		return codedError{2, err}
	}
	summary, err := stageTaskTodoChanges(ctx, mergeCtx.TaskWorktree, *reset, adds, removes)
	if err != nil {
		return codedError{1, err}
	}
	appendTaskMergeEvent(mergeCtx, runstate.Event{
		"type":     "task.todo.staged",
		"task_id":  mergeCtx.TaskID,
		"index":    index,
		"staged":   len(summary.Staged),
		"unstaged": len(summary.Unstaged),
	})
	out := map[string]any{
		"task":     mergeCtx.TaskID,
		"index":    index,
		"staged":   stagedFileJSONItems(summary.Staged),
		"unstaged": statusEntryJSONItems(summary.Unstaged),
	}
	return printResult(g, out, renderTaskTodoStageSummary(summary))
}

func commandTaskTodoComplete(ctx context.Context, g globals, args []string) error {
	return updateTaskTodoStatus(ctx, g, args, "complete")
}

func updateTaskTodoStatus(ctx context.Context, g globals, args []string, action string) error {
	fs := flag.NewFlagSet("task todo "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop task todo %s <n> [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]", action)}
	}
	index, err := parseTodoIndex(fs.Arg(0))
	if err != nil {
		return codedError{2, err}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	if index < 1 || index > len(todos.Items) {
		return codedError{2, fmt.Errorf("todo index %d is out of range", index)}
	}
	if action == "start" {
		if err := startTaskTodoItem(&todos, index); err != nil {
			return codedError{2, err}
		}
		if err := writeTaskTodoFile(mergeCtx, todos); err != nil {
			return codedError{1, err}
		}
		appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.todo.started", "task_id": mergeCtx.TaskID, "index": index})
		return printResult(g, map[string]any{"task": mergeCtx.TaskID, "index": index, "todo": taskTodoJSONItem(index, todos.Items[index-1])}, fmt.Sprintf("started task todo %d\n", index))
	}
	commit, err := completeTaskTodoItem(ctx, mergeCtx, &todos, index)
	if err != nil {
		return codedError{1, err}
	}
	if err := writeTaskTodoFile(mergeCtx, todos); err != nil {
		return codedError{1, err}
	}
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.todo.completed", "task_id": mergeCtx.TaskID, "index": index, "commit": commit.SHA})
	out := map[string]any{"task": mergeCtx.TaskID, "index": index, "todo": taskTodoJSONItem(index, todos.Items[index-1])}
	if commit.SHA != "" {
		out["commit"] = commit
		return printResult(g, out, fmt.Sprintf("completed task todo %d: %s %s\n", index, shortSHA(commit.SHA), commit.Subject))
	}
	return printResult(g, out, fmt.Sprintf("completed task todo %d without commit\n", index))
}

func commandTaskDiscard(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task discard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	reason := fs.String("reason", "", "discard reason")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true, "reason": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 0 || strings.TrimSpace(*reason) == "" {
		return codedError{2, fmt.Errorf("usage: loop task discard --reason <reason> [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	result := workflow.TaskResult{
		SchemaVersion: 1,
		TaskID:        mergeCtx.TaskID,
		Status:        "discarded",
		Summary:       "Task discarded.",
		DiscardReason: strings.TrimSpace(*reason),
	}
	data, err := workflow.MarshalIndent(result)
	if err != nil {
		return codedError{1, err}
	}
	globalPath := artifactdb.GlobalDBPathForIteration(mergeCtx.IterationDir)
	if err := artifactdb.WriteRoleHandoff(globalPath, mergeCtx.RunID, mergeCtx.IterationID, "task-result", mergeCtx.TaskID, string(data)); err != nil {
		return codedError{1, err}
	}
	if err := writeTaskResultAudit(mergeCtx.IterationDir, mergeCtx.TaskDir, mergeCtx.TaskID, data); err != nil {
		return codedError{1, err}
	}
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.discarded", "task_id": mergeCtx.TaskID, "reason": result.DiscardReason})
	return printResult(g, map[string]any{"task": mergeCtx.TaskID, "status": "discarded", "reason": result.DiscardReason}, "discarded task "+mergeCtx.TaskID+"\n")
}

func commandTaskMerge(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task merge", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	kind := fs.String("type", "", "merge commit type")
	continueMerge := fs.Bool("continue", false, "commit a conflict resolution for a pending task merge")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true,
		"type": true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if *continueMerge {
		if fs.NArg() != 0 {
			return codedError{2, fmt.Errorf("usage: loop task merge --continue [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
		}
	} else if strings.TrimSpace(*kind) == "" || fs.NArg() < 1 {
		return codedError{2, fmt.Errorf("usage: loop task merge --type <type> <summary> [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	if *continueMerge {
		return continueTaskMerge(ctx, g, mergeCtx)
	}
	subject, err := buildLoopCommitSubject(*kind, strings.Join(fs.Args(), " "), loopCommitMessageMaxLength)
	if err != nil {
		return codedError{2, err}
	}
	return startTaskMerge(ctx, g, mergeCtx, subject)
}

func resolveTaskMergeContext(ctx context.Context, g globals, iterDir, runID, iteration, taskID string) (taskMergeContext, error) {
	resolvedDir, err := resolveIterationDir(ctx, g, iterDir, runID, iteration)
	if err != nil {
		return taskMergeContext{}, err
	}
	if strings.TrimSpace(resolvedDir) == "" {
		return taskMergeContext{}, errors.New("iteration directory is required")
	}
	runtime := map[string]any{}
	if loaded, err := readRuntimeMap(resolvedDir); err == nil {
		runtime = loaded
	}
	run, iter := artifactdb.ParseIterationDir(resolvedDir)
	run = firstNonEmpty(run, strings.TrimSpace(runID), os.Getenv("LOOP_RUN_ID"))
	iter = firstNonEmpty(iter, strings.TrimSpace(iteration), os.Getenv("LOOP_ITERATION_ID"))
	if run == "" || iter == "" || iter == "latest" {
		return taskMergeContext{}, errors.New("run id and concrete iteration id are required")
	}
	taskID = firstNonEmpty(strings.TrimSpace(taskID), runtimeString(runtime, "task_id"), os.Getenv("LOOP_TASK_ID"))
	if taskID == "" {
		return taskMergeContext{}, errors.New("task id is required")
	}
	repoRoot, _ := gitx.RepoRoot(ctx, ".")
	storageRoot := storageRootFromIterationDir(resolvedDir)
	if storageRoot == "" {
		storageRoot = repoRoot
	}
	taskDir := firstNonEmpty(runtimeString(runtime, "task_dir"), os.Getenv("LOOP_TASK_DIR"))
	if taskDir == "" {
		taskDir, err = findTaskAuditDir(resolvedDir, taskID)
		if err != nil {
			return taskMergeContext{}, err
		}
	}
	task, err := readTaskAuditFile(taskDir, taskID)
	if err != nil {
		return taskMergeContext{}, err
	}
	taskWorktree := firstNonEmpty(runtimeString(runtime, "workdir"), os.Getenv("LOOP_WORKDIR"))
	if taskWorktree == "" {
		if cwd, err := os.Getwd(); err == nil {
			taskWorktree = cwd
		}
	}
	taskBranch := firstNonEmpty(runtimeString(runtime, "current_branch"), os.Getenv("LOOP_CURRENT_BRANCH"))
	if taskBranch == "" && taskWorktree != "" {
		taskBranch, _ = (gitx.Runner{Dir: taskWorktree}).CurrentBranch(ctx)
	}
	iterationBranch := firstNonEmpty(
		runtimeString(runtime, "iteration_branch"),
		os.Getenv("LOOP_ITERATION_BRANCH"),
		runtimeString(runtime, "initial_branch"),
		os.Getenv("LOOP_INITIAL_BRANCH"),
	)
	if iterationBranch == "" {
		iterationBranch = gitx.InitialBranchName(iterationNumberFromID(iter))
	}
	iterationWorktree := firstNonEmpty(runtimeString(runtime, "iteration_worktree"), os.Getenv("LOOP_ITERATION_WORKTREE"))
	if iterationWorktree == "" && storageRoot != "" {
		iterationWorktree = filepath.Join(storageRoot, "worktrees", run, iter, "iteration")
	}
	if taskWorktree == "" || taskBranch == "" || iterationBranch == "" || iterationWorktree == "" || storageRoot == "" {
		return taskMergeContext{}, errors.New("task merge context is incomplete; run inside a loop coding task")
	}
	return taskMergeContext{
		IterationDir:      resolvedDir,
		RunID:             run,
		IterationID:       iter,
		TaskID:            taskID,
		TaskDir:           taskDir,
		TaskWorktree:      taskWorktree,
		TaskBranch:        taskBranch,
		IterationBranch:   iterationBranch,
		IterationWorktree: iterationWorktree,
		StorageRoot:       storageRoot,
		Task:              task,
	}, nil
}

func startTaskMerge(ctx context.Context, g globals, mergeCtx taskMergeContext, subject string) error {
	lockDir, err := acquireTaskMergeLock(ctx, mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	keepLock := false
	defer func() {
		if !keepLock {
			_ = os.RemoveAll(lockDir)
		}
	}()
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.merge.started", "task_id": mergeCtx.TaskID, "branch": mergeCtx.TaskBranch})
	taskCommits, err := requireCompletedTaskTodos(ctx, mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	if err := requireCleanTaskWorktree(ctx, mergeCtx.TaskWorktree, "complete task TODOs before merging"); err != nil {
		return codedError{1, err}
	}
	lock := taskMergeLock{
		SchemaVersion:     1,
		RunID:             mergeCtx.RunID,
		IterationID:       mergeCtx.IterationID,
		TaskID:            mergeCtx.TaskID,
		Branch:            mergeCtx.TaskBranch,
		IterationBranch:   mergeCtx.IterationBranch,
		IterationWorktree: mergeCtx.IterationWorktree,
		TaskCommits:       taskCommits,
		Subject:           subject,
		Status:            "merging",
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeTaskMergeLock(lockDir, lock); err != nil {
		return codedError{1, err}
	}
	iterRunner := gitx.Runner{Dir: mergeCtx.IterationWorktree}
	if err := ensureIterationBranch(ctx, iterRunner, mergeCtx.IterationBranch); err != nil {
		return codedError{1, err}
	}
	beforeMergeHead, err := iterRunner.Head(ctx)
	if err != nil {
		return codedError{1, err}
	}
	if _, err := iterRunner.Run(ctx, "merge", "--no-ff", "-m", subject, mergeCtx.TaskBranch); err != nil {
		conflicts := unmergedPaths(ctx, iterRunner)
		if len(conflicts) > 0 {
			lock.Status = "conflicts"
			lock.ConflictPaths = conflicts
			lock.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			_ = writeTaskMergeLock(lockDir, lock)
			appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.merge.conflicts", "task_id": mergeCtx.TaskID, "branch": mergeCtx.TaskBranch, "conflicts": conflicts, "iteration_worktree": mergeCtx.IterationWorktree})
			keepLock = true
			return codedError{1, fmt.Errorf("task merge for %s has conflicts in %s; resolve them and run `loop task merge --continue`: %w", mergeCtx.TaskID, mergeCtx.IterationWorktree, err)}
		}
		_, _ = iterRunner.Run(context.Background(), "reset", "--hard")
		_, _ = runGitCleanPreservingLoopRuntime(context.Background(), iterRunner)
		return codedError{1, err}
	}
	mergeCommit, err := taskMergeHead(ctx, iterRunner)
	if err != nil {
		return codedError{1, err}
	}
	if mergeCommit.SHA == strings.TrimSpace(beforeMergeHead) {
		return codedError{1, errors.New("no merged task changes to commit")}
	}
	record := buildTaskMergeRecord(mergeCtx, taskCommits, mergeCommit)
	if err := writeTaskMergeAudit(mergeCtx.TaskDir, record); err != nil {
		return codedError{1, err}
	}
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.merge.completed", "task_id": mergeCtx.TaskID, "branch": mergeCtx.TaskBranch, "merge_commit": mergeCommit.SHA})
	return printTaskMergeResult(g, record)
}

func taskMergeHead(ctx context.Context, runner gitx.Runner) (taskMergeCommit, error) {
	sha, err := runner.Head(ctx)
	if err != nil {
		return taskMergeCommit{}, err
	}
	subject, err := runner.Run(ctx, "log", "-1", "--format=%s")
	if err != nil {
		return taskMergeCommit{}, err
	}
	return taskMergeCommit{SHA: strings.TrimSpace(sha), Subject: strings.TrimSpace(subject)}, nil
}

func continueTaskMerge(ctx context.Context, g globals, mergeCtx taskMergeContext) error {
	lockDir := taskMergeLockDir(mergeCtx)
	lock, err := readTaskMergeLock(lockDir)
	if err != nil {
		return codedError{1, fmt.Errorf("no pending task merge for %s: %w", mergeCtx.TaskID, err)}
	}
	if lock.TaskID != "" && lock.TaskID != mergeCtx.TaskID {
		return codedError{2, fmt.Errorf("pending task merge belongs to %s, not %s", lock.TaskID, mergeCtx.TaskID)}
	}
	iterRunner := gitx.Runner{Dir: firstNonEmpty(lock.IterationWorktree, mergeCtx.IterationWorktree)}
	subject := strings.TrimSpace(lock.Subject)
	if subject == "" {
		return codedError{1, errors.New("pending task merge is missing its commit subject")}
	}
	mergeCommit, err := commitMergedTask(ctx, iterRunner, subject)
	if err != nil {
		return codedError{1, err}
	}
	record := buildTaskMergeRecord(mergeCtx, lock.TaskCommits, mergeCommit)
	if err := writeTaskMergeAudit(mergeCtx.TaskDir, record); err != nil {
		return codedError{1, err}
	}
	_ = os.RemoveAll(lockDir)
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.merge.completed", "task_id": mergeCtx.TaskID, "branch": mergeCtx.TaskBranch, "merge_commit": mergeCommit.SHA})
	return printTaskMergeResult(g, record)
}

func readTaskTodoFile(mergeCtx taskMergeContext) (taskTodoFile, error) {
	path := taskTodoPath(mergeCtx.TaskDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return taskTodoFile{SchemaVersion: 1, TaskID: mergeCtx.TaskID}, nil
		}
		return taskTodoFile{}, err
	}
	var todos taskTodoFile
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&todos); err != nil {
		return taskTodoFile{}, err
	}
	if todos.SchemaVersion != 1 {
		return taskTodoFile{}, errors.New("task TODO schema_version must be 1")
	}
	if todos.TaskID != mergeCtx.TaskID {
		return taskTodoFile{}, fmt.Errorf("task TODO task_id %q does not match %q", todos.TaskID, mergeCtx.TaskID)
	}
	for i, item := range todos.Items {
		if err := validateTaskTodoItem(i+1, item); err != nil {
			return taskTodoFile{}, err
		}
		todos.Items[i].WorkType = taskTodoWorkType(item)
	}
	return todos, nil
}

func writeTaskTodoFile(mergeCtx taskMergeContext, todos taskTodoFile) error {
	todos.SchemaVersion = 1
	todos.TaskID = mergeCtx.TaskID
	for i := range todos.Items {
		todos.Items[i].WorkType = taskTodoWorkType(todos.Items[i])
	}
	data, err := json.MarshalIndent(todos, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(mergeCtx.TaskDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(taskTodoPath(mergeCtx.TaskDir), data, 0o644)
}

func taskTodoPath(taskDir string) string {
	return filepath.Join(taskDir, "task-todo.json")
}

func validateTaskTodoItem(index int, item taskTodoItem) error {
	if !validTaskTodoStatus(item.Status) {
		return fmt.Errorf("todo %d status must be pending, active, done, or cancelled", index)
	}
	if strings.TrimSpace(item.Title) == "" {
		return fmt.Errorf("todo %d title is required", index)
	}
	if len(item.Acceptance) == 0 {
		return fmt.Errorf("todo %d acceptance must have at least one item", index)
	}
	workType, err := normalizeTaskTodoWorkType(item.WorkType)
	if err != nil {
		return fmt.Errorf("todo %d %w", index, err)
	}
	switch workType {
	case "commit":
		if _, err := normalizeLoopCommitType(item.Type); err != nil {
			return fmt.Errorf("todo %d %w", index, err)
		}
		if strings.TrimSpace(item.CommitMessage) == "" {
			return fmt.Errorf("todo %d commit_message is required", index)
		}
		if item.Status == "done" {
			if strings.TrimSpace(item.CommitSHA) == "" || strings.TrimSpace(item.CommitSubject) == "" {
				return fmt.Errorf("todo %d done commit item requires commit_sha and commit_subject", index)
			}
			if err := validateLoopCommitSubject(item.CommitSubject, loopCommitMessageMaxLength); err != nil {
				return fmt.Errorf("todo %d commit_subject is invalid: %w", index, err)
			}
		}
	case "no_commit":
		if strings.TrimSpace(item.Type) != "" || strings.TrimSpace(item.CommitMessage) != "" || strings.TrimSpace(item.CommitSHA) != "" || strings.TrimSpace(item.CommitSubject) != "" {
			return fmt.Errorf("todo %d no_commit item must not include commit metadata", index)
		}
	}
	if item.Status == "cancelled" && strings.TrimSpace(item.CancelledAt) == "" {
		return fmt.Errorf("todo %d cancelled item requires cancelled_at", index)
	}
	return nil
}

func normalizeTaskTodoWorkType(workType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(workType)) {
	case "", "commit":
		return "commit", nil
	case "no_commit", "no-commit", "none", "no commit":
		return "no_commit", nil
	default:
		return "", fmt.Errorf("work_type must be commit or no_commit")
	}
}

func taskTodoWorkType(item taskTodoItem) string {
	workType, err := normalizeTaskTodoWorkType(item.WorkType)
	if err != nil {
		return ""
	}
	return workType
}

func validTaskTodoStatus(status string) bool {
	switch status {
	case "pending", "active", "done", "cancelled":
		return true
	default:
		return false
	}
}

func taskTodoFixedBoundary(items []taskTodoItem) int {
	boundary := 0
	for i, item := range items {
		if taskTodoStatusFixed(item.Status) {
			boundary = i + 1
		}
	}
	return boundary
}

func taskTodoStatusFixed(status string) bool {
	return status == "done" || status == "active" || status == "cancelled"
}

func taskTodoStatusTerminal(status string) bool {
	return status == "done" || status == "cancelled"
}

func requireTaskTodoInsertAllowed(items []taskTodoItem, after int) error {
	boundary := taskTodoFixedBoundary(items)
	if after < boundary {
		return fmt.Errorf("cannot insert before fixed task TODO %d; use --after %d or later", boundary, boundary)
	}
	return nil
}

func moveTaskTodoItem(todos *taskTodoFile, index, after int) (int, error) {
	if len(todos.Items) == 0 {
		return 0, errors.New("task TODO list is empty")
	}
	if index < 1 || index > len(todos.Items) {
		return 0, fmt.Errorf("todo index %d is out of range", index)
	}
	if after < 0 || after > len(todos.Items) {
		return 0, fmt.Errorf("--after index %d is out of range", after)
	}
	if todos.Items[index-1].Status != "pending" {
		return 0, fmt.Errorf("todo %d status is %s, not pending", index, todos.Items[index-1].Status)
	}
	if err := requireTaskTodoInsertAllowed(todos.Items, after); err != nil {
		return 0, err
	}
	if after == index {
		return 0, fmt.Errorf("cannot move todo %d after itself", index)
	}
	if after == index-1 {
		return index, nil
	}
	item := todos.Items[index-1]
	items := append([]taskTodoItem{}, todos.Items[:index-1]...)
	items = append(items, todos.Items[index:]...)
	insertAt := after
	if after > index {
		insertAt = after - 1
	}
	items = append(items[:insertAt], append([]taskTodoItem{item}, items[insertAt:]...)...)
	todos.Items = items
	return insertAt + 1, nil
}

func removeTaskTodoItem(todos *taskTodoFile, index int) (taskTodoItem, error) {
	if len(todos.Items) == 0 {
		return taskTodoItem{}, errors.New("task TODO list is empty")
	}
	if index < 1 || index > len(todos.Items) {
		return taskTodoItem{}, fmt.Errorf("todo index %d is out of range", index)
	}
	item := todos.Items[index-1]
	if item.Status != "pending" {
		return taskTodoItem{}, fmt.Errorf("todo %d status is %s, not pending", index, item.Status)
	}
	todos.Items = append(todos.Items[:index-1], todos.Items[index:]...)
	return item, nil
}

func requireCleanTaskWorktree(ctx context.Context, worktree, purpose string) error {
	runner := gitx.Runner{Dir: worktree}
	dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return err
	}
	if !dirty.Clean {
		if strings.TrimSpace(purpose) == "" {
			purpose = "continue"
		}
		return fmt.Errorf("%s; working tree is dirty: %s", purpose, dirtyList(dirty.Dirty))
	}
	return nil
}

func startTaskTodoItem(todos *taskTodoFile, index int) error {
	if len(todos.Items) == 0 {
		return errors.New("task TODO list is empty")
	}
	for i := range todos.Items {
		switch {
		case i < index-1 && !taskTodoStatusTerminal(todos.Items[i].Status):
			return fmt.Errorf("todo %d must be completed before todo %d can start", i+1, index)
		case i == index-1:
			if todos.Items[i].Status != "pending" {
				return fmt.Errorf("todo %d status is %s, not pending", index, todos.Items[i].Status)
			}
		case todos.Items[i].Status == "active":
			return fmt.Errorf("todo %d is already active", i+1)
		}
	}
	todos.Items[index-1].Status = "active"
	todos.Items[index-1].StartedAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

func cancelTaskTodoItem(ctx context.Context, workDir string, todos *taskTodoFile, index int, discardChanges bool) error {
	if len(todos.Items) == 0 {
		return errors.New("task TODO list is empty")
	}
	if index < 1 || index > len(todos.Items) {
		return fmt.Errorf("todo index %d is out of range", index)
	}
	item := todos.Items[index-1]
	switch item.Status {
	case "pending":
		// No worktree changes belong to a pending TODO yet.
	case "active":
		if !discardChanges {
			return fmt.Errorf("todo %d is active; pass --discard-changes to cancel and discard worktree changes", index)
		}
		if err := discardTaskTodoWorktreeChanges(ctx, workDir); err != nil {
			return err
		}
	default:
		return fmt.Errorf("todo %d status is %s, not pending or active", index, item.Status)
	}
	todos.Items[index-1].Status = "cancelled"
	todos.Items[index-1].CancelledAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

func discardTaskTodoWorktreeChanges(ctx context.Context, workDir string) error {
	runner := gitx.Runner{Dir: workDir}
	dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return err
	}
	pathspecs := dirtyPathspecs(dirty.Dirty)
	if _, err := runner.Run(ctx, "reset", "--hard", "-q", "HEAD"); err != nil {
		return err
	}
	if len(pathspecs) == 0 {
		return nil
	}
	args := append([]string{"clean", "-fd", "-q", "--"}, pathspecs...)
	_, err = runner.Run(ctx, args...)
	return err
}

func completeTaskTodoItem(ctx context.Context, mergeCtx taskMergeContext, todos *taskTodoFile, index int) (taskMergeCommit, error) {
	if len(todos.Items) == 0 {
		return taskMergeCommit{}, errors.New("task TODO list is empty")
	}
	for i := range todos.Items {
		if i < index-1 && !taskTodoStatusTerminal(todos.Items[i].Status) {
			return taskMergeCommit{}, fmt.Errorf("todo %d must be completed before todo %d can complete", i+1, index)
		}
	}
	item := todos.Items[index-1]
	if item.Status != "active" {
		return taskMergeCommit{}, fmt.Errorf("todo %d status is %s, not active", index, item.Status)
	}
	if taskTodoWorkType(item) == "no_commit" {
		if err := completeNoCommitTaskTodoChanges(ctx, mergeCtx.TaskWorktree); err != nil {
			return taskMergeCommit{}, err
		}
		todos.Items[index-1].Status = "done"
		todos.Items[index-1].CompletedAt = time.Now().UTC().Format(time.RFC3339)
		return taskMergeCommit{}, nil
	}
	subject, err := buildLoopCommitSubject(item.Type, item.CommitMessage, loopCommitMessageMaxLength)
	if err != nil {
		return taskMergeCommit{}, err
	}
	commit, err := commitTaskTodoChanges(ctx, mergeCtx.TaskWorktree, subject)
	if err != nil {
		return taskMergeCommit{}, err
	}
	todos.Items[index-1].Status = "done"
	todos.Items[index-1].CommitSHA = commit.SHA
	todos.Items[index-1].CommitSubject = commit.Subject
	todos.Items[index-1].CompletedAt = time.Now().UTC().Format(time.RFC3339)
	return commit, nil
}

func completeNoCommitTaskTodoChanges(ctx context.Context, workDir string) error {
	runner := gitx.Runner{Dir: workDir}
	unstageRuntimePaths(ctx, runner)
	dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return err
	}
	if !dirty.Clean {
		return fmt.Errorf("no_commit task TODO cannot complete with repository changes: %s", dirtyList(dirty.Dirty))
	}
	return nil
}

func commitTaskTodoChanges(ctx context.Context, workDir, subject string) (taskMergeCommit, error) {
	runner := gitx.Runner{Dir: workDir}
	unstageRuntimePaths(ctx, runner)
	staged, err := stagedFiles(ctx, runner)
	if err != nil {
		return taskMergeCommit{}, err
	}
	if len(staged) == 0 {
		dirty, cleanErr := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
		if cleanErr != nil {
			return taskMergeCommit{}, cleanErr
		}
		if dirty.Clean {
			return taskMergeCommit{}, errors.New("no repository changes to commit for active task TODO")
		}
		return taskMergeCommit{}, errors.New("no staged changes to commit for active task TODO; run loop task todo stage <n> first")
	}
	if _, err := runner.Run(ctx, "commit", "-m", subject); err != nil {
		return taskMergeCommit{}, err
	}
	sha, err := runner.Head(ctx)
	if err != nil {
		return taskMergeCommit{}, err
	}
	return taskMergeCommit{SHA: strings.TrimSpace(sha), Subject: subject}, nil
}

func requireStageableTaskTodo(todos taskTodoFile, index int) error {
	if len(todos.Items) == 0 {
		return errors.New("task TODO list is empty")
	}
	if index < 1 || index > len(todos.Items) {
		return fmt.Errorf("todo index %d is out of range", index)
	}
	for i := range todos.Items {
		if i < index-1 && !taskTodoStatusTerminal(todos.Items[i].Status) {
			return fmt.Errorf("todo %d must be completed before todo %d can be staged", i+1, index)
		}
	}
	if todos.Items[index-1].Status != "active" {
		return fmt.Errorf("todo %d status is %s, not active", index, todos.Items[index-1].Status)
	}
	if taskTodoWorkType(todos.Items[index-1]) == "no_commit" {
		return fmt.Errorf("todo %d work_type is no_commit; no files should be staged", index)
	}
	return nil
}

func stageTaskTodoChanges(ctx context.Context, workDir string, reset bool, adds, removes []string) (taskTodoStageSummary, error) {
	runner := gitx.Runner{Dir: workDir}
	if reset {
		if _, err := runner.Run(ctx, "reset", "-q"); err != nil {
			return taskTodoStageSummary{}, err
		}
	}
	if len(removes) > 0 {
		args := append([]string{"reset", "-q", "--"}, removes...)
		if _, err := runner.Run(ctx, args...); err != nil {
			return taskTodoStageSummary{}, err
		}
	}
	if len(adds) > 0 {
		args := append([]string{"add", "-A", "--"}, adds...)
		if _, err := runner.Run(ctx, args...); err != nil {
			return taskTodoStageSummary{}, err
		}
	}
	if !reset && len(adds) == 0 && len(removes) == 0 {
		dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
		if err != nil {
			return taskTodoStageSummary{}, err
		}
		pathspecs := dirtyPathspecs(dirty.Dirty)
		if len(pathspecs) > 0 {
			args := append([]string{"add", "-A", "--"}, pathspecs...)
			if _, err := runner.Run(ctx, args...); err != nil {
				return taskTodoStageSummary{}, err
			}
		}
	}
	unstageRuntimePaths(ctx, runner)
	return taskTodoStageStatus(ctx, runner)
}

func taskTodoStageStatus(ctx context.Context, runner gitx.Runner) (taskTodoStageSummary, error) {
	staged, err := stagedFiles(ctx, runner)
	if err != nil {
		return taskTodoStageSummary{}, err
	}
	dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return taskTodoStageSummary{}, err
	}
	return taskTodoStageSummary{Staged: staged, Unstaged: unstagedStatusEntries(dirty.Dirty)}, nil
}

func stagedFiles(ctx context.Context, runner gitx.Runner) ([]stagedFile, error) {
	out, err := runner.Run(ctx, "diff", "--cached", "--name-status", "-z")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	parts := strings.Split(out, "\x00")
	files := make([]stagedFile, 0, len(parts)/2)
	for i := 0; i < len(parts); i++ {
		status := parts[i]
		if status == "" {
			continue
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if i+2 >= len(parts) {
				break
			}
			files = append(files, stagedFile{Status: status, Orig: parts[i+1], Path: parts[i+2]})
			i += 2
			continue
		}
		if i+1 >= len(parts) {
			break
		}
		files = append(files, stagedFile{Status: status, Path: parts[i+1]})
		i++
	}
	return files, nil
}

func unstagedStatusEntries(entries []gitx.StatusEntry) []gitx.StatusEntry {
	var out []gitx.StatusEntry
	for _, entry := range entries {
		if entry.Code == "??" || (len(entry.Code) > 1 && entry.Code[1] != ' ') {
			out = append(out, entry)
		}
	}
	return out
}

func requireCompletedTaskTodos(ctx context.Context, mergeCtx taskMergeContext) ([]taskMergeCommit, error) {
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return nil, err
	}
	if len(todos.Items) == 0 {
		return nil, fmt.Errorf("task %s has no TODOs; create task TODOs before implementation", mergeCtx.TaskID)
	}
	commits := make([]taskMergeCommit, 0, len(todos.Items))
	for i, item := range todos.Items {
		switch item.Status {
		case "done":
			if taskTodoWorkType(item) == "commit" {
				commits = append(commits, taskMergeCommit{SHA: item.CommitSHA, Subject: item.CommitSubject})
			}
		case "cancelled":
			continue
		default:
			return nil, fmt.Errorf("task TODO %d is %s, not done or cancelled", i+1, item.Status)
		}
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("task %s has no completed TODO commits", mergeCtx.TaskID)
	}
	gitCommits, err := (gitx.Runner{Dir: mergeCtx.TaskWorktree}).ListCommits(ctx, mergeCtx.IterationBranch, "HEAD")
	if err != nil {
		return nil, err
	}
	if len(gitCommits) != len(commits) {
		return nil, fmt.Errorf("task branch has %d commit(s), but task TODOs recorded %d completed commit(s)", len(gitCommits), len(commits))
	}
	return commits, nil
}

func renderTaskTodoList(items []taskTodoItem) string {
	var b strings.Builder
	for i, item := range items {
		detail := "no_commit"
		if taskTodoWorkType(item) == "commit" {
			detail, _ = buildLoopCommitSubject(item.Type, item.CommitMessage, loopCommitMessageMaxLength)
		}
		fmt.Fprintf(&b, "%d. [%s] %s - %s\n", i+1, taskTodoStatusMarker(item.Status), item.Title, detail)
	}
	return b.String()
}

func renderTaskTodoStageSummary(summary taskTodoStageSummary) string {
	var b strings.Builder
	b.WriteString("staged files:\n")
	if len(summary.Staged) == 0 {
		b.WriteString("  none\n")
	} else {
		for _, file := range summary.Staged {
			fmt.Fprintf(&b, "  %s\n", renderStagedFile(file))
		}
	}
	b.WriteString("unstaged files:\n")
	if len(summary.Unstaged) == 0 {
		b.WriteString("  none\n")
	} else {
		for _, entry := range summary.Unstaged {
			fmt.Fprintf(&b, "  %s\n", renderStatusEntry(entry))
		}
	}
	return b.String()
}

func renderStagedFile(file stagedFile) string {
	status := stagedStatusLabel(file.Status)
	if file.Orig != "" {
		return fmt.Sprintf("%s %s -> %s", status, file.Orig, file.Path)
	}
	return fmt.Sprintf("%s %s", status, file.Path)
}

func stagedStatusLabel(status string) string {
	if strings.HasPrefix(status, "R") {
		return "R"
	}
	if strings.HasPrefix(status, "C") {
		return "C"
	}
	if status == "" {
		return "?"
	}
	return status[:1]
}

func renderStatusEntry(entry gitx.StatusEntry) string {
	if entry.Orig != "" {
		return fmt.Sprintf("%s %s -> %s", entry.Code, entry.Orig, entry.Path)
	}
	return fmt.Sprintf("%s %s", entry.Code, entry.Path)
}

func taskTodoJSONItems(items []taskTodoItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for i, item := range items {
		out = append(out, taskTodoJSONItem(i+1, item))
	}
	return out
}

func stagedFileJSONItems(files []stagedFile) []map[string]any {
	out := make([]map[string]any, 0, len(files))
	for _, file := range files {
		item := map[string]any{"status": stagedStatusLabel(file.Status), "path": file.Path}
		if file.Orig != "" {
			item["orig"] = file.Orig
		}
		out = append(out, item)
	}
	return out
}

func statusEntryJSONItems(entries []gitx.StatusEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		item := map[string]any{"status": entry.Code, "path": entry.Path}
		if entry.Orig != "" {
			item["orig"] = entry.Orig
		}
		out = append(out, item)
	}
	return out
}

func taskTodoJSONItem(index int, item taskTodoItem) map[string]any {
	value := map[string]any{
		"index":      index,
		"status":     item.Status,
		"work_type":  taskTodoWorkType(item),
		"title":      item.Title,
		"acceptance": item.Acceptance,
	}
	if taskTodoWorkType(item) == "commit" {
		value["type"] = item.Type
		value["commit_message"] = item.CommitMessage
	}
	if item.CommitSHA != "" {
		value["commit_sha"] = item.CommitSHA
	}
	if item.CommitSubject != "" {
		value["commit_subject"] = item.CommitSubject
	}
	if item.CancelledAt != "" {
		value["cancelled_at"] = item.CancelledAt
	}
	return value
}

func taskTodoStatusMarker(status string) string {
	switch status {
	case "active":
		return ">"
	case "done":
		return "x"
	case "cancelled":
		return "c"
	default:
		return " "
	}
}

func commitMergedTask(ctx context.Context, runner gitx.Runner, subject string) (taskMergeCommit, error) {
	dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return taskMergeCommit{}, err
	}
	if dirty.Clean {
		return taskMergeCommit{}, errors.New("no merged task changes to commit")
	}
	pathspecs := dirtyPathspecs(dirty.Dirty)
	if len(pathspecs) == 0 {
		return taskMergeCommit{}, errors.New("no merged task changes to commit")
	}
	addArgs := append([]string{"add", "-A", "--"}, pathspecs...)
	if _, err := runner.Run(ctx, addArgs...); err != nil {
		return taskMergeCommit{}, err
	}
	unstageRuntimePaths(ctx, runner)
	conflicts := unmergedPaths(ctx, runner)
	if len(conflicts) > 0 {
		return taskMergeCommit{}, fmt.Errorf("unresolved conflicts remain: %s", strings.Join(conflicts, ", "))
	}
	if _, err := runner.Run(ctx, "commit", "-m", subject); err != nil {
		return taskMergeCommit{}, err
	}
	sha, err := runner.Head(ctx)
	if err != nil {
		return taskMergeCommit{}, err
	}
	return taskMergeCommit{SHA: strings.TrimSpace(sha), Subject: subject}, nil
}

func buildTaskMergeRecord(mergeCtx taskMergeContext, taskCommits []taskMergeCommit, mergeCommit taskMergeCommit) taskMergeRecord {
	return taskMergeRecord{
		SchemaVersion:   1,
		TaskID:          mergeCtx.TaskID,
		Status:          "merged",
		Branch:          mergeCtx.TaskBranch,
		IterationBranch: mergeCtx.IterationBranch,
		TaskCommits:     append([]taskMergeCommit(nil), taskCommits...),
		MergeCommit:     mergeCommit,
		MergedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

func printTaskMergeResult(g globals, record taskMergeRecord) error {
	return printResult(g, map[string]any{"task_merge": record}, fmt.Sprintf("Merged task %s into %s\nTask commits: %d\nMerge commit: %s %s\n", record.TaskID, record.IterationBranch, len(record.TaskCommits), shortSHA(record.MergeCommit.SHA), record.MergeCommit.Subject))
}

func readTaskAuditFile(taskDir, taskID string) (workflow.Task, error) {
	data, err := os.ReadFile(filepath.Join(taskDir, "task.json"))
	if err != nil {
		return workflow.Task{}, err
	}
	var task workflow.Task
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&task); err != nil {
		return workflow.Task{}, err
	}
	if task.ID != taskID {
		return workflow.Task{}, fmt.Errorf("task audit id %q does not match %q", task.ID, taskID)
	}
	return task, nil
}

func readTaskMergeAudit(taskDir, taskID string) (taskMergeRecord, error) {
	data, err := os.ReadFile(filepath.Join(taskDir, "task-merge.json"))
	if err != nil {
		return taskMergeRecord{}, err
	}
	var record taskMergeRecord
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&record); err != nil {
		return taskMergeRecord{}, err
	}
	if record.SchemaVersion != 1 {
		return taskMergeRecord{}, errors.New("task merge schema_version must be 1")
	}
	if record.TaskID != taskID {
		return taskMergeRecord{}, fmt.Errorf("task merge task_id %q does not match %q", record.TaskID, taskID)
	}
	if record.Status != "merged" {
		return taskMergeRecord{}, fmt.Errorf("task merge status is %q", record.Status)
	}
	if len(record.TaskCommits) == 0 {
		return taskMergeRecord{}, errors.New("task merge task_commits must have at least one item")
	}
	for i, commit := range record.TaskCommits {
		if strings.TrimSpace(commit.SHA) == "" || strings.TrimSpace(commit.Subject) == "" {
			return taskMergeRecord{}, fmt.Errorf("task_commits[%d] requires sha and subject", i)
		}
	}
	return record, nil
}

func writeTaskMergeAudit(taskDir string, record taskMergeRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(taskDir, "task-merge.json"), data, 0o644)
}

func acquireTaskMergeLock(ctx context.Context, mergeCtx taskMergeContext) (string, error) {
	lockDir := taskMergeLockDir(mergeCtx)
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o755); err != nil {
		return "", err
	}
	for {
		err := os.Mkdir(lockDir, 0o755)
		if err == nil {
			return lockDir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func taskMergeLockDir(mergeCtx taskMergeContext) string {
	name := sanitizeTempPart(mergeCtx.RunID) + "-" + sanitizeTempPart(mergeCtx.IterationID) + "-task-merge.lock"
	return filepath.Join(mergeCtx.StorageRoot, "locks", name)
}

func writeTaskMergeLock(lockDir string, lock taskMergeLock) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(lockDir, "state.json"), data, 0o644)
}

func readTaskMergeLock(lockDir string) (taskMergeLock, error) {
	data, err := os.ReadFile(filepath.Join(lockDir, "state.json"))
	if err != nil {
		return taskMergeLock{}, err
	}
	var lock taskMergeLock
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lock); err != nil {
		return taskMergeLock{}, err
	}
	return lock, nil
}

func unmergedPaths(ctx context.Context, runner gitx.Runner) []string {
	out, err := runner.Run(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if path := strings.TrimSpace(line); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func appendTaskMergeEvent(mergeCtx taskMergeContext, event runstate.Event) {
	if mergeCtx.TaskDir == "" {
		return
	}
	_ = runstate.AppendEvent(filepath.Join(mergeCtx.TaskDir, "agent-events.jsonl"), event)
}

func cleanupPendingTaskMerge(ctx context.Context, paths pathSet) {
	root := storageRootFromIterationDir(paths.IterationDir)
	ownsLock := true
	if root != "" && paths.RunID != "" && paths.IterationID != "" {
		lockDir := filepath.Join(root, "locks", sanitizeTempPart(paths.RunID)+"-"+sanitizeTempPart(paths.IterationID)+"-task-merge.lock")
		if lock, err := readTaskMergeLock(lockDir); err == nil {
			ownsLock = lock.TaskID == "" || lock.TaskID == paths.TaskID
			if ownsLock && lock.Branch != "" && paths.CurrentBranch != "" {
				ownsLock = lock.Branch == paths.CurrentBranch
			}
		}
		if ownsLock {
			_ = os.RemoveAll(lockDir)
		}
	}
	if strings.TrimSpace(paths.IterationWorktree) == "" {
		return
	}
	if !ownsLock {
		return
	}
	runner := gitx.Runner{Dir: paths.IterationWorktree}
	if len(unmergedPaths(ctx, runner)) == 0 {
		dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
		if err != nil || dirty.Clean {
			return
		}
	}
	_, _ = runner.Run(context.Background(), "reset", "--hard")
	_, _ = runGitCleanPreservingLoopRuntime(context.Background(), runner)
	if paths.TaskDir != "" {
		_ = runstate.AppendEvent(filepath.Join(paths.TaskDir, "agent-events.jsonl"), runstate.Event{"type": "task.merge.cleanup", "task_id": paths.TaskID, "iteration_worktree": paths.IterationWorktree})
	}
}

func storageRootFromIterationDir(iterationDir string) string {
	globalPath := artifactdb.GlobalDBPathForIteration(iterationDir)
	if globalPath == "" {
		return ""
	}
	return filepath.Dir(globalPath)
}

func iterationNumberFromID(iterationID string) int {
	n, _ := strconv.Atoi(strings.TrimLeft(strings.TrimSpace(iterationID), "0"))
	if n <= 0 {
		return 1
	}
	return n
}
