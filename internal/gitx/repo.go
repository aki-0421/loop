package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultGitTimeout = 30 * time.Second

// Runner executes git commands rooted at Dir.
type Runner struct {
	Dir     string
	GitPath string
	Env     []string
	Timeout time.Duration
}

// CommandError includes command output for integration logs and diagnostics.
type CommandError struct {
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
	return fmt.Sprintf("git %s failed in %s: %s", strings.Join(e.Args, " "), e.Dir, msg)
}

func (e *CommandError) Unwrap() error {
	return e.Err
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
	return defaultGitTimeout
}

// Run executes git and returns stdout. Stderr is preserved in CommandError.
func (r Runner) Run(ctx context.Context, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()

	cmd := exec.CommandContext(cctx, r.gitPath(), args...)
	cmd.Dir = r.Dir
	if len(r.Env) > 0 {
		cmd.Env = append(cmd.Environ(), r.Env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := stdout.String()
	if cctx.Err() != nil {
		err = cctx.Err()
	}
	if err != nil {
		return out, &CommandError{
			Dir:    r.Dir,
			Args:   append([]string(nil), args...),
			Stdout: out,
			Stderr: stderr.String(),
			Err:    err,
		}
	}
	return out, nil
}

// RepoRoot returns the absolute path to the containing git repository.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	out, err := (Runner{Dir: dir}).Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", errors.New("git repository root was empty")
	}
	return filepath.Abs(root)
}

// IsRepository reports whether dir is inside a git work tree.
func IsRepository(ctx context.Context, dir string) bool {
	out, err := (Runner{Dir: dir}).Run(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// CurrentBranch returns the checked-out branch name.
func (r Runner) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "", errors.New("current git HEAD is detached")
	}
	return branch, nil
}

func (r Runner) Head(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(out)
	if head == "" {
		return "", errors.New("current git HEAD is empty")
	}
	return head, nil
}

// MainBranch returns the repository's primary branch from configured remote HEADs.
func (r Runner) MainBranch(ctx context.Context) (string, error) {
	for _, ref := range []string{"refs/remotes/origin/HEAD", "refs/remotes/upstream/HEAD"} {
		out, err := r.Run(ctx, "symbolic-ref", "--quiet", "--short", ref)
		if err != nil {
			continue
		}
		branch := strings.TrimSpace(out)
		if remote, name, ok := strings.Cut(branch, "/"); ok && remote != "" && name != "" {
			return name, nil
		}
		if branch != "" {
			return branch, nil
		}
	}
	return "", errors.New("main branch could not be inferred")
}
