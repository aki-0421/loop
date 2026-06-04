package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aki-0421/loop/internal/agent"
	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/prompt"
	"github.com/aki-0421/loop/internal/runstate"
	"github.com/aki-0421/loop/internal/skills"
	"github.com/aki-0421/loop/internal/validation"
	"github.com/aki-0421/loop/internal/workflow"
)

type taskExecutionResult struct {
	Task          workflow.Task
	Result        workflow.TaskResult
	Branch        string
	Commits       []gitx.Commit
	Attempt       int
	ActiveDir     string
	Worktree      string
	Discarded     bool
	DiscardReason string
	Err           error
}

type taskSetExecution struct {
	Results []workflow.TaskResult
	Commits []gitx.Commit
	Discard *discardedTask
}

type discardedTask struct {
	Task   workflow.Task
	Result workflow.TaskResult
	Reason string
}

type iterationWorkflowResult struct {
	Summary        string
	GoalComplete   bool
	GoalEvaluation string
	Commits        []gitx.Commit
	Integrated     bool
}

func commandRun(ctx context.Context, g globals, args []string) error {
	args = flagsFirst(args, map[string]bool{
		"agent": true, "goal": true, "max-iterations": true, "base": true,
		"resume": true, "from-iteration": true, "keep-branches": true, "keep-worktrees": true,
		"human-review": true, "review-mode": true,
	})
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentName := fs.String("agent", g.Agent, "agent adapter")
	goal := fs.String("goal", "", "natural-language stop condition")
	maxIterations := fs.Int("max-iterations", 0, "maximum iterations, 0 for unlimited")
	prFlag := fs.Bool("pr", false, "use pull request integration")
	base := fs.String("base", "", "base branch")
	humanReview := fs.Bool("human-review", false, "open pull requests and pause for external post-hoc review before merge")
	reviewMode := fs.String("review-mode", "", "PR review mode: auto_merge, parallel_human_review, or serial_human_review")
	resumeID := fs.String("resume", "", "accepted for older scripts; use loop resume")
	fromIteration := fs.Int("from-iteration", 0, "accepted for older scripts; currently ignored")
	keepBranches := fs.String("keep-branches", "", "accepted for older scripts; cleanup is automatic")
	keepWorktrees := fs.String("keep-worktrees", "", "accepted for older scripts; cleanup is automatic")
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	_ = resumeID
	_ = fromIteration
	_ = keepBranches
	_ = keepWorktrees
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop run <instruction.md> [flags]")}
	}

	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, fmt.Errorf("not inside a git repository: %w", err)}
	}
	instructionPath := fs.Arg(0)
	if !filepath.IsAbs(instructionPath) {
		instructionPath = filepath.Join(root, instructionPath)
	}
	if _, err := os.Stat(instructionPath); err != nil {
		return codedError{2, fmt.Errorf("invalid instruction file: %w", err)}
	}

	overrides := config.Overrides{Agent: *agentName, BaseBranch: *base, NoColor: g.NoColor}
	humanReviewSet := false
	reviewModeSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-iterations":
			overrides.MaxIterations = maxIterations
		case "human-review":
			humanReviewSet = true
			v := *humanReview
			overrides.HumanReview = &v
		case "review-mode":
			reviewModeSet = true
			overrides.ReviewMode = *reviewMode
		}
	})
	if humanReviewSet && !reviewModeSet {
		overrides.ReviewMode = config.ReviewModeSerialHumanReview
	}
	if *prFlag {
		v := true
		overrides.PRMode = &v
	}
	cfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: overrides})
	if err != nil {
		return codedError{3, err}
	}

	runner := gitx.Runner{Dir: root}
	startBranch, err := runner.CurrentBranch(ctx)
	if err != nil {
		return codedError{1, err}
	}
	if strings.TrimSpace(cfg.Git.BaseBranch) == "" {
		cfg.Git.BaseBranch = startBranch
	}
	mainBranch, _ := runner.MainBranch(ctx)
	clean, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return codedError{1, err}
	}
	if !clean.Clean {
		return codedError{1, fmt.Errorf("working tree is dirty: %s", dirtyList(clean.Dirty))}
	}
	if cfg.Skills.SyncOnRun {
		if _, err := skills.Sync(root, cfg); err != nil {
			return codedError{1, err}
		}
	}

	runID, err := runstate.NewRunID(time.Now())
	if err != nil {
		return codedError{1, err}
	}
	runDir := filepath.Join(root, cfg.Logs.Dir, runID)
	statePath := filepath.Join(runDir, "run-state.json")
	state := runstate.New(runID, *goal, cfg.Git.BaseBranch, cfg.Agent.Default)
	renderer := newRunRenderer(g, runID, cfg.Agent.Default, filepath.Base(root), "", cfg.Git.BaseBranch, *goal, rel(root, runDir), cfg.Run.MaxIterations)
	renderer.Start(ctx)
	registerRunGracefulShutdownCallback(ctx, renderer.GracefulShutdownRequested)
	defer func() { renderer.Stop(state.Stage, "") }()
	if err := runstate.Write(statePath, state); err != nil {
		return codedError{1, err}
	}
	if shouldConfirmTargetBranch(cfg.Git.BaseBranch, mainBranch) {
		if err := renderer.ConfirmTargetBranch(runGracefulContext(ctx), cfg.Git.BaseBranch, mainBranch, targetBranchConfirmationDuration); err != nil {
			state.Stage = runstate.StageCancelled
			_ = runstate.Write(statePath, state)
			if runGracefulShutdownRequested(ctx) {
				return codedError{interruptExitCode, nil}
			}
			return codedError{1, err}
		}
	}
	if err := syncMemoryBeforeRun(ctx, root, cfg, renderer); err != nil {
		state.Stage = runstate.StageFailed
		_ = runstate.Write(statePath, state)
		return codedError{1, err}
	}

	var last iterationWorkflowResult
	mergedCount := 0
	gracefulStop := false
	for i := 1; cfg.Run.MaxIterations == 0 || i <= cfg.Run.MaxIterations; i++ {
		if runGracefulShutdownRequested(ctx) {
			gracefulStop = true
			break
		}
		refreshPendingPullRequests(ctx, root, cfg, &state, renderer)
		_ = runstate.Write(statePath, state)
		result, err := runOrchestratedIteration(ctx, orchestrationRequest{
			Globals:         g,
			Config:          cfg,
			Root:            root,
			InstructionPath: instructionPath,
			RunID:           runID,
			IterationNumber: i,
			Goal:            *goal,
			RunDir:          runDir,
			StatePath:       statePath,
			State:           &state,
			Renderer:        renderer,
		})
		if err != nil {
			state.Stage = runstate.StageFailed
			_ = runstate.Write(statePath, state)
			return err
		}
		last = result
		if result.Integrated {
			mergedCount++
			renderer.Merged(mergedCount)
		}
		if result.GoalComplete {
			state.Stage = runstate.StageCompleted
			_ = runstate.Write(statePath, state)
			break
		}
		if runGracefulShutdownRequested(ctx) {
			gracefulStop = true
			break
		}
	}
	if gracefulStop {
		state.Stage = runstate.StageCancelled
		_ = runstate.Write(statePath, state)
	} else if state.Stage != runstate.StageCompleted {
		state.Stage = runstate.StageCompleted
		_ = runstate.Write(statePath, state)
	}
	if err := printResult(g, map[string]any{"run_id": runID, "status": state.Stage, "summary": last.Summary, "logs": rel(root, runDir)}, fmt.Sprintf("Run: %s\nStatus: %s\nSummary: %s\nLogs: %s\n", runID, state.Stage, last.Summary, rel(root, runDir))); err != nil {
		return err
	}
	if gracefulStop {
		return codedError{interruptExitCode, nil}
	}
	return nil
}

type orchestrationRequest struct {
	Globals         globals
	Config          config.Config
	Root            string
	InstructionPath string
	RunID           string
	IterationNumber int
	Goal            string
	RunDir          string
	StatePath       string
	State           *runstate.State
	Renderer        *runRenderer
}

func resumeRun(ctx context.Context, g globals, runID string, fromIteration int) error {
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, fmt.Errorf("not inside a git repository: %w", err)}
	}
	baseCfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{NoColor: g.NoColor}})
	if err != nil {
		return codedError{3, err}
	}
	runDir := filepath.Join(root, baseCfg.Logs.Dir, runID)
	statePath := filepath.Join(runDir, "run-state.json")
	state, err := runstate.Read(statePath)
	if err != nil {
		return codedError{1, err}
	}
	if state.Stage == runstate.StageCompleted {
		return commandStatus(ctx, g, []string{runID})
	}
	iterationID := state.CurrentIteration
	if fromIteration > 0 {
		iterationID = runstate.IterationID(fromIteration)
	}
	if strings.TrimSpace(iterationID) == "" {
		return codedError{2, errors.New("run has no current iteration to resume")}
	}
	if iterationID != state.CurrentIteration {
		return codedError{2, fmt.Errorf("resume from iteration %s is not supported for run currently at %s", iterationID, state.CurrentIteration)}
	}
	iterationNumber, err := parseIterationNumber(iterationID)
	if err != nil {
		return codedError{2, err}
	}
	iterDir := filepath.Join(runDir, "iterations", iterationID)
	cfg := baseCfg
	if _, err := os.Stat(filepath.Join(iterDir, "effective-config.yaml")); err == nil {
		cfg, err = config.Load(config.LoadOptions{CWD: root, ConfigPath: filepath.Join(iterDir, "effective-config.yaml"), Env: []string{}, Overrides: config.Overrides{NoColor: g.NoColor}})
		if err != nil {
			return codedError{3, err}
		}
	}
	if strings.TrimSpace(cfg.Git.BaseBranch) == "" {
		cfg.Git.BaseBranch = state.BaseBranch
	}
	instructionPath := filepath.Join(iterDir, "prompt.md")
	if _, err := os.Stat(instructionPath); err != nil {
		return codedError{1, fmt.Errorf("resume instruction artifact: %w", err)}
	}
	renderer := newRunRenderer(g, runID, cfg.Agent.Default, filepath.Base(root), "", cfg.Git.BaseBranch, state.Goal, rel(root, runDir), cfg.Run.MaxIterations)
	renderer.Start(ctx)
	registerRunGracefulShutdownCallback(ctx, renderer.GracefulShutdownRequested)
	defer func() { renderer.Stop(state.Stage, "") }()

	result, err := resumeOrchestratedIteration(ctx, orchestrationRequest{
		Globals:         g,
		Config:          cfg,
		Root:            root,
		InstructionPath: instructionPath,
		RunID:           runID,
		IterationNumber: iterationNumber,
		Goal:            state.Goal,
		RunDir:          runDir,
		StatePath:       statePath,
		State:           &state,
		Renderer:        renderer,
	})
	if err != nil {
		state.Stage = runstate.StageFailed
		_ = runstate.Write(statePath, state)
		return err
	}
	if result.Integrated {
		renderer.Merged(1)
	}
	if runGracefulShutdownRequested(ctx) {
		state.Stage = runstate.StageCancelled
		_ = runstate.Write(statePath, state)
		return codedError{interruptExitCode, nil}
	}
	if result.GoalComplete || state.Stage != runstate.StageCompleted {
		state.Stage = runstate.StageCompleted
		_ = runstate.Write(statePath, state)
	}
	return printResult(g, map[string]any{"run_id": runID, "status": state.Stage, "summary": result.Summary, "logs": rel(root, runDir)}, fmt.Sprintf("Run: %s\nStatus: %s\nSummary: %s\nLogs: %s\n", runID, state.Stage, result.Summary, rel(root, runDir)))
}

func parseIterationNumber(iterationID string) (int, error) {
	n, err := strconv.Atoi(strings.TrimLeft(strings.TrimSpace(iterationID), "0"))
	if err != nil {
		return 0, fmt.Errorf("invalid iteration id %q", iterationID)
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid iteration id %q", iterationID)
	}
	return n, nil
}

func resumeOrchestratedIteration(ctx context.Context, req orchestrationRequest) (result iterationWorkflowResult, retErr error) {
	cfg := req.Config
	rootRunner := gitx.Runner{Dir: req.Root}
	iterationID := runstate.IterationID(req.IterationNumber)
	iterDir := filepath.Join(req.RunDir, "iterations", iterationID)
	activeDir, err := createIterationTempDir(req.RunID, iterationID+"-resume")
	if err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	record := iterationRecordByID(req.State, iterationID)
	initialBranch := firstNonEmpty(record.BranchInitial, gitx.InitialBranchName(req.IterationNumber))
	currentBranch := firstNonEmpty(record.BranchCurrent, record.BranchFinal, initialBranch)
	iterationWorktree := filepath.Join(req.Root, ".loop", "worktrees", req.RunID, iterationID, "iteration")
	paths := promptPathsWithActive(iterDir, activeDir)
	paths.Goal = req.Goal
	paths.Language = cfg.Language.Default
	paths.RunID = req.RunID
	paths.IterationID = iterationID
	paths.BaseBranch = cfg.Git.BaseBranch
	paths.InitialBranch = initialBranch
	paths.IterationBranch = currentBranch
	paths.CurrentBranch = currentBranch
	paths.IntegrationMode = cfg.Git.Integration.Mode
	paths.PRReviewMode = cfg.Git.Integration.PR.ReviewMode
	paths.PullRequestMode = cfg.Git.Integration.Mode == "pr"
	paths.RoleOrchestrated = true
	paths.WorkDir = iterationWorktree
	paths.IterationWorktree = iterationWorktree
	paths.PendingPRs = append([]runstate.PendingPullRequest(nil), req.State.PendingPullRequests...)
	cleanup := &iterationCleanup{
		RootRunner:        rootRunner,
		BaseBranch:        cfg.Git.BaseBranch,
		Branch:            currentBranch,
		WorktreePath:      iterationWorktree,
		WorkDir:           iterationWorktree,
		TaskWorktreesRoot: filepath.Join(req.Root, ".loop", "worktrees", req.RunID, iterationID, "tasks"),
		TaskBranchPrefix:  "task/" + iterationID + "-",
		LockDir:           filepath.Join(req.Root, ".loop", "locks", sanitizeTempPart(req.RunID)+"-"+sanitizeTempPart(iterationID)+"-task-merge.lock"),
		ActiveDir:         activeDir,
		Active:            true,
		EventLogPath:      paths.Events,
		OnEvent:           req.Renderer.AgentEvent,
	}
	defer cleanup.OnExit(ctx, req.StatePath, req.State, &retErr)
	req.Renderer.Iteration(iterationID)
	req.Renderer.Branch(currentBranch)
	if err := writeRuntimeArtifact(paths); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	if _, err := os.Stat(iterationWorktree); err != nil {
		return iterationWorkflowResult{}, codedError{1, fmt.Errorf("resume iteration worktree: %w", err)}
	}
	resumeStage := req.State.Stage
	tree, err := readTaskTreeAudit(iterDir)
	if err != nil {
		req.State.Stage = runstate.StagePlanning
		_ = runstate.Write(req.StatePath, *req.State)
		req.Renderer.Stage(runstate.StagePlanning, "planner agent running")
		tree, err = runPlannerRole(ctx, cfg, iterationWorktree, paths, req.Renderer.AgentEvent)
		if err != nil {
			return iterationWorkflowResult{}, codedError{4, err}
		}
		if err := writeTaskTreeAudit(iterDir, tree); err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
	}
	taskDirs := newTaskDirectoryAllocator(iterDir)
	currentTaskDirs, err := taskDirs.Ensure(tree.Tasks)
	if err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	req.Renderer.TasksPlanned(tree.Tasks)
	completedTaskResults, allCommits, pendingTasks := loadResumableTaskState(paths, tree.Tasks, currentTaskDirs)
	if len(pendingTasks) > 0 {
		req.State.Stage = runstate.StageCoding
		_ = runstate.Write(req.StatePath, *req.State)
		req.Renderer.Stage(runstate.StageCoding, "coding tasks")
		pendingTaskDirs, err := taskDirs.Ensure(pendingTasks)
		if err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
		taskSet, err := executeTaskSet(ctx, taskSetRequest{
			Config:            cfg,
			Root:              req.Root,
			IterationWorktree: iterationWorktree,
			IterationBranch:   currentBranch,
			IterationDir:      iterDir,
			Paths:             paths,
			RunID:             req.RunID,
			IterationID:       iterationID,
			Tasks:             filterCompletedDependencies(pendingTasks),
			TaskDirs:          pendingTaskDirs,
			Renderer:          req.Renderer,
		})
		if err != nil {
			return iterationWorkflowResult{}, codedError{4, err}
		}
		if taskSet.Discard != nil {
			return iterationWorkflowResult{}, codedError{4, fmt.Errorf("resume encountered discarded task %s: %s", taskSet.Discard.Task.ID, taskSet.Discard.Reason)}
		}
		completedTaskResults = append(completedTaskResults, taskSet.Results...)
		allCommits = append(allCommits, taskSet.Commits...)
	}
	req.State.Stage = runstate.StageValidating
	_ = runstate.Write(req.StatePath, *req.State)
	req.Renderer.Stage(runstate.StageValidating, "running validation")
	validationResults, validationErr := runConfiguredValidation(ctx, iterationWorktree, paths, cfg.Validation.Commands)
	if validationErr != nil || validation.StatusFromResults(validationResults) == "failed" {
		return iterationWorkflowResult{}, codedError{5, firstNonNil(validationErr, errors.New("required validation failed"))}
	}
	review, reviewErr := readReviewAudit(iterDir)
	if reviewErr != nil || resumeStage == runstate.StageReviewing {
		req.State.Stage = runstate.StageReviewing
		_ = runstate.Write(req.StatePath, *req.State)
		req.Renderer.Stage(runstate.StageReviewing, "review agent running")
		review, err = runReviewRole(ctx, cfg, iterationWorktree, paths, tree, completedTaskResults, validationResults, req.Renderer.AgentEvent)
		if err != nil {
			return iterationWorkflowResult{}, codedError{4, err}
		}
		if err := writeReviewAudit(iterDir, review); err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
	}
	if err := refreshTrackedBranch(ctx, iterationWorktree, &paths); err != nil {
		return iterationWorkflowResult{}, codedError{4, err}
	}
	updateOrchestratedBranchState(req, cleanup, paths.CurrentBranch)
	return finalizeOrchestratedIteration(ctx, req, cleanup, paths, tree, review, allCommits, iterationWorktree, initialBranch)
}

func iterationRecordByID(state *runstate.State, iterationID string) runstate.IterationRecord {
	if state == nil {
		return runstate.IterationRecord{}
	}
	for _, record := range state.Iterations {
		if record.IterationID == iterationID {
			return record
		}
	}
	return runstate.IterationRecord{}
}

func loadResumableTaskState(paths pathSet, tasks []workflow.Task, taskDirs map[string]string) ([]workflow.TaskResult, []gitx.Commit, []workflow.Task) {
	var results []workflow.TaskResult
	var commits []gitx.Commit
	var pending []workflow.Task
	for _, task := range tasks {
		taskPaths := paths
		taskPaths.TaskID = task.ID
		taskPaths.TaskDir = taskDirs[task.ID]
		result, taskCommits, err := readCompletedCodingRoleResult(taskPaths, task.ID, "", 0)
		if err == nil && result.Status == "completed" {
			results = append(results, result)
			commits = append(commits, taskCommits...)
			continue
		}
		pending = append(pending, task)
	}
	return results, commits, pending
}

func filterCompletedDependencies(tasks []workflow.Task) []workflow.Task {
	pending := map[string]bool{}
	for _, task := range tasks {
		pending[task.ID] = true
	}
	out := make([]workflow.Task, 0, len(tasks))
	for _, task := range tasks {
		copyTask := task
		copyTask.DependsOn = filterTaskIDs(copyTask.DependsOn, pending)
		copyTask.ConflictsWith = filterTaskIDs(copyTask.ConflictsWith, pending)
		out = append(out, copyTask)
	}
	return out
}

func filterTaskIDs(ids []string, keep map[string]bool) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if keep[id] {
			out = append(out, id)
		}
	}
	return out
}

func runOrchestratedIteration(ctx context.Context, req orchestrationRequest) (result iterationWorkflowResult, retErr error) {
	cfg := req.Config
	rootRunner := gitx.Runner{Dir: req.Root}
	iterationID := runstate.IterationID(req.IterationNumber)
	iterDir := filepath.Join(req.RunDir, "iterations", iterationID)
	activeDir, err := createIterationTempDir(req.RunID, iterationID)
	if err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	initialBranch := gitx.InitialBranchName(req.IterationNumber)
	iterationWorktree := filepath.Join(req.Root, ".loop", "worktrees", req.RunID, iterationID, "iteration")
	paths := promptPathsWithActive(iterDir, activeDir)
	paths.Goal = req.Goal
	paths.Language = cfg.Language.Default
	paths.RunID = req.RunID
	paths.IterationID = iterationID
	paths.BaseBranch = cfg.Git.BaseBranch
	paths.InitialBranch = initialBranch
	paths.IterationBranch = ""
	paths.CurrentBranch = cfg.Git.BaseBranch
	paths.IntegrationMode = cfg.Git.Integration.Mode
	paths.PRReviewMode = cfg.Git.Integration.PR.ReviewMode
	paths.PullRequestMode = cfg.Git.Integration.Mode == "pr"
	paths.RoleOrchestrated = true
	paths.WorkDir = req.Root
	paths.PendingPRs = append([]runstate.PendingPullRequest(nil), req.State.PendingPullRequests...)
	cleanupRegistered := false
	defer func() {
		if retErr != nil && !cleanupRegistered {
			_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "run.error_cleanup.started", "branch": "", "worktree": ""})
			issues := cleanupDisposableIterationFiles(activeDir, paths.Events)
			event := runstate.Event{"type": "run.error_cleanup.completed", "branch": "", "worktree": ""}
			if len(issues) > 0 {
				appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
				event["issues"] = issues
			}
			_ = runstate.AppendEvent(paths.Events, event)
		}
	}()
	req.Renderer.Iteration(iterationID)
	req.State.CurrentIteration = iterationID
	req.State.Stage = runstate.StagePlanning
	req.State.Iterations = append(req.State.Iterations, runstate.IterationRecord{IterationID: iterationID, BranchInitial: initialBranch, Stage: string(req.State.Stage)})
	_ = runstate.Write(req.StatePath, *req.State)

	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	if err := config.WriteEffective(paths.EffectiveConfig, cfg); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	instructionContent, err := os.ReadFile(req.InstructionPath)
	if err != nil {
		return iterationWorkflowResult{}, codedError{1, fmt.Errorf("read instruction file: %w", err)}
	}
	if err := prompt.WritePrompt(paths.Prompt, instructionContent); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	if err := writeRuntimeArtifact(paths); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	_ = artifactdb.ClearRoleHandoffs(artifactdb.GlobalDBPathForIteration(iterDir), req.RunID, iterationID)

	req.State.Stage = runstate.StagePlanning
	_ = runstate.Write(req.StatePath, *req.State)
	req.Renderer.Stage(runstate.StagePlanning, "planner agent running")
	tree, err := runPlannerRole(ctx, cfg, req.Root, paths, req.Renderer.AgentEvent)
	if err != nil {
		return iterationWorkflowResult{}, codedError{4, err}
	}
	if err := writeTaskTreeAudit(iterDir, tree); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	taskDirs := newTaskDirectoryAllocator(iterDir)
	if _, err := taskDirs.Ensure(tree.Tasks); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	req.Renderer.TasksPlanned(tree.Tasks)
	if len(tree.Tasks) == 0 {
		goalComplete := tree.GoalComplete && strings.TrimSpace(req.Goal) != ""
		req.State.Iterations[len(req.State.Iterations)-1].ShouldFullyStop = goalComplete
		req.State.Iterations[len(req.State.Iterations)-1].SummarySentence = tree.Summary
		_ = runstate.Write(req.StatePath, *req.State)
		if tree.WaitForPendingPRs {
			if cfg.Git.Integration.Mode != "pr" || cfg.Git.Integration.PR.ReviewMode != config.ReviewModeParallelHumanReview {
				return iterationWorkflowResult{}, codedError{4, errors.New("wait_for_pending_prs requires parallel_human_review PR mode")}
			}
			if len(req.State.PendingPullRequests) == 0 {
				return iterationWorkflowResult{}, codedError{4, errors.New("wait_for_pending_prs requires at least one pending pull request")}
			}
			req.State.Iterations[len(req.State.Iterations)-1].ShouldFullyStop = false
			req.State.Stage = runstate.StagePullRequest
			_ = runstate.Write(req.StatePath, *req.State)
			issues := cleanupDisposableIterationFiles(paths.ActiveDir, paths.Events)
			if len(issues) > 0 {
				appendErrorLog(paths.Errors, "pending PR wait cleanup failed: "+strings.Join(issues, "; "))
			}
			if err := waitForPendingPullRequestUpdate(runGracefulContext(ctx), req.Root, cfg, req.State, req.StatePath, paths, req.Renderer); err != nil {
				if runGracefulShutdownRequested(ctx) {
					return iterationWorkflowResult{}, codedError{interruptExitCode, nil}
				}
				return iterationWorkflowResult{}, codedError{6, err}
			}
			return iterationWorkflowResult{Summary: tree.Summary, GoalComplete: false, GoalEvaluation: tree.GoalEvaluation}, nil
		}
		if issues := cleanupDisposableIterationFiles(paths.ActiveDir, paths.Events); len(issues) > 0 {
			appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
		}
		return iterationWorkflowResult{Summary: tree.Summary, GoalComplete: goalComplete, GoalEvaluation: tree.GoalEvaluation}, nil
	}

	pendingPRRepair, err := pendingPullRequestRepairFromTree(req.State.PendingPullRequests, tree)
	if err != nil {
		return iterationWorkflowResult{}, codedError{4, err}
	}
	if pendingPRRepair != nil && (cfg.Git.Integration.Mode != "pr" || cfg.Git.Integration.PR.ReviewMode != config.ReviewModeParallelHumanReview) {
		return iterationWorkflowResult{}, codedError{4, errors.New("repair_pull_request requires parallel_human_review PR mode")}
	}
	currentBranch := initialBranch
	if pendingPRRepair != nil {
		currentBranch = pendingPRRepair.Branch
		paths.PendingPRRepair = pendingPRRepair
		paths.AgentPromptExtra = pendingPRRepairPrompt(*pendingPRRepair)
	}
	paths.IterationBranch = currentBranch
	paths.CurrentBranch = currentBranch
	paths.BranchRenamed = currentBranch != initialBranch
	paths.WorkDir = iterationWorktree
	paths.IterationWorktree = iterationWorktree
	req.Renderer.Branch(currentBranch)
	req.State.Stage = runstate.StageBranchCreated
	req.State.Iterations[len(req.State.Iterations)-1].BranchCurrent = currentBranch
	req.State.Iterations[len(req.State.Iterations)-1].Stage = string(req.State.Stage)
	_ = runstate.Write(req.StatePath, *req.State)
	if err := os.MkdirAll(filepath.Dir(iterationWorktree), 0o755); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	if pendingPRRepair != nil {
		if err := addPendingPRRepairWorktree(ctx, rootRunner, strings.TrimSpace(pendingPRRepair.Branch), iterationWorktree); err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
		_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "pr.human_review.repair_started", "pr": pendingPRRepair.PR, "branch": pendingPRRepair.Branch})
	} else {
		if _, err := rootRunner.Run(ctx, "worktree", "add", "-b", initialBranch, iterationWorktree, cfg.Git.BaseBranch); err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
	}
	if err := writeRuntimeArtifact(paths); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	cleanup := &iterationCleanup{
		RootRunner:        rootRunner,
		BaseBranch:        cfg.Git.BaseBranch,
		Branch:            currentBranch,
		WorktreePath:      iterationWorktree,
		WorkDir:           iterationWorktree,
		TaskWorktreesRoot: filepath.Join(req.Root, ".loop", "worktrees", req.RunID, iterationID, "tasks"),
		TaskBranchPrefix:  "task/" + iterationID + "-",
		LockDir:           filepath.Join(req.Root, ".loop", "locks", sanitizeTempPart(req.RunID)+"-"+sanitizeTempPart(iterationID)+"-task-merge.lock"),
		ActiveDir:         activeDir,
		Active:            true,
		EventLogPath:      paths.Events,
		OnEvent:           req.Renderer.AgentEvent,
	}
	cleanupRegistered = true
	defer cleanup.OnExit(ctx, req.StatePath, req.State, &retErr)

	var allCommits []gitx.Commit
	var completedTaskResults []workflow.TaskResult
	pendingTasks := append([]workflow.Task(nil), tree.Tasks...)
	var review workflow.ReviewResult
	planRevisions := 0
	for cycle := 0; cycle <= cfg.Run.MaxReviewFixCycles; cycle++ {
		currentTaskDirs, err := taskDirs.Ensure(pendingTasks)
		if err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
		req.Renderer.TasksPlanned(pendingTasks)
		req.State.Stage = runstate.StageCoding
		_ = runstate.Write(req.StatePath, *req.State)
		req.Renderer.Stage(runstate.StageCoding, "coding tasks")
		taskSet, err := executeTaskSet(ctx, taskSetRequest{
			Config:            cfg,
			Root:              req.Root,
			IterationWorktree: iterationWorktree,
			IterationBranch:   firstNonEmpty(paths.IterationBranch, paths.CurrentBranch, initialBranch),
			IterationDir:      iterDir,
			Paths:             paths,
			RunID:             req.RunID,
			IterationID:       iterationID,
			Tasks:             pendingTasks,
			TaskDirs:          currentTaskDirs,
			Renderer:          req.Renderer,
		})
		if err != nil {
			return iterationWorkflowResult{}, codedError{4, err}
		}
		completedTaskResults = append(completedTaskResults, taskSet.Results...)
		allCommits = append(allCommits, taskSet.Commits...)
		req.Renderer.Commits(len(allCommits))
		if taskSet.Discard != nil {
			if planRevisions >= cfg.Run.MaxPlanRevisions {
				return iterationWorkflowResult{}, codedError{4, fmt.Errorf("planner revision limit reached after discarded task %s: %s", taskSet.Discard.Task.ID, taskSet.Discard.Reason)}
			}
			planRevisions++
			req.State.Stage = runstate.StagePlanning
			_ = runstate.Write(req.StatePath, *req.State)
			req.Renderer.Stage(runstate.StagePlanning, "planner revising discarded task")
			revisionPaths := paths
			revisionPaths.AgentPromptExtra = plannerRevisionPrompt(tree, completedTaskResults, *taskSet.Discard, planRevisions)
			tree, err = runPlannerRole(ctx, cfg, iterationWorktree, revisionPaths, req.Renderer.AgentEvent)
			if err != nil {
				return iterationWorkflowResult{}, codedError{4, err}
			}
			if err := writeTaskTreeAudit(iterDir, tree); err != nil {
				return iterationWorkflowResult{}, codedError{1, err}
			}
			pendingTasks = append([]workflow.Task(nil), tree.Tasks...)
			if len(pendingTasks) == 0 {
				return iterationWorkflowResult{}, codedError{4, errors.New("planner returned no tasks after a discarded task")}
			}
			continue
		}

		req.State.Stage = runstate.StageValidating
		_ = runstate.Write(req.StatePath, *req.State)
		req.Renderer.Stage(runstate.StageValidating, "running validation")
		validationResults, validationErr := runConfiguredValidation(ctx, iterationWorktree, paths, cfg.Validation.Commands)
		if validationErr != nil || validation.StatusFromResults(validationResults) == "failed" {
			if cycle >= cfg.Run.MaxReviewFixCycles {
				return iterationWorkflowResult{}, codedError{5, firstNonNil(validationErr, errors.New("required validation failed"))}
			}
			pendingTasks = []workflow.Task{validationRepairTask(cycle + 1)}
			continue
		}

		req.State.Stage = runstate.StageReviewing
		_ = runstate.Write(req.StatePath, *req.State)
		req.Renderer.Stage(runstate.StageReviewing, "review agent running")
		review, err = runReviewRole(ctx, cfg, iterationWorktree, paths, tree, completedTaskResults, validationResults, req.Renderer.AgentEvent)
		if err != nil {
			return iterationWorkflowResult{}, codedError{4, err}
		}
		if err := refreshTrackedBranch(ctx, iterationWorktree, &paths); err != nil {
			return iterationWorkflowResult{}, codedError{4, err}
		}
		updateOrchestratedBranchState(req, cleanup, paths.CurrentBranch)
		if err := writeReviewAudit(iterDir, review); err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
		if review.Status == "approved" {
			break
		}
		if cycle >= cfg.Run.MaxReviewFixCycles {
			return iterationWorkflowResult{}, codedError{4, fmt.Errorf("review did not approve after %d fix cycles", cfg.Run.MaxReviewFixCycles)}
		}
		pendingTasks = repairTasksFromReview(review)
		if len(pendingTasks) == 0 {
			return iterationWorkflowResult{}, codedError{4, errors.New("review requested changes without repair findings")}
		}
	}

	return finalizeOrchestratedIteration(ctx, req, cleanup, paths, tree, review, allCommits, iterationWorktree, initialBranch)
}

func finalizeOrchestratedIteration(ctx context.Context, req orchestrationRequest, cleanup *iterationCleanup, paths pathSet, tree workflow.TaskTree, review workflow.ReviewResult, allCommits []gitx.Commit, iterationWorktree, initialBranch string) (iterationWorkflowResult, error) {
	cfg := req.Config
	rootRunner := gitx.Runner{Dir: req.Root}
	iterDir := paths.IterationDir
	iterationID := paths.IterationID
	if len(allCommits) == 0 {
		return iterationWorkflowResult{}, codedError{4, errors.New("iteration tasks did not produce commits")}
	}
	req.State.Stage = runstate.StagePullRequest
	_ = runstate.Write(req.StatePath, *req.State)
	req.Renderer.Stage(runstate.StagePullRequest, "integrating iteration")
	summary := firstNonEmpty(strings.TrimSpace(review.Summary), tree.Summary)
	goalComplete := review.GoalComplete && strings.TrimSpace(req.Goal) != ""
	finalBranch := firstNonEmpty(paths.IterationBranch, paths.CurrentBranch, initialBranch)
	integrated := false
	pendingPR := false
	if cfg.Git.Integration.Mode == "pr" {
		state, ok, err := readPRState(iterDir)
		if err != nil {
			return iterationWorkflowResult{}, codedError{6, err}
		}
		if !ok {
			return iterationWorkflowResult{}, codedError{6, errors.New("approved PR-mode review must create the pull request before approval")}
		}
		finalBranch = firstNonEmpty(state.Branch, initialBranch)
		cleanup.Branch = finalBranch
		switch cfg.Git.Integration.PR.ReviewMode {
		case config.ReviewModeAutoMerge:
			if state.Status != "merged" {
				return iterationWorkflowResult{}, codedError{6, errors.New("approved PR-mode review must merge the pull request with `loop pr merge` before approval")}
			}
			if err := finalizeAgentOwnedPR(ctx, rootRunner, cleanup, iterationWorktree, req.Root, cfg, finalBranch); err != nil {
				return iterationWorkflowResult{}, codedError{6, err}
			}
			cleanup.Integrated = true
			integrated = true
		case config.ReviewModeParallelHumanReview:
			if state.Status == "merged" {
				removePendingPullRequest(req.State, state.PR)
				if err := finalizeAgentOwnedPR(ctx, rootRunner, cleanup, iterationWorktree, req.Root, cfg, finalBranch); err != nil {
					return iterationWorkflowResult{}, codedError{6, err}
				}
				cleanup.Integrated = true
				integrated = true
				break
			}
			if state.Status != "waiting_for_human" {
				return iterationWorkflowResult{}, codedError{6, errors.New("approved human-review PR must run `loop pr checks` and leave pr-state.status=waiting_for_human")}
			}
			changedFiles, _ := changedFilesForBranch(ctx, gitx.Runner{Dir: iterationWorktree}, cfg.Git.BaseBranch, finalBranch)
			pending := pendingPullRequestFromState(req.RunID, iterationID, state, changedFiles)
			if paths.PendingPRRepair != nil && paths.PendingPRRepair.PR == pending.PR {
				if feedback, _, err := prRunner(cfg, iterationWorktree).ViewReviewFeedback(ctx, pending.PR); err == nil {
					pending.ReviewDecision = feedback.ReviewDecision
					pending.ReviewFeedback = feedback.Summary
					pending.ReviewFeedbackAt = feedback.LatestAt
					pending.FeedbackHandledAt = firstNonEmpty(feedback.LatestAt, paths.PendingPRRepair.FeedbackHandledAt)
				} else {
					pending.FeedbackHandledAt = firstNonEmpty(paths.PendingPRRepair.ReviewFeedbackAt, paths.PendingPRRepair.FeedbackHandledAt)
					pending.ReviewDecision = paths.PendingPRRepair.ReviewDecision
				}
			}
			upsertPendingPullRequest(req.State, pending)
			req.Renderer.PendingPullRequests(req.State.PendingPullRequests)
			if err := finalizePendingHumanPR(ctx, rootRunner, cleanup, iterationWorktree, req.Root, cfg, finalBranch); err != nil {
				return iterationWorkflowResult{}, codedError{6, err}
			}
			pendingPR = true
			goalComplete = false
		case config.ReviewModeSerialHumanReview:
			if state.Status != "merged" {
				if state.Status != "waiting_for_human" {
					return iterationWorkflowResult{}, codedError{6, errors.New("approved human-review PR must run `loop pr checks` and leave pr-state.status=waiting_for_human")}
				}
				req.State.Stage = runstate.StagePullRequest
				_ = runstate.Write(req.StatePath, *req.State)
				req.Renderer.Stage(runstate.StagePullRequest, "waiting for human PR merge")
				mergedState, err := waitForHumanPRMerge(runGracefulContext(ctx), iterationWorktree, iterDir, cfg, paths, state, req.Renderer)
				if err != nil {
					if runGracefulShutdownRequested(ctx) {
						return iterationWorkflowResult{}, codedError{interruptExitCode, nil}
					}
					return iterationWorkflowResult{}, codedError{6, err}
				}
				state = mergedState
			}
			removePendingPullRequest(req.State, state.PR)
			if err := finalizeAgentOwnedPR(ctx, rootRunner, cleanup, iterationWorktree, req.Root, cfg, finalBranch); err != nil {
				return iterationWorkflowResult{}, codedError{6, err}
			}
			cleanup.Integrated = true
			integrated = true
		default:
			return iterationWorkflowResult{}, codedError{3, fmt.Errorf("unsupported PR review mode %q", cfg.Git.Integration.PR.ReviewMode)}
		}
	} else {
		if err := rootRunner.RemoveWorktree(ctx, iterationWorktree, false); err != nil {
			return iterationWorkflowResult{}, codedError{6, err}
		}
		cleanup.WorktreePath = ""
		cleanup.DirectIntegrating = true
		baseHead, _ := rootRunner.Run(context.Background(), "rev-parse", cfg.Git.BaseBranch)
		cleanup.BaseHead = strings.TrimSpace(baseHead)
		if err := rootRunner.SquashMerge(ctx, cfg.Git.BaseBranch, finalBranch, summary, false); err != nil {
			return iterationWorkflowResult{}, codedError{6, err}
		}
		cleanup.Integrated = true
		_ = rootRunner.DeleteBranch(ctx, finalBranch, true)
		integrated = true
	}
	if integrated || pendingPR {
		if err := refreshTargetBranch(ctx, rootRunner, cfg.Git.BaseBranch); err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
	}
	if issues := cleanupDisposableIterationFiles(paths.ActiveDir, paths.Events); len(issues) > 0 {
		appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
	}
	cleanup.Active = false
	req.State.Iterations[len(req.State.Iterations)-1].BranchFinal = finalBranch
	req.State.Iterations[len(req.State.Iterations)-1].SummarySentence = summary
	req.State.Iterations[len(req.State.Iterations)-1].ShouldFullyStop = goalComplete
	_ = runstate.Write(req.StatePath, *req.State)
	return iterationWorkflowResult{Summary: summary, GoalComplete: goalComplete, GoalEvaluation: review.GoalEvaluation, Commits: allCommits, Integrated: integrated}, nil
}

type taskSetRequest struct {
	Config            config.Config
	Root              string
	IterationWorktree string
	IterationBranch   string
	IterationDir      string
	Paths             pathSet
	RunID             string
	IterationID       string
	Tasks             []workflow.Task
	TaskDirs          map[string]string
	Renderer          *runRenderer
}

type taskDirectoryAllocator struct {
	iterationDir string
	next         int
	dirs         map[string]string
}

func newTaskDirectoryAllocator(iterDir string) *taskDirectoryAllocator {
	return &taskDirectoryAllocator{iterationDir: iterDir, dirs: map[string]string{}}
}

func (a *taskDirectoryAllocator) Ensure(tasks []workflow.Task) (map[string]string, error) {
	out := make(map[string]string, len(tasks))
	for _, task := range tasks {
		dir, ok := a.dirs[task.ID]
		if !ok {
			a.next++
			dir = filepath.Join(a.iterationDir, "tasks", fmt.Sprintf("%04d", a.next))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			if err := writeTaskAudit(dir, task); err != nil {
				return nil, err
			}
			if err := ensureEmptyFile(filepath.Join(dir, "agent-events.jsonl")); err != nil {
				return nil, err
			}
			a.dirs[task.ID] = dir
		}
		out[task.ID] = dir
	}
	return out, nil
}

func executeTaskSet(ctx context.Context, req taskSetRequest) (taskSetExecution, error) {
	waves, err := workflow.ExecutionWaves(req.Tasks, req.Config.Run.MaxParallelTasks)
	if err != nil {
		return taskSetExecution{}, err
	}
	var results []workflow.TaskResult
	var commits []gitx.Commit
	for _, wave := range waves {
		executed, err := executeTaskWave(ctx, req, wave)
		if err != nil {
			return taskSetExecution{Results: results, Commits: commits}, err
		}
		sort.Slice(executed, func(i, j int) bool { return executed[i].Task.ID < executed[j].Task.ID })
		for _, item := range executed {
			if item.Discarded {
				reason := firstNonEmpty(item.DiscardReason, item.Result.DiscardReason)
				if reason == "" && item.Err != nil {
					reason = item.Err.Error()
				}
				if reason == "" {
					reason = "Task was discarded."
				}
				result, err := writeDiscardedTaskResult(req, item.Task, item.Result, reason)
				if err != nil {
					return taskSetExecution{Results: results, Commits: commits}, err
				}
				_ = (gitx.Runner{Dir: req.Root}).DeleteBranch(ctx, item.Branch, true)
				return taskSetExecution{
					Results: results,
					Commits: commits,
					Discard: &discardedTask{Task: item.Task, Result: result, Reason: reason},
				}, nil
			}
			if item.Err != nil {
				return taskSetExecution{Results: results, Commits: commits}, item.Err
			}
			if item.Result.Status != "completed" {
				return taskSetExecution{Results: results, Commits: commits}, fmt.Errorf("task %s ended with status %s", item.Task.ID, item.Result.Status)
			}
			_ = (gitx.Runner{Dir: req.Root}).DeleteBranch(ctx, item.Branch, true)
			results = append(results, item.Result)
			commits = append(commits, item.Commits...)
		}
	}
	return taskSetExecution{Results: results, Commits: commits}, nil
}

func updateOrchestratedBranchState(req orchestrationRequest, cleanup *iterationCleanup, branch string) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return
	}
	if cleanup != nil {
		cleanup.Branch = branch
	}
	if req.Renderer != nil {
		req.Renderer.Branch(branch)
	}
	if req.State == nil || len(req.State.Iterations) == 0 {
		return
	}
	req.State.Iterations[len(req.State.Iterations)-1].BranchCurrent = branch
	_ = runstate.Write(req.StatePath, *req.State)
}

func pendingPullRequestFromState(runID, iterationID string, state prState, changedFiles []string) runstate.PendingPullRequest {
	return runstate.PendingPullRequest{
		PR:           state.PR,
		Branch:       state.Branch,
		Base:         state.Base,
		Title:        state.Title,
		RunID:        runID,
		IterationID:  iterationID,
		ChangedFiles: changedFiles,
		CreatedAt:    state.CreatedAt,
		Status:       "waiting_for_human",
	}
}

func upsertPendingPullRequest(state *runstate.State, pending runstate.PendingPullRequest) {
	if state == nil || strings.TrimSpace(pending.PR) == "" {
		return
	}
	pending.Status = firstNonEmpty(pending.Status, "waiting_for_human")
	for i := range state.PendingPullRequests {
		if state.PendingPullRequests[i].PR == pending.PR {
			state.PendingPullRequests[i] = pending
			return
		}
	}
	state.PendingPullRequests = append(state.PendingPullRequests, pending)
}

func removePendingPullRequest(state *runstate.State, prID string) {
	if state == nil || strings.TrimSpace(prID) == "" {
		return
	}
	out := state.PendingPullRequests[:0]
	for _, pending := range state.PendingPullRequests {
		if pending.PR == prID {
			continue
		}
		out = append(out, pending)
	}
	if len(out) == 0 {
		state.PendingPullRequests = nil
		return
	}
	state.PendingPullRequests = out
}

func pendingPullRequestRepairFromTree(pending []runstate.PendingPullRequest, tree workflow.TaskTree) (*runstate.PendingPullRequest, error) {
	requested := strings.TrimSpace(tree.RepairPullRequest)
	if requested == "" {
		return nil, nil
	}
	for i := range pending {
		if pullRequestMatches(pending[i].PR, requested) {
			if strings.TrimSpace(pending[i].Branch) == "" {
				return nil, fmt.Errorf("repair_pull_request %s has no recorded branch", requested)
			}
			copy := pending[i]
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("repair_pull_request %s is not a pending pull request", requested)
}

func pullRequestMatches(recorded, requested string) bool {
	recorded = strings.TrimSpace(recorded)
	requested = strings.TrimSpace(requested)
	if recorded == "" || requested == "" {
		return false
	}
	if recorded == requested {
		return true
	}
	return pullRequestDisplayID(recorded) == pullRequestDisplayID(requested)
}

func refreshPendingPullRequests(ctx context.Context, root string, cfg config.Config, state *runstate.State, renderer *runRenderer) bool {
	if state == nil {
		return false
	}
	if cfg.Git.Integration.Mode != "pr" {
		if len(state.PendingPullRequests) > 0 {
			state.PendingPullRequests = nil
			if renderer != nil {
				renderer.PendingPullRequests(nil)
			}
			return true
		}
		if renderer != nil {
			renderer.PendingPullRequests(nil)
		}
		return false
	}
	if len(state.PendingPullRequests) == 0 {
		if renderer != nil {
			renderer.PendingPullRequests(nil)
		}
		return false
	}
	if renderer != nil {
		renderer.PendingPullRequests(state.PendingPullRequests)
	}
	runner := prRunner(cfg, root)
	changed := false
	out := state.PendingPullRequests[:0]
	for _, pending := range state.PendingPullRequests {
		if strings.TrimSpace(pending.PR) == "" {
			changed = true
			continue
		}
		current, _, err := runner.ViewState(ctx, pending.PR)
		if err != nil {
			out = append(out, pending)
			continue
		}
		switch strings.ToLower(strings.TrimSpace(current.State)) {
		case "merged":
			if renderer != nil {
				renderer.Stage(runstate.StagePullRequest, "observed merged pending PR "+pending.PR)
			}
			_ = refreshTargetBranch(ctx, gitx.Runner{Dir: root}, cfg.Git.BaseBranch)
			changed = true
			continue
		case "closed":
			changed = true
			continue
		default:
			if pending.Status != "waiting_for_human" {
				pending.Status = "waiting_for_human"
				changed = true
			}
			out = append(out, pending)
		}
	}
	if len(out) == 0 {
		state.PendingPullRequests = nil
		if renderer != nil {
			renderer.PendingPullRequests(nil)
		}
		return true
	}
	if len(out) != len(state.PendingPullRequests) {
		changed = true
	}
	state.PendingPullRequests = out
	if renderer != nil {
		renderer.PendingPullRequests(state.PendingPullRequests)
	}
	return changed
}

func waitForHumanPRMerge(ctx context.Context, workDir, iterDir string, cfg config.Config, paths pathSet, state prState, renderer *runRenderer) (prState, error) {
	if strings.TrimSpace(state.PR) == "" {
		return state, errors.New("pull request identifier is required while waiting for human review")
	}
	runner := prRunner(cfg, workDir)
	for {
		current, _, err := runner.ViewState(ctx, state.PR)
		if err == nil {
			switch strings.ToLower(strings.TrimSpace(current.State)) {
			case "merged":
				state.Status = "merged"
				state.MergedAt = firstNonEmpty(current.MergedAt, time.Now().UTC().Format(time.RFC3339))
				if err := writePRState(iterDir, state); err != nil {
					return state, err
				}
				_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "pr.human_review.merged", "pr": state.PR})
				return state, nil
			case "closed":
				return state, fmt.Errorf("pull request %s was closed before merge", state.PR)
			}
		} else {
			_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "pr.human_review.poll_failed", "pr": state.PR, "error": err.Error()})
		}
		_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "pr.human_review.waiting", "pr": state.PR})
		if renderer != nil {
			renderer.SleepWaitingForPullRequests([]runstate.PendingPullRequest{pendingPullRequestFromState("", "", state, nil)})
		}
		if err := waitForGitHubSleepPoll(ctx, githubSleepPollInterval, renderer); err != nil {
			return state, err
		}
	}
}

func waitForPendingPullRequestUpdate(ctx context.Context, root string, cfg config.Config, state *runstate.State, statePath string, paths pathSet, renderer *runRenderer) error {
	if state == nil || len(state.PendingPullRequests) == 0 {
		return nil
	}
	for {
		if renderer != nil {
			renderer.SleepWaitingForPullRequests(state.PendingPullRequests)
		}
		_ = runstate.AppendEvent(paths.Events, runstate.Event{
			"type":          "pr.human_review.pending_waiting",
			"pending_count": len(state.PendingPullRequests),
		})
		if err := waitForGitHubSleepPoll(ctx, githubSleepPollInterval, renderer); err != nil {
			return err
		}
		changed := refreshPendingPullRequests(ctx, root, cfg, state, renderer)
		if statePath != "" {
			_ = runstate.Write(statePath, *state)
		}
		if changed || len(state.PendingPullRequests) == 0 {
			_ = runstate.AppendEvent(paths.Events, runstate.Event{
				"type":          "pr.human_review.pending_updated",
				"pending_count": len(state.PendingPullRequests),
			})
			return nil
		}
	}
}

func addPendingPRRepairWorktree(ctx context.Context, runner gitx.Runner, branch, worktree string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("pending PR branch is required")
	}
	if localBranchExists(ctx, runner, branch) {
		_, err := runner.Run(ctx, "worktree", "add", worktree, branch)
		return err
	}
	if remoteBranchExists(ctx, runner, branch) {
		if _, err := runner.Run(ctx, "fetch", "origin", branch+":refs/heads/"+branch); err != nil {
			return err
		}
		_, err := runner.Run(ctx, "worktree", "add", worktree, branch)
		return err
	}
	_, err := runner.Run(ctx, "worktree", "add", worktree, branch)
	return err
}

func changedFilesForBranch(ctx context.Context, runner gitx.Runner, base, branch string) ([]string, error) {
	base = strings.TrimSpace(base)
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, nil
	}
	revision := branch
	if base != "" {
		revision = base + ".." + branch
	}
	out, err := runner.Run(ctx, "diff", "--name-only", revision)
	if err != nil {
		return nil, err
	}
	var files []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		file := strings.TrimSpace(line)
		if file == "" || seen[file] {
			continue
		}
		seen[file] = true
		files = append(files, file)
	}
	sort.Strings(files)
	return files, nil
}

func writeDiscardedTaskResult(req taskSetRequest, task workflow.Task, existing workflow.TaskResult, reason string) (workflow.TaskResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Task was discarded."
	}
	result := existing
	if result.TaskID == "" {
		result.TaskID = task.ID
	}
	if result.SchemaVersion == 0 {
		result.SchemaVersion = workflow.SchemaVersion
	}
	result.Status = "discarded"
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = "Task discarded."
	}
	result.DiscardReason = reason
	data, err := workflow.MarshalIndent(result)
	if err != nil {
		return workflow.TaskResult{}, err
	}
	taskDir := req.TaskDirs[task.ID]
	globalPath := artifactdb.GlobalDBPathForIteration(req.IterationDir)
	if err := artifactdb.WriteRoleHandoff(globalPath, req.RunID, req.IterationID, "task-result", task.ID, string(data)); err != nil {
		return workflow.TaskResult{}, err
	}
	if err := writeTaskResultAudit(req.IterationDir, taskDir, task.ID, data); err != nil {
		return workflow.TaskResult{}, err
	}
	return result, nil
}

func executeTaskWave(ctx context.Context, req taskSetRequest, tasks []workflow.Task) ([]taskExecutionResult, error) {
	type taskContext struct {
		task       workflow.Task
		taskDir    string
		branchBase string
	}
	contexts := make([]taskContext, 0, len(tasks))
	for _, task := range tasks {
		taskDir := req.TaskDirs[task.ID]
		if strings.TrimSpace(taskDir) == "" {
			return nil, fmt.Errorf("task directory not allocated for task %s", task.ID)
		}
		branchBase := "task/" + req.IterationID + "-" + gitx.Slug(task.ID)
		contexts = append(contexts, taskContext{task: task, taskDir: taskDir, branchBase: branchBase})
	}
	out := make(chan taskExecutionResult, len(contexts))
	var wg sync.WaitGroup
	var gitMu sync.Mutex
	for _, taskCtx := range contexts {
		wg.Add(1)
		go func(taskCtx taskContext) {
			defer wg.Done()
			req.Renderer.TaskDirectory(taskCtx.task, taskCtx.taskDir)
			req.Renderer.TaskStarted(taskCtx.task)
			result := runCodingTaskWithAttempts(ctx, req, taskCtx.task, taskCtx.taskDir, taskCtx.branchBase, &gitMu, req.Renderer.AgentEvent)
			if result.Err != nil || result.Result.Status != "completed" {
				req.Renderer.TaskFailed(taskCtx.task, result.Err)
			} else {
				req.Renderer.TaskCompleted(taskCtx.task)
			}
			out <- result
		}(taskCtx)
	}
	wg.Wait()
	close(out)
	rootRunner := gitx.Runner{Dir: req.Root}
	var results []taskExecutionResult
	for item := range out {
		results = append(results, item)
		if item.Err != nil {
			if item.Worktree != "" {
				_ = rootRunner.RemoveWorktree(ctx, item.Worktree, true)
			}
			_ = os.RemoveAll(item.ActiveDir)
			continue
		}
		if item.Worktree != "" {
			_ = rootRunner.RemoveWorktree(ctx, item.Worktree, false)
		}
		_ = os.RemoveAll(item.ActiveDir)
	}
	return results, nil
}

type taskAttemptContext struct {
	branch    string
	worktree  string
	activeDir string
	paths     pathSet
}

func runCodingTaskWithAttempts(ctx context.Context, req taskSetRequest, task workflow.Task, taskDir, branchBase string, gitMu *sync.Mutex, onEvent func(runstate.Event)) taskExecutionResult {
	attempts := req.Config.Run.MaxTaskAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var last taskExecutionResult
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, err := prepareCodingTaskAttempt(ctx, req, task, taskDir, branchBase, attempt, gitMu)
		if err != nil {
			last = taskExecutionResult{Task: task, Attempt: attempt, Err: err}
			return last
		}
		result, commits, err := runCodingRole(ctx, req.Config, attemptCtx.worktree, attemptCtx.paths, task, attemptCtx.branch, attempt, onEvent)
		last = taskExecutionResult{Task: task, Result: result, Branch: attemptCtx.branch, Commits: commits, Attempt: attempt, ActiveDir: attemptCtx.activeDir, Worktree: attemptCtx.worktree, Err: err}
		if err == nil && result.Status == "discarded" {
			last.Discarded = true
			last.DiscardReason = result.DiscardReason
			return last
		}
		if err == nil && result.Status == "completed" {
			return last
		}
		discardTaskAttempt(ctx, req.Root, attemptCtx, task.ID, attempt, err, gitMu)
		last.ActiveDir = ""
		last.Worktree = ""
		if attempt < attempts {
			continue
		}
	}
	if last.Err == nil {
		last.Err = fmt.Errorf("task %s did not complete after %d attempts", task.ID, attempts)
	}
	if last.Attempt >= attempts {
		last.Discarded = true
		last.DiscardReason = last.Err.Error()
	}
	return last
}

func prepareCodingTaskAttempt(ctx context.Context, req taskSetRequest, task workflow.Task, taskDir, branchBase string, attempt int, gitMu *sync.Mutex) (taskAttemptContext, error) {
	rootRunner := gitx.Runner{Dir: req.Root}
	attemptName := "attempt-" + strconv.Itoa(attempt)
	branchStem := branchBase + "-" + attemptName
	branch, err := uniqueBranchNameLocked(ctx, rootRunner, branchStem, gitMu)
	if err != nil {
		return taskAttemptContext{}, err
	}
	worktree := filepath.Join(req.Root, ".loop", "worktrees", req.RunID, req.IterationID, "tasks", task.ID, attemptName)
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		return taskAttemptContext{}, err
	}
	if err := addTaskWorktreeLocked(ctx, rootRunner, branch, worktree, req.IterationBranch, gitMu); err != nil {
		deleteBranchLocked(ctx, rootRunner, branch, gitMu)
		return taskAttemptContext{}, err
	}
	activeDir, err := createIterationTempDir(req.RunID, req.IterationID+"-"+task.ID+"-"+attemptName)
	if err != nil {
		removeWorktreeAndBranchLocked(ctx, rootRunner, worktree, branch, true, gitMu)
		return taskAttemptContext{}, err
	}
	taskPaths := req.Paths
	taskPaths.ActiveDir = activeDir
	taskPaths.Runtime = filepath.Join(activeDir, "runtime.json")
	taskPaths.Plan = filepath.Join(activeDir, "plan.md")
	taskPaths.Todo = filepath.Join(activeDir, "todo.md")
	taskPaths.Validation = filepath.Join(activeDir, "validation.md")
	taskPaths.PRTitle = filepath.Join(activeDir, "pr-title.txt")
	taskPaths.PRBody = filepath.Join(activeDir, "pr-body.md")
	taskPaths.WorkDir = worktree
	taskPaths.IterationWorktree = req.IterationWorktree
	taskPaths.IterationBranch = req.IterationBranch
	taskPaths.CurrentBranch = branch
	taskPaths.TaskID = task.ID
	taskPaths.TaskDir = taskDir
	taskPaths.Events = filepath.Join(taskDir, "agent-events.jsonl")
	if err := clearTaskAttemptState(taskPaths); err != nil {
		removeWorktreeAndBranchLocked(ctx, rootRunner, worktree, branch, true, gitMu)
		_ = os.RemoveAll(activeDir)
		return taskAttemptContext{}, err
	}
	if err := writeRuntimeArtifact(taskPaths); err != nil {
		removeWorktreeAndBranchLocked(ctx, rootRunner, worktree, branch, true, gitMu)
		_ = os.RemoveAll(activeDir)
		return taskAttemptContext{}, err
	}
	return taskAttemptContext{branch: branch, worktree: worktree, activeDir: activeDir, paths: taskPaths}, nil
}

func uniqueBranchNameLocked(ctx context.Context, runner gitx.Runner, branchStem string, gitMu *sync.Mutex) (string, error) {
	if gitMu != nil {
		gitMu.Lock()
		defer gitMu.Unlock()
	}
	return runner.UniqueBranchName(ctx, branchStem, "")
}

func addTaskWorktreeLocked(ctx context.Context, runner gitx.Runner, branch, worktree, startPoint string, gitMu *sync.Mutex) error {
	if gitMu != nil {
		gitMu.Lock()
		defer gitMu.Unlock()
	}
	_, err := runner.Run(ctx, "worktree", "add", "-b", branch, worktree, startPoint)
	return err
}

func removeWorktreeAndBranchLocked(ctx context.Context, runner gitx.Runner, worktree, branch string, force bool, gitMu *sync.Mutex) {
	if gitMu != nil {
		gitMu.Lock()
		defer gitMu.Unlock()
	}
	if worktree != "" {
		_ = runner.RemoveWorktree(ctx, worktree, force)
	}
	if branch != "" {
		_ = runner.DeleteBranch(ctx, branch, true)
	}
}

func deleteBranchLocked(ctx context.Context, runner gitx.Runner, branch string, gitMu *sync.Mutex) {
	if gitMu != nil {
		gitMu.Lock()
		defer gitMu.Unlock()
	}
	if branch != "" {
		_ = runner.DeleteBranch(ctx, branch, true)
	}
}

func discardTaskAttempt(ctx context.Context, root string, attemptCtx taskAttemptContext, taskID string, attempt int, attemptErr error, gitMu *sync.Mutex) {
	cleanupPendingTaskMerge(ctx, attemptCtx.paths)
	_ = clearTaskAttemptState(attemptCtx.paths)
	removeWorktreeAndBranchLocked(ctx, gitx.Runner{Dir: root}, attemptCtx.worktree, attemptCtx.branch, true, gitMu)
	_ = os.RemoveAll(attemptCtx.activeDir)
	event := runstate.Event{"type": "task.attempt.discarded", "task_id": taskID, "attempt": attempt, "branch": attemptCtx.branch, "worktree": attemptCtx.worktree}
	if attemptErr != nil {
		event["error"] = attemptErr.Error()
	}
	_ = runstate.AppendEvent(filepath.Join(attemptCtx.paths.TaskDir, "agent-events.jsonl"), event)
}

func clearTaskAttemptState(paths pathSet) error {
	if err := artifactdb.ClearRoleHandoff(artifactdb.GlobalDBPathForIteration(paths.IterationDir), paths.RunID, paths.IterationID, "task-result", paths.TaskID); err != nil {
		return err
	}
	if strings.TrimSpace(paths.TaskDir) == "" {
		return nil
	}
	for _, name := range []string{"task-result.json", "task-merge.json", "task-result-source.json", "task-todo.json"} {
		if err := os.Remove(filepath.Join(paths.TaskDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func runPlannerRole(ctx context.Context, cfg config.Config, workDir string, paths pathSet, onEvent func(runstate.Event)) (workflow.TaskTree, error) {
	promptText := buildRolePrompt("planner", paths, workflow.Task{}, nil, nil, nil)
	attempts := roleAgentAttempts(cfg)
	var lastErr error
	var lastDecodeErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			appendRoleRestartEvent(paths, "planner", "", attempt, lastErr, onEvent)
		}
		err := runRoleAgentOnce(ctx, cfg, workDir, paths, "planner", "", promptText, onEvent)
		handoff, decodeErr := artifactdb.ReadRoleHandoff(artifactdb.GlobalDBPathForIteration(paths.IterationDir), paths.RunID, paths.IterationID, "task-tree", "")
		if decodeErr == nil {
			tree, treeErr := workflow.DecodeTaskTree([]byte(handoff.Payload))
			if treeErr == nil {
				return tree, nil
			}
			decodeErr = treeErr
		}
		lastErr = err
		lastDecodeErr = decodeErr
		if err == nil || !shouldRestartRoleAgent(err) || attempt == attempts {
			break
		}
	}
	if lastErr != nil {
		return workflow.TaskTree{}, lastErr
	}
	return workflow.TaskTree{}, lastDecodeErr
}

func runCodingRole(ctx context.Context, cfg config.Config, workDir string, paths pathSet, task workflow.Task, branch string, attempt int, onEvent func(runstate.Event)) (workflow.TaskResult, []gitx.Commit, error) {
	promptText := buildRolePrompt("coding", paths, task, nil, nil, nil)
	attempts := roleAgentAttempts(cfg)
	var lastErr error
	var lastDecodeErr error
	for roleAttempt := 1; roleAttempt <= attempts; roleAttempt++ {
		if roleAttempt > 1 {
			appendRoleRestartEvent(paths, "coding", task.ID, roleAttempt, lastErr, onEvent)
		}
		err := runRoleAgentOnce(ctx, cfg, workDir, paths, "coding", task.ID, promptText, onEvent)
		result, commits, decodeErr := readCompletedCodingRoleResult(paths, task.ID, branch, attempt)
		if decodeErr == nil {
			return result, commits, nil
		}
		lastErr = err
		lastDecodeErr = decodeErr
		if err == nil || !shouldRestartRoleAgent(err) || roleAttempt == attempts {
			break
		}
	}
	if lastErr != nil {
		return workflow.TaskResult{}, nil, lastErr
	}
	return workflow.TaskResult{}, nil, lastDecodeErr
}

func readCompletedCodingRoleResult(paths pathSet, taskID, branch string, attempt int) (workflow.TaskResult, []gitx.Commit, error) {
	handoff, err := artifactdb.ReadRoleHandoff(artifactdb.GlobalDBPathForIteration(paths.IterationDir), paths.RunID, paths.IterationID, "task-result", taskID)
	if err != nil {
		return workflow.TaskResult{}, nil, err
	}
	result, err := workflow.DecodeTaskResult([]byte(handoff.Payload))
	if err != nil {
		return workflow.TaskResult{}, nil, err
	}
	resultData, _ := workflow.MarshalIndent(result)
	if err := writeTaskResultAudit(paths.IterationDir, paths.TaskDir, taskID, resultData); err != nil {
		return workflow.TaskResult{}, nil, err
	}
	if result.Status == "discarded" {
		return result, nil, nil
	}
	if result.Status != "completed" {
		return result, nil, fmt.Errorf("task %s ended with status %s", taskID, result.Status)
	}
	mergeRecord, err := readTaskMergeAudit(paths.TaskDir, taskID)
	if err != nil {
		return result, nil, fmt.Errorf("task %s completed without `loop task merge`: %w", taskID, err)
	}
	if len(mergeRecord.TaskCommits) == 0 {
		return result, nil, fmt.Errorf("task %s merge record did not include task commits", taskID)
	}
	_ = attempt
	_ = branch
	commits := make([]gitx.Commit, 0, len(mergeRecord.TaskCommits))
	for _, commit := range mergeRecord.TaskCommits {
		commits = append(commits, gitx.Commit{Hash: commit.SHA, Subject: commit.Subject})
	}
	return result, commits, nil
}

func runReviewRole(ctx context.Context, cfg config.Config, workDir string, paths pathSet, tree workflow.TaskTree, taskResults []workflow.TaskResult, validationResults []validation.CommandResult, onEvent func(runstate.Event)) (workflow.ReviewResult, error) {
	promptText := buildRolePrompt("review", paths, workflow.Task{}, tree, taskResults, validationResults)
	attempts := roleAgentAttempts(cfg)
	var lastErr error
	var lastDecodeErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			appendRoleRestartEvent(paths, "review", "", attempt, lastErr, onEvent)
		}
		err := runRoleAgentOnce(ctx, cfg, workDir, paths, "review", "", promptText, onEvent)
		handoff, decodeErr := artifactdb.ReadRoleHandoff(artifactdb.GlobalDBPathForIteration(paths.IterationDir), paths.RunID, paths.IterationID, "review-result", "")
		if decodeErr == nil {
			review, reviewErr := workflow.DecodeReviewResult([]byte(handoff.Payload))
			if reviewErr == nil {
				return review, nil
			}
			decodeErr = reviewErr
		}
		lastErr = err
		lastDecodeErr = decodeErr
		if err == nil || !shouldRestartRoleAgent(err) || attempt == attempts {
			break
		}
	}
	if lastErr != nil {
		return workflow.ReviewResult{}, lastErr
	}
	return workflow.ReviewResult{}, lastDecodeErr
}

func runRoleAgentOnce(ctx context.Context, cfg config.Config, workDir string, paths pathSet, role, taskID, promptText string, onEvent func(runstate.Event)) error {
	adapterCfg, ok := cfg.Adapter(cfg.Agent.Default)
	if !ok {
		return fmt.Errorf("agent adapter not found: %s", cfg.Agent.Default)
	}
	if cfg.Agent.Default == "fake" {
		if exe, err := os.Executable(); err == nil {
			adapterCfg.Command = exe
		}
		adapterCfg.Args = []string{"__fake-agent"}
	}
	recordAgentPromptAudit(paths.ActiveDir, promptText)
	pa := agent.ProcessAdapter{
		AdapterName: cfg.Agent.Default,
		Command:     adapterCfg.Command,
		Args:        adapterCfg.Args,
		PromptMode:  agent.PromptMode(adapterCfg.Prompt),
		Env:         adapterCfg.Env,
	}
	env := map[string]string{
		"LOOP_RESULT_HANDOFF":            "role-db",
		"LOOP_ROLE":                      role,
		"LOOP_TASK_ID":                   taskID,
		"LOOP_ITERATION_DIR":             paths.IterationDir,
		artifactdb.ActiveIterationDirEnv: paths.ActiveDir,
		"LOOP_WORKDIR":                   workDir,
		"LOOP_RUN_GOAL":                  paths.Goal,
		"LOOP_OUTPUT_LANGUAGE":           paths.Language,
		"LOOP_RUN_ID":                    paths.RunID,
		"LOOP_ITERATION_ID":              paths.IterationID,
		"LOOP_BASE_BRANCH":               paths.BaseBranch,
		"LOOP_INITIAL_BRANCH":            paths.InitialBranch,
		"LOOP_ITERATION_BRANCH":          firstNonEmpty(paths.IterationBranch, paths.CurrentBranch),
		"LOOP_CURRENT_BRANCH":            paths.CurrentBranch,
		"LOOP_ITERATION_WORKTREE":        paths.IterationWorktree,
		"LOOP_INTEGRATION_MODE":          paths.IntegrationMode,
		"LOOP_PR_REVIEW_MODE":            paths.PRReviewMode,
		"LOOP_PULL_REQUEST_MODE":         loopBoolEnv(paths.PullRequestMode),
		"LOOP_PR_MODE":                   loopBoolEnv(paths.PullRequestMode),
		"LOOP_ROLE_ORCHESTRATED":         loopBoolEnv(paths.RoleOrchestrated),
	}
	if paths.PendingPRRepair != nil {
		env["LOOP_REPAIR_PR"] = paths.PendingPRRepair.PR
		env["LOOP_REPAIR_PR_BRANCH"] = paths.PendingPRRepair.Branch
	}
	if strings.TrimSpace(paths.TaskDir) != "" {
		env["LOOP_TASK_DIR"] = paths.TaskDir
	}
	eventMetadata := map[string]any{"agent_type": role}
	if strings.TrimSpace(taskID) != "" {
		eventMetadata["task_id"] = taskID
	}
	if strings.TrimSpace(paths.TaskDir) != "" {
		eventMetadata["task_dir"] = paths.TaskDir
	}
	_, err := pa.Run(ctx, agent.RunRequest{
		WorkDir: workDir, Env: env, PromptText: promptText,
		IterationDir: paths.IterationDir, EventLogPath: paths.Events, ErrorsLogPath: paths.Errors,
		IdleTimeout:   roleAgentIdleTimeout(cfg),
		EventMetadata: eventMetadata,
		OnEvent:       onEvent,
	})
	return err
}

func roleAgentIdleTimeout(cfg config.Config) time.Duration {
	if cfg.Run.AgentIdleTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(cfg.Run.AgentIdleTimeoutSeconds) * time.Second
}

func roleAgentAttempts(cfg config.Config) int {
	return cfg.Run.MaxRoleAgentRestarts + 1
}

func shouldRestartRoleAgent(err error) bool {
	return agent.IsIdleTimeout(err)
}

func appendRoleRestartEvent(paths pathSet, role, taskID string, attempt int, previous error, onEvent func(runstate.Event)) {
	event := runstate.Event{
		"type":       "agent.restart",
		"agent_type": role,
		"attempt":    attempt,
		"reason":     "recoverable agent process failure",
	}
	if strings.TrimSpace(taskID) != "" {
		event["task_id"] = taskID
	}
	if previous != nil {
		event["previous_error"] = previous.Error()
	}
	_ = runstate.AppendEvent(paths.Events, event)
	if onEvent != nil {
		onEvent(event)
	}
}

func pendingPRRepairPrompt(pending runstate.PendingPullRequest) string {
	data, _ := json.MarshalIndent(pending, "", "  ")
	return "Selected pending pull request feedback must be addressed in this iteration. Plan and implement only the work needed to satisfy the PR review feedback, then rerun the existing PR checks on the same branch.\n\nSelected pull request:\n\n```json\n" + string(data) + "\n```"
}

func plannerRevisionPrompt(tree workflow.TaskTree, completed []workflow.TaskResult, discarded discardedTask, revision int) string {
	payload := map[string]any{
		"revision":       revision,
		"current_plan":   tree,
		"completed":      completed,
		"discarded_task": discarded.Task,
		"discard_reason": discarded.Reason,
	}
	if discarded.Result.TaskID != "" {
		payload["discarded_result"] = discarded.Result
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "A coding task was discarded. Review the current plan, completed tasks, and discarded task details below. Decide whether to revise the failed task for another attempt or rewrite the remaining plan tree. Return a full replacement task-tree for the remaining work only.\n\n```json\n" + string(data) + "\n```"
}

func buildRolePrompt(role string, paths pathSet, task workflow.Task, tree any, taskResults []workflow.TaskResult, validationResults []validation.CommandResult) string {
	base := prompt.Assemble(prompt.Request{PullRequestMode: paths.PullRequestMode})
	var b strings.Builder
	b.WriteString(base)
	fmt.Fprintf(&b, "\n## Role\n\nYou are the %s agent in a CLI-orchestrated loop iteration.\n", role)
	b.WriteString("Read runtime context with `loop iteration read runtime` and the instruction with `loop iteration read instruction`.\n")
	b.WriteString("Do not run Git or GitHub commands directly; use loop-owned commands for commits, branch renames, pull requests, handoffs, and task merges.\n")
	b.WriteString("Use `loop help` for the agent-facing command reference and `loop help handoff write` for the current handoff schema and command flags if needed.\n")
	if paths.PendingPRRepair != nil {
		b.WriteString("This iteration resumes an existing human-review pull request branch. Address only the selected PR feedback and keep the work on the existing PR branch.\n")
	}
	switch role {
	case "planner":
		b.WriteString("\nWrite one task-tree handoff:\n\n```bash\nloop handoff write task-tree --file task-tree.json\n```\n\n")
		b.WriteString("Plan one AI sprint-sized PR: the largest coherent sprint goal suitable for an autonomous coding run while still producing an independently mergeable result. If the obvious next slice is only a narrow affordance, isolated implementation layer, or commit-sized change, expand to adjacent behavior that belongs to the same product or technical goal.\n")
		b.WriteString("Choose the fewest task boundaries that preserve autonomy, dependency ordering, conflict avoidance, validation, and safe parallelism. Each task should own a meaningful vertical outcome or substantial subsystem slice, not a file-level, layer-only, or commit-sized microtask. Keep documentation and validation with the behavior owner unless a final cross-cutting hardening task adds distinct value. When tasks must be serial, each step should still produce a meaningful integrated increment.\n")
		b.WriteString("Task entries describe task goals, implementation context, dependencies, conflicts, and acceptance criteria only. Do not include commit split messages or commit metadata.\n")
		if shouldPromptInitialPlannerForPendingPRs(paths) {
			data, _ := json.MarshalIndent(paths.PendingPRs, "", "  ")
			b.WriteString("\nRuntime includes review-pending pull requests. Before planning unrelated implementation work, inspect open pending PRs from oldest to newest with `loop pr feedback <pr>` and compare the latest feedback timestamp with each record's feedback_handled_at. If an unhandled review comment or change-request review needs code changes, return a task tree with `repair_pull_request` set to that PR and tasks that address only that feedback.\n")
			b.WriteString("For pending PRs that do not need repair, treat their changed_files as reserved work and avoid planning tasks that are likely to overlap, conflict with, or depend on those changes until the PRs merge.\n")
			b.WriteString("If every safe implementation area is blocked by review-pending PRs, write an empty task tree with `wait_for_pending_prs: true` so the CLI enters PR review wait mode instead of inventing overlapping work.\n")
			b.WriteString("\nReview-pending pull requests:\n\n```json\n" + string(data) + "\n```\n")
		}
		b.WriteString("The JSON must match this schema: " + taskTreeSchemaHelpText() + "\n")
		b.WriteString("Minimal example:\n\n```json\n{\n  \"schema_version\": 1,\n  \"summary\": \"Implement the requested sprint goal.\",\n  \"goal_evaluation\": \"This iteration plans the work needed for the current goal.\",\n  \"tasks\": [\n    {\n      \"id\": \"implement-core\",\n      \"title\": \"Implement core behavior\",\n      \"description\": \"Update the relevant code paths for the requested behavior.\",\n      \"depends_on\": [],\n      \"conflicts_with\": [],\n      \"acceptance\": [\"Focused tests or validation cover the behavior.\"]\n    }\n  ]\n}\n```\n")
	case "coding":
		data, _ := workflow.MarshalIndent(task)
		b.WriteString("\nComplete only this task. Understand its objective and success criteria, explore the repository, then create the task TODO list before editing files.\n\n")
		b.WriteString("```json\n" + string(data) + "```\n")
		b.WriteString("\nCreate task-local TODOs before implementation. Use `--work-type commit` for TODOs that should create one task-branch commit, and `--work-type no_commit` for validation, inspection, handoff, or other work that must leave no repository changes. Commit TODO titles and commit messages must be specific to this task; do not use generic placeholder text such as \"Implement behavior\". Run task TODO mutation commands one at a time. If pending TODOs need a different order before work starts, use `loop task todo add --after <n>` or `loop task todo move <n> --after <n>`; `--after 0` places an item at the top. During implementation, you may add, move, remove, or cancel pending follow-up TODOs after the fixed done/active/cancelled boundary when you discover additional work such as documentation, tests, validation, or cleanup.\n\n```bash\nloop task todo add --work-type commit --type F --title \"Add publish review route\" --acceptance \"The route renders the review workflow and focused coverage passes.\" add publish review route\nloop task todo add --work-type no_commit --title \"Run focused validation\" --acceptance \"The focused validation command passes.\"\nloop task todo list\n```\n")
		b.WriteString("\nProcess TODOs serially. For each item, run `loop task todo start <n>`. For commit TODOs, make only that TODO's changes, then run `loop task todo stage <n>` to inspect staged commit candidates and remove unrelated files with `loop task todo stage <n> --remove <path>` if needed. Run `loop task todo complete <n>` only after the staged file list matches the TODO. For no_commit TODOs, do not stage files; `loop task todo complete <n>` succeeds only when the task worktree has no repository changes. To cancel an active TODO, run `loop task todo cancel <n> --discard-changes`; this discards task worktree and index changes before marking the TODO cancelled. Do not proceed to the next TODO until the current one is completed or cancelled.\n")
		b.WriteString("\nAfter all TODOs are complete, write the handoff source outside repository changes, then merge the completed task:\n\n```bash\ncat > \"$LOOP_TASK_DIR/task-result.json\" <<'JSON'\n{...}\nJSON\nloop handoff write task-result --task \"" + task.ID + "\" --file \"$LOOP_TASK_DIR/task-result.json\"\nloop task merge --type F complete " + task.ID + "\n```\n")
		b.WriteString("\nIf this task should be abandoned, run `loop task discard --reason <reason>` and exit without merging.\n")
		b.WriteString("\nIf `loop task merge` reports conflicts, resolve them in the printed iteration worktree and run `loop task merge --continue`. Do not exit until the merge command succeeds.\n")
		b.WriteString("\nThe JSON must match this schema: " + taskResultSchemaHelpText() + "\n")
	case "review":
		treeData, _ := workflow.MarshalIndent(tree)
		resultsData, _ := workflow.MarshalIndent(taskResults)
		b.WriteString("\nReview the iteration branch diff, task results, and validation evidence. Approve only if the integrated code matches the planner's task tree and acceptance criteria, the coding-agent results accurately describe the implemented work, code quality is acceptable, and validation is acceptable. Use the task results to write an accurate PR title and body. If CI or PR checks fail, write `changes_requested` findings with concrete repair acceptance so the coding loop can fix them and return for review.\n")
		b.WriteString("\nTask tree:\n\n```json\n" + string(treeData) + "```\n")
		b.WriteString("\nTask results:\n\n```json\n" + string(resultsData) + "```\n")
		b.WriteString("\nValidation status: " + validation.StatusFromResults(validationResults) + "\n")
		if paths.PullRequestMode {
			switch paths.PRReviewMode {
			case config.ReviewModeParallelHumanReview, config.ReviewModeSerialHumanReview:
				if paths.PendingPRRepair != nil {
					b.WriteString("\nIn existing human-review PR repair mode, do not rename the branch. Read the template with `loop iteration read pr-template`, write updated `pr-title` and `pr-body` artifacts when useful, run `loop pr create` so the CLI binds to the existing PR, and run `loop pr checks`. Do not run `loop pr merge`; after checks pass, `pr-state.status` must be `waiting_for_human` so a human can review and merge externally. If checks fail, inspect logs as needed and write `changes_requested` findings instead of approving.\n")
				} else {
					b.WriteString("\nIn human-review pull request mode, before writing an approved review-result you must rename the iteration branch with `loop branch rename`, read the template with `loop iteration read pr-template`, write `pr-title` and `pr-body`, run `loop pr create`, and run `loop pr checks`. Do not run `loop pr merge`; after checks pass, `pr-state.status` must be `waiting_for_human` so a human can review and merge externally. If checks fail, inspect logs as needed and write `changes_requested` findings instead of approving.\n")
				}
			default:
				b.WriteString("\nIn pull request mode, before writing an approved review-result you must rename the iteration branch with `loop branch rename`, read the template with `loop iteration read pr-template`, write `pr-title` and `pr-body`, run `loop pr create`, run `loop pr checks`, and run `loop pr merge`. If checks fail, inspect logs as needed and write `changes_requested` findings instead of approving.\n")
			}
		}
		b.WriteString("\nWrite one review-result handoff:\n\n```bash\nloop handoff write review-result --file review-result.json\n```\n")
		b.WriteString("\nThe JSON must match this schema: " + reviewResultSchemaHelpText() + "\n")
	}
	if strings.TrimSpace(paths.AgentPromptExtra) != "" {
		b.WriteString("\n## Additional Context\n\n")
		b.WriteString(strings.TrimSpace(paths.AgentPromptExtra) + "\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func shouldPromptInitialPlannerForPendingPRs(paths pathSet) bool {
	return len(paths.PendingPRs) > 0 &&
		paths.PendingPRRepair == nil &&
		strings.TrimSpace(paths.AgentPromptExtra) == "" &&
		strings.TrimSpace(paths.IterationWorktree) == ""
}

func loopBoolEnv(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func validationRepairTask(cycle int) workflow.Task {
	return workflow.Task{
		ID:          fmt.Sprintf("repair-validation-%d", cycle),
		Title:       "Repair validation failure",
		Description: "Inspect the validation artifact, fix only the failing behavior, and leave unrelated failures untouched.",
		Acceptance:  []string{"Configured validation no longer fails for the selected iteration branch."},
	}
}

func repairTasksFromReview(review workflow.ReviewResult) []workflow.Task {
	tasks := make([]workflow.Task, 0, len(review.Findings))
	for _, finding := range review.Findings {
		task := workflow.FindingTask(finding)
		if task.ID == "" {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks
}

func writeTaskTreeAudit(iterDir string, tree workflow.TaskTree) error {
	data, err := workflow.MarshalIndent(tree)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(iterDir, "task-tree.json"), data, 0o644)
}

func readTaskTreeAudit(iterDir string) (workflow.TaskTree, error) {
	data, err := os.ReadFile(filepath.Join(iterDir, "task-tree.json"))
	if err != nil {
		return workflow.TaskTree{}, err
	}
	return workflow.DecodeTaskTree(data)
}

func writeTaskAudit(taskDir string, task workflow.Task) error {
	data, err := workflow.MarshalIndent(task)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(taskDir, "task.json"), data, 0o644)
}

func writeReviewAudit(iterDir string, review workflow.ReviewResult) error {
	data, err := workflow.MarshalIndent(review)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(iterDir, "review-result.json"), data, 0o644)
}

func readReviewAudit(iterDir string) (workflow.ReviewResult, error) {
	data, err := os.ReadFile(filepath.Join(iterDir, "review-result.json"))
	if err != nil {
		return workflow.ReviewResult{}, err
	}
	return workflow.DecodeReviewResult(data)
}

func ensureEmptyFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return file.Close()
}

func firstNonNil(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
