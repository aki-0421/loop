package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/runstate"
)

const worktreeIncludeFile = ".worktreeinclude"

func copyWorktreeIncludedIgnoredPaths(ctx context.Context, runner gitx.Runner, root, worktree, events string) error {
	includePath := filepath.Join(root, worktreeIncludeFile)
	if _, err := os.Stat(includePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", worktreeIncludeFile, err)
	}
	paths, err := listWorktreeIncludeCandidates(ctx, runner, includePath)
	if err != nil {
		return err
	}
	var copied []string
	for _, rel := range paths {
		if shouldSkipWorktreeIncludePath(rel) {
			continue
		}
		if !isIgnoredByStandardRules(ctx, runner, rel) {
			continue
		}
		source := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(source)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspect worktree include source %s: %w", rel, err)
		}
		destination := filepath.Join(worktree, filepath.FromSlash(rel))
		if err := replaceWithCopy(source, destination, info); err != nil {
			return fmt.Errorf("copy worktree include path %s: %w", rel, err)
		}
		copied = append(copied, rel)
	}
	if len(copied) > 0 && events != "" {
		_ = runstate.AppendEvent(events, runstate.Event{"type": "worktree.include_copied", "paths": copied, "worktree": worktree})
	}
	return nil
}

func listWorktreeIncludeCandidates(ctx context.Context, runner gitx.Runner, includePath string) ([]string, error) {
	out, err := runner.Run(ctx, "ls-files", "--others", "--ignored", "--exclude-from", includePath, "--directory", "-z")
	if err != nil {
		return nil, fmt.Errorf("list %s matches: %w", worktreeIncludeFile, err)
	}
	parts := bytes.Split([]byte(out), []byte{0})
	seen := map[string]bool{}
	var paths []string
	for _, part := range parts {
		rel := cleanWorktreeIncludePath(string(part))
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		paths = append(paths, rel)
	}
	return paths, nil
}

func cleanWorktreeIncludePath(path string) string {
	rel := strings.TrimSpace(path)
	if rel == "" {
		return ""
	}
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSuffix(rel, "/")))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}

func shouldSkipWorktreeIncludePath(rel string) bool {
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return true
	}
	return rel == ".loop/worktrees" || strings.HasPrefix(rel, ".loop/worktrees/")
}

func isIgnoredByStandardRules(ctx context.Context, runner gitx.Runner, rel string) bool {
	_, err := runner.Run(ctx, "check-ignore", "-q", "--", rel)
	return err == nil
}

func replaceWithCopy(source, destination string, info os.FileInfo) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(target, destination)
	}
	if info.IsDir() {
		return copyDirectory(source, destination)
	}
	return copyFile(source, destination, info.Mode())
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(destination, 0o755)
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
