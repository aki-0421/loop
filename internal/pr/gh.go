package pr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultTimeout = 2 * time.Minute
const checksPendingExitCode = 8

type Runner struct {
	Dir                   string
	GHPath                string
	GitPath               string
	Env                   []string
	Timeout               time.Duration
	ChecksTimeout         time.Duration
	ChecksIntervalSeconds int
	ChecksRequiredOnly    bool
}

type CommandResult struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	NoChecks bool
	Pending  bool
}

type CommandError struct {
	Tool     string
	Dir      string
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

func (e *CommandError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(e.Stdout)
	}
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("%s %s failed in %s: %s", e.Tool, strings.Join(e.Args, " "), e.Dir, msg)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func (r Runner) ghPath() string {
	if r.GHPath != "" {
		return r.GHPath
	}
	return "gh"
}

func (r Runner) gitPath() string {
	if r.GitPath != "" {
		return r.GitPath
	}
	return "git"
}

func (r Runner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return defaultTimeout
}

func (r Runner) checksTimeout() time.Duration {
	if r.ChecksTimeout > 0 {
		return r.ChecksTimeout
	}
	return r.timeout()
}

func (r Runner) Verify(ctx context.Context) error {
	_, err := r.run(ctx, r.ghPath(), "gh", "--version")
	return err
}

func (r Runner) Push(ctx context.Context, branch string) (CommandResult, error) {
	return r.run(ctx, r.gitPath(), "git", "push", "-u", "origin", branch)
}

func (r Runner) Create(ctx context.Context, opts CreateOptions) (CommandResult, error) {
	if opts.Base == "" || opts.Head == "" || opts.BodyFile == "" {
		return CommandResult{}, errors.New("base, head, and body file are required")
	}
	title, err := createTitle(opts)
	if err != nil {
		return CommandResult{}, err
	}
	if title == "" {
		return CommandResult{}, errors.New("title is required")
	}
	opts.Title = title
	args := CreateArgs(opts)
	return r.run(ctx, r.ghPath(), "gh", args...)
}

func (r Runner) View(ctx context.Context, branch string) (CommandResult, error) {
	if strings.TrimSpace(branch) == "" {
		return CommandResult{}, errors.New("branch is required")
	}
	return r.run(ctx, r.ghPath(), "gh", "pr", "view", branch, "--json", "url", "--jq", ".url")
}

type CreateOptions struct {
	Base      string
	Head      string
	Title     string
	TitleFile string
	BodyFile  string
}

func CreateArgs(opts CreateOptions) []string {
	return []string{
		"pr", "create",
		"--base", opts.Base,
		"--head", opts.Head,
		"--title", opts.Title,
		"--body-file", opts.BodyFile,
	}
}

func createTitle(opts CreateOptions) (string, error) {
	if strings.TrimSpace(opts.Title) != "" {
		return strings.TrimSpace(opts.Title), nil
	}
	if opts.TitleFile == "" {
		return "", errors.New("title or title file is required")
	}
	data, err := os.ReadFile(opts.TitleFile)
	if err != nil {
		return "", fmt.Errorf("read PR title file: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if title := strings.TrimSpace(line); title != "" {
			return title, nil
		}
	}
	return "", nil
}

func (r Runner) Checks(ctx context.Context, pr string, watch bool) (CommandResult, error) {
	if pr == "" {
		return CommandResult{}, errors.New("pr identifier is required")
	}
	args := []string{"pr", "checks", pr}
	if r.ChecksRequiredOnly {
		args = append(args, "--required")
	}
	if watch {
		args = append(args, "--watch")
		if r.ChecksIntervalSeconds > 0 {
			args = append(args, "--interval", strconv.Itoa(r.ChecksIntervalSeconds))
		}
	}
	result, err := r.runWithTimeout(ctx, r.checksTimeout(), r.ghPath(), "gh", args...)
	if err != nil && isNoChecksReported(err) {
		result.NoChecks = true
		return result, nil
	}
	if err != nil && isChecksPending(err) {
		result.Pending = true
		return result, nil
	}
	return result, err
}

func (r Runner) Logs(ctx context.Context, ref string) (CommandResult, error) {
	runID, jobID := ParseJobRef(ref)
	if jobID == "" {
		return CommandResult{}, errors.New("job id is required")
	}
	args := []string{"run", "view"}
	if runID != "" {
		args = append(args, runID)
	}
	args = append(args, "--job", jobID, "--log")
	return r.run(ctx, r.ghPath(), "gh", args...)
}

func (r Runner) GraphQL(ctx context.Context, query string, variables map[string]string) (CommandResult, error) {
	if strings.TrimSpace(query) == "" {
		return CommandResult{}, errors.New("graphql query is required")
	}
	args := []string{"api", "graphql", "-f", "query=" + query}
	keys := make([]string, 0, len(variables))
	for key, value := range variables {
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-F", key+"="+variables[key])
	}
	return r.run(ctx, r.ghPath(), "gh", args...)
}

func ParseJobRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ""
	}
	lower := strings.ToLower(strings.TrimSuffix(ref, "/"))
	runMarker := "/actions/runs/"
	jobMarker := "/job/"
	runIndex := strings.Index(lower, runMarker)
	jobIndex := strings.Index(lower, jobMarker)
	if runIndex >= 0 && jobIndex > runIndex {
		runStart := runIndex + len(runMarker)
		runPart := ref[runStart:jobIndex]
		jobStart := jobIndex + len(jobMarker)
		jobPart := ref[jobStart:]
		if slash := strings.Index(jobPart, "/"); slash >= 0 {
			jobPart = jobPart[:slash]
		}
		if query := strings.IndexAny(jobPart, "?#"); query >= 0 {
			jobPart = jobPart[:query]
		}
		return keepDigits(runPart), keepDigits(jobPart)
	}
	return "", keepDigits(ref)
}

func keepDigits(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isNoChecksReported(err error) bool {
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	output := strings.ToLower(commandErr.Stdout + "\n" + commandErr.Stderr)
	return strings.Contains(output, "no checks reported")
}

func isChecksPending(err error) bool {
	var commandErr *CommandError
	return errors.As(err, &commandErr) && commandErr.ExitCode == checksPendingExitCode
}

func (r Runner) Merge(ctx context.Context, opts MergeOptions) (CommandResult, error) {
	if opts.PR == "" {
		return CommandResult{}, errors.New("pr identifier is required")
	}
	args := MergeArgs(opts)
	return r.run(ctx, r.ghPath(), "gh", args...)
}

func (r Runner) Close(ctx context.Context, pr string) (CommandResult, error) {
	if strings.TrimSpace(pr) == "" {
		return CommandResult{}, errors.New("pr identifier is required")
	}
	return r.run(ctx, r.ghPath(), "gh", "pr", "close", pr)
}

func MergeArgs(opts MergeOptions) []string {
	args := []string{"pr", "merge", opts.PR, "--squash"}
	if subject := mergeSubject(opts.Subject, opts.PR); subject != "" {
		args = append(args, "--subject", subject)
	}
	if opts.BodyFile != "" {
		args = append(args, "--body-file", opts.BodyFile)
	}
	if opts.DeleteBranch {
		args = append(args, "--delete-branch")
	}
	return args
}

func mergeSubject(subject, pr string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	number := prNumber(pr)
	if number == "" {
		return subject
	}
	suffix := "(#" + number + ")"
	if strings.HasSuffix(subject, suffix) {
		return subject
	}
	return subject + " " + suffix
}

func prNumber(pr string) string {
	pr = strings.TrimSpace(strings.TrimSuffix(pr, "/"))
	if allDigits(pr) {
		return pr
	}
	for _, marker := range []string{"/pull/", "/pr/"} {
		if i := strings.LastIndex(pr, marker); i >= 0 {
			candidate := strings.TrimSuffix(pr[i+len(marker):], "/")
			if slash := strings.Index(candidate, "/"); slash >= 0 {
				candidate = candidate[:slash]
			}
			if allDigits(candidate) {
				return candidate
			}
		}
	}
	if i := strings.LastIndex(pr, "#"); i >= 0 {
		candidate := pr[i+1:]
		if allDigits(candidate) {
			return candidate
		}
	}
	return ""
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type MergeOptions struct {
	PR           string
	Subject      string
	BodyFile     string
	DeleteBranch bool
}

func (r Runner) PullBase(ctx context.Context, base string) error {
	if base == "" {
		return errors.New("base branch is required")
	}
	if _, err := r.run(ctx, r.gitPath(), "git", "checkout", base); err != nil {
		return err
	}
	_, err := r.run(ctx, r.gitPath(), "git", "pull", "--ff-only")
	return err
}

func (r Runner) run(ctx context.Context, path, tool string, args ...string) (CommandResult, error) {
	return r.runWithTimeout(ctx, r.timeout(), path, tool, args...)
}

func (r Runner) runWithTimeout(ctx context.Context, timeout time.Duration, path, tool string, args ...string) (CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, path, args...)
	cmd.Dir = r.Dir
	if len(r.Env) > 0 {
		cmd.Env = append(cmd.Environ(), r.Env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	result := CommandResult{Args: append([]string(nil), args...), Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}
	if cctx.Err() != nil {
		err = cctx.Err()
		exitCode = -1
		result.ExitCode = exitCode
	}
	if err != nil {
		return result, &CommandError{Tool: tool, Dir: r.Dir, Args: result.Args, Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: exitCode, Err: err}
	}
	return result, nil
}
