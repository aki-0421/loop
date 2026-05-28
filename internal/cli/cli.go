package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

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
)

type codedError struct {
	code int
	err  error
}

var prChecksSleep = sleepContext
var githubSleepPoll = sleepContext
var targetBranchConfirmationSleep = sleepContext
var runSkillsCLIAdd = runSkillsCLIAddCommand

var githubSleepPollInterval = 5 * time.Minute
var targetBranchConfirmationDuration = 5 * time.Second
var resultHandoffPollInterval = 200 * time.Millisecond

var errPRChecksTimedOut = errors.New("pull request checks timed out")

const defaultSkillPackage = "aki-0421/loop"

func (e codedError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e codedError) Unwrap() error { return e.err }

func ExitCode(err error) (int, bool) {
	var coded codedError
	if errors.As(err, &coded) {
		return coded.code, true
	}
	return 0, false
}

type globals struct {
	ConfigPath string
	Agent      string
	CWD        string
	LogLevel   string
	JSON       bool
	NoColor    bool
}

func Run(args []string) error {
	if len(args) > 0 && args[0] == "__fake-agent" {
		os.Exit(agent.RunFakeAgentFromEnv())
	}
	g, rest, err := parseGlobals(args)
	if err != nil {
		return codedError{2, err}
	}
	if len(rest) == 0 {
		return codedError{2, fmt.Errorf("usage: loop <command> [flags]; run `loop help`")}
	}
	if g.CWD != "" {
		if err := os.Chdir(g.CWD); err != nil {
			return codedError{1, err}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch rest[0] {
	case "help":
		return commandHelp(ctx, g, rest[1:])
	case "init":
		return commandInit(ctx, g, rest[1:])
	case "run":
		return commandRun(ctx, g, rest[1:])
	case "resume":
		return commandResume(ctx, g, rest[1:])
	case "status":
		return commandStatus(ctx, g, rest[1:])
	case "logs":
		return commandLogs(ctx, g, rest[1:])
	case "commit":
		return commandCommit(ctx, g, rest[1:])
	case "branch":
		return commandBranch(ctx, g, rest[1:])
	case "pr":
		return commandPR(ctx, g, rest[1:])
	case "issue":
		return commandIssue(ctx, g, rest[1:])
	case "iteration":
		return commandIteration(ctx, g, rest[1:])
	case "skills":
		return commandSkills(ctx, g, rest[1:])
	case "memory":
		return commandMemory(ctx, g, rest[1:])
	case "doctor":
		return commandDoctor(ctx, g, rest[1:])
	default:
		return codedError{2, fmt.Errorf("unknown command %q; run `loop help`", rest[0])}
	}
}

func parseGlobals(args []string) (globals, []string, error) {
	g := globals{ConfigPath: config.DefaultRepoConfig, Agent: "codex", LogLevel: "info"}
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" || arg == "-h" {
			rest = append([]string{"help"}, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			rest = args[i:]
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			i++
			if i >= len(args) {
				return "", fmt.Errorf("--%s requires a value", name)
			}
			return args[i], nil
		}
		switch name {
		case "config":
			v, err := takeValue()
			if err != nil {
				return g, nil, err
			}
			g.ConfigPath = v
		case "agent":
			v, err := takeValue()
			if err != nil {
				return g, nil, err
			}
			g.Agent = v
		case "cwd":
			v, err := takeValue()
			if err != nil {
				return g, nil, err
			}
			g.CWD = v
		case "log-level":
			v, err := takeValue()
			if err != nil {
				return g, nil, err
			}
			g.LogLevel = v
		case "json":
			g.JSON = true
		case "no-color":
			g.NoColor = true
		default:
			return g, nil, fmt.Errorf("unknown global flag --%s", name)
		}
	}
	if rest == nil {
		rest = []string{}
	}
	return g, rest, nil
}

func commandInit(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "overwrite generated files")
	agentName := fs.String("agent", g.Agent, "default agent")
	installSkills := fs.Bool("skills", true, "install the default skill through npx skills")
	syncAgentSkills := fs.Bool("sync-agent-skills", false, "sync skills into configured agent targets")
	base := fs.String("base", "", "base branch")
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, fmt.Errorf("not inside a git repository: %w", err)}
	}
	baseBranch := *base
	selectedAgent := strings.TrimSpace(*agentName)
	if selectedAgent == "" {
		selectedAgent = "codex"
	}
	cfg := config.Defaults()
	cfg.Agent.Default = selectedAgent
	if baseBranch != "" {
		cfg.Git.BaseBranch = baseBranch
	}
	skillDir := skills.PreferredInstallDir(root, cfg)
	if *installSkills {
		installedDir, err := installDefaultSkillWithSkillsCLI(ctx, root, cfg, selectedAgent, *force, g.JSON)
		if err != nil {
			return codedError{1, err}
		}
		if installedDir != "" {
			skillDir = installedDir
		}
	}
	cfg.Skills.SourceDir = filepath.ToSlash(rel(root, skillDir))
	if target, ok := cfg.Skills.Targets["codex"]; ok && target.Mode == "off" {
		target.Path = cfg.Skills.SourceDir
		cfg.Skills.Targets["codex"] = target
	}
	configPath := filepath.Join(root, ".loop", "config.yaml")
	initConfig, err := minimalInitConfig(selectedAgent, baseBranch, cfg.Skills.SourceDir)
	if err != nil {
		return codedError{1, err}
	}
	if err := writeUnlessExists(configPath, initConfig, *force); err != nil {
		return codedError{1, err}
	}
	ignorePath := filepath.Join(root, ".loop", ".gitignore")
	if err := writeUnlessExists(ignorePath, []byte("runs/\nworktrees/\ntmp/\nlocks/\nloop.db\n*.db\n*.db-wal\n*.db-shm\n*.log\n"), *force); err != nil {
		return codedError{1, err}
	}
	if *syncAgentSkills {
		if _, err := skills.Sync(root, cfg); err != nil {
			return codedError{1, err}
		}
	}
	out := map[string]any{"config": rel(root, configPath), "agent": selectedAgent, "skills": rel(root, skillDir)}
	baseLine := "Base: current branch at run start\n"
	if baseBranch != "" {
		out["base"] = baseBranch
		baseLine = fmt.Sprintf("Base: %s\n", baseBranch)
	}
	return printResult(g, out, fmt.Sprintf("Initialized loop in %s\nConfig: %s\nSkills: %s\n%s", root, rel(root, configPath), rel(root, skillDir), baseLine))
}

func commandRun(ctx context.Context, g globals, args []string) error {
	args = flagsFirst(args, map[string]bool{
		"agent": true, "goal": true, "max-iterations": true, "base": true,
		"resume": true, "from-iteration": true, "keep-branches": true, "keep-worktrees": true,
	})
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentName := fs.String("agent", g.Agent, "agent adapter")
	goal := fs.String("goal", "", "natural-language stop condition")
	maxIterations := fs.Int("max-iterations", 0, "maximum iterations, 0 for unlimited")
	prFlag := fs.Bool("pr", false, "use pull request integration")
	base := fs.String("base", "", "base branch")
	resumeID := fs.String("resume", "", "resume run id")
	fromIteration := fs.Int("from-iteration", 0, "resume from iteration")
	keepBranches := fs.String("keep-branches", "", "branch cleanup mode")
	keepWorktrees := fs.String("keep-worktrees", "", "worktree cleanup mode")
	dryRun := fs.Bool("dry-run", false, "build prompt and state files only")
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
	instructionArg := fs.Arg(0)
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, fmt.Errorf("not inside a git repository: %w", err)}
	}
	instructionPath := instructionArg
	if !filepath.IsAbs(instructionPath) {
		instructionPath = filepath.Join(root, instructionPath)
	}
	if _, err := os.Stat(instructionPath); err != nil {
		return codedError{2, fmt.Errorf("invalid instruction file: %w", err)}
	}
	overrides := config.Overrides{Agent: *agentName, BaseBranch: *base, NoColor: g.NoColor}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "max-iterations" {
			overrides.MaxIterations = maxIterations
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
	defer func() {
		renderer.Stop(state.Stage, "")
	}()
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
	var lastResult *validation.IterationResult
	var pendingGitHubUpdates []memory.Record
	mergedCount := 0
	for i := 1; cfg.Run.MaxIterations == 0 || i <= cfg.Run.MaxIterations; i++ {
		iterationID := runstate.IterationID(i)
		renderer.Stage(runstate.StageBranchCreated, "starting iteration "+iterationID)
		iterDir := filepath.Join(runDir, "iterations", iterationID)
		activeDir := iterDir
		if !*dryRun {
			var err error
			activeDir, err = createIterationTempDir(runID, iterationID)
			if err != nil {
				return codedError{1, err}
			}
		}
		initialBranch := gitx.InitialBranchName(i)
		renderer.Branch(initialBranch)
		state.CurrentIteration = iterationID
		state.Stage = runstate.StageBranchCreated
		state.Iterations = append(state.Iterations, runstate.IterationRecord{IterationID: iterationID, BranchInitial: initialBranch, BranchCurrent: initialBranch, Stage: string(state.Stage)})
		_ = runstate.Write(statePath, state)
		workDir := root
		branchRunner := runner
		worktreePath := ""
		cleanup := &iterationCleanup{
			RootRunner: runner,
			BaseBranch: cfg.Git.BaseBranch,
			Branch:     initialBranch,
			WorkDir:    root,
			OnEvent:    renderer.AgentEvent,
		}
		defer cleanup.OnCancel(ctx, statePath, &state)
		if !*dryRun {
			worktreePath = filepath.Join(root, ".loop", "worktrees", runID, iterationID)
			cleanup.WorktreePath = worktreePath
			cleanup.Active = true
			if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
				return codedError{1, err}
			}
			if _, err := runner.Run(ctx, "worktree", "add", "-b", initialBranch, worktreePath, cfg.Git.BaseBranch); err != nil {
				return codedError{1, err}
			}
			renderer.Stage(runstate.StageBranchCreated, "created worktree "+rel(root, worktreePath))
			workDir = worktreePath
			branchRunner = gitx.Runner{Dir: workDir}
			cleanup.WorkDir = workDir
		}
		paths := promptPathsWithActive(iterDir, activeDir)
		paths.Goal = *goal
		paths.Language = cfg.Language.Default
		paths.RunID = runID
		paths.IterationID = iterationID
		paths.BaseBranch = cfg.Git.BaseBranch
		paths.InitialBranch = initialBranch
		paths.CurrentBranch = initialBranch
		paths.IntegrationMode = cfg.Git.Integration.Mode
		paths.PullRequestMode = cfg.Git.Integration.Mode == "pr"
		paths.WorkDir = workDir
		cleanup.EventLogPath = paths.Events
		renderer.Iteration(iterationID, paths.Todo)
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			return codedError{1, err}
		}
		if err := config.WriteEffective(paths.EffectiveConfig, cfg); err != nil {
			return codedError{1, err}
		}
		instructionContent, err := os.ReadFile(instructionPath)
		if err != nil {
			return codedError{1, fmt.Errorf("read instruction file: %w", err)}
		}
		if err := prompt.WritePrompt(paths.Prompt, instructionContent); err != nil {
			return codedError{1, err}
		}
		if err := writeRuntimeArtifact(paths); err != nil {
			return codedError{1, err}
		}
		if *dryRun {
			state.Stage = runstate.StageCompleted
			_ = runstate.Write(statePath, state)
			return printResult(g, map[string]any{"run_id": runID, "prompt": rel(root, paths.Prompt)}, fmt.Sprintf("Dry run created prompt: %s\n", rel(root, paths.Prompt)))
		}
		if i > 1 {
			syncMemoryBeforeIteration(ctx, root, cfg, paths)
		}
		if len(pendingGitHubUpdates) > 0 {
			writeGitHubUpdatesArtifact(iterDir, pendingGitHubUpdates)
			_ = runstate.AppendEvent(paths.Events, runstate.Event{
				"type":    "github_sleep.updates_delivered",
				"records": len(pendingGitHubUpdates),
			})
			pendingGitHubUpdates = nil
		}
		state.Stage = runstate.StageAgentRunning
		_ = runstate.Write(statePath, state)
		renderer.Stage(runstate.StageAgentRunning, "agent running")
		result, err := runAgentAndReadResult(ctx, cfg, workDir, &paths, renderer.AgentEvent)
		if err != nil {
			return codedError{4, err}
		}
		renderer.Branch(paths.CurrentBranch)
		cleanup.Branch = paths.CurrentBranch
		state.Iterations[len(state.Iterations)-1].BranchCurrent = paths.CurrentBranch
		lastResult = result
		mergedPR := paths.PullRequestMode && prStateMerged(iterDir)
		state.Iterations[len(state.Iterations)-1].SummarySentence = terminalSummary(result)
		state.Iterations[len(state.Iterations)-1].ShouldFullyStop = result.ShouldFullyStop

		if result.Action == "skip_merge" {
			if err := finalizeSkipMerge(ctx, runner, cleanup, workDir, iterDir, cfg, paths, "skip merge: "+result.SkipMergeReason); err != nil {
				state.Stage = runstate.StageFailed
				_ = runstate.Write(statePath, state)
				return codedError{1, err}
			}
			if updates, slept, err := maybeSleepForGitHubUpdates(ctx, root, cfg, result, paths, renderer); err != nil {
				state.Stage = runstate.StageFailed
				_ = runstate.Write(statePath, state)
				return codedError{4, err}
			} else if slept {
				pendingGitHubUpdates = updates
				continue
			}
			if result.ShouldFullyStop {
				state.Stage = runstate.StageCompleted
				_ = runstate.Write(statePath, state)
				break
			}
			continue
		}
		if result.Action != "merge" {
			return codedError{4, fmt.Errorf("unknown iteration close action %q", result.Action)}
		}
		state.Stage = runstate.StageValidating
		_ = runstate.Write(statePath, state)
		renderer.Stage(runstate.StageValidating, "running validation")
		if !mergedPR {
			if err := ensureIterationBranch(ctx, branchRunner, paths.CurrentBranch); err != nil {
				return codedError{4, err}
			}
			validationResults, validationErr := runConfiguredValidation(ctx, workDir, paths, cfg.Validation.Commands)
			if validationErr != nil {
				reason := fmt.Sprintf("validation error: %v", validationErr)
				appendErrorLog(paths.Errors, reason)
				if err := finalizeSkipMerge(ctx, runner, cleanup, workDir, iterDir, cfg, paths, reason); err != nil {
					state.Stage = runstate.StageFailed
					_ = runstate.Write(statePath, state)
					return codedError{1, err}
				}
				continue
			}
			if validation.StatusFromResults(validationResults) == "failed" {
				reason := "required validation failed"
				appendErrorLog(paths.Errors, reason)
				if err := finalizeSkipMerge(ctx, runner, cleanup, workDir, iterDir, cfg, paths, reason); err != nil {
					state.Stage = runstate.StageFailed
					_ = runstate.Write(statePath, state)
					return codedError{1, err}
				}
				continue
			}
		}
		var commits []gitx.Commit
		if mergedPR {
			commits = commitsFromResult(result)
		} else {
			commits, err = runner.ListCommits(ctx, cfg.Git.BaseBranch, paths.CurrentBranch)
			if err != nil {
				return codedError{1, err}
			}
		}
		renderer.Commits(len(commits))
		if !mergedPR {
			if len(commits) == 0 {
				return codedError{4, fmt.Errorf("merge iteration did not create commits")}
			}
			if err := validateIterationCommitSubjects(commits); err != nil {
				return codedError{4, err}
			}
			clean, err := branchRunner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
			if err != nil {
				return codedError{1, err}
			}
			if !clean.Clean && !mergedPR {
				return codedError{4, fmt.Errorf("working tree is dirty after agent: %s", dirtyList(clean.Dirty))}
			}
			if !mergedPR {
				if err := ensureIterationBranch(ctx, branchRunner, paths.CurrentBranch); err != nil {
					return codedError{4, err}
				}
			}
		}
		finalBranch := paths.CurrentBranch
		renderer.Branch(finalBranch)
		cleanup.Branch = finalBranch
		state.Iterations[len(state.Iterations)-1].BranchFinal = finalBranch
		state.Stage = runstate.StageIntegrating
		_ = runstate.Write(statePath, state)
		renderer.Stage(runstate.StageIntegrating, "integrating "+finalBranch)
		if cfg.Git.Integration.Mode == "pr" {
			if !prStateMerged(iterDir) {
				state.Stage = runstate.StageFailed
				_ = runstate.Write(statePath, state)
				return codedError{6, fmt.Errorf("merge close requires a merged PR; run `loop pr merge` before `loop iteration close --merge`")}
			}
			mergedCount++
			renderer.Merged(mergedCount)
			if err := finalizeAgentOwnedPR(ctx, runner, cleanup, worktreePath, root, cfg, finalBranch); err != nil {
				state.Stage = runstate.StageFailed
				_ = runstate.Write(statePath, state)
				return codedError{6, err}
			}
			worktreePath = ""
			cleanup.Integrated = true
		} else if len(commits) > 0 {
			if worktreePath != "" {
				if err := runner.RemoveWorktree(ctx, worktreePath, false); err != nil {
					return codedError{6, err}
				}
			}
			cleanup.DirectIntegrating = true
			baseHead, _ := runner.Run(context.Background(), "rev-parse", cfg.Git.BaseBranch)
			cleanup.BaseHead = strings.TrimSpace(baseHead)
			if err := runner.SquashMerge(ctx, cfg.Git.BaseBranch, finalBranch, result.SummarySentence, false); err != nil {
				state.Stage = runstate.StageFailed
				_ = runstate.Write(statePath, state)
				return codedError{6, err}
			}
			mergedCount++
			renderer.Merged(mergedCount)
			cleanup.Integrated = true
			_ = runner.DeleteBranch(ctx, finalBranch, true)
		}
		if err := refreshTargetBranch(ctx, runner, cfg.Git.BaseBranch); err != nil {
			state.Stage = runstate.StageFailed
			_ = runstate.Write(statePath, state)
			return codedError{1, err}
		}
		if issues := cleanupDisposableIterationFiles(paths.ActiveDir, paths.Events); len(issues) > 0 {
			appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
		}
		if result.ShouldFullyStop {
			state.Stage = runstate.StageCompleted
			_ = runstate.Write(statePath, state)
			break
		}
	}
	if state.Stage != runstate.StageCompleted {
		state.Stage = runstate.StageCompleted
		_ = runstate.Write(statePath, state)
	}
	summary := ""
	if lastResult != nil {
		summary = terminalSummary(lastResult)
	}
	return printResult(g, map[string]any{"run_id": runID, "status": state.Stage, "summary": summary, "logs": rel(root, runDir)}, fmt.Sprintf("Run: %s\nStatus: %s\nSummary: %s\nLogs: %s\n", runID, state.Stage, summary, rel(root, runDir)))
}

func installDefaultSkillWithSkillsCLI(ctx context.Context, root string, cfg config.Config, agentName string, force bool, quiet bool) (string, error) {
	if !force {
		if dir, ok, err := findDefaultSkillDir(root, cfg); err != nil {
			return "", err
		} else if ok {
			return dir, nil
		}
	}
	skillsAgent := skillsCLIAgentName(agentName)
	args := []string{
		"--yes",
		"skills",
		"add",
		defaultSkillPackage,
		"--skill",
		"loop",
		"--agent",
		skillsAgent,
		"--yes",
	}
	if err := runSkillsCLIAdd(ctx, root, args, quiet); err != nil {
		return "", err
	}
	if dir, ok, err := findDefaultSkillDir(root, cfg); err != nil {
		return "", err
	} else if ok {
		return dir, nil
	}
	return filepath.Join(root, skillsCLIDefaultDir(skillsAgent)), nil
}

func runSkillsCLIAddCommand(ctx context.Context, root string, args []string, quiet bool) error {
	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = root
	if quiet {
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Run(); err != nil {
			text := strings.TrimSpace(output.String())
			if text == "" {
				return fmt.Errorf("install loop skill with npx skills: %w", err)
			}
			return fmt.Errorf("install loop skill with npx skills: %w\n%s", err, text)
		}
		return nil
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install loop skill with npx skills: %w", err)
	}
	return nil
}

func findDefaultSkillDir(root string, cfg config.Config) (string, bool, error) {
	dirs := skills.DiscoverDirs(root, cfg)
	if len(dirs) == 0 {
		dirs = []skills.Directory{{Agent: "project", Path: skills.PreferredInstallDir(root, cfg)}}
	}
	for _, dir := range dirs {
		path := filepath.Join(dir.Path, "loop", "SKILL.md")
		if _, err := os.Stat(path); err == nil {
			return dir.Path, true, nil
		} else if !os.IsNotExist(err) {
			return "", false, err
		}
	}
	return "", false, nil
}

func skillsCLIAgentName(agentName string) string {
	switch strings.TrimSpace(agentName) {
	case "", "codex":
		return "codex"
	case "claude":
		return "claude-code"
	case "gemini":
		return "gemini-cli"
	default:
		return strings.TrimSpace(agentName)
	}
}

func skillsCLIDefaultDir(agentName string) string {
	switch agentName {
	case "claude-code":
		return ".claude/skills"
	case "cline":
		return ".cline/skills"
	case "codebuddy":
		return ".codebuddy/skills"
	case "cursor":
		return ".cursor/skills"
	case "gemini-cli":
		return ".gemini/skills"
	case "opencode":
		return ".opencode/skills"
	default:
		return skills.CanonicalProjectDir
	}
}

func minimalInitConfig(agentName, baseBranch, skillSourceDir string) ([]byte, error) {
	cfg := minimalInitConfigFile{
		Version: 1,
	}
	if baseBranch != "" {
		cfg.Git = &minimalGitConfig{BaseBranch: baseBranch}
	}
	if agentName != "" && agentName != "codex" {
		agent := &minimalAgentConfig{Default: agentName}
		if agentName != "fake" {
			agent.Adapters = map[string]minimalAdapterConfig{
				agentName: {Command: agentName},
			}
		}
		cfg.Agent = agent
	}
	if skillSourceDir != "" && filepath.ToSlash(skillSourceDir) != skills.CanonicalProjectDir {
		cfg.Skills = &minimalSkillsConfig{SourceDir: filepath.ToSlash(skillSourceDir)}
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func shouldConfirmTargetBranch(targetBranch, mainBranch string) bool {
	targetBranch = strings.TrimSpace(targetBranch)
	mainBranch = strings.TrimSpace(mainBranch)
	return targetBranch != "" && mainBranch != "" && targetBranch != mainBranch
}

type minimalInitConfigFile struct {
	Version int                  `yaml:"version"`
	Agent   *minimalAgentConfig  `yaml:"agent,omitempty"`
	Skills  *minimalSkillsConfig `yaml:"skills,omitempty"`
	Git     *minimalGitConfig    `yaml:"git,omitempty"`
}

type minimalAgentConfig struct {
	Default  string                          `yaml:"default"`
	Adapters map[string]minimalAdapterConfig `yaml:"adapters,omitempty"`
}

type minimalAdapterConfig struct {
	Command string `yaml:"command"`
}

type minimalSkillsConfig struct {
	SourceDir string `yaml:"sourceDir"`
}

type minimalGitConfig struct {
	BaseBranch string `yaml:"baseBranch,omitempty"`
}

type iterationCleanup struct {
	Active            bool
	Integrated        bool
	DirectIntegrating bool
	RootRunner        gitx.Runner
	BaseBranch        string
	BaseHead          string
	Branch            string
	WorkDir           string
	WorktreePath      string
	EventLogPath      string
	OnEvent           func(runstate.Event)
}

func (c *iterationCleanup) OnCancel(parent context.Context, statePath string, state *runstate.State) {
	if parent.Err() == nil || !c.Active || c.Integrated {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	c.appendEvent(runstate.Event{"type": "run.cancelled_cleanup.started", "branch": c.Branch, "worktree": c.WorktreePath})
	issues := c.cleanup(ctx)
	if state != nil {
		state.Stage = runstate.StageCancelled
		if len(state.Iterations) > 0 {
			state.Iterations[len(state.Iterations)-1].Stage = string(runstate.StageCancelled)
		}
		_ = runstate.Write(statePath, *state)
	}
	event := runstate.Event{"type": "run.cancelled_cleanup.completed", "branch": c.Branch, "worktree": c.WorktreePath}
	if len(issues) > 0 {
		event["issues"] = issues
	}
	c.appendEvent(event)
}

func (c *iterationCleanup) appendEvent(event runstate.Event) {
	if c.EventLogPath != "" {
		_ = runstate.AppendEvent(c.EventLogPath, event)
	} else if _, ok := event["ts"]; !ok {
		event["ts"] = time.Now().UTC().Format(time.RFC3339)
	}
	if c.OnEvent != nil {
		c.OnEvent(event)
	}
}

func (c *iterationCleanup) cleanup(ctx context.Context) []string {
	var issues []string
	record := func(action string, err error) {
		if err != nil {
			issues = append(issues, action+": "+err.Error())
		}
	}

	if c.DirectIntegrating {
		_, _ = c.RootRunner.Run(ctx, "checkout", c.BaseBranch)
		head, err := c.RootRunner.Head(ctx)
		if err == nil && c.BaseHead != "" && head != c.BaseHead {
			c.Integrated = true
			if c.Branch != "" && c.Branch != c.BaseBranch {
				_ = c.RootRunner.DeleteBranch(ctx, c.Branch, true)
			}
			return issues
		}
		if c.BaseHead != "" {
			_, err = c.RootRunner.Run(ctx, "reset", "--hard", c.BaseHead)
		} else {
			_, err = c.RootRunner.Run(ctx, "reset", "--hard")
		}
		record("reset integration state", err)
		_, err = runGitCleanPreservingLoopRuntime(ctx, c.RootRunner)
		record("clean integration state", err)
		if c.Branch != "" && c.Branch != c.BaseBranch {
			record("delete branch "+c.Branch, c.RootRunner.DeleteBranch(ctx, c.Branch, true))
		}
		return issues
	}

	if c.WorktreePath != "" {
		record("remove worktree "+c.WorktreePath, c.RootRunner.RemoveWorktree(ctx, c.WorktreePath, true))
		removeEmptyDir(filepath.Dir(c.WorktreePath))
		removeEmptyDir(filepath.Dir(filepath.Dir(c.WorktreePath)))
		_, _ = c.RootRunner.Run(ctx, "worktree", "prune")
		if c.Branch != "" && c.Branch != c.BaseBranch {
			record("delete branch "+c.Branch, c.RootRunner.DeleteBranch(ctx, c.Branch, true))
		}
		return issues
	}

	workDir := c.WorkDir
	if workDir == "" {
		workDir = c.RootRunner.Dir
	}
	workRunner := gitx.Runner{Dir: workDir}
	_, err := workRunner.Run(ctx, "reset", "--hard")
	record("reset working tree", err)
	_, err = runGitCleanPreservingLoopRuntime(ctx, workRunner)
	record("clean working tree", err)
	_, err = c.RootRunner.Run(ctx, "checkout", c.BaseBranch)
	record("checkout "+c.BaseBranch, err)
	if c.Branch != "" && c.Branch != c.BaseBranch {
		record("delete branch "+c.Branch, c.RootRunner.DeleteBranch(ctx, c.Branch, true))
	}
	return issues
}

func removeEmptyDir(path string) {
	if path == "." || path == string(filepath.Separator) {
		return
	}
	_ = os.Remove(path)
}

func createIterationTempDir(runID, iterationID string) (string, error) {
	prefix := "loop-" + sanitizeTempPart(runID) + "-" + sanitizeTempPart(iterationID) + "-"
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".loop-active-temp"), []byte("1\n"), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func sanitizeTempPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "iteration"
	}
	return out
}

func cleanupDisposableIterationFiles(activeDir, eventLogPath string) []string {
	if strings.TrimSpace(activeDir) == "" {
		return nil
	}
	var removed []string
	var issues []string
	marker := filepath.Join(activeDir, ".loop-active-temp")
	if _, err := os.Stat(marker); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		issues = append(issues, "active temp marker: "+err.Error())
		return issues
	}
	if err := filepath.WalkDir(activeDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			issues = append(issues, path+": "+err.Error())
			return nil
		}
		if !entry.IsDir() {
			name, relErr := filepath.Rel(activeDir, path)
			if relErr != nil {
				name = filepath.Base(path)
			}
			if name != ".loop-active-temp" {
				removed = append(removed, name)
			}
		}
		return nil
	}); err != nil {
		issues = append(issues, "walk active temp dir: "+err.Error())
	}
	if err := os.RemoveAll(activeDir); err != nil {
		issues = append(issues, "remove active temp dir: "+err.Error())
	}
	if eventLogPath != "" {
		event := runstate.Event{"type": "iteration.active_temp.cleanup.completed", "active_dir": activeDir, "removed": removed}
		if len(issues) > 0 {
			event["issues"] = issues
		}
		_ = runstate.AppendEvent(eventLogPath, event)
	}
	return issues
}

func runGitCleanPreservingLoopRuntime(ctx context.Context, runner gitx.Runner) (string, error) {
	args := []string{
		"clean", "-fd",
		"-e", ".loop/runs/",
		"-e", ".loop/worktrees/",
		"-e", ".loop/tmp/",
		"-e", ".loop/locks/",
		"-e", ".loop/loop.db",
		"-e", ".loop/*.db",
		"-e", ".loop/*.db-wal",
		"-e", ".loop/*.db-shm",
		"-e", ".loop/*.log",
	}
	return runner.Run(ctx, args...)
}

func ensureIterationBranch(ctx context.Context, runner gitx.Runner, expected string) error {
	current, err := runner.CurrentBranch(ctx)
	if err != nil {
		return fmt.Errorf("inspect iteration branch: %w", err)
	}
	if current != expected {
		return fmt.Errorf("current branch is %q, but loop runtime tracks %q; use `loop branch rename ...` for branch changes and do not switch branches directly", current, expected)
	}
	return nil
}

func removeWorktreeBeforePRIntegration(ctx context.Context, runner gitx.Runner, cleanup *iterationCleanup, worktreePath, root string) error {
	if worktreePath == "" {
		return nil
	}
	if _, err := os.Stat(worktreePath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		_, _ = runner.Run(ctx, "worktree", "prune")
		if cleanup != nil {
			cleanup.WorktreePath = ""
			cleanup.WorkDir = root
		}
		return nil
	}
	if err := runner.RemoveWorktree(ctx, worktreePath, true); err != nil {
		return err
	}
	removeEmptyDir(filepath.Dir(worktreePath))
	removeEmptyDir(filepath.Dir(filepath.Dir(worktreePath)))
	_, _ = runner.Run(ctx, "worktree", "prune")
	if cleanup != nil {
		cleanup.WorktreePath = ""
		cleanup.WorkDir = root
	}
	return nil
}

func preparePRMerge(ctx context.Context, runner gitx.Runner, cleanup *iterationCleanup, worktreePath, root, base string) error {
	if err := removeWorktreeBeforePRIntegration(ctx, runner, cleanup, worktreePath, root); err != nil {
		return err
	}
	if base != "" {
		if _, err := runner.Run(ctx, "checkout", base); err != nil {
			return err
		}
	}
	if cleanup != nil {
		cleanup.WorkDir = root
	}
	return nil
}

func finalizeSkipMerge(ctx context.Context, runner gitx.Runner, cleanup *iterationCleanup, workDir, iterDir string, cfg config.Config, paths pathSet, reason string) error {
	if strings.TrimSpace(reason) != "" {
		appendErrorLog(paths.Errors, reason)
	}
	if err := closeUnmergedPR(ctx, cfg, workDir, iterDir, paths); err != nil {
		appendErrorLog(paths.Errors, "skip-merge PR cleanup failed: "+err.Error())
	}
	branch := ""
	if cleanup != nil {
		branch = cleanup.Branch
		if cleanup.Active {
			if issues := cleanup.cleanup(ctx); len(issues) > 0 {
				return fmt.Errorf("skip-merge cleanup failed: %s", strings.Join(issues, "; "))
			}
			cleanup.Integrated = true
		}
	}
	if branch != "" && branch != cfg.Git.BaseBranch {
		if err := deleteRemoteBranchAfterPRMerge(ctx, runner, branch); err != nil {
			_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "pr.remote_branch_cleanup.failed", "branch": branch, "error": err.Error()})
		}
	}
	pruneRemoteBranches(ctx, runner)
	if err := refreshTargetBranch(ctx, runner, cfg.Git.BaseBranch); err != nil {
		return err
	}
	if issues := cleanupDisposableIterationFiles(paths.ActiveDir, paths.Events); len(issues) > 0 {
		appendErrorLog(paths.Errors, "iteration file cleanup failed: "+strings.Join(issues, "; "))
	}
	_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "iteration.skip_merge", "reason": strings.TrimSpace(reason)})
	return nil
}

func closeUnmergedPR(ctx context.Context, cfg config.Config, workDir, iterDir string, paths pathSet) error {
	if cfg.Git.Integration.Mode != "pr" {
		return nil
	}
	state, ok, err := readPRState(iterDir)
	if err != nil || !ok || strings.TrimSpace(state.PR) == "" || state.Status == "merged" || state.Status == "closed" {
		return err
	}
	runner := prRunner(cfg, workDir)
	if _, err := runner.Close(ctx, state.PR); err != nil {
		return err
	}
	state.Status = "closed"
	if err := writePRState(iterDir, state); err != nil {
		return err
	}
	_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "pr.closed", "pr": state.PR})
	return nil
}

func refreshTargetBranch(ctx context.Context, runner gitx.Runner, base string) error {
	if strings.TrimSpace(base) == "" {
		return nil
	}
	if _, err := runner.Run(ctx, "checkout", base); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		return nil
	}
	_, err := runner.Run(ctx, "pull", "--ff-only")
	return err
}

func terminalSummary(result *validation.IterationResult) string {
	if result == nil {
		return ""
	}
	if strings.TrimSpace(result.SummarySentence) != "" {
		return strings.TrimSpace(result.SummarySentence)
	}
	return strings.TrimSpace(result.SkipMergeReason)
}

func maybeSleepForGitHubUpdates(ctx context.Context, root string, cfg config.Config, result *validation.IterationResult, paths pathSet, renderer *runRenderer) ([]memory.Record, bool, error) {
	if result == nil || result.Action != "skip_merge" || !result.SleepUntilGitHubUpdate {
		return nil, false, nil
	}
	if _, _, _, err := memory.ResolveGitHubRepository(ctx, root); err != nil {
		return nil, false, err
	}
	updates, err := waitForGitHubUpdates(ctx, root, cfg, paths, renderer)
	return updates, true, err
}

func waitForGitHubUpdates(ctx context.Context, root string, cfg config.Config, paths pathSet, renderer *runRenderer) ([]memory.Record, error) {
	for {
		if renderer != nil {
			renderer.SleepWaitingForGitHub()
		}
		_ = runstate.AppendEvent(paths.Events, runstate.Event{"type": "github_sleep.waiting"})
		if err := waitForGitHubSleepPoll(ctx, githubSleepPollInterval, renderer); err != nil {
			return nil, err
		}
		updates, err := memory.SyncGitHubUpdates(ctx, memory.SyncOptions{WorkDir: root, RunsDir: filepath.Join(root, cfg.Logs.Dir)})
		if err != nil {
			recordGitHubContextWarning(paths, err)
			if errors.Is(err, memory.ErrNoGitHubRemote) {
				return nil, err
			}
			continue
		}
		if len(updates.Records) == 0 {
			_ = runstate.AppendEvent(paths.Events, runstate.Event{
				"type": "github_sleep.no_updates",
				"repo": updates.Repo,
			})
			continue
		}
		_ = runstate.AppendEvent(paths.Events, runstate.Event{
			"type":    "github_sleep.updates",
			"repo":    updates.Repo,
			"records": len(updates.Records),
		})
		return updates.Records, nil
	}
}

func commandResume(ctx context.Context, g globals, args []string) error {
	args = flagsFirst(args, map[string]bool{
		"from-iteration": true,
	})
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	from := fs.Int("from-iteration", 0, "iteration")
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	_ = from
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop resume <run-id>")}
	}
	return commandStatus(ctx, g, []string{fs.Arg(0)})
}

func commandStatus(ctx context.Context, g globals, args []string) error {
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, err}
	}
	cfg, _ := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent}})
	runID := ""
	if len(args) > 0 {
		runID = args[0]
	} else {
		runID, err = latestRun(filepath.Join(root, cfg.Logs.Dir))
		if err != nil {
			return codedError{1, err}
		}
	}
	state, err := runstate.Read(filepath.Join(root, cfg.Logs.Dir, runID, "run-state.json"))
	if err != nil {
		return codedError{1, err}
	}
	next := "none"
	if state.Stage != runstate.StageCompleted {
		next = "resume or inspect logs"
	}
	lastSummary := ""
	if len(state.Iterations) > 0 {
		lastSummary = state.Iterations[len(state.Iterations)-1].SummarySentence
	}
	out := map[string]any{"run_id": state.RunID, "base": state.BaseBranch, "iteration": state.CurrentIteration, "stage": state.Stage, "last_summary": lastSummary, "next": next}
	text := fmt.Sprintf("Run: %s\nBase: %s\nIteration: %s\nStage: %s\nLast summary: %s\nNext: %s\nLogs: %s\n", state.RunID, state.BaseBranch, state.CurrentIteration, state.Stage, lastSummary, next, filepath.Join(cfg.Logs.Dir, runID))
	return printResult(g, out, text)
}

func commandLogs(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iteration := fs.String("iteration", "latest", "iteration")
	follow := fs.Bool("follow", false, "follow logs")
	fileName := fs.String("file", "", "file name")
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	_ = follow
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop logs <run-id>")}
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, err}
	}
	cfg, _ := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent}})
	iter := *iteration
	if iter == "latest" {
		iter, err = latestIteration(filepath.Join(root, cfg.Logs.Dir, fs.Arg(0), "iterations"))
		if err != nil {
			return codedError{1, err}
		}
	}
	name := *fileName
	if name == "" {
		name = "agent-events.jsonl"
	}
	iterDir := filepath.Join(root, cfg.Logs.Dir, fs.Arg(0), "iterations", iter)
	if artifact, err := lookupIterationArtifact(name); err == nil && artifact.Database {
		data, err := artifactdb.Read(iterDir, artifact.Name)
		if err != nil {
			return codedError{1, err}
		}
		fmt.Print(data)
		return nil
	}
	path := filepath.Join(iterDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return codedError{1, err}
	}
	fmt.Print(string(data))
	return nil
}

func commandSkills(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop skills <list|install|sync|doctor>")}
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, err}
	}
	cfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent}})
	if err != nil {
		return codedError{3, err}
	}
	sourceDir := skills.PreferredInstallDir(root, cfg)
	switch args[0] {
	case "list":
		list, err := skills.ListDiscovered(root, cfg)
		if err != nil {
			return codedError{1, err}
		}
		for _, s := range list {
			fmt.Printf("%s\t%s\n", s.Name, s.Description)
		}
	case "install":
		fs := flag.NewFlagSet("skills install", flag.ContinueOnError)
		force := fs.Bool("force", false, "overwrite")
		if err := fs.Parse(args[1:]); err != nil {
			return codedError{2, err}
		}
		if fs.NArg() != 1 {
			return codedError{2, fmt.Errorf("usage: loop skills install <name-or-path> [--force]")}
		}
		name := fs.Arg(0)
		if contains(skills.DefaultNames, name) {
			_, err = skills.InstallBuiltIn(root, sourceDir, name, *force)
		} else {
			_, err = skills.InstallFromPath(name, root, sourceDir, *force)
		}
		if err != nil {
			return codedError{1, err}
		}
		fmt.Println("installed", name)
	case "sync":
		results, err := skills.Sync(root, cfg)
		if err != nil {
			return codedError{1, err}
		}
		for _, r := range results {
			fmt.Printf("%s\t%s\t%s\n", r.Agent, r.Mode, rel(root, r.Path))
		}
	case "doctor":
		report := skills.DoctorDiscovered(root, cfg)
		if !report.OK() {
			for _, issue := range report.Errors {
				fmt.Println(issue)
			}
			return codedError{1, fmt.Errorf("skill doctor found %d issue(s)", len(report.Errors))}
		}
		fmt.Println("skills ok")
	default:
		return codedError{2, fmt.Errorf("unknown skills subcommand %q", args[0])}
	}
	return nil
}

func commandMemory(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop memory <recent|search>")}
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, err}
	}
	cfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent}})
	if err != nil {
		return codedError{3, err}
	}
	runsDir := filepath.Join(root, cfg.Logs.Dir)
	switch args[0] {
	case "recent":
		recentArgs := flagsFirst(args[1:], map[string]bool{"run": true, "repo": true, "limit": true})
		fs := flag.NewFlagSet("memory recent", flag.ContinueOnError)
		run := fs.String("run", "", "deprecated; ignored")
		repo := fs.String("repo", "", "GitHub owner/name")
		limit := fs.Int("limit", 0, "limit")
		if err := fs.Parse(recentArgs); err != nil {
			return codedError{2, err}
		}
		if err := requireLimitFlag(fs, limit); err != nil {
			return codedError{2, err}
		}
		_ = run
		items, err := memory.RecentWithRepo(runsDir, *repo, *limit)
		if err != nil {
			return codedError{1, err}
		}
		for _, item := range items {
			fmt.Println(formatMemoryRecord(item))
		}
	case "search":
		searchArgs := flagsFirst(args[1:], map[string]bool{
			"run": true, "iteration": true, "artifact": true, "repo": true, "limit": true,
		})
		fs := flag.NewFlagSet("memory search", flag.ContinueOnError)
		run := fs.String("run", "", "deprecated; ignored")
		iteration := fs.String("iteration", "", "deprecated; ignored")
		artifact := fs.String("artifact", "", "deprecated; ignored")
		repo := fs.String("repo", "", "GitHub owner/name")
		limit := fs.Int("limit", 0, "limit")
		if err := fs.Parse(searchArgs); err != nil {
			return codedError{2, err}
		}
		if err := requireLimitFlag(fs, limit); err != nil {
			return codedError{2, err}
		}
		if fs.NArg() != 1 {
			return codedError{2, fmt.Errorf("usage: loop memory search <query>")}
		}
		_, _, _ = run, iteration, artifact
		items, err := memory.SearchWithOptions(runsDir, memory.SearchOptions{
			Query: fs.Arg(0),
			Repo:  *repo,
			Limit: *limit,
		})
		if err != nil {
			return codedError{1, err}
		}
		for _, item := range items {
			fmt.Println(formatMemoryRecord(item))
		}
	default:
		return codedError{2, fmt.Errorf("unknown memory subcommand %q", args[0])}
	}
	return nil
}

func requireLimitFlag(fs *flag.FlagSet, limit *int) error {
	seen := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			seen = true
		}
	})
	if !seen {
		return errors.New("--limit is required")
	}
	if limit == nil || *limit <= 0 {
		return errors.New("--limit must be positive")
	}
	return nil
}

func commandIssue(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop issue <ask|report>")}
	}
	switch args[0] {
	case "ask":
		return commandIssueAsk(ctx, g, args[1:])
	case "report":
		return commandIssueReport(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown issue subcommand %q", args[0])}
	}
}

func commandIssueAsk(ctx context.Context, g globals, args []string) error {
	args = flagsFirst(args, map[string]bool{
		"title": true, "body": true, "body-file": true,
		"iteration-dir": true, "dir": true, "run": true, "iteration": true,
	})
	fs := flag.NewFlagSet("issue ask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	title := fs.String("title", "", "issue title")
	body := fs.String("body", "", "issue body")
	bodyFile := fs.String("body-file", "", "read issue body from file")
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop issue ask --title <text> --body <text>")}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if *bodyFile != "" {
		data, err := os.ReadFile(*bodyFile)
		if err != nil {
			return codedError{1, fmt.Errorf("read issue body file: %w", err)}
		}
		*body = string(data)
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, err}
	}
	storageRoot, err := loopStorageRoot(ctx)
	if err != nil {
		return codedError{1, err}
	}
	cfg, err := config.Load(config.LoadOptions{CWD: storageRoot, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent, NoColor: g.NoColor}})
	if err != nil {
		return codedError{3, err}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	metadataRunID := strings.TrimSpace(*runID)
	metadataIterationID := strings.TrimSpace(*iteration)
	if resolvedDir != "" {
		parsedRun, parsedIteration := artifactdb.ParseIterationDir(resolvedDir)
		if parsedRun != "" {
			metadataRunID = parsedRun
		}
		if parsedIteration != "" {
			metadataIterationID = parsedIteration
		}
	}
	record, err := memory.CreateIssueQuestion(ctx, memory.IssueQuestionOptions{
		WorkDir:     root,
		RunsDir:     filepath.Join(storageRoot, cfg.Logs.Dir),
		Title:       *title,
		Body:        *body,
		RunID:       metadataRunID,
		IterationID: metadataIterationID,
	})
	if err != nil {
		return codedError{1, err}
	}
	value := map[string]any{
		"number": record.Number,
		"url":    record.URL,
		"title":  record.Title,
	}
	return printResult(g, value, fmt.Sprintf("created issue #%d %s\n", record.Number, record.URL))
}

func commandIssueReport(ctx context.Context, g globals, args []string) error {
	args = flagsFirst(args, map[string]bool{
		"title": true, "body": true, "body-file": true, "kind": true,
		"iteration-dir": true, "dir": true, "run": true, "iteration": true,
	})
	fs := flag.NewFlagSet("issue report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	title := fs.String("title", "", "issue title")
	body := fs.String("body", "", "issue body")
	bodyFile := fs.String("body-file", "", "read issue body from file")
	kind := fs.String("kind", "other", "proposal kind: tool, docs, guardrail, observability, environment, workflow, or other")
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop issue report --title <text> --body <text> [--kind <kind>]")}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if *bodyFile != "" {
		data, err := os.ReadFile(*bodyFile)
		if err != nil {
			return codedError{1, fmt.Errorf("read issue body file: %w", err)}
		}
		*body = string(data)
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, err}
	}
	storageRoot, err := loopStorageRoot(ctx)
	if err != nil {
		return codedError{1, err}
	}
	cfg, err := config.Load(config.LoadOptions{CWD: storageRoot, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent, NoColor: g.NoColor}})
	if err != nil {
		return codedError{3, err}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	metadataRunID := strings.TrimSpace(*runID)
	metadataIterationID := strings.TrimSpace(*iteration)
	if resolvedDir != "" {
		parsedRun, parsedIteration := artifactdb.ParseIterationDir(resolvedDir)
		if parsedRun != "" {
			metadataRunID = parsedRun
		}
		if parsedIteration != "" {
			metadataIterationID = parsedIteration
		}
	}
	record, err := memory.CreateIssueReport(ctx, memory.IssueReportOptions{
		WorkDir:     root,
		RunsDir:     filepath.Join(storageRoot, cfg.Logs.Dir),
		Title:       *title,
		Body:        *body,
		Kind:        *kind,
		RunID:       metadataRunID,
		IterationID: metadataIterationID,
	})
	if err != nil {
		return codedError{1, err}
	}
	value := map[string]any{
		"number": record.Number,
		"url":    record.URL,
		"title":  record.Title,
		"kind":   *kind,
	}
	return printResult(g, value, fmt.Sprintf("reported issue #%d %s\n", record.Number, record.URL))
}

func formatMemoryRecord(item memory.Record) string {
	kind := item.Kind
	if kind == "" {
		kind = "pr"
	}
	return strings.Join([]string{
		cleanMemoryField(kind),
		fmt.Sprintf("#%d", item.Number),
		item.State,
		cleanMemoryField(item.Repo),
		cleanMemoryField(item.Title),
		cleanMemoryField(item.URL),
		cleanMemoryField(item.Excerpt),
	}, "\t")
}

func cleanMemoryField(value string) string {
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func syncMemoryBeforeRun(ctx context.Context, root string, cfg config.Config, renderer *runRenderer) error {
	runsDir := filepath.Join(root, cfg.Logs.Dir)
	repo, _, _, err := memory.ResolveGitHubRepository(ctx, root)
	if errors.Is(err, memory.ErrNoGitHubRemote) {
		return nil
	}
	if err != nil {
		return err
	}
	count, err := artifactdb.CountPRMemory(artifactdb.GlobalDBPathFromRunsPath(runsDir), repo)
	if err != nil {
		return err
	}
	lastSync, err := artifactdb.PRMemoryLastSync(artifactdb.GlobalDBPathFromRunsPath(runsDir), repo)
	if err != nil {
		return err
	}
	if lastSync == "" && renderer != nil {
		renderer.MemorySync(repo, true)
	}
	if _, err := memory.Sync(ctx, memory.SyncOptions{WorkDir: root, RunsDir: runsDir}); err != nil {
		if count == 0 && lastSync == "" {
			return fmt.Errorf("initial GitHub PR memory sync failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "warning: GitHub PR memory sync failed; using cached memory: %v\n", err)
		return nil
	}
	contextCount, err := artifactdb.CountGitHubContext(artifactdb.GlobalDBPathFromRunsPath(runsDir), repo, "issue")
	if err != nil {
		return err
	}
	contextLastSync, err := artifactdb.GitHubContextLastSync(artifactdb.GlobalDBPathFromRunsPath(runsDir), "github_context.issues.last_sync."+repo)
	if err != nil {
		return err
	}
	if _, err := memory.SyncIssues(ctx, memory.SyncOptions{WorkDir: root, RunsDir: runsDir}); err != nil {
		if contextCount == 0 && contextLastSync == "" {
			return fmt.Errorf("initial GitHub Issue context sync failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "warning: GitHub Issue context sync failed; using cached context: %v\n", err)
		return nil
	}
	return nil
}

func syncMemoryBeforeIteration(ctx context.Context, root string, cfg config.Config, paths pathSet) {
	runsDir := filepath.Join(root, cfg.Logs.Dir)
	_, _, _, err := memory.ResolveGitHubRepository(ctx, root)
	if errors.Is(err, memory.ErrNoGitHubRemote) {
		return
	}
	if err != nil {
		recordMemorySyncWarning(paths, err)
		return
	}
	result, err := memory.Sync(ctx, memory.SyncOptions{WorkDir: root, RunsDir: runsDir})
	if err != nil {
		recordMemorySyncWarning(paths, err)
		return
	}
	_ = runstate.AppendEvent(paths.Events, runstate.Event{
		"type":     "memory.sync.completed",
		"repo":     result.Repo,
		"full":     result.Full,
		"fetched":  result.Fetched,
		"upserted": result.Upserted,
		"deleted":  result.Deleted,
	})
	contextResult, err := memory.SyncGitHubUpdates(ctx, memory.SyncOptions{WorkDir: root, RunsDir: runsDir})
	if err != nil {
		recordGitHubContextWarning(paths, err)
		return
	}
	if len(contextResult.Records) > 0 {
		writeGitHubUpdatesArtifact(filepath.Dir(paths.Result), contextResult.Records)
	}
	_ = runstate.AppendEvent(paths.Events, runstate.Event{
		"type":     "github_context.sync.completed",
		"repo":     contextResult.Repo,
		"fetched":  contextResult.Fetched,
		"upserted": contextResult.Upserted,
	})
}

func recordMemorySyncWarning(paths pathSet, err error) {
	msg := fmt.Sprintf("GitHub PR memory sync failed; using cached memory: %v", err)
	appendErrorLog(paths.Errors, msg)
	_ = runstate.AppendEvent(paths.Events, runstate.Event{
		"type":  "memory.sync.failed",
		"error": err.Error(),
	})
}

func recordGitHubContextWarning(paths pathSet, err error) {
	msg := fmt.Sprintf("GitHub context sync failed; using cached context: %v", err)
	appendErrorLog(paths.Errors, msg)
	_ = runstate.AppendEvent(paths.Events, runstate.Event{
		"type":  "github_context.sync.failed",
		"error": err.Error(),
	})
}

func writeGitHubUpdatesArtifact(iterDir string, records []memory.Record) {
	if len(records) == 0 {
		return
	}
	_ = artifactdb.Write(iterDir, "github-updates", formatGitHubUpdates(records))
}

func formatGitHubUpdates(records []memory.Record) string {
	var b strings.Builder
	b.WriteString("# GitHub Updates\n\n")
	for _, record := range records {
		kind := firstNonEmpty(record.Kind, "github")
		title := strings.TrimSpace(record.Title)
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "- %s #%d %s %s\n", kind, record.Number, strings.TrimSpace(record.State), title)
		if strings.TrimSpace(record.URL) != "" {
			fmt.Fprintf(&b, "  URL: %s\n", strings.TrimSpace(record.URL))
		}
		if strings.TrimSpace(record.Author) != "" {
			fmt.Fprintf(&b, "  Author: %s\n", strings.TrimSpace(record.Author))
		}
		if strings.TrimSpace(record.Excerpt) != "" {
			fmt.Fprintf(&b, "  Excerpt: %s\n", strings.TrimSpace(record.Excerpt))
		}
	}
	return b.String()
}

func commandDoctor(ctx context.Context, g globals, args []string) error {
	_ = args
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, err}
	}
	cfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent}})
	if err != nil {
		return codedError{3, err}
	}
	var problems []string
	if _, err := exec.LookPath("go"); err != nil {
		problems = append(problems, "go binary not found")
	}
	if !gitx.IsRepository(ctx, root) {
		problems = append(problems, "git repository not found")
	}
	runner := gitx.Runner{Dir: root}
	if cfg.Git.BaseBranch == "" {
		cfg.Git.BaseBranch, _ = runner.CurrentBranch(ctx)
	}
	if ok, err := runner.BranchExists(ctx, cfg.Git.BaseBranch); err != nil || !ok {
		problems = append(problems, "base branch not found: "+cfg.Git.BaseBranch)
	}
	adapter, ok := cfg.Adapter(cfg.Agent.Default)
	if !ok {
		problems = append(problems, "agent adapter not configured: "+cfg.Agent.Default)
	} else if cfg.Agent.Default != "fake" {
		if _, err := exec.LookPath(adapter.Command); err != nil {
			problems = append(problems, "agent command not found: "+adapter.Command)
		}
	}
	if cfg.Git.Integration.Mode == "pr" {
		if _, err := exec.LookPath("gh"); err != nil {
			problems = append(problems, "gh command not found")
		}
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Println("fail:", p)
		}
		return codedError{1, fmt.Errorf("doctor found %d issue(s)", len(problems))}
	}
	return printResult(g, map[string]any{"status": "ok"}, "doctor ok\n")
}

type pathSet struct {
	IterationDir    string
	ActiveDir       string
	EffectiveConfig string
	Runtime         string
	Prompt          string
	Plan            string
	Todo            string
	Validation      string
	Result          string
	Events          string
	PRTitle         string
	PRBody          string
	Errors          string

	Goal             string
	Language         string
	RunID            string
	IterationID      string
	BaseBranch       string
	InitialBranch    string
	CurrentBranch    string
	BranchRenamed    bool
	IntegrationMode  string
	PullRequestMode  bool
	WorkDir          string
	AgentPromptExtra string
}

func promptPaths(iterDir string) pathSet {
	return promptPathsWithActive(iterDir, artifactdb.ActiveDirFromEnv(iterDir))
}

func promptPathsWithActive(iterDir, activeDir string) pathSet {
	if strings.TrimSpace(activeDir) == "" {
		activeDir = iterDir
	}
	return pathSet{
		IterationDir:    iterDir,
		ActiveDir:       activeDir,
		EffectiveConfig: filepath.Join(iterDir, "effective-config.yaml"),
		Runtime:         filepath.Join(activeDir, "runtime.json"),
		Prompt:          filepath.Join(iterDir, "prompt.md"),
		Plan:            filepath.Join(activeDir, "plan.md"),
		Todo:            filepath.Join(activeDir, "todo.md"),
		Validation:      filepath.Join(activeDir, "validation.md"),
		Result:          filepath.Join(iterDir, "result.json"),
		Events:          filepath.Join(iterDir, "agent-events.jsonl"),
		PRTitle:         filepath.Join(activeDir, "pr-title.txt"),
		PRBody:          filepath.Join(activeDir, "pr-body.md"),
		Errors:          filepath.Join(iterDir, "errors.log"),
	}
}

func writeRuntimeArtifact(paths pathSet) error {
	initialBranch := firstNonEmpty(paths.InitialBranch, paths.CurrentBranch)
	branchRenamed := paths.BranchRenamed
	if initialBranch != "" && paths.CurrentBranch != "" && initialBranch != paths.CurrentBranch {
		branchRenamed = true
	}
	data, err := json.MarshalIndent(map[string]any{
		"goal":              paths.Goal,
		"output_language":   paths.Language,
		"run_id":            paths.RunID,
		"iteration_id":      paths.IterationID,
		"base_branch":       paths.BaseBranch,
		"initial_branch":    initialBranch,
		"current_branch":    paths.CurrentBranch,
		"branch_renamed":    branchRenamed,
		"integration_mode":  paths.IntegrationMode,
		"pull_request_mode": paths.PullRequestMode,
		"workdir":           paths.WorkDir,
	}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return artifactdb.Write(filepath.Dir(paths.Runtime), "runtime", string(data))
}

func runAgent(ctx context.Context, cfg config.Config, root string, paths pathSet, onEvent func(runstate.Event)) error {
	adapterCfg, ok := cfg.Adapter(cfg.Agent.Default)
	if !ok {
		return fmt.Errorf("agent adapter not found: %s", cfg.Agent.Default)
	}
	if cfg.Agent.Default == "fake" {
		exe, err := os.Executable()
		if err == nil {
			adapterCfg.Command = exe
		}
		adapterCfg.Args = []string{"__fake-agent"}
	}
	promptText := buildAgentPrompt(paths)
	recordAgentPromptAudit(paths.ActiveDir, promptText)
	pa := agent.ProcessAdapter{
		AdapterName: cfg.Agent.Default,
		Command:     adapterCfg.Command,
		Args:        adapterCfg.Args,
		PromptMode:  agent.PromptMode(adapterCfg.Prompt),
		Env:         adapterCfg.Env,
	}
	env := map[string]string{
		"LOOP_RESULT_HANDOFF":            "master-db",
		"LOOP_ITERATION_DIR":             paths.IterationDir,
		artifactdb.ActiveIterationDirEnv: paths.ActiveDir,
		"LOOP_WORKDIR":                   root,
		"LOOP_RUN_GOAL":                  paths.Goal,
		"LOOP_OUTPUT_LANGUAGE":           paths.Language,
		"LOOP_RUN_ID":                    paths.RunID,
		"LOOP_ITERATION_ID":              paths.IterationID,
		"LOOP_BASE_BRANCH":               paths.BaseBranch,
		"LOOP_INITIAL_BRANCH":            firstNonEmpty(paths.InitialBranch, paths.CurrentBranch),
		"LOOP_CURRENT_BRANCH":            paths.CurrentBranch,
		"LOOP_BRANCH_RENAMED":            strconv.FormatBool(paths.BranchRenamed),
		"LOOP_INTEGRATION_MODE":          paths.IntegrationMode,
		"LOOP_PULL_REQUEST_MODE":         strconv.FormatBool(paths.PullRequestMode),
		"LOOP_PR_MODE":                   strconv.FormatBool(paths.PullRequestMode),
	}
	_, err := pa.Run(ctx, agent.RunRequest{
		WorkDir: root, Env: env, PromptText: promptText,
		IterationDir: paths.IterationDir, EventLogPath: paths.Events, ErrorsLogPath: paths.Errors,
		OnEvent: onEvent,
	})
	return err
}

func buildAgentPrompt(paths pathSet) string {
	text := prompt.Assemble(prompt.Request{PullRequestMode: paths.PullRequestMode})
	if strings.TrimSpace(paths.AgentPromptExtra) == "" {
		return text
	}
	return strings.TrimRight(text, "\n") + "\n\n" + strings.TrimLeft(paths.AgentPromptExtra, "\n")
}

func recordAgentPromptAudit(iterDir, text string) {
	entry := fmt.Sprintf("\n## Agent Prompt %s\n\n%s\n", time.Now().UTC().Format(time.RFC3339), strings.TrimRight(text, "\n"))
	_ = artifactdb.Append(iterDir, "agent-prompt-audit", entry)
}

func runAgentAndReadResult(ctx context.Context, cfg config.Config, workDir string, paths *pathSet, onEvent func(runstate.Event)) (*validation.IterationResult, error) {
	if paths == nil {
		return nil, errors.New("path set is required")
	}
	result, agentErr, resultErr := runAgentUntilResult(ctx, cfg, workDir, paths, onEvent)
	if err := refreshTrackedBranch(ctx, workDir, paths); err != nil {
		appendErrorLog(paths.Errors, fmt.Sprintf("branch tracking validation failed: %v", err))
		return nil, err
	}
	if ctx.Err() != nil {
		if agentErr != nil {
			return nil, fmt.Errorf("agent cancelled: %w", agentErr)
		}
		return nil, ctx.Err()
	}
	if resultErr != nil {
		appendErrorLog(paths.Errors, fmt.Sprintf("iteration close handoff validation failed: %v", resultErr))
		if agentErr != nil {
			return nil, fmt.Errorf("agent failed and iteration close handoff is invalid: %w; agent error: %v", resultErr, agentErr)
		}
		return nil, resultErr
	}
	return result, nil
}

func runAgentUntilResult(ctx context.Context, cfg config.Config, workDir string, paths *pathSet, onEvent func(runstate.Event)) (*validation.IterationResult, error, error) {
	globalPath := artifactdb.GlobalDBPathForIteration(filepath.Dir(paths.Result))
	if globalPath == "" {
		return nil, nil, errors.New("result handoff requires an iteration directory under .loop/runs")
	}
	_ = artifactdb.ClearResultHandoff(globalPath, paths.RunID, paths.IterationID)
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan error, 1)
	go func() {
		done <- runAgent(runCtx, cfg, workDir, *paths, onEvent)
	}()

	ticker := time.NewTicker(resultHandoffPollInterval)
	defer ticker.Stop()
	tickerC := ticker.C
	var handoffResult *validation.IterationResult
	for {
		select {
		case agentErr := <-done:
			if handoffResult != nil {
				return handoffResult, agentErr, nil
			}
			result, resultErr := validateResultHandoff(ctx, workDir, paths, globalPath)
			return result, agentErr, resultErr
		case <-tickerC:
			result, resultErr := validateResultHandoff(ctx, workDir, paths, globalPath)
			if resultErr == nil {
				handoffResult = result
				ticker.Stop()
				tickerC = nil
			}
		case <-ctx.Done():
			cancel(ctx.Err())
			agentErr := <-done
			return nil, agentErr, ctx.Err()
		}
	}
}

func validateResultHandoff(ctx context.Context, workDir string, paths *pathSet, globalPath string) (*validation.IterationResult, error) {
	data, err := artifactdb.ReadResultHandoff(globalPath, paths.RunID, paths.IterationID)
	if err != nil {
		return nil, err
	}
	result, err := validation.ValidateResultJSON([]byte(data))
	if err != nil {
		return nil, err
	}
	if err := refreshTrackedBranch(ctx, workDir, paths); err != nil {
		return nil, err
	}
	if err := validateResultBranchContract(result, *paths); err != nil {
		return nil, err
	}
	if err := validateResultGoalContract(result, *paths); err != nil {
		return nil, err
	}
	return result, nil
}

func commitsFromResult(result *validation.IterationResult) []gitx.Commit {
	if result == nil {
		return nil
	}
	commits := make([]gitx.Commit, 0, len(result.Commits))
	for _, commit := range result.Commits {
		commits = append(commits, gitx.Commit{Hash: commit.SHA, Subject: commit.Message})
	}
	return commits
}

func validateResultBranchContract(result *validation.IterationResult, paths pathSet) error {
	if result == nil {
		return errors.New("iteration close handoff is missing")
	}
	initialBranch := firstNonEmpty(paths.InitialBranch, paths.CurrentBranch)
	currentBranch := paths.CurrentBranch
	if result.Branch.InitialName != "" && initialBranch != "" && result.Branch.InitialName != initialBranch {
		return fmt.Errorf("result branch.initial_name is %q, but loop created %q", result.Branch.InitialName, initialBranch)
	}
	if result.Action == "merge" {
		if initialBranch == "" || currentBranch == "" || initialBranch == currentBranch {
			return fmt.Errorf("merge close requires a renamed branch; run `loop branch rename <kind>/<slug>` before `loop iteration close --merge`")
		}
		if result.Branch.FinalName == "" {
			return fmt.Errorf("merge close branch.final_name must match tracked branch %q; use `loop iteration close --merge` to generate it", currentBranch)
		}
		if result.Branch.FinalName != "" && result.Branch.FinalName != currentBranch {
			return fmt.Errorf("result branch.final_name is %q, but loop runtime tracks %q", result.Branch.FinalName, currentBranch)
		}
	}
	if paths.BranchRenamed && result.Branch.FinalName != "" && currentBranch != "" && result.Branch.FinalName != currentBranch {
		return fmt.Errorf("result branch.final_name is %q, but loop runtime tracks %q", result.Branch.FinalName, currentBranch)
	}
	if result.Action == "merge" && paths.PullRequestMode && !prStateMerged(filepath.Dir(paths.Result)) {
		return errors.New("merge close requires a merged PR; run `loop pr merge` before `loop iteration close --merge`")
	}
	return nil
}

func validateResultGoalContract(result *validation.IterationResult, paths pathSet) error {
	if result == nil {
		return errors.New("iteration close handoff is missing")
	}
	if strings.TrimSpace(paths.Goal) == "" && result.ShouldFullyStop {
		return errors.New("should_fully_stop=true requires a CLI --goal")
	}
	return nil
}

func runConfiguredValidation(ctx context.Context, root string, paths pathSet, commands []config.ValidationCommand) ([]validation.CommandResult, error) {
	var converted []validation.Command
	for _, command := range commands {
		converted = append(converted, validation.Command{Name: command.Name, Run: command.Run, Required: command.Required})
	}
	results, err := validation.Runner{WorkDir: root}.Run(ctx, converted)
	iterDir := firstNonEmpty(paths.ActiveDir, filepath.Dir(paths.Result))
	for i, result := range results {
		name := result.Name
		if name == "" {
			name = result.Command
		}
		if name == "" {
			name = "command"
		}
		outputName := sanitizeValidationOutputName(name)
		results[i].OutputPath = "validation-output-" + outputName + ".log"
		_ = artifactdb.WriteValidationOutput(iterDir, outputName, result.Output)
	}
	if writeErr := artifactdb.Write(iterDir, "validation", validation.FormatMarkdown(results)); writeErr != nil && err == nil {
		err = writeErr
	}
	if err != nil {
		appendErrorLog(paths.Errors, fmt.Sprintf("validation failed: %v", err))
	}
	return results, err
}

type prIntegrationSession struct {
	runner pr.Runner
	prID   string
}

func waitForPRChecks(ctx context.Context, session *prIntegrationSession, cfg config.Config) (pr.CommandResult, bool, error) {
	if delay := prChecksStartupDelay(cfg); delay > 0 {
		if err := prChecksSleep(ctx, delay); err != nil {
			return pr.CommandResult{}, false, err
		}
	}
	discoveryTimeout := prChecksDiscoveryTimeout(cfg)
	discoveryDeadline := time.Now().Add(discoveryTimeout)
	watchTimeout := prChecksWatchTimeout(cfg)
	watchDeadline := time.Now().Add(watchTimeout)
	for {
		checks, err := session.runner.Checks(ctx, session.prID, true)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return checks, false, fmt.Errorf("%w after %s", errPRChecksTimedOut, watchTimeout)
			}
			return checks, false, err
		}
		if !checks.NoChecks && !checks.Pending {
			return checks, false, nil
		}
		now := time.Now()
		if checks.NoChecks && discoveryTimeout <= 0 {
			return checks, true, nil
		}
		if checks.NoChecks && !now.Before(discoveryDeadline) {
			return checks, true, nil
		}
		if !now.Before(watchDeadline) {
			return checks, false, fmt.Errorf("%w after %s", errPRChecksTimedOut, watchTimeout)
		}
		sleep := prChecksPollInterval(cfg)
		if checks.NoChecks && discoveryTimeout > 0 {
			if remaining := time.Until(discoveryDeadline); remaining < sleep {
				sleep = remaining
			}
		}
		if remaining := time.Until(watchDeadline); remaining < sleep {
			sleep = remaining
		}
		if err := prChecksSleep(ctx, sleep); err != nil {
			return checks, false, err
		}
	}
}

func prChecksStartupDelay(cfg config.Config) time.Duration {
	return time.Duration(cfg.Git.Integration.PR.ChecksStartupDelaySeconds) * time.Second
}

func prChecksDiscoveryTimeout(cfg config.Config) time.Duration {
	return time.Duration(cfg.Git.Integration.PR.ChecksDiscoveryTimeoutSeconds) * time.Second
}

func prChecksPollInterval(cfg config.Config) time.Duration {
	return time.Duration(prChecksPollIntervalSeconds(cfg)) * time.Second
}

func prChecksPollIntervalSeconds(cfg config.Config) int {
	seconds := cfg.Git.Integration.PR.ChecksPollIntervalSeconds
	if seconds <= 0 {
		seconds = 1
	}
	return seconds
}

func prChecksWatchTimeout(cfg config.Config) time.Duration {
	return time.Duration(cfg.Git.Integration.PR.ChecksWatchTimeoutSeconds) * time.Second
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readPullRequestTemplate(root string) string {
	_, content, ok := findPullRequestTemplate(root)
	if !ok {
		return ""
	}
	return content
}

func fallbackPRTitle(branch string) string {
	return "Integrate " + branch
}

func fallbackPRBody(branch, template string) string {
	if strings.TrimSpace(template) != "" {
		body := strings.TrimRight(template, "\n")
		return body + "\n\n## Loop Notes\n\n- Branch: `" + branch + "`\n- See loop validation logs for generated details.\n"
	}
	return "## Summary\n\nGenerated by loop.\n\n## Verification\n\nSee loop validation logs.\n"
}

func writeUnlessExists(path string, data []byte, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func printResult(g globals, value map[string]any, text string) error {
	if g.JSON {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Print(text)
	return nil
}

func latestRun(runsDir string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no runs found")
	}
	sort.Strings(names)
	return names[len(names)-1], nil
}

func latestIteration(iterDir string) (string, error) {
	entries, err := os.ReadDir(iterDir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no iterations found")
	}
	sort.Strings(names)
	return names[len(names)-1], nil
}

func dirtyList(entries []gitx.StatusEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.Path)
	}
	return strings.Join(parts, ", ")
}

func appendErrorLog(path, message string) {
	message = strings.TrimSpace(message)
	if path == "" || message == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), message)
}

func sanitizeValidationOutputName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "command"
	}
	return out
}

func readArtifactOptional(iterationDir, artifactName string) string {
	data, err := artifactdb.Read(iterationDir, artifactName)
	if err == nil {
		return data
	}
	return ""
}

func materializePRBody(root, body string) (string, func(), error) {
	tmpDir := filepath.Join(root, ".loop", "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", func() {}, err
	}
	f, err := os.CreateTemp(tmpDir, "pr-body-*.md")
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func rel(root, path string) string {
	out, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return out
}

func contains(items []string, item string) bool {
	for _, candidate := range items {
		if candidate == item {
			return true
		}
	}
	return false
}

func flagsFirst(args []string, takesValue map[string]bool) []string {
	var flags []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimPrefix(arg, "--")
		if before, _, ok := strings.Cut(name, "="); ok {
			name = before
		}
		if takesValue[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}
