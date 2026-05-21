package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/memory"
	"github.com/aki-0421/loop/internal/pr"
	"github.com/aki-0421/loop/internal/runstate"
	"github.com/aki-0421/loop/internal/validation"
)

type prState struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	PR            string `json:"pr"`
	Branch        string `json:"branch"`
	Base          string `json:"base"`
	Title         string `json:"title,omitempty"`
	BodyArtifact  string `json:"body_artifact,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	MergedAt      string `json:"merged_at,omitempty"`
}

type prChecksArtifact struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	PR            string `json:"pr"`
	Branch        string `json:"branch"`
	CheckedAt     string `json:"checked_at"`
	ExitCode      int    `json:"exit_code"`
	NoChecks      bool   `json:"no_checks,omitempty"`
	Pending       bool   `json:"pending,omitempty"`
	Error         string `json:"error,omitempty"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
}

type prCommandContext struct {
	root    string
	workDir string
	iterDir string
	paths   pathSet
	cfg     config.Config
	runtime map[string]string
	branch  string
	base    string
}

func commandPR(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop pr <create|checks|logs|merge> [flags]")}
	}
	switch args[0] {
	case "create":
		return commandPRCreate(ctx, g, args[1:])
	case "checks":
		return commandPRChecks(ctx, g, args[1:])
	case "logs":
		return commandPRLogs(ctx, g, args[1:])
	case "merge":
		return commandPRMerge(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown pr subcommand %q", args[0])}
	}
}

func commandPRCreate(ctx context.Context, g globals, args []string) error {
	prCtx, err := loadPRCommandContext(ctx, g, "create", args, 0)
	if err != nil {
		return err
	}
	if err := ensurePRMode(prCtx); err != nil {
		return codedError{2, err}
	}
	if err := ensureIterationBranch(ctx, gitx.Runner{Dir: prCtx.workDir}, prCtx.branch); err != nil {
		return codedError{4, err}
	}

	template := readPullRequestTemplate(prCtx.workDir)
	title := strings.TrimSpace(readArtifactOptional(prCtx.iterDir, "pr-title"))
	if title == "" {
		title = fallbackPRTitle(prCtx.branch)
		_ = artifactdb.Write(prCtx.iterDir, "pr-title", title+"\n")
	}
	body := strings.TrimSpace(readArtifactOptional(prCtx.iterDir, "pr-body"))
	if body == "" {
		body = fallbackPRBody(prCtx.branch, template)
		_ = artifactdb.Write(prCtx.iterDir, "pr-body", body)
	}
	bodyFile, cleanupBody, err := materializePRBody(prCtx.root, body)
	if err != nil {
		return codedError{1, err}
	}
	defer cleanupBody()

	runner := prRunner(prCtx.cfg, prCtx.workDir)
	if prCtx.cfg.Git.Integration.PR.Push {
		if _, err := runner.Push(ctx, prCtx.branch); err != nil {
			return codedError{6, err}
		}
	}
	prID := ""
	if viewed, err := runner.View(ctx, prCtx.branch); err == nil {
		prID = strings.TrimSpace(viewed.Stdout)
		if prID == "null" {
			prID = ""
		}
	}
	if prID == "" {
		created, err := runner.Create(ctx, pr.CreateOptions{Base: prCtx.base, Head: prCtx.branch, Title: title, BodyFile: bodyFile})
		if err != nil {
			return codedError{6, err}
		}
		prID = strings.TrimSpace(created.Stdout)
	}
	if prID == "" {
		prID = prCtx.branch
	}
	state := prState{
		SchemaVersion: 1,
		Status:        "created",
		PR:            prID,
		Branch:        prCtx.branch,
		Base:          prCtx.base,
		Title:         title,
		BodyArtifact:  "pr-body",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := writePRState(prCtx.iterDir, state); err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"pr": prID, "status": state.Status, "branch": prCtx.branch}, fmt.Sprintf("PR: %s\nStatus: %s\n", prID, state.Status))
}

func commandPRChecks(ctx context.Context, g globals, args []string) error {
	prCtx, err := loadPRCommandContext(ctx, g, "checks", args, 0)
	if err != nil {
		return err
	}
	if err := ensurePRMode(prCtx); err != nil {
		return codedError{2, err}
	}
	state, err := requirePRState(prCtx.iterDir)
	if err != nil {
		return codedError{2, err}
	}
	runner := prRunner(prCtx.cfg, prCtx.workDir)
	if prCtx.cfg.Git.Integration.PR.Push {
		if _, err := runner.Push(ctx, prCtx.branch); err != nil {
			return codedError{6, err}
		}
	}
	session := &prIntegrationSession{runner: runner, prID: state.PR}
	checks, skipped, checksErr := waitForPRChecks(ctx, session, prCtx.cfg)
	status := "passed"
	if skipped {
		status = "skipped"
	}
	if checksErr != nil {
		status = "failed"
	}
	if err := writePRChecks(prCtx.iterDir, prChecksArtifact{
		SchemaVersion: 1,
		Status:        status,
		PR:            state.PR,
		Branch:        prCtx.branch,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
		ExitCode:      checks.ExitCode,
		NoChecks:      checks.NoChecks,
		Pending:       checks.Pending,
		Error:         prErrorString(checksErr),
		Stdout:        checks.Stdout,
		Stderr:        checks.Stderr,
	}); err != nil {
		return codedError{1, err}
	}
	if checksErr != nil {
		appendErrorLog(prCtx.paths.Errors, fmt.Sprintf("pull request checks failed for %s; see pr-checks artifact", state.PR))
		return codedError{6, fmt.Errorf("pull request checks failed for %s; see `loop iteration read pr-checks`", state.PR)}
	}
	return printResult(g, map[string]any{"pr": state.PR, "status": status}, fmt.Sprintf("PR checks: %s\n", status))
}

func commandPRLogs(ctx context.Context, g globals, args []string) error {
	prCtx, err := loadPRCommandContext(ctx, g, "logs", args, 1)
	if err != nil {
		return err
	}
	if err := ensurePRMode(prCtx); err != nil {
		return codedError{2, err}
	}
	job := strings.TrimSpace(argsWithoutPRLocatorFlags(args)[0])
	result, err := prRunner(prCtx.cfg, prCtx.workDir).Logs(ctx, job)
	output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if output != "" {
		_ = artifactdb.Write(prCtx.iterDir, "pr-check-log", output+"\n")
	}
	if err != nil {
		appendErrorLog(prCtx.paths.Errors, "pull request check log fetch failed; see command output")
		return codedError{6, err}
	}
	return printResult(g, map[string]any{"artifact": "pr-check-log", "job": job}, output+"\n")
}

func commandPRMerge(ctx context.Context, g globals, args []string) error {
	prCtx, err := loadPRCommandContext(ctx, g, "merge", args, 0)
	if err != nil {
		return err
	}
	if err := ensurePRMode(prCtx); err != nil {
		return codedError{2, err}
	}
	state, err := requirePRState(prCtx.iterDir)
	if err != nil {
		return codedError{2, err}
	}
	if state.Status == "merged" {
		return printResult(g, map[string]any{"pr": state.PR, "status": state.Status}, fmt.Sprintf("PR already merged: %s\n", state.PR))
	}
	results, validationErr := runConfiguredValidation(ctx, prCtx.workDir, prCtx.paths, prCtx.cfg.Validation.Commands)
	if validationErr != nil {
		return codedError{5, validationErr}
	}
	if validation.StatusFromResults(results) == "failed" {
		return codedError{5, fmt.Errorf("required validation failed")}
	}

	runner := prRunner(prCtx.cfg, prCtx.workDir)
	if prCtx.cfg.Git.Integration.PR.Push {
		if _, err := runner.Push(ctx, prCtx.branch); err != nil {
			return codedError{6, err}
		}
	}
	if prCtx.cfg.Git.Integration.PR.WaitChecks {
		session := &prIntegrationSession{runner: runner, prID: state.PR}
		checks, skipped, checksErr := waitForPRChecks(ctx, session, prCtx.cfg)
		status := "passed"
		if skipped {
			status = "skipped"
		}
		if checksErr != nil {
			status = "failed"
		}
		_ = writePRChecks(prCtx.iterDir, prChecksArtifact{
			SchemaVersion: 1,
			Status:        status,
			PR:            state.PR,
			Branch:        prCtx.branch,
			CheckedAt:     time.Now().UTC().Format(time.RFC3339),
			ExitCode:      checks.ExitCode,
			NoChecks:      checks.NoChecks,
			Pending:       checks.Pending,
			Error:         prErrorString(checksErr),
			Stdout:        checks.Stdout,
			Stderr:        checks.Stderr,
		})
		if checksErr != nil {
			appendErrorLog(prCtx.paths.Errors, fmt.Sprintf("pull request checks failed before merge for %s; see pr-checks artifact", state.PR))
			return codedError{6, fmt.Errorf("pull request checks failed for %s; see `loop iteration read pr-checks`", state.PR)}
		}
	}
	if err := validatePRBranchCommits(ctx, prCtx); err != nil {
		return codedError{4, err}
	}

	body := strings.TrimSpace(readArtifactOptional(prCtx.iterDir, "pr-body"))
	bodyFile := ""
	if body != "" {
		var cleanupBody func()
		bodyFile, cleanupBody, err = materializePRBody(prCtx.root, body)
		if err != nil {
			return codedError{1, err}
		}
		defer cleanupBody()
	}
	var fetchedPR memory.Record
	if _, err := runner.Merge(ctx, pr.MergeOptions{PR: state.PR, Subject: state.Title, BodyFile: bodyFile}); err != nil {
		recovered, recoveryErr := fetchMergedPullRequest(ctx, prCtx, state.PR)
		if recoveryErr != nil {
			return codedError{6, fmt.Errorf("%w; also failed to verify whether the pull request was already merged: %v", err, recoveryErr)}
		}
		fetchedPR = recovered
		_ = runstate.AppendEvent(prCtx.paths.Events, runstate.Event{"type": "pr.merge.recovered", "pr": state.PR})
	} else if record, err := fetchPullRequestAfterMerge(ctx, prCtx, state.PR); err != nil {
		_ = runstate.AppendEvent(prCtx.paths.Events, runstate.Event{"type": "memory.pr_fetch.failed", "pr": state.PR, "error": err.Error()})
	} else {
		fetchedPR = record
	}
	state.Status = "merged"
	state.MergedAt = firstNonEmpty(fetchedPR.MergedAt, time.Now().UTC().Format(time.RFC3339))
	if err := writePRState(prCtx.iterDir, state); err != nil {
		return codedError{1, err}
	}
	if state.Branch != "" && state.Branch != state.Base {
		if err := deleteRemoteBranchAfterPRMerge(ctx, gitx.Runner{Dir: prCtx.workDir}, state.Branch); err != nil {
			_ = runstate.AppendEvent(prCtx.paths.Events, runstate.Event{"type": "pr.remote_branch_cleanup.failed", "branch": state.Branch, "error": err.Error()})
		}
	}
	return printResult(g, map[string]any{"pr": state.PR, "status": state.Status}, fmt.Sprintf("PR merged: %s\n", state.PR))
}

func fetchPullRequestAfterMerge(ctx context.Context, prCtx prCommandContext, ref string) (memory.Record, error) {
	return memory.FetchPullRequest(ctx, memory.FetchOptions{
		WorkDir: prCtx.workDir,
		RunsDir: filepath.Join(prCtx.root, prCtx.cfg.Logs.Dir),
		Ref:     ref,
	})
}

func fetchMergedPullRequest(ctx context.Context, prCtx prCommandContext, ref string) (memory.Record, error) {
	record, err := fetchPullRequestAfterMerge(ctx, prCtx, ref)
	if err != nil {
		return memory.Record{}, err
	}
	if strings.ToLower(strings.TrimSpace(record.State)) != "merged" {
		return memory.Record{}, fmt.Errorf("pull request %s state is %q, not merged", ref, record.State)
	}
	return record, nil
}

func loadPRCommandContext(ctx context.Context, g globals, subcommand string, args []string, positional int) (prCommandContext, error) {
	fs := flag.NewFlagSet("pr "+subcommand, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true})
	if err := fs.Parse(args); err != nil {
		return prCommandContext{}, codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != positional {
		usage := "loop pr " + subcommand
		if positional > 0 {
			usage += " <arg>"
		}
		usage += " [--iteration-dir <dir>]"
		return prCommandContext{}, codedError{2, fmt.Errorf("usage: %s", usage)}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return prCommandContext{}, codedError{1, err}
	}
	if strings.TrimSpace(resolvedDir) == "" {
		return prCommandContext{}, codedError{2, errors.New("iteration directory is required; run inside an agent iteration or pass --iteration-dir")}
	}
	workRoot, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return prCommandContext{}, codedError{1, fmt.Errorf("not inside a git repository: %w", err)}
	}
	storageRoot, err := loopStorageRoot(ctx)
	if err != nil {
		storageRoot = workRoot
	}
	cfg, err := config.Load(config.LoadOptions{CWD: workRoot, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent, NoColor: g.NoColor}})
	if err != nil {
		return prCommandContext{}, codedError{3, err}
	}
	paths := promptPaths(resolvedDir)
	runtime := readResultRuntime(resolvedDir)
	workDir := firstNonEmpty(runtime["workdir"], os.Getenv("LOOP_WORKDIR"), workRoot)
	branch := firstNonEmpty(runtime["current_branch"], os.Getenv("LOOP_CURRENT_BRANCH"))
	if branch == "" && gitx.IsRepository(ctx, workDir) {
		if current, err := (gitx.Runner{Dir: workDir}).CurrentBranch(ctx); err == nil {
			branch = current
		}
	}
	base := firstNonEmpty(runtime["base_branch"], os.Getenv("LOOP_BASE_BRANCH"), cfg.Git.BaseBranch)
	paths.Goal = runtime["goal"]
	paths.Language = firstNonEmpty(runtime["output_language"], cfg.Language.Default)
	paths.RunID = firstNonEmpty(runtime["run_id"], os.Getenv("LOOP_RUN_ID"))
	paths.IterationID = firstNonEmpty(runtime["iteration_id"], os.Getenv("LOOP_ITERATION_ID"))
	paths.BaseBranch = base
	paths.InitialBranch = firstNonEmpty(runtime["initial_branch"], os.Getenv("LOOP_INITIAL_BRANCH"))
	paths.CurrentBranch = branch
	paths.IntegrationMode = firstNonEmpty(runtime["integration_mode"], cfg.Git.Integration.Mode)
	paths.PullRequestMode = paths.IntegrationMode == "pr"
	paths.WorkDir = workDir
	return prCommandContext{
		root:    storageRoot,
		workDir: workDir,
		iterDir: resolvedDir,
		paths:   paths,
		cfg:     cfg,
		runtime: runtime,
		branch:  branch,
		base:    base,
	}, nil
}

func argsWithoutPRLocatorFlags(args []string) []string {
	fs := flag.NewFlagSet("pr args", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.String("iteration-dir", "", "")
	fs.String("dir", "", "")
	fs.String("run", "", "")
	fs.String("iteration", "", "")
	_ = fs.Parse(flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true}))
	return fs.Args()
}

func ensurePRMode(prCtx prCommandContext) error {
	if prCtx.cfg.Git.Integration.Mode != "pr" && prCtx.paths.IntegrationMode != "pr" {
		return errors.New("pull request commands require git.integration.mode=pr")
	}
	return nil
}

func prRunner(cfg config.Config, dir string) pr.Runner {
	return pr.Runner{
		Dir:                   dir,
		ChecksTimeout:         prChecksWatchTimeout(cfg),
		ChecksIntervalSeconds: prChecksPollIntervalSeconds(cfg),
		ChecksRequiredOnly:    cfg.Git.Integration.PR.ChecksRequiredOnly,
	}
}

func validatePRBranchCommits(ctx context.Context, prCtx prCommandContext) error {
	commits, err := (gitx.Runner{Dir: prCtx.workDir}).ListCommits(ctx, prCtx.base, prCtx.branch)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		return fmt.Errorf("mergeable iteration did not create commits")
	}
	return validateIterationCommitSubjects(commits)
}

func requirePRState(iterDir string) (prState, error) {
	state, ok, err := readPRState(iterDir)
	if err != nil {
		return state, err
	}
	if !ok || strings.TrimSpace(state.PR) == "" {
		return state, errors.New("pull request state not found; run `loop pr create` first")
	}
	return state, nil
}

func readPRState(iterDir string) (prState, bool, error) {
	var state prState
	data, err := artifactdb.Read(iterDir, "pr-state")
	if err != nil {
		if errors.Is(err, artifactdb.ErrNotFound) {
			return state, false, nil
		}
		return state, false, err
	}
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return state, false, err
	}
	return state, true, nil
}

func writePRState(iterDir string, state prState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return artifactdb.Write(iterDir, "pr-state", string(data))
}

func writePRChecks(iterDir string, checks prChecksArtifact) error {
	data, err := json.MarshalIndent(checks, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return artifactdb.Write(iterDir, "pr-checks", string(data))
}

func prStateMerged(iterDir string) bool {
	state, ok, err := readPRState(iterDir)
	return err == nil && ok && state.Status == "merged" && strings.TrimSpace(state.PR) != ""
}

func finalizeAgentOwnedPR(ctx context.Context, runner gitx.Runner, cleanup *iterationCleanup, worktreePath, root string, cfg config.Config, branch string) error {
	if worktreePath == "" {
		if _, err := runner.Run(ctx, "reset", "--hard"); err != nil {
			return err
		}
		if _, err := runGitCleanPreservingLoopRuntime(ctx, runner); err != nil {
			return err
		}
	}
	if err := preparePRMerge(ctx, runner, cleanup, worktreePath, root, cfg.Git.BaseBranch); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "pull", "--ff-only"); err != nil {
		return err
	}
	if branch != "" && branch != cfg.Git.BaseBranch {
		if err := deleteRemoteBranchAfterPRMerge(ctx, runner, branch); err != nil && cleanup != nil {
			cleanup.appendEvent(runstate.Event{"type": "pr.remote_branch_cleanup.failed", "branch": branch, "error": err.Error()})
		}
	}
	pruneRemoteBranches(ctx, runner)
	if branch != "" && branch != cfg.Git.BaseBranch {
		if err := runner.DeleteBranch(ctx, branch, true); err != nil && localBranchExists(ctx, runner, branch) {
			return err
		}
	}
	return nil
}

func localBranchExists(ctx context.Context, runner gitx.Runner, branch string) bool {
	_, err := runner.Run(ctx, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func deleteRemoteBranchAfterPRMerge(ctx context.Context, runner gitx.Runner, branch string) error {
	if !remoteBranchExists(ctx, runner, branch) {
		return nil
	}
	if _, err := runner.Run(ctx, "push", "origin", "--delete", branch); err != nil && remoteBranchExists(ctx, runner, branch) {
		return err
	}
	return nil
}

func remoteBranchExists(ctx context.Context, runner gitx.Runner, branch string) bool {
	_, err := runner.Run(ctx, "ls-remote", "--exit-code", "--heads", "origin", branch)
	return err == nil
}

func pruneRemoteBranches(ctx context.Context, runner gitx.Runner) {
	_, _ = runner.Run(ctx, "fetch", "--prune", "origin")
}

func prErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
