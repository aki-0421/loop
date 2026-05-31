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
}

func commandRun(ctx context.Context, g globals, args []string) error {
	args = flagsFirst(args, map[string]bool{
		"agent": true, "goal": true, "max-iterations": true, "base": true,
		"resume": true, "from-iteration": true, "keep-branches": true, "keep-worktrees": true,
		"human-review": true,
	})
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentName := fs.String("agent", g.Agent, "agent adapter")
	goal := fs.String("goal", "", "natural-language stop condition")
	maxIterations := fs.Int("max-iterations", 0, "maximum iterations, 0 for unlimited")
	prFlag := fs.Bool("pr", false, "use pull request integration")
	base := fs.String("base", "", "base branch")
	humanReview := fs.Bool("human-review", false, "open pull requests and pause for external post-hoc review before merge")
	resumeID := fs.String("resume", "", "accepted for older scripts; use loop resume")
	fromIteration := fs.Int("from-iteration", 0, "accepted for older scripts; currently ignored")
	keepBranches := fs.String("keep-branches", "", "accepted for older scripts; cleanup is automatic")
	keepWorktrees := fs.String("keep-worktrees", "", "accepted for older scripts; cleanup is automatic")
	dryRun := fs.Bool("dry-run", false, "build prompt and state files only")
	legacy := fs.Bool("legacy", false, "run the pre-orchestration single-agent workflow")
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	_ = resumeID
	_ = fromIteration
	_ = keepBranches
	_ = keepWorktrees
	if *legacy {
		return commandRunLegacy(ctx, g, stripBoolFlag(args, "legacy"))
	}
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
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-iterations":
			overrides.MaxIterations = maxIterations
		case "human-review":
			v := *humanReview
			overrides.HumanReview = &v
		}
	})
	if *prFlag {
		v := true
		overrides.PRMode = &v
	}
	cfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: overrides})
	if err != nil {
		return codedError{3, err}
	}
	if shouldUseLegacyRunForAdapter(cfg) {
		return commandRunLegacy(ctx, g, args)
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
	if !*dryRun {
		clean, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
		if err != nil {
			return codedError{1, err}
		}
		if !clean.Clean {
			return codedError{1, fmt.Errorf("working tree is dirty: %s", dirtyList(clean.Dirty))}
		}
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
	renderer := newRunRenderer(g, runID, cfg.Agent.Default, filepath.Base(root), "", cfg.Git.BaseBranch, *goal, rel(root, runDir), cfg.Run.MaxIterations, *dryRun)
	renderer.Start(ctx)
	defer func() { renderer.Stop(state.Stage, "") }()
	if err := runstate.Write(statePath, state); err != nil {
		return codedError{1, err}
	}
	if shouldConfirmTargetBranch(cfg.Git.BaseBranch, mainBranch) {
		if err := renderer.ConfirmTargetBranch(ctx, cfg.Git.BaseBranch, mainBranch, targetBranchConfirmationDuration); err != nil {
			state.Stage = runstate.StageCancelled
			_ = runstate.Write(statePath, state)
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
	for i := 1; cfg.Run.MaxIterations == 0 || i <= cfg.Run.MaxIterations; i++ {
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
			DryRun:          *dryRun,
		})
		if err != nil {
			state.Stage = runstate.StageFailed
			_ = runstate.Write(statePath, state)
			return err
		}
		last = result
		if *dryRun {
			return printResult(g, map[string]any{"run_id": runID, "prompt": filepath.Join(rel(root, runDir), "iterations", "0001", "prompt.md")}, fmt.Sprintf("Dry run created prompt: %s\n", filepath.Join(rel(root, runDir), "iterations", "0001", "prompt.md")))
		}
		if len(result.Commits) > 0 {
			mergedCount++
			renderer.Merged(mergedCount)
		}
		if result.GoalComplete {
			state.Stage = runstate.StageCompleted
			_ = runstate.Write(statePath, state)
			break
		}
	}
	if state.Stage != runstate.StageCompleted {
		state.Stage = runstate.StageCompleted
		_ = runstate.Write(statePath, state)
	}
	return printResult(g, map[string]any{"run_id": runID, "status": state.Stage, "summary": last.Summary, "logs": rel(root, runDir)}, fmt.Sprintf("Run: %s\nStatus: %s\nSummary: %s\nLogs: %s\n", runID, state.Stage, last.Summary, rel(root, runDir)))
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
	DryRun          bool
}

func runOrchestratedIteration(ctx context.Context, req orchestrationRequest) (result iterationWorkflowResult, retErr error) {
	cfg := req.Config
	rootRunner := gitx.Runner{Dir: req.Root}
	iterationID := runstate.IterationID(req.IterationNumber)
	iterDir := filepath.Join(req.RunDir, "iterations", iterationID)
	activeDir := iterDir
	if !req.DryRun {
		var err error
		activeDir, err = createIterationTempDir(req.RunID, iterationID)
		if err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
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
	paths.IterationBranch = initialBranch
	paths.CurrentBranch = initialBranch
	paths.IntegrationMode = cfg.Git.Integration.Mode
	paths.PullRequestMode = cfg.Git.Integration.Mode == "pr"
	paths.RoleOrchestrated = true
	paths.WorkDir = iterationWorktree
	paths.IterationWorktree = iterationWorktree
	cleanupRegistered := false
	if !req.DryRun {
		defer func() {
			if retErr != nil && !cleanupRegistered {
				if issues := cleanupDisposableIterationFiles(activeDir, paths.Events); len(issues) > 0 {
					appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
				}
			}
		}()
	}
	req.Renderer.Iteration(iterationID)
	req.Renderer.Branch(initialBranch)
	req.State.CurrentIteration = iterationID
	req.State.Stage = runstate.StageBranchCreated
	req.State.Iterations = append(req.State.Iterations, runstate.IterationRecord{IterationID: iterationID, BranchInitial: initialBranch, BranchCurrent: initialBranch, Stage: string(req.State.Stage)})
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
	if req.DryRun {
		paths.WorkDir = req.Root
		if err := writeRuntimeArtifact(paths); err != nil {
			return iterationWorkflowResult{}, codedError{1, err}
		}
	}
	if req.DryRun {
		return iterationWorkflowResult{Summary: "dry run"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(iterationWorktree), 0o755); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	if _, err := rootRunner.Run(ctx, "worktree", "add", "-b", initialBranch, iterationWorktree, cfg.Git.BaseBranch); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	paths.WorkDir = iterationWorktree
	paths.IterationWorktree = iterationWorktree
	if err := writeRuntimeArtifact(paths); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	_ = artifactdb.ClearRoleHandoffs(artifactdb.GlobalDBPathForIteration(iterDir), req.RunID, iterationID)
	cleanup := &iterationCleanup{
		RootRunner:        rootRunner,
		BaseBranch:        cfg.Git.BaseBranch,
		Branch:            initialBranch,
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

	req.State.Stage = runstate.StagePlanning
	_ = runstate.Write(req.StatePath, *req.State)
	req.Renderer.Stage(runstate.StagePlanning, "planner agent running")
	tree, err := runPlannerRole(ctx, cfg, iterationWorktree, paths, req.Renderer.AgentEvent)
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
		_ = cleanup.cleanup(ctx)
		if issues := cleanupDisposableIterationFiles(paths.ActiveDir, paths.Events); len(issues) > 0 {
			appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
		}
		cleanup.Active = false
		return iterationWorkflowResult{Summary: tree.Summary, GoalComplete: goalComplete, GoalEvaluation: tree.GoalEvaluation}, nil
	}

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

	if len(allCommits) == 0 {
		return iterationWorkflowResult{}, codedError{4, errors.New("iteration tasks did not produce commits")}
	}
	req.State.Stage = runstate.StagePullRequest
	_ = runstate.Write(req.StatePath, *req.State)
	req.Renderer.Stage(runstate.StagePullRequest, "integrating iteration")
	summary := firstNonEmpty(strings.TrimSpace(review.Summary), tree.Summary)
	goalComplete := review.GoalComplete && strings.TrimSpace(req.Goal) != ""
	finalBranch := firstNonEmpty(paths.IterationBranch, paths.CurrentBranch, initialBranch)
	if cfg.Git.Integration.Mode == "pr" {
		state, ok, err := readPRState(iterDir)
		if err != nil {
			return iterationWorkflowResult{}, codedError{6, err}
		}
		if !ok || state.Status != "merged" {
			return iterationWorkflowResult{}, codedError{6, errors.New("approved PR-mode review must merge the pull request with `loop pr merge` before approval")}
		}
		finalBranch = firstNonEmpty(state.Branch, initialBranch)
		cleanup.Branch = finalBranch
		if err := finalizeAgentOwnedPR(ctx, rootRunner, cleanup, iterationWorktree, req.Root, cfg, finalBranch); err != nil {
			return iterationWorkflowResult{}, codedError{6, err}
		}
		cleanup.Integrated = true
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
	}
	if err := refreshTargetBranch(ctx, rootRunner, cfg.Git.BaseBranch); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	if issues := cleanupDisposableIterationFiles(paths.ActiveDir, paths.Events); len(issues) > 0 {
		appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
	}
	cleanup.Active = false
	req.State.Iterations[len(req.State.Iterations)-1].BranchFinal = finalBranch
	req.State.Iterations[len(req.State.Iterations)-1].SummarySentence = summary
	req.State.Iterations[len(req.State.Iterations)-1].ShouldFullyStop = goalComplete
	_ = runstate.Write(req.StatePath, *req.State)
	return iterationWorkflowResult{Summary: summary, GoalComplete: goalComplete, GoalEvaluation: review.GoalEvaluation, Commits: allCommits}, nil
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
	if err := runRoleAgent(ctx, cfg, workDir, paths, "planner", "", promptText, onEvent); err != nil {
		return workflow.TaskTree{}, err
	}
	handoff, err := artifactdb.ReadRoleHandoff(artifactdb.GlobalDBPathForIteration(paths.IterationDir), paths.RunID, paths.IterationID, "task-tree", "")
	if err != nil {
		return workflow.TaskTree{}, err
	}
	return workflow.DecodeTaskTree([]byte(handoff.Payload))
}

func runCodingRole(ctx context.Context, cfg config.Config, workDir string, paths pathSet, task workflow.Task, branch string, attempt int, onEvent func(runstate.Event)) (workflow.TaskResult, []gitx.Commit, error) {
	promptText := buildRolePrompt("coding", paths, task, nil, nil, nil)
	if err := runRoleAgent(ctx, cfg, workDir, paths, "coding", task.ID, promptText, onEvent); err != nil {
		return workflow.TaskResult{}, nil, err
	}
	handoff, err := artifactdb.ReadRoleHandoff(artifactdb.GlobalDBPathForIteration(paths.IterationDir), paths.RunID, paths.IterationID, "task-result", task.ID)
	if err != nil {
		return workflow.TaskResult{}, nil, err
	}
	result, err := workflow.DecodeTaskResult([]byte(handoff.Payload))
	if err != nil {
		return workflow.TaskResult{}, nil, err
	}
	resultData, _ := workflow.MarshalIndent(result)
	if err := writeTaskResultAudit(paths.IterationDir, paths.TaskDir, task.ID, resultData); err != nil {
		return workflow.TaskResult{}, nil, err
	}
	if result.Status == "discarded" {
		return result, nil, nil
	}
	if result.Status != "completed" {
		return result, nil, fmt.Errorf("task %s ended with status %s", task.ID, result.Status)
	}
	mergeRecord, err := readTaskMergeAudit(paths.TaskDir, task.ID)
	if err != nil {
		return result, nil, fmt.Errorf("task %s completed without `loop task merge`: %w", task.ID, err)
	}
	if len(mergeRecord.TaskCommits) == 0 {
		return result, nil, fmt.Errorf("task %s merge record did not include task commits", task.ID)
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
	if err := runRoleAgent(ctx, cfg, workDir, paths, "review", "", promptText, onEvent); err != nil {
		return workflow.ReviewResult{}, err
	}
	handoff, err := artifactdb.ReadRoleHandoff(artifactdb.GlobalDBPathForIteration(paths.IterationDir), paths.RunID, paths.IterationID, "review-result", "")
	if err != nil {
		return workflow.ReviewResult{}, err
	}
	return workflow.DecodeReviewResult([]byte(handoff.Payload))
}

func runRoleAgent(ctx context.Context, cfg config.Config, workDir string, paths pathSet, role, taskID, promptText string, onEvent func(runstate.Event)) error {
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
		"LOOP_PULL_REQUEST_MODE":         strconv.FormatBool(paths.PullRequestMode),
		"LOOP_PR_MODE":                   strconv.FormatBool(paths.PullRequestMode),
		"LOOP_ROLE_ORCHESTRATED":         strconv.FormatBool(paths.RoleOrchestrated),
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
		EventMetadata: eventMetadata,
		OnEvent:       onEvent,
	})
	return err
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
	b.WriteString("Use `loop help agent handoff write` for the current handoff schema and command flags if needed.\n")
	switch role {
	case "planner":
		b.WriteString("\nWrite one task-tree handoff:\n\n```bash\nloop handoff write task-tree --file task-tree.json\n```\n\n")
		b.WriteString("Plan one AI sprint-sized PR: the largest coherent sprint goal suitable for an autonomous coding run while still producing an independently mergeable result. If the obvious next slice is only a narrow affordance, isolated implementation layer, or commit-sized change, expand to adjacent behavior that belongs to the same product or technical goal.\n")
		b.WriteString("Choose the fewest task boundaries that preserve autonomy, dependency ordering, conflict avoidance, validation, and safe parallelism. Each task should own a meaningful vertical outcome or substantial subsystem slice, not a file-level, layer-only, or commit-sized microtask. Keep documentation and validation with the behavior owner unless a final cross-cutting hardening task adds distinct value. When tasks must be serial, each step should still produce a meaningful integrated increment.\n")
		b.WriteString("Task entries describe task goals, implementation context, dependencies, conflicts, and acceptance criteria only. Do not include commit split messages or commit metadata.\n")
		b.WriteString("The JSON must match this schema: " + taskTreeSchemaHelpText() + "\n")
		b.WriteString("Minimal example:\n\n```json\n{\n  \"schema_version\": 1,\n  \"summary\": \"Implement the requested sprint goal.\",\n  \"goal_evaluation\": \"This iteration plans the work needed for the current goal.\",\n  \"tasks\": [\n    {\n      \"id\": \"implement-core\",\n      \"title\": \"Implement core behavior\",\n      \"description\": \"Update the relevant code paths for the requested behavior.\",\n      \"depends_on\": [],\n      \"conflicts_with\": [],\n      \"acceptance\": [\"Focused tests or validation cover the behavior.\"]\n    }\n  ]\n}\n```\n")
	case "coding":
		data, _ := workflow.MarshalIndent(task)
		b.WriteString("\nComplete only this task. Understand its objective and success criteria, explore the repository, then create the task TODO list before editing files.\n\n")
		b.WriteString("```json\n" + string(data) + "```\n")
		b.WriteString("\nCreate one TODO per task-branch commit before implementation. TODO titles and commit messages must be specific to this task; do not use generic placeholder text such as \"Implement behavior\".\n\n```bash\nloop task todo add --type F --title \"Add publish review route\" --acceptance \"The route renders the review workflow and focused coverage passes.\" add publish review route\nloop task todo list\n```\n")
		b.WriteString("\nProcess TODOs serially. For each item, run `loop task todo start <n>`, make only that TODO's changes, then run `loop task todo complete <n>` so the CLI creates the commit. Do not proceed to the next TODO until the current one is completed.\n")
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
			b.WriteString("\nIn pull request mode, before writing an approved review-result you must rename the iteration branch with `loop branch rename`, read the template with `loop iteration read pr-template`, write `pr-title` and `pr-body`, run `loop pr create`, run `loop pr checks`, and run `loop pr merge`. If checks fail, inspect logs as needed and write `changes_requested` findings instead of approving.\n")
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

func shouldUseLegacyRunForAdapter(cfg config.Config) bool {
	adapter, ok := cfg.Adapter(cfg.Agent.Default)
	if !ok {
		return false
	}
	for key, value := range adapter.Env {
		if strings.HasPrefix(key, "LOOP_TEST_") && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func stripBoolFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	long := "--" + name
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			continue
		}
		out = append(out, arg)
	}
	return out
}
