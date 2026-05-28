package cli

import (
	"context"
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
	"github.com/aki-0421/loop/internal/memory"
	"github.com/aki-0421/loop/internal/pr"
	"github.com/aki-0421/loop/internal/prompt"
	"github.com/aki-0421/loop/internal/runstate"
	"github.com/aki-0421/loop/internal/skills"
	"github.com/aki-0421/loop/internal/validation"
	"github.com/aki-0421/loop/internal/workflow"
)

type taskExecutionResult struct {
	Task      workflow.Task
	Result    workflow.TaskResult
	Branch    string
	Commit    gitx.Commit
	Attempt   int
	ActiveDir string
	Err       error
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

func runOrchestratedIteration(ctx context.Context, req orchestrationRequest) (iterationWorkflowResult, error) {
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
	paths.CurrentBranch = initialBranch
	paths.IntegrationMode = cfg.Git.Integration.Mode
	paths.PullRequestMode = cfg.Git.Integration.Mode == "pr"
	paths.WorkDir = iterationWorktree
	req.Renderer.Iteration(iterationID, paths.Todo)
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
	if err := writeRuntimeArtifact(paths); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	_ = artifactdb.ClearRoleHandoffs(artifactdb.GlobalDBPathForIteration(iterDir), req.RunID, iterationID)
	cleanup := &iterationCleanup{
		RootRunner:   rootRunner,
		BaseBranch:   cfg.Git.BaseBranch,
		Branch:       initialBranch,
		WorktreePath: iterationWorktree,
		WorkDir:      iterationWorktree,
		Active:       true,
		EventLogPath: paths.Events,
		OnEvent:      req.Renderer.AgentEvent,
	}
	defer cleanup.OnCancel(ctx, req.StatePath, req.State)

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
	if len(tree.Tasks) == 0 {
		goalComplete := tree.GoalComplete && strings.TrimSpace(req.Goal) != ""
		req.State.Iterations[len(req.State.Iterations)-1].ShouldFullyStop = goalComplete
		req.State.Iterations[len(req.State.Iterations)-1].SummarySentence = tree.Summary
		_ = runstate.Write(req.StatePath, *req.State)
		_ = cleanup.cleanup(ctx)
		cleanup.Active = false
		return iterationWorkflowResult{Summary: tree.Summary, GoalComplete: goalComplete, GoalEvaluation: tree.GoalEvaluation}, nil
	}

	var allCommits []gitx.Commit
	pendingTasks := append([]workflow.Task(nil), tree.Tasks...)
	var review workflow.ReviewResult
	for cycle := 0; cycle <= cfg.Run.MaxReviewFixCycles; cycle++ {
		req.State.Stage = runstate.StageCoding
		_ = runstate.Write(req.StatePath, *req.State)
		req.Renderer.Stage(runstate.StageCoding, "coding tasks")
		taskResults, commits, err := executeTaskSet(ctx, taskSetRequest{
			Config:            cfg,
			Root:              req.Root,
			IterationWorktree: iterationWorktree,
			IterationBranch:   initialBranch,
			IterationDir:      iterDir,
			Paths:             paths,
			RunID:             req.RunID,
			IterationID:       iterationID,
			Tasks:             pendingTasks,
			Renderer:          req.Renderer,
		})
		if err != nil {
			return iterationWorkflowResult{}, codedError{4, err}
		}
		allCommits = append(allCommits, commits...)
		req.Renderer.Commits(len(allCommits))

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
		review, err = runReviewRole(ctx, cfg, iterationWorktree, paths, tree, taskResults, validationResults, req.Renderer.AgentEvent)
		if err != nil {
			return iterationWorkflowResult{}, codedError{4, err}
		}
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
	if cfg.Git.Integration.Mode == "pr" {
		if err := integrateIterationPR(ctx, cfg, req.Root, iterationWorktree, iterDir, paths, initialBranch, summary, review, tree, req.Renderer); err != nil {
			return iterationWorkflowResult{}, codedError{6, err}
		}
	} else {
		if err := rootRunner.RemoveWorktree(ctx, iterationWorktree, false); err != nil {
			return iterationWorkflowResult{}, codedError{6, err}
		}
		cleanup.WorktreePath = ""
		cleanup.DirectIntegrating = true
		baseHead, _ := rootRunner.Run(context.Background(), "rev-parse", cfg.Git.BaseBranch)
		cleanup.BaseHead = strings.TrimSpace(baseHead)
		if err := rootRunner.SquashMerge(ctx, cfg.Git.BaseBranch, initialBranch, summary, false); err != nil {
			return iterationWorkflowResult{}, codedError{6, err}
		}
		cleanup.Integrated = true
		_ = rootRunner.DeleteBranch(ctx, initialBranch, true)
	}
	if err := refreshTargetBranch(ctx, rootRunner, cfg.Git.BaseBranch); err != nil {
		return iterationWorkflowResult{}, codedError{1, err}
	}
	if issues := cleanupDisposableIterationFiles(paths.ActiveDir, paths.Events); len(issues) > 0 {
		appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
	}
	cleanup.Active = false
	req.State.Iterations[len(req.State.Iterations)-1].BranchFinal = initialBranch
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
	Renderer          *runRenderer
}

func executeTaskSet(ctx context.Context, req taskSetRequest) ([]workflow.TaskResult, []gitx.Commit, error) {
	waves, err := workflow.ExecutionWaves(req.Tasks, req.Config.Run.MaxParallelTasks)
	if err != nil {
		return nil, nil, err
	}
	var results []workflow.TaskResult
	var commits []gitx.Commit
	for _, wave := range waves {
		executed, err := executeTaskWave(ctx, req, wave)
		if err != nil {
			return nil, nil, err
		}
		sort.Slice(executed, func(i, j int) bool { return executed[i].Task.ID < executed[j].Task.ID })
		for _, item := range executed {
			if item.Err != nil {
				return nil, nil, item.Err
			}
			if item.Result.Status != "completed" {
				return nil, nil, fmt.Errorf("task %s ended with status %s", item.Task.ID, item.Result.Status)
			}
			if err := squashTaskBranch(ctx, req.IterationWorktree, item.Branch, item.Commit.Subject); err != nil {
				return nil, nil, err
			}
			_ = (gitx.Runner{Dir: req.Root}).DeleteBranch(ctx, item.Branch, true)
			results = append(results, item.Result)
			commits = append(commits, item.Commit)
		}
	}
	return results, commits, nil
}

func executeTaskWave(ctx context.Context, req taskSetRequest, tasks []workflow.Task) ([]taskExecutionResult, error) {
	rootRunner := gitx.Runner{Dir: req.Root}
	type taskContext struct {
		task       workflow.Task
		branch     string
		worktree   string
		taskPaths  pathSet
		branchBase string
	}
	contexts := make([]taskContext, 0, len(tasks))
	for _, task := range tasks {
		branchBase := "task/" + req.IterationID + "-" + gitx.Slug(task.ID)
		branch, err := rootRunner.UniqueBranchName(ctx, branchBase, "")
		if err != nil {
			return nil, err
		}
		worktree := filepath.Join(req.Root, ".loop", "worktrees", req.RunID, req.IterationID, "tasks", task.ID)
		if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
			return nil, err
		}
		if _, err := rootRunner.Run(ctx, "worktree", "add", "-b", branch, worktree, req.IterationBranch); err != nil {
			return nil, err
		}
		activeDir, err := createIterationTempDir(req.RunID, req.IterationID+"-"+task.ID)
		if err != nil {
			return nil, err
		}
		taskPaths := req.Paths
		taskPaths.ActiveDir = activeDir
		taskPaths.WorkDir = worktree
		taskPaths.CurrentBranch = branch
		if err := writeRuntimeArtifact(taskPaths); err != nil {
			return nil, err
		}
		contexts = append(contexts, taskContext{task: task, branch: branch, worktree: worktree, taskPaths: taskPaths, branchBase: branchBase})
	}
	out := make(chan taskExecutionResult, len(contexts))
	var wg sync.WaitGroup
	for _, taskCtx := range contexts {
		wg.Add(1)
		go func(taskCtx taskContext) {
			defer wg.Done()
			result := runCodingTaskWithAttempts(ctx, req.Config, taskCtx.worktree, taskCtx.taskPaths, taskCtx.task, taskCtx.branch, req.Renderer.AgentEvent)
			result.ActiveDir = taskCtx.taskPaths.ActiveDir
			out <- result
		}(taskCtx)
	}
	wg.Wait()
	close(out)
	var results []taskExecutionResult
	for item := range out {
		results = append(results, item)
		if item.Err != nil {
			_ = rootRunner.RemoveWorktree(ctx, filepath.Join(req.Root, ".loop", "worktrees", req.RunID, req.IterationID, "tasks", item.Task.ID), true)
			_ = os.RemoveAll(item.ActiveDir)
			continue
		}
		_ = rootRunner.RemoveWorktree(ctx, filepath.Join(req.Root, ".loop", "worktrees", req.RunID, req.IterationID, "tasks", item.Task.ID), false)
		_ = os.RemoveAll(item.ActiveDir)
	}
	return results, nil
}

func runCodingTaskWithAttempts(ctx context.Context, cfg config.Config, workDir string, paths pathSet, task workflow.Task, branch string, onEvent func(runstate.Event)) taskExecutionResult {
	attempts := cfg.Run.MaxTaskAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var last taskExecutionResult
	for attempt := 1; attempt <= attempts; attempt++ {
		result, commit, err := runCodingRole(ctx, cfg, workDir, paths, task, branch, attempt, onEvent)
		last = taskExecutionResult{Task: task, Result: result, Branch: branch, Commit: commit, Attempt: attempt, Err: err}
		if err == nil && result.Status == "completed" {
			return last
		}
	}
	if last.Err == nil {
		last.Err = fmt.Errorf("task %s did not complete after %d attempts", task.ID, attempts)
	}
	return last
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

func runCodingRole(ctx context.Context, cfg config.Config, workDir string, paths pathSet, task workflow.Task, branch string, attempt int, onEvent func(runstate.Event)) (workflow.TaskResult, gitx.Commit, error) {
	promptText := buildRolePrompt("coding", paths, task, nil, nil, nil)
	if err := runRoleAgent(ctx, cfg, workDir, paths, "coding", task.ID, promptText, onEvent); err != nil {
		return workflow.TaskResult{}, gitx.Commit{}, err
	}
	handoff, err := artifactdb.ReadRoleHandoff(artifactdb.GlobalDBPathForIteration(paths.IterationDir), paths.RunID, paths.IterationID, "task-result", task.ID)
	if err != nil {
		return workflow.TaskResult{}, gitx.Commit{}, err
	}
	result, err := workflow.DecodeTaskResult([]byte(handoff.Payload))
	if err != nil {
		return workflow.TaskResult{}, gitx.Commit{}, err
	}
	commit, err := commitTaskChanges(ctx, workDir, task)
	if err != nil {
		return result, gitx.Commit{}, err
	}
	if commit.Hash == "" {
		return result, gitx.Commit{}, fmt.Errorf("task %s completed without repository changes", task.ID)
	}
	_ = attempt
	_ = branch
	return result, commit, nil
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
		"LOOP_CURRENT_BRANCH":            paths.CurrentBranch,
		"LOOP_INTEGRATION_MODE":          paths.IntegrationMode,
		"LOOP_PULL_REQUEST_MODE":         strconv.FormatBool(paths.PullRequestMode),
		"LOOP_PR_MODE":                   strconv.FormatBool(paths.PullRequestMode),
	}
	_, err := pa.Run(ctx, agent.RunRequest{
		WorkDir: workDir, Env: env, PromptText: promptText,
		IterationDir: paths.IterationDir, EventLogPath: paths.Events, ErrorsLogPath: paths.Errors,
		OnEvent: onEvent,
	})
	return err
}

func buildRolePrompt(role string, paths pathSet, task workflow.Task, tree any, taskResults []workflow.TaskResult, validationResults []validation.CommandResult) string {
	base := prompt.Assemble(prompt.Request{PullRequestMode: paths.PullRequestMode})
	var b strings.Builder
	b.WriteString(base)
	fmt.Fprintf(&b, "\n## Role\n\nYou are the %s agent in a CLI-orchestrated loop iteration.\n", role)
	b.WriteString("Read runtime context with `loop iteration read runtime` and the instruction with `loop iteration read instruction`.\n")
	b.WriteString("Do not create branches, commits, pull requests, or iteration closes; write the required handoff JSON and exit.\n")
	switch role {
	case "planner":
		b.WriteString("\nWrite one task-tree handoff:\n\n```bash\nloop handoff write task-tree --file task-tree.json\n```\n\n")
		b.WriteString("Plan one AI sprint-sized PR: a coherent sprint goal that autonomous agents can complete in hours, not a small review-sized batch. Post-hoc review is outside planning and must not constrain scope.\n")
		b.WriteString("The JSON must include schema_version, summary, goal_evaluation, optional goal_complete, and tasks with id, title, description, depends_on, conflicts_with, acceptance, commit_type, and commit_message.\n")
	case "coding":
		data, _ := workflow.MarshalIndent(task)
		b.WriteString("\nComplete only this task, then write a task-result handoff and exit.\n\n")
		b.WriteString("```json\n" + string(data) + "```\n")
		b.WriteString("\nUse:\n\n```bash\nloop handoff write task-result --task \"" + task.ID + "\" --file task-result.json\n```\n")
	case "review":
		treeData, _ := workflow.MarshalIndent(tree)
		resultsData, _ := workflow.MarshalIndent(taskResults)
		b.WriteString("\nReview the iteration branch diff, task results, and validation evidence. Approve only if the tasks are complete and validation is acceptable.\n")
		b.WriteString("\nTask tree:\n\n```json\n" + string(treeData) + "```\n")
		b.WriteString("\nTask results:\n\n```json\n" + string(resultsData) + "```\n")
		b.WriteString("\nValidation status: " + validation.StatusFromResults(validationResults) + "\n")
		b.WriteString("\nWrite one review-result handoff:\n\n```bash\nloop handoff write review-result --file review-result.json\n```\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func commitTaskChanges(ctx context.Context, workDir string, task workflow.Task) (gitx.Commit, error) {
	runner := gitx.Runner{Dir: workDir}
	dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return gitx.Commit{}, err
	}
	if dirty.Clean {
		return gitx.Commit{}, nil
	}
	subject, err := buildLoopCommitSubject(task.CommitType, task.CommitMessage, loopCommitMessageMaxLength)
	if err != nil {
		return gitx.Commit{}, err
	}
	pathspecs := dirtyPathspecs(dirty.Dirty)
	if len(pathspecs) == 0 {
		return gitx.Commit{}, nil
	}
	addArgs := append([]string{"add", "-A", "--"}, pathspecs...)
	if _, err := runner.Run(ctx, addArgs...); err != nil {
		return gitx.Commit{}, err
	}
	unstageRuntimePaths(ctx, runner)
	if _, err := runner.Run(ctx, "commit", "-m", subject); err != nil {
		return gitx.Commit{}, err
	}
	sha, err := runner.Head(ctx)
	if err != nil {
		return gitx.Commit{}, err
	}
	return gitx.Commit{Hash: strings.TrimSpace(sha), Subject: subject}, nil
}

func squashTaskBranch(ctx context.Context, iterationWorktree, taskBranch, subject string) error {
	runner := gitx.Runner{Dir: iterationWorktree}
	if _, err := runner.Run(ctx, "merge", "--squash", taskBranch); err != nil {
		_, _ = runner.Run(ctx, "merge", "--abort")
		_, _ = runner.Run(ctx, "reset", "--hard")
		_, _ = runGitCleanPreservingLoopRuntime(ctx, runner)
		return err
	}
	_, err := runner.Run(ctx, "commit", "-m", subject)
	return err
}

func integrateIterationPR(ctx context.Context, cfg config.Config, root, workDir, iterDir string, paths pathSet, branch, summary string, review workflow.ReviewResult, tree workflow.TaskTree, renderer *runRenderer) error {
	title := firstNonEmpty(summary, tree.Summary, fallbackPRTitle(branch))
	body := orchestratedPRBody(tree, review)
	_ = artifactdb.Write(iterDir, "pr-title", title+"\n")
	_ = artifactdb.Write(iterDir, "pr-body", body)
	runner := prRunner(cfg, workDir)
	if cfg.Git.Integration.PR.Push {
		if _, err := runner.Push(ctx, branch); err != nil {
			return err
		}
	}
	prID := ""
	if viewed, err := runner.View(ctx, branch); err == nil {
		prID = strings.TrimSpace(viewed.Stdout)
		if prID == "null" {
			prID = ""
		}
	}
	if prID == "" {
		bodyFile, cleanup, err := materializePRBody(root, body)
		if err != nil {
			return err
		}
		defer cleanup()
		created, err := runner.Create(ctx, pr.CreateOptions{Base: cfg.Git.BaseBranch, Head: branch, Title: title, BodyFile: bodyFile})
		if err != nil {
			return err
		}
		prID = strings.TrimSpace(created.Stdout)
	}
	if prID == "" {
		prID = branch
	}
	state := prState{SchemaVersion: 1, Status: "created", PR: prID, Branch: branch, Base: cfg.Git.BaseBranch, Title: title, BodyArtifact: "pr-body", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if cfg.Git.Integration.PR.HumanReview {
		state.Status = "pending_human_review"
		if err := writePRState(iterDir, state); err != nil {
			return err
		}
		return waitForHumanReviewMerge(ctx, cfg, root, workDir, iterDir, paths, runner, state, renderer)
	}
	if err := writePRState(iterDir, state); err != nil {
		return err
	}
	if cfg.Git.Integration.PR.WaitChecks {
		session := &prIntegrationSession{runner: runner, prID: prID}
		checks, skipped, checksErr := waitForPRChecks(ctx, session, cfg)
		status := "passed"
		if skipped {
			status = "skipped"
		}
		if checksErr != nil {
			status = "failed"
		}
		_ = writePRChecks(iterDir, prChecksArtifact{SchemaVersion: 1, Status: status, PR: prID, Branch: branch, CheckedAt: time.Now().UTC().Format(time.RFC3339), ExitCode: checks.ExitCode, NoChecks: checks.NoChecks, Pending: checks.Pending, Error: prErrorString(checksErr), Stdout: checks.Stdout, Stderr: checks.Stderr})
		if checksErr != nil {
			return fmt.Errorf("pull request checks failed for %s; see pr-checks artifact", prID)
		}
	}
	if err := validatePRBranchCommits(ctx, prCommandContext{workDir: workDir, base: cfg.Git.BaseBranch, branch: branch}); err != nil {
		return err
	}
	bodyFile, cleanup, err := materializePRBody(root, body)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := runner.Merge(ctx, pr.MergeOptions{PR: prID, Subject: title, BodyFile: bodyFile}); err != nil {
		return err
	}
	state.Status = "merged"
	state.MergedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writePRState(iterDir, state); err != nil {
		return err
	}
	if branch != "" && branch != cfg.Git.BaseBranch {
		_ = deleteRemoteBranchAfterPRMerge(ctx, gitx.Runner{Dir: workDir}, branch)
	}
	return finalizeAgentOwnedPR(ctx, gitx.Runner{Dir: root}, &iterationCleanup{RootRunner: gitx.Runner{Dir: root}, BaseBranch: cfg.Git.BaseBranch}, workDir, root, cfg, branch)
}

func waitForHumanReviewMerge(ctx context.Context, cfg config.Config, root, workDir, iterDir string, paths pathSet, runner pr.Runner, state prState, renderer *runRenderer) error {
	for {
		record, err := memory.FetchPullRequest(ctx, memory.FetchOptions{WorkDir: workDir, RunsDir: filepath.Join(root, cfg.Logs.Dir), Ref: state.PR})
		if err == nil && strings.ToLower(strings.TrimSpace(record.State)) == "merged" {
			state.Status = "merged"
			state.MergedAt = firstNonEmpty(record.MergedAt, time.Now().UTC().Format(time.RFC3339))
			return writePRState(iterDir, state)
		}
		renderer.SleepWaitingForGitHub()
		if err := githubSleepPoll(ctx, githubSleepPollInterval); err != nil {
			return err
		}
		_ = runner
		_ = paths
	}
}

func orchestratedPRBody(tree workflow.TaskTree, review workflow.ReviewResult) string {
	var b strings.Builder
	b.WriteString("## Summary\n\n")
	b.WriteString(strings.TrimSpace(tree.Summary) + "\n\n")
	b.WriteString("## Review\n\n")
	b.WriteString(strings.TrimSpace(review.Summary) + "\n\n")
	b.WriteString("## Goal\n\n")
	b.WriteString(strings.TrimSpace(review.GoalEvaluation) + "\n")
	return b.String()
}

func validationRepairTask(cycle int) workflow.Task {
	return workflow.Task{
		ID:            fmt.Sprintf("repair-validation-%d", cycle),
		Title:         "Repair validation failure",
		Description:   "Inspect the validation artifact, fix only the failing behavior, and leave unrelated failures untouched.",
		Acceptance:    []string{"Configured validation no longer fails for the selected iteration branch."},
		CommitType:    "F",
		CommitMessage: "repair validation failure",
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

func writeReviewAudit(iterDir string, review workflow.ReviewResult) error {
	data, err := workflow.MarshalIndent(review)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(iterDir, "review-result.json"), data, 0o644)
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
