package gitx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

var loopBranchKinds = []string{"feat", "fix", "refactor", "docs", "test", "style", "build", "ci", "chore"}

func BranchKinds() []string {
	return append([]string(nil), loopBranchKinds...)
}

// InitialBranchName formats the temporary numbered branch name for an iteration.
func InitialBranchName(iteration int) string {
	if iteration < 0 {
		iteration = 0
	}
	return fmt.Sprintf("wip/%04d", iteration)
}

// FinalBranchName builds a branch name from a fixed loop kind and a slug source.
func FinalBranchName(kind, proposal string) (string, error) {
	kind = strings.TrimSpace(kind)
	proposal = strings.TrimSpace(proposal)
	if slash := strings.Index(proposal, "/"); kind == "" && slash > 0 {
		kind = strings.TrimSpace(proposal[:slash])
		proposal = proposal[slash+1:]
	}
	if kind == "" {
		kind = "chore"
	}
	if !allowedKind(kind) {
		return "", fmt.Errorf("branch kind %q is not allowed", kind)
	}
	slug := Slug(proposal)
	if slug == "" {
		slug = "iteration"
	}
	return kind + "/" + slug, nil
}

// Slug converts text to lowercase ASCII words separated by hyphens.
func Slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevHyphen := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevHyphen = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func allowedKind(kind string) bool {
	for _, candidate := range loopBranchKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

// BranchExists checks for either a local or remote branch ref.
func (r Runner) BranchExists(ctx context.Context, branch string) (bool, error) {
	_, err := r.Run(ctx, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	if err != nil && !isExitStatus(err, 1) {
		return false, err
	}
	_, err = r.Run(ctx, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err == nil {
		return true, nil
	}
	if err != nil && !isExitStatus(err, 1) {
		return false, err
	}
	return false, nil
}

func isExitStatus(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

// UniqueBranchName appends suffixes only when needed to avoid branch collisions.
func (r Runner) UniqueBranchName(ctx context.Context, branch, iterationSuffix string) (string, error) {
	candidates := []string{branch}
	if iterationSuffix != "" {
		candidates = append(candidates, branch+"-"+iterationSuffix)
	}
	for i := 2; i < 1000; i++ {
		stem := branch
		if iterationSuffix != "" {
			stem = branch + "-" + iterationSuffix
		}
		candidates = append(candidates, stem+"-"+strconv.Itoa(i))
	}
	for _, candidate := range candidates {
		exists, err := r.BranchExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find available branch name for %q", branch)
}

func (r Runner) CreateBranch(ctx context.Context, branch, startPoint string) error {
	args := []string{"checkout", "-b", branch}
	if startPoint != "" {
		args = append(args, startPoint)
	}
	_, err := r.Run(ctx, args...)
	return err
}

func (r Runner) RenameBranch(ctx context.Context, oldName, newName string) error {
	args := []string{"branch", "-m"}
	if oldName != "" {
		args = append(args, oldName)
	}
	args = append(args, newName)
	_, err := r.Run(ctx, args...)
	return err
}

func (r Runner) DeleteBranch(ctx context.Context, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := r.Run(ctx, "branch", flag, branch)
	return err
}
