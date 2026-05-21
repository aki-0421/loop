package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/validation"
)

type repeatedStringFlag []string

func (f *repeatedStringFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedStringFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func commandIterationResult(ctx context.Context, g globals, args []string) error {
	var validationCommandFlags repeatedStringFlag
	var assumptionFlags repeatedStringFlag
	var commitFlags repeatedStringFlag

	fs := flag.NewFlagSet("iteration result", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	writeResult := fs.Bool("write", false, "write the generated JSON to the result artifact")
	status := fs.String("status", "completed", "result status")
	summary := fs.String("summary", "", "summary sentence")
	fs.StringVar(summary, "summary-sentence", "", "summary sentence")
	shouldStopRaw := fs.String("should-stop", "", "whether the run goal is fully satisfied")
	fs.StringVar(shouldStopRaw, "should-fully-stop", "", "whether the run goal is fully satisfied")
	goalEvaluation := fs.String("goal-evaluation", "", "explanation of the stop decision")
	validationStatus := fs.String("validation-status", "", "validation status")
	blockedReason := fs.String("blocked-reason", "", "blocked reason")
	errorMessage := fs.String("error", "", "failure error")
	fs.Var(&validationCommandFlags, "validation-command", "validation command as JSON or name|command|exit_code|required[|output_path]")
	fs.Var(&validationCommandFlags, "validation", "alias for --validation-command")
	fs.Var(&assumptionFlags, "assumption", "recorded assumption")
	fs.Var(&commitFlags, "commit", "commit as JSON or sha|message")

	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true,
		"dir":           true,
		"run":           true,
		"iteration":     true,
		"status":        true,
		"summary":       true, "summary-sentence": true,
		"should-stop": true, "should-fully-stop": true,
		"goal-evaluation":    true,
		"validation-status":  true,
		"blocked-reason":     true,
		"error":              true,
		"validation-command": true, "validation": true,
		"assumption": true,
		"commit":     true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop iteration result [flags]")}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	if *writeResult && strings.TrimSpace(resolvedDir) == "" {
		return codedError{2, errors.New("iteration directory is required when --write is used")}
	}

	result, err := buildIterationResult(ctx, resolvedDir, iterationResultOptions{
		Status:                  *status,
		SummarySentence:         *summary,
		ShouldFullyStopRaw:      *shouldStopRaw,
		GoalEvaluation:          *goalEvaluation,
		ValidationStatus:        *validationStatus,
		BlockedReason:           *blockedReason,
		Error:                   *errorMessage,
		ValidationCommandInputs: []string(validationCommandFlags),
		Assumptions:             []string(assumptionFlags),
		CommitInputs:            []string(commitFlags),
	})
	if err != nil {
		return codedError{2, err}
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return codedError{1, err}
	}
	data = append(data, '\n')
	if _, err := validation.ValidateResultJSON(data); err != nil {
		return codedError{2, err}
	}
	if *writeResult {
		if err := artifactdb.Write(resolvedDir, "result", string(data)); err != nil {
			return codedError{1, err}
		}
		return printResult(g, map[string]any{"artifact": "result", "action": "write"}, "wrote result\n")
	}
	fmt.Print(string(data))
	return nil
}

type iterationResultOptions struct {
	Status                  string
	SummarySentence         string
	ShouldFullyStopRaw      string
	GoalEvaluation          string
	ValidationStatus        string
	BlockedReason           string
	Error                   string
	ValidationCommandInputs []string
	Assumptions             []string
	CommitInputs            []string
}

func buildIterationResult(ctx context.Context, iterationDir string, opts iterationResultOptions) (validation.IterationResult, error) {
	if strings.TrimSpace(opts.SummarySentence) == "" {
		return validation.IterationResult{}, errors.New("--summary is required")
	}
	if strings.TrimSpace(opts.GoalEvaluation) == "" {
		return validation.IterationResult{}, errors.New("--goal-evaluation is required")
	}
	if strings.TrimSpace(opts.ShouldFullyStopRaw) == "" {
		return validation.IterationResult{}, errors.New("--should-stop true|false is required")
	}
	if strings.TrimSpace(opts.Status) == "blocked" && strings.TrimSpace(opts.BlockedReason) == "" {
		return validation.IterationResult{}, errors.New("--blocked-reason is required when --status blocked")
	}
	if strings.TrimSpace(opts.Status) == "failed" && strings.TrimSpace(opts.Error) == "" {
		return validation.IterationResult{}, errors.New("--error is required when --status failed")
	}
	shouldStop, err := strconv.ParseBool(strings.TrimSpace(opts.ShouldFullyStopRaw))
	if err != nil {
		return validation.IterationResult{}, fmt.Errorf("--should-stop must be true or false: %w", err)
	}

	runtime := readResultRuntime(iterationDir)
	workDir := firstNonEmpty(runtime["workdir"], os.Getenv("LOOP_WORKDIR"), ".")
	baseBranch := firstNonEmpty(runtime["base_branch"], os.Getenv("LOOP_BASE_BRANCH"))
	initialBranch := firstNonEmpty(runtime["initial_branch"], os.Getenv("LOOP_INITIAL_BRANCH"), runtime["current_branch"], os.Getenv("LOOP_CURRENT_BRANCH"))
	currentBranch := firstNonEmpty(runtime["current_branch"], os.Getenv("LOOP_CURRENT_BRANCH"))
	if currentBranch == "" && gitx.IsRepository(ctx, workDir) {
		if branch, err := (gitx.Runner{Dir: workDir}).CurrentBranch(ctx); err == nil {
			currentBranch = branch
		}
	}
	if initialBranch == "" {
		initialBranch = currentBranch
	}
	if initialBranch == "" {
		return validation.IterationResult{}, errors.New("could not infer branch.initial_name from loop runtime")
	}

	commits, err := parseCommitInputs(opts.CommitInputs)
	if err != nil {
		return validation.IterationResult{}, err
	}
	if len(commits) == 0 {
		commits, err = inferIterationCommits(ctx, workDir, baseBranch)
		if err != nil {
			return validation.IterationResult{}, err
		}
	}
	kind := ""
	slug := ""
	finalName := ""
	branchRenamed, _ := strconv.ParseBool(strings.TrimSpace(runtime["branch_renamed"]))
	if currentBranch != "" && initialBranch != "" && currentBranch != initialBranch {
		branchRenamed = true
	}
	if branchRenamed && currentBranch != "" {
		if parsedKind, parsedSlug, ok := splitBranchName(currentBranch); ok {
			kind = parsedKind
			slug = parsedSlug
		}
	}
	if kind == "" {
		kind = inferBranchKind(commits)
	}
	if slug == "" {
		slug = gitx.Slug(opts.SummarySentence)
	}
	if branchRenamed {
		finalName = currentBranch
	} else {
		finalName = ""
	}

	if err := validateResultCommandState(ctx, workDir, initialBranch, currentBranch, opts.Status, commits); err != nil {
		return validation.IterationResult{}, err
	}
	if strings.TrimSpace(opts.Status) == "completed" && runtime["integration_mode"] == "pr" && !prStateMerged(iterationDir) {
		return validation.IterationResult{}, errors.New("completed pull request results require a merged PR; run `loop pr merge` before `loop iteration result --write`")
	}

	commands, err := parseValidationCommandInputs(opts.ValidationCommandInputs)
	if err != nil {
		return validation.IterationResult{}, err
	}
	vStatus := strings.TrimSpace(opts.ValidationStatus)
	if vStatus == "" {
		vStatus = validationStatusFromCommandLogs(commands)
	}

	return validation.IterationResult{
		SchemaVersion:   1,
		Status:          strings.TrimSpace(opts.Status),
		SummarySentence: strings.TrimSpace(opts.SummarySentence),
		ShouldFullyStop: shouldStop,
		GoalEvaluation:  strings.TrimSpace(opts.GoalEvaluation),
		Branch: validation.BranchResult{
			InitialName: initialBranch,
			Kind:        kind,
			Slug:        slug,
			FinalName:   finalName,
		},
		Commits: commits,
		Validation: validation.ValidationResult{
			Status:   vStatus,
			Commands: commands,
		},
		Artifacts:     inferResultArtifacts(iterationDir),
		Assumptions:   trimNonEmpty(opts.Assumptions),
		BlockedReason: strings.TrimSpace(opts.BlockedReason),
		Error:         strings.TrimSpace(opts.Error),
	}, nil
}

func validateResultCommandState(ctx context.Context, workDir, initialBranch, currentBranch, status string, commits []validation.CommitResult) error {
	status = strings.TrimSpace(status)
	if gitx.IsRepository(ctx, workDir) {
		runner := gitx.Runner{Dir: workDir}
		if current, err := runner.CurrentBranch(ctx); err == nil && currentBranch != "" && current != currentBranch {
			return fmt.Errorf("current branch is %q, but loop runtime tracks %q; use `loop branch rename ...` instead of direct Git branch changes", current, currentBranch)
		}
		clean, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
		if err != nil {
			return fmt.Errorf("check working tree cleanliness: %w", err)
		}
		if (status == "completed" || status == "no_change") && !clean.Clean {
			return fmt.Errorf("%s result requires a clean working tree; commit complete work with `loop commit`, revert incomplete work, or use blocked/failed: %s", status, dirtyList(clean.Dirty))
		}
	}
	switch status {
	case "completed":
		if strings.TrimSpace(currentBranch) == "" || strings.TrimSpace(currentBranch) == strings.TrimSpace(initialBranch) {
			return errors.New("completed result requires a renamed branch; run `loop branch rename <kind>/<slug>` before `loop iteration result --write`")
		}
		if len(commits) == 0 {
			return errors.New("completed result requires at least one commit; use `loop commit` for completed work or `--status no_change` when nothing changed")
		}
	case "no_change":
		if len(commits) > 0 {
			return errors.New("no_change result must not report commits; use `--status completed` for committed work")
		}
	}
	return nil
}

func splitBranchName(branch string) (string, string, bool) {
	kind, slug, ok := strings.Cut(strings.TrimSpace(branch), "/")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(slug) == "" {
		return "", "", false
	}
	return strings.TrimSpace(kind), strings.TrimSpace(slug), true
}

func readResultRuntime(iterationDir string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(iterationDir) == "" {
		return out
	}
	data, err := artifactdb.Read(iterationDir, "runtime")
	if err != nil {
		return out
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return out
	}
	for key, value := range raw {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case bool:
			out[key] = strconv.FormatBool(typed)
		case float64:
			out[key] = strconv.FormatFloat(typed, 'f', -1, 64)
		}
	}
	return out
}

func inferIterationCommits(ctx context.Context, workDir, baseBranch string) ([]validation.CommitResult, error) {
	if !gitx.IsRepository(ctx, workDir) {
		return nil, nil
	}
	if strings.TrimSpace(baseBranch) == "" {
		return nil, nil
	}
	gitCommits, err := (gitx.Runner{Dir: workDir}).ListCommits(ctx, baseBranch, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("infer commits from %s..HEAD: %w", baseBranch, err)
	}
	out := make([]validation.CommitResult, 0, len(gitCommits))
	for i := len(gitCommits) - 1; i >= 0; i-- {
		out = append(out, validation.CommitResult{
			SHA:     gitCommits[i].Hash,
			Message: gitCommits[i].Subject,
		})
	}
	return out, nil
}

func inferResultArtifacts(iterationDir string) validation.ArtifactResult {
	var out validation.ArtifactResult
	if strings.TrimSpace(iterationDir) == "" {
		return out
	}
	artifacts, err := artifactdb.List(iterationDir)
	if err != nil {
		return out
	}
	for _, artifact := range artifacts {
		switch artifact.Name {
		case "plan":
			out.Plan = "plan"
		case "todo":
			out.Todo = "todo"
		case "summary":
			out.Summary = "summary"
		case "worklog":
			out.Worklog = "worklog"
		case "pr-title":
			out.PRTitle = "pr-title"
		case "pr-body":
			out.PRBody = "pr-body"
		}
	}
	return out
}

func parseCommitInputs(inputs []string) ([]validation.CommitResult, error) {
	out := make([]validation.CommitResult, 0, len(inputs))
	for _, input := range inputs {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		var item validation.CommitResult
		if strings.HasPrefix(input, "{") {
			if err := decodeStrictJSON(input, &item); err != nil {
				return nil, fmt.Errorf("parse --commit: %w", err)
			}
		} else if sha, message, ok := strings.Cut(input, "|"); ok {
			item.SHA = strings.TrimSpace(sha)
			item.Message = strings.TrimSpace(message)
		} else {
			item.Message = input
		}
		out = append(out, item)
	}
	return out, nil
}

func parseValidationCommandInputs(inputs []string) ([]validation.ValidationCommandLog, error) {
	out := make([]validation.ValidationCommandLog, 0, len(inputs))
	for _, input := range inputs {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		item, err := parseValidationCommandInput(input)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func parseValidationCommandInput(input string) (validation.ValidationCommandLog, error) {
	if strings.HasPrefix(input, "{") {
		var item validation.ValidationCommandLog
		if err := decodeStrictJSON(input, &item); err != nil {
			return item, fmt.Errorf("parse --validation-command: %w", err)
		}
		return item, nil
	}
	parts := strings.Split(input, "|")
	if len(parts) < 4 || len(parts) > 5 {
		return validation.ValidationCommandLog{}, errors.New("--validation-command must be JSON or name|command|exit_code|required[|output_path]")
	}
	exitCode, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return validation.ValidationCommandLog{}, fmt.Errorf("parse validation exit code: %w", err)
	}
	required, err := strconv.ParseBool(strings.TrimSpace(parts[3]))
	if err != nil {
		return validation.ValidationCommandLog{}, fmt.Errorf("parse validation required flag: %w", err)
	}
	item := validation.ValidationCommandLog{
		Name:     strings.TrimSpace(parts[0]),
		Command:  strings.TrimSpace(parts[1]),
		ExitCode: exitCode,
		Required: required,
	}
	if len(parts) == 5 {
		item.OutputPath = strings.TrimSpace(parts[4])
	}
	return item, nil
}

func validationStatusFromCommandLogs(commands []validation.ValidationCommandLog) string {
	if len(commands) == 0 {
		return "skipped"
	}
	optionalFailed := false
	for _, command := range commands {
		if command.ExitCode != 0 {
			if command.Required {
				return "failed"
			}
			optionalFailed = true
		}
	}
	if optionalFailed {
		return "partial"
	}
	return "passed"
}

func inferBranchKind(commits []validation.CommitResult) string {
	for _, commit := range commits {
		switch {
		case strings.HasPrefix(commit.Message, "F:"):
			return "feat"
		case strings.HasPrefix(commit.Message, "T:"):
			return "test"
		case strings.HasPrefix(commit.Message, "R:"):
			return "refactor"
		case strings.HasPrefix(commit.Message, "D:"):
			return "docs"
		case strings.HasPrefix(commit.Message, "S:"):
			return "style"
		case strings.HasPrefix(commit.Message, "C:"):
			return "chore"
		case strings.HasPrefix(commit.Message, "V:"):
			return "chore"
		}
	}
	return "chore"
}

func decodeStrictJSON(input string, target any) error {
	dec := json.NewDecoder(bytes.NewBufferString(input))
	dec.DisallowUnknownFields()
	return dec.Decode(target)
}

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
