package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/gitx"
)

func commandCommit(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() < 2 {
		return codedError{2, fmt.Errorf("usage: loop commit <type> <message>")}
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, fmt.Errorf("not inside a git repository: %w", err)}
	}
	cfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent}})
	if err != nil {
		return codedError{3, err}
	}
	subject, err := buildLoopCommitSubject(fs.Arg(0), strings.Join(fs.Args()[1:], " "), cfg.Git.Commits.MessageMaxLength)
	if err != nil {
		return codedError{2, err}
	}
	runner := gitx.Runner{Dir: root}
	dirty, err := runner.CheckClean(ctx, gitx.CleanOptions{IgnoreRuntime: true})
	if err != nil {
		return codedError{1, err}
	}
	if dirty.Clean {
		return codedError{1, errors.New("no repository changes to commit")}
	}
	pathspecs := dirtyPathspecs(dirty.Dirty)
	if len(pathspecs) == 0 {
		return codedError{1, errors.New("no repository changes to commit")}
	}
	addArgs := append([]string{"add", "-A", "--"}, pathspecs...)
	if _, err := runner.Run(ctx, addArgs...); err != nil {
		return codedError{1, err}
	}
	unstageRuntimePaths(ctx, runner)
	if _, err := runner.Run(ctx, "commit", "-m", subject); err != nil {
		return codedError{1, err}
	}
	sha, err := runner.Head(ctx)
	if err != nil {
		return codedError{1, err}
	}
	sha = strings.TrimSpace(sha)
	out := map[string]any{"sha": sha, "message": subject}
	return printResult(g, out, fmt.Sprintf("Committed %s %s\n", shortSHA(sha), subject))
}

func buildLoopCommitSubject(kind, message string, maxLength int) (string, error) {
	prefix, err := normalizeLoopCommitType(kind)
	if err != nil {
		return "", err
	}
	body := strings.TrimSpace(message)
	subject := prefix + ": " + body
	if err := validateLoopCommitSubject(subject, maxLength); err != nil {
		return "", err
	}
	return subject, nil
}

func normalizeLoopCommitType(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "f", "feat", "feature", "fix", "behavior":
		return "F", nil
	case "t", "test", "tests":
		return "T", nil
	case "r", "refactor":
		return "R", nil
	case "d", "doc", "docs", "documentation":
		return "D", nil
	case "s", "style", "presentation":
		return "S", nil
	case "v", "version", "dependency", "dependencies", "deps", "license", "licensing":
		return "V", nil
	case "c", "config", "build", "ci", "tool", "tools", "tooling", "chore":
		return "C", nil
	default:
		return "", fmt.Errorf("commit type must be one of F, T, R, D, S, V, or C")
	}
}

func validateLoopCommitSubject(subject string, maxLength int) error {
	var problems []string
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return errors.New("commit message is required")
	}
	if strings.ContainsAny(subject, "\r\n") {
		problems = append(problems, "commit message must be a single line")
	}
	if maxLength > 0 && len(subject) > maxLength {
		problems = append(problems, fmt.Sprintf("commit message must be at most %d characters", maxLength))
	}
	prefix, body, ok := strings.Cut(subject, ": ")
	if !ok {
		problems = append(problems, "commit message must use <TYPE>: <message>")
	} else {
		if !isLoopCommitPrefix(prefix) {
			problems = append(problems, "commit type must be one of F, T, R, D, S, V, or C")
		}
		if strings.TrimSpace(body) == "" {
			problems = append(problems, "commit message body is required")
		} else {
			if strings.TrimSpace(body) != body {
				problems = append(problems, "commit message body must not have leading or trailing whitespace")
			}
			first := body[0]
			if first < 'a' || first > 'z' {
				problems = append(problems, "commit message body must start with a lowercase English letter")
			}
			if strings.HasSuffix(body, ".") {
				problems = append(problems, "commit message must not end with a period")
			}
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateIterationCommitSubjects(commits []gitx.Commit, cfg config.Config) error {
	if !cfg.Git.Commits.EnforcePattern {
		return nil
	}
	var problems []string
	for _, commit := range commits {
		if err := validateLoopCommitSubject(commit.Subject, cfg.Git.Commits.MessageMaxLength); err != nil {
			problems = append(problems, fmt.Sprintf("commit %s %q is invalid: %v", shortSHA(commit.Hash), commit.Subject, err))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func isLoopCommitPrefix(prefix string) bool {
	switch prefix {
	case "F", "T", "R", "D", "S", "V", "C":
		return true
	default:
		return false
	}
}

func dirtyPathspecs(entries []gitx.StatusEntry) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, entry := range entries {
		add(entry.Path)
		add(entry.Orig)
	}
	return paths
}

func unstageRuntimePaths(ctx context.Context, runner gitx.Runner) {
	_, _ = runner.Run(ctx, "reset", "-q", "--", ".loop/runs", ".loop/worktrees", ".loop/tmp", ".loop/locks")
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
