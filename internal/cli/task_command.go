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
	SchemaVersion   int             `json:"schema_version"`
	TaskID          string          `json:"task_id"`
	Status          string          `json:"status"`
	Branch          string          `json:"branch"`
	IterationBranch string          `json:"iteration_branch"`
	TaskCommit      taskMergeCommit `json:"task_commit"`
	MergeCommit     taskMergeCommit `json:"merge_commit"`
	MergedAt        string          `json:"merged_at"`
}

type taskMergeLock struct {
	SchemaVersion     int             `json:"schema_version"`
	RunID             string          `json:"run_id"`
	IterationID       string          `json:"iteration_id"`
	TaskID            string          `json:"task_id"`
	Branch            string          `json:"branch"`
	IterationBranch   string          `json:"iteration_branch"`
	IterationWorktree string          `json:"iteration_worktree"`
	TaskCommit        taskMergeCommit `json:"task_commit"`
	Subject           string          `json:"subject"`
	Status            string          `json:"status"`
	ConflictPaths     []string        `json:"conflict_paths,omitempty"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

func commandTask(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop task <merge> ...")}
	}
	switch args[0] {
	case "merge":
		return commandTaskMerge(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown task subcommand %q", args[0])}
	}
}

func commandTaskMerge(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("task merge", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id")
	continueMerge := fs.Bool("continue", false, "commit a conflict resolution for a pending task merge")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop task merge [--continue] [--task <id>] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	mergeCtx, err := resolveTaskMergeContext(ctx, g, *iterDir, *runID, *iteration, *taskID)
	if err != nil {
		return codedError{2, err}
	}
	if *continueMerge {
		return continueTaskMerge(ctx, g, mergeCtx)
	}
	return startTaskMerge(ctx, g, mergeCtx)
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
	iterationBranch := firstNonEmpty(runtimeString(runtime, "initial_branch"), os.Getenv("LOOP_INITIAL_BRANCH"))
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

func startTaskMerge(ctx context.Context, g globals, mergeCtx taskMergeContext) error {
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
	taskCommit, err := ensureTaskCommit(ctx, mergeCtx)
	if err != nil {
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
		TaskCommit:        taskCommit,
		Subject:           taskCommit.Subject,
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
	mergeCommit, err := commitMergedTask(ctx, iterRunner, taskCommit.Subject)
	if err != nil {
		return codedError{1, err}
	}
	record := buildTaskMergeRecord(mergeCtx, taskCommit, mergeCommit)
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
	subject := firstNonEmpty(lock.Subject, lock.TaskCommit.Subject)
	if subject == "" {
		return codedError{1, errors.New("pending task merge is missing its commit subject")}
	}
	mergeCommit, err := commitMergedTask(ctx, iterRunner, subject)
	if err != nil {
		return codedError{1, err}
	}
	taskCommit := lock.TaskCommit
	record := buildTaskMergeRecord(mergeCtx, taskCommit, mergeCommit)
	if err := writeTaskMergeAudit(mergeCtx.TaskDir, record); err != nil {
		return codedError{1, err}
	}
	_ = os.RemoveAll(lockDir)
	appendTaskMergeEvent(mergeCtx, runstate.Event{"type": "task.merge.completed", "task_id": mergeCtx.TaskID, "branch": mergeCtx.TaskBranch, "merge_commit": mergeCommit.SHA})
	return printTaskMergeResult(g, record)
}

func ensureTaskCommit(ctx context.Context, mergeCtx taskMergeContext) (taskMergeCommit, error) {
	commit, err := commitTaskChanges(ctx, mergeCtx.TaskWorktree, mergeCtx.Task)
	if err != nil {
		return taskMergeCommit{}, err
	}
	if commit.Hash != "" {
		return taskMergeCommit{SHA: commit.Hash, Subject: commit.Subject}, nil
	}
	commits, err := (gitx.Runner{Dir: mergeCtx.TaskWorktree}).ListCommits(ctx, mergeCtx.IterationBranch, "HEAD")
	if err != nil {
		return taskMergeCommit{}, err
	}
	if len(commits) == 0 {
		return taskMergeCommit{}, fmt.Errorf("task %s completed without repository changes", mergeCtx.TaskID)
	}
	latest := commits[0]
	if err := validateLoopCommitSubject(latest.Subject, loopCommitMessageMaxLength); err != nil {
		return taskMergeCommit{}, err
	}
	return taskMergeCommit{SHA: latest.Hash, Subject: latest.Subject}, nil
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

func buildTaskMergeRecord(mergeCtx taskMergeContext, taskCommit, mergeCommit taskMergeCommit) taskMergeRecord {
	return taskMergeRecord{
		SchemaVersion:   1,
		TaskID:          mergeCtx.TaskID,
		Status:          "merged",
		Branch:          mergeCtx.TaskBranch,
		IterationBranch: mergeCtx.IterationBranch,
		TaskCommit:      taskCommit,
		MergeCommit:     mergeCommit,
		MergedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

func printTaskMergeResult(g globals, record taskMergeRecord) error {
	return printResult(g, map[string]any{"task_merge": record}, fmt.Sprintf("Merged task %s into %s\nTask commit: %s %s\nMerge commit: %s %s\n", record.TaskID, record.IterationBranch, shortSHA(record.TaskCommit.SHA), record.TaskCommit.Subject, shortSHA(record.MergeCommit.SHA), record.MergeCommit.Subject))
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
