package pr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 2 * time.Minute

type Runner struct {
	Dir     string
	GHPath  string
	GitPath string
	Env     []string
	Timeout time.Duration
}

type CommandResult struct {
	Args   []string
	Stdout string
	Stderr string
}

type CommandError struct {
	Tool   string
	Dir    string
	Args   []string
	Stdout string
	Stderr string
	Err    error
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
	if watch {
		args = append(args, "--watch")
	}
	result, err := r.run(ctx, r.ghPath(), "gh", args...)
	if err != nil && isNoChecksReported(err) {
		return result, nil
	}
	return result, err
}

func isNoChecksReported(err error) bool {
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	output := strings.ToLower(commandErr.Stdout + "\n" + commandErr.Stderr)
	return strings.Contains(output, "no checks reported")
}

func (r Runner) Merge(ctx context.Context, opts MergeOptions) (CommandResult, error) {
	if opts.PR == "" {
		return CommandResult{}, errors.New("pr identifier is required")
	}
	args := MergeArgs(opts)
	return r.run(ctx, r.ghPath(), "gh", args...)
}

func MergeArgs(opts MergeOptions) []string {
	args := []string{"pr", "merge", opts.PR, "--squash"}
	if opts.Subject != "" {
		args = append(args, "--subject", opts.Subject)
	}
	if opts.BodyFile != "" {
		args = append(args, "--body-file", opts.BodyFile)
	}
	if opts.DeleteBranch {
		args = append(args, "--delete-branch")
	}
	return args
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
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, r.timeout())
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
	result := CommandResult{Args: append([]string(nil), args...), Stdout: stdout.String(), Stderr: stderr.String()}
	if cctx.Err() != nil {
		err = cctx.Err()
	}
	if err != nil {
		return result, &CommandError{Tool: tool, Dir: r.Dir, Args: result.Args, Stdout: result.Stdout, Stderr: result.Stderr, Err: err}
	}
	return result, nil
}
