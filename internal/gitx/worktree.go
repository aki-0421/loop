package gitx

import "context"

func (r Runner) AddWorktree(ctx context.Context, path, branch string, createBranch bool) error {
	args := []string{"worktree", "add"}
	if createBranch {
		args = append(args, "-b", branch)
	} else if branch != "" {
		args = append(args, path, branch)
		_, err := r.Run(ctx, args...)
		return err
	}
	args = append(args, path)
	if createBranch {
		args = append(args, "HEAD")
	}
	_, err := r.Run(ctx, args...)
	return err
}

func (r Runner) RemoveWorktree(ctx context.Context, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := r.Run(ctx, args...)
	return err
}
