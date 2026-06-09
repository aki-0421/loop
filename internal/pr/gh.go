package pr

import (
	"bytes"
	"context"
	"encoding/json"
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
	Checks   []CheckStatus
}

type CheckStatus struct {
	Bucket      string `json:"bucket,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
	Description string `json:"description,omitempty"`
	Event       string `json:"event,omitempty"`
	Link        string `json:"link,omitempty"`
	Name        string `json:"name,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	State       string `json:"state,omitempty"`
	Workflow    string `json:"workflow,omitempty"`
}

type PullRequestState struct {
	State    string
	MergedAt string
	URL      string
}

type ReviewFeedback struct {
	ReviewDecision string               `json:"review_decision"`
	UpdatedAt      string               `json:"updated_at,omitempty"`
	LatestAt       string               `json:"latest_at,omitempty"`
	Summary        string               `json:"summary,omitempty"`
	Items          []ReviewFeedbackItem `json:"items,omitempty"`
}

type ReviewFeedbackItem struct {
	Kind      string `json:"kind"`
	State     string `json:"state,omitempty"`
	Author    string `json:"author,omitempty"`
	Body      string `json:"body,omitempty"`
	URL       string `json:"url,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
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

func (r Runner) ViewState(ctx context.Context, pr string) (PullRequestState, CommandResult, error) {
	if strings.TrimSpace(pr) == "" {
		return PullRequestState{}, CommandResult{}, errors.New("pr identifier is required")
	}
	result, err := r.run(ctx, r.ghPath(), "gh", "pr", "view", pr, "--json", "state,mergedAt,url", "--jq", "[.state, (.mergedAt // \"\"), (.url // \"\")] | @tsv")
	if err != nil {
		return PullRequestState{}, result, err
	}
	fields := strings.Split(strings.TrimSpace(result.Stdout), "\t")
	state := PullRequestState{}
	if len(fields) > 0 {
		state.State = strings.TrimSpace(fields[0])
	}
	if len(fields) > 1 {
		state.MergedAt = strings.TrimSpace(fields[1])
	}
	if len(fields) > 2 {
		state.URL = strings.TrimSpace(fields[2])
	}
	return state, result, nil
}

func (r Runner) ViewReviewFeedback(ctx context.Context, pr string) (ReviewFeedback, CommandResult, error) {
	if strings.TrimSpace(pr) == "" {
		return ReviewFeedback{}, CommandResult{}, errors.New("pr identifier is required")
	}
	result, err := r.run(ctx, r.ghPath(), "gh", "pr", "view", pr, "--json", "reviewDecision,latestReviews,comments,updatedAt")
	if err != nil {
		return ReviewFeedback{}, result, err
	}
	feedback, err := parseReviewFeedback(result.Stdout)
	if err != nil {
		return ReviewFeedback{}, result, err
	}
	return feedback, result, nil
}

func parseReviewFeedback(data string) (ReviewFeedback, error) {
	var payload struct {
		ReviewDecision string `json:"reviewDecision"`
		UpdatedAt      string `json:"updatedAt"`
		LatestReviews  []struct {
			State       string `json:"state"`
			Body        string `json:"body"`
			SubmittedAt string `json:"submittedAt"`
			URL         string `json:"url"`
			Author      struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"latestReviews"`
		Comments []struct {
			Body      string `json:"body"`
			CreatedAt string `json:"createdAt"`
			URL       string `json:"url"`
			Author    struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return ReviewFeedback{}, err
	}
	feedback := ReviewFeedback{
		ReviewDecision: strings.TrimSpace(payload.ReviewDecision),
		UpdatedAt:      strings.TrimSpace(payload.UpdatedAt),
	}
	for _, review := range payload.LatestReviews {
		state := strings.TrimSpace(review.State)
		if !strings.EqualFold(state, "CHANGES_REQUESTED") {
			continue
		}
		item := ReviewFeedbackItem{
			Kind:      "review",
			State:     state,
			Author:    strings.TrimSpace(review.Author.Login),
			Body:      strings.TrimSpace(review.Body),
			URL:       strings.TrimSpace(review.URL),
			CreatedAt: strings.TrimSpace(review.SubmittedAt),
		}
		feedback.Items = append(feedback.Items, item)
	}
	for _, comment := range payload.Comments {
		body := strings.TrimSpace(comment.Body)
		if body == "" {
			continue
		}
		item := ReviewFeedbackItem{
			Kind:      "comment",
			Author:    strings.TrimSpace(comment.Author.Login),
			Body:      body,
			URL:       strings.TrimSpace(comment.URL),
			CreatedAt: strings.TrimSpace(comment.CreatedAt),
		}
		feedback.Items = append(feedback.Items, item)
	}
	sort.Slice(feedback.Items, func(i, j int) bool {
		return feedback.Items[i].CreatedAt < feedback.Items[j].CreatedAt
	})
	for _, item := range feedback.Items {
		if item.CreatedAt > feedback.LatestAt {
			feedback.LatestAt = item.CreatedAt
		}
	}
	if feedback.LatestAt == "" && strings.EqualFold(feedback.ReviewDecision, "CHANGES_REQUESTED") {
		feedback.LatestAt = feedback.UpdatedAt
	}
	feedback.Summary = summarizeReviewFeedback(feedback)
	return feedback, nil
}

func summarizeReviewFeedback(feedback ReviewFeedback) string {
	var lines []string
	if feedback.ReviewDecision != "" {
		lines = append(lines, "Review decision: "+feedback.ReviewDecision)
	}
	start := 0
	if len(feedback.Items) > 3 {
		start = len(feedback.Items) - 3
	}
	for _, item := range feedback.Items[start:] {
		kind := firstNonEmpty(item.Kind, "feedback")
		author := firstNonEmpty(item.Author, "unknown")
		at := strings.TrimSpace(item.CreatedAt)
		prefix := kind + " from " + author
		if at != "" {
			prefix += " at " + at
		}
		body := strings.TrimSpace(item.Body)
		if body == "" && item.State != "" {
			body = item.State
		}
		if body != "" {
			lines = append(lines, prefix+": "+truncateFeedbackBody(body, 500))
		}
	}
	return strings.Join(lines, "\n")
}

func truncateFeedbackBody(body string, limit int) string {
	body = strings.Join(strings.Fields(body), " ")
	if limit <= 0 || len(body) <= limit {
		return body
	}
	return strings.TrimSpace(body[:limit]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func (r Runner) CheckList(ctx context.Context, pr string) ([]CheckStatus, CommandResult, error) {
	if pr == "" {
		return nil, CommandResult{}, errors.New("pr identifier is required")
	}
	args := []string{"pr", "checks", pr}
	if r.ChecksRequiredOnly {
		args = append(args, "--required")
	}
	args = append(args, "--json", "bucket,completedAt,description,event,link,name,startedAt,state,workflow")
	result, err := r.run(ctx, r.ghPath(), "gh", args...)
	if err != nil {
		return nil, result, err
	}
	var checks []CheckStatus
	if strings.TrimSpace(result.Stdout) != "" {
		if err := json.Unmarshal([]byte(result.Stdout), &checks); err != nil {
			return nil, result, fmt.Errorf("parse pr checks JSON: %w", err)
		}
	}
	result.Checks = checks
	return checks, result, nil
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
	args := []string{"pr", "merge", opts.PR, mergeFlag(opts.Method)}
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

func mergeFlag(method string) string {
	switch strings.TrimSpace(method) {
	case "merge_commit", "merge":
		return "--merge"
	default:
		return "--squash"
	}
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
	Method       string
	Subject      string
	BodyFile     string
	DeleteBranch bool
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
