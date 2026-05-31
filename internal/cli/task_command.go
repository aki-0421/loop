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
	RepoRoot          string
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
	Type          string   `json:"type"`
	Title         string   `json:"title"`
	Acceptance    []string `json:"acceptance"`
	CommitMessage string   `json:"commit_message"`
	CommitSHA     string   `json:"commit_sha,omitempty"`
	CommitSubject string   `json:"commit_subject,omitempty"`
	StartedAt     string   `json:"started_at,omitempty"`
	CompletedAt   string   `json:"completed_at,omitempty"`
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
		return codedError{2, fmt.Errorf("usage: loop task todo <add|list|move|start|complete> ...")}
	}
	switch args[0] {
	case "add":
		return commandTaskTodoAdd(ctx, g, args[1:])
	case "list":
		return commandTaskTodoList(ctx, g, args[1:])
	case "move":
		return commandTaskTodoMove(ctx, g, args[1:])
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
	kind := fs.String("type", "", "commit type")
	title := fs.String("title", "", "TODO title")
	after := fs.Int("after", -1, "insert after 1-based TODO index; 0 inserts at the top")
	var acceptance stringListFlag
	fs.Var(&acceptance, "acceptance", "acceptance criterion; repeatable")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true, "dir": true, "run": true, "iteration": true,
		"task": true, "type": true, "title": true, "after": true, "acceptance": true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if strings.TrimSpace(*kind) == "" || strings.TrimSpace(*title) == "" || len(acceptance) == 0 || fs.NArg() < 1 {
		return codedError{2, fmt.Errorf("usage: loop task todo add --type <type> --title <title> --acceptance <text>... [--after <n>] <commit-message>")}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	if err := requireCleanTaskWorktree(ctx, mergeCtx.TaskWorktree, "add task TODOs before editing"); err != nil {
		return codedError{1, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	if taskTodoWorkStarted(todos.Items) {
		return codedError{2, errors.New("cannot add task TODOs after task work has started")}
	}
	insertAfter := *after
	if insertAfter == -1 {
		insertAfter = len(todos.Items)
	}
	if insertAfter < 0 || insertAfter > len(todos.Items) {
		return codedError{2, fmt.Errorf("--after index %d is out of range", *after)}
	}
	subject, err := buildLoopCommitSubject(*kind, strings.Join(fs.Args(), " "), loopCommitMessageMaxLength)
	if err != nil {
		return codedError{2, err}
	}
	prefix, _, _ := strings.Cut(subject, ": ")
	item := taskTodoItem{
		Status:        "pending",
		Type:          prefix,
		Title:         strings.TrimSpace(*title),
		Acceptance:    append([]string(nil), acceptance...),
		CommitMessage: strings.Join(fs.Args(), " "),
	}
	todos.Items = append(todos.Items[:insertAfter], append([]taskTodoItem{item}, todos.Items[insertAfter:]...)...)
	if err := writeTaskTodoFile(mergeCtx, todos); err != nil {
		return codedError{1, err}
	}
	index := insertAfter + 1
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.todo.added", "task_id": mergeCtx.TaskID, "index": index, "title": item.Title, "commit_subject": subject})
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
	if err := requireCleanTaskWorktree(ctx, mergeCtx.TaskWorktree, "move task TODOs before editing"); err != nil {
		return codedError{1, err}
	}
	todos, err := readTaskTodoFile(mergeCtx)
	if err != nil {
		return codedError{1, err}
	}
	if taskTodoWorkStarted(todos.Items) {
		return codedError{2, errors.New("cannot move task TODOs after task work has started")}
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

func commandTaskTodoStart(ctx context.Context, g globals, args []string) error {
	return updateTaskTodoStatus(ctx, g, args, "start")
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
	return printResult(g, map[string]any{"task": mergeCtx.TaskID, "index": index, "commit": commit, "todo": taskTodoJSONItem(index, todos.Items[index-1])}, fmt.Sprintf("completed task todo %d: %s %s\n", index, shortSHA(commit.SHA), commit.Subject))
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
	root, err := loopStorageRoot(ctx)
	if err != nil {
		root = repoRootFromIterationDir(resolvedDir)
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
	if iterationWorktree == "" && root != "" {
		iterationWorktree = filepath.Join(root, ".loop", "worktrees", run, iter, "iteration")
	}
	if taskWorktree == "" || taskBranch == "" || iterationBranch == "" || iterationWorktree == "" || root == "" {
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
		RepoRoot:          root,
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
	if _, err := iterRunner.Run(ctx, "merge", "--squash", mergeCtx.TaskBranch); err != nil {
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
	mergeCommit, err := commitMergedTask(ctx, iterRunner, subject)
	if err != nil {
		return codedError{1, err}
	}
	record := buildTaskMergeRecord(mergeCtx, taskCommits, mergeCommit)
	if err := writeTaskMergeAudit(mergeCtx.TaskDir, record); err != nil {
		return codedError{1, err}
	}
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.merge.completed", "task_id": mergeCtx.TaskID, "branch": mergeCtx.TaskBranch, "merge_commit": mergeCommit.SHA})
	return printTaskMergeResult(g, record)
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
	}
	return todos, nil
}

func writeTaskTodoFile(mergeCtx taskMergeContext, todos taskTodoFile) error {
	todos.SchemaVersion = 1
	todos.TaskID = mergeCtx.TaskID
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
		return fmt.Errorf("todo %d status must be pending, active, or done", index)
	}
	if _, err := normalizeLoopCommitType(item.Type); err != nil {
		return fmt.Errorf("todo %d %w", index, err)
	}
	if strings.TrimSpace(item.Title) == "" {
		return fmt.Errorf("todo %d title is required", index)
	}
	if len(item.Acceptance) == 0 {
		return fmt.Errorf("todo %d acceptance must have at least one item", index)
	}
	if strings.TrimSpace(item.CommitMessage) == "" {
		return fmt.Errorf("todo %d commit_message is required", index)
	}
	if item.Status == "done" {
		if strings.TrimSpace(item.CommitSHA) == "" || strings.TrimSpace(item.CommitSubject) == "" {
			return fmt.Errorf("todo %d done item requires commit_sha and commit_subject", index)
		}
		if err := validateLoopCommitSubject(item.CommitSubject, loopCommitMessageMaxLength); err != nil {
			return fmt.Errorf("todo %d commit_subject is invalid: %w", index, err)
		}
	}
	return nil
}

func validTaskTodoStatus(status string) bool {
	switch status {
	case "pending", "active", "done":
		return true
	default:
		return false
	}
}

func taskTodoWorkStarted(items []taskTodoItem) bool {
	for _, item := range items {
		if item.Status != "pending" {
			return true
		}
	}
	return false
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
		case i < index-1 && todos.Items[i].Status != "done":
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

func completeTaskTodoItem(ctx context.Context, mergeCtx taskMergeContext, todos *taskTodoFile, index int) (taskMergeCommit, error) {
	if len(todos.Items) == 0 {
		return taskMergeCommit{}, errors.New("task TODO list is empty")
	}
	for i := range todos.Items {
		if i < index-1 && todos.Items[i].Status != "done" {
			return taskMergeCommit{}, fmt.Errorf("todo %d must be completed before todo %d can complete", i+1, index)
		}
	}
	item := todos.Items[index-1]
	if item.Status != "active" {
		return taskMergeCommit{}, fmt.Errorf("todo %d status is %s, not active", index, item.Status)
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

func commitTaskTodoChanges(ctx context.Context, workDir, subject string) (taskMergeCommit, error) {
	runner := gitx.Runner{Dir: workDir}
	dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return taskMergeCommit{}, err
	}
	if dirty.Clean {
		return taskMergeCommit{}, errors.New("no repository changes to commit for active task TODO")
	}
	pathspecs := dirtyPathspecs(dirty.Dirty)
	if len(pathspecs) == 0 {
		return taskMergeCommit{}, errors.New("no repository changes to commit for active task TODO")
	}
	addArgs := append([]string{"add", "-A", "--"}, pathspecs...)
	if _, err := runner.Run(ctx, addArgs...); err != nil {
		return taskMergeCommit{}, err
	}
	unstageRuntimePaths(ctx, runner)
	if _, err := runner.Run(ctx, "commit", "-m", subject); err != nil {
		return taskMergeCommit{}, err
	}
	sha, err := runner.Head(ctx)
	if err != nil {
		return taskMergeCommit{}, err
	}
	return taskMergeCommit{SHA: strings.TrimSpace(sha), Subject: subject}, nil
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
		if item.Status != "done" {
			return nil, fmt.Errorf("task TODO %d is %s, not done", i+1, item.Status)
		}
		commits = append(commits, taskMergeCommit{SHA: item.CommitSHA, Subject: item.CommitSubject})
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
		subject, _ := buildLoopCommitSubject(item.Type, item.CommitMessage, loopCommitMessageMaxLength)
		fmt.Fprintf(&b, "%d. [%s] %s - %s\n", i+1, taskTodoStatusMarker(item.Status), item.Title, subject)
	}
	return b.String()
}

func taskTodoJSONItems(items []taskTodoItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for i, item := range items {
		out = append(out, taskTodoJSONItem(i+1, item))
	}
	return out
}

func taskTodoJSONItem(index int, item taskTodoItem) map[string]any {
	value := map[string]any{
		"index":          index,
		"status":         item.Status,
		"type":           item.Type,
		"title":          item.Title,
		"acceptance":     item.Acceptance,
		"commit_message": item.CommitMessage,
	}
	if item.CommitSHA != "" {
		value["commit_sha"] = item.CommitSHA
	}
	if item.CommitSubject != "" {
		value["commit_subject"] = item.CommitSubject
	}
	return value
}

func taskTodoStatusMarker(status string) string {
	switch status {
	case "active":
		return ">"
	case "done":
		return "x"
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
	return filepath.Join(mergeCtx.RepoRoot, ".loop", "locks", name)
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
	root := repoRootFromIterationDir(paths.IterationDir)
	ownsLock := true
	if root != "" && paths.RunID != "" && paths.IterationID != "" {
		lockDir := filepath.Join(root, ".loop", "locks", sanitizeTempPart(paths.RunID)+"-"+sanitizeTempPart(paths.IterationID)+"-task-merge.lock")
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

func repoRootFromIterationDir(iterationDir string) string {
	clean := filepath.Clean(iterationDir)
	sep := string(os.PathSeparator)
	marker := sep + ".loop" + sep + "runs" + sep
	if idx := strings.Index(clean, marker); idx >= 0 {
		return clean[:idx]
	}
	relativeMarker := ".loop" + sep + "runs" + sep
	if strings.HasPrefix(clean, relativeMarker) {
		return "."
	}
	return ""
}

func iterationNumberFromID(iterationID string) int {
	n, _ := strconv.Atoi(strings.TrimLeft(strings.TrimSpace(iterationID), "0"))
	if n <= 0 {
		return 1
	}
	return n
}
