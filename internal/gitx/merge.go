package gitx

import (
	"context"
	"strings"
)

type Commit struct {
	Hash    string
	Subject string
}

func (r Runner) ListCommits(ctx context.Context, from, to string) ([]Commit, error) {
	revision := to
	if from != "" {
		revision = from + ".." + to
	}
	out, err := r.Run(ctx, "log", "--format=%H%x1f%s%x1e", revision)
	if err != nil {
		return nil, err
	}
	records := strings.Split(out, "\x1e")
	commits := make([]Commit, 0, len(records))
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		hash := strings.TrimSpace(parts[0])
		subject := strings.TrimSpace(parts[1])
		if hash == "" {
			continue
		}
		commits = append(commits, Commit{Hash: hash, Subject: subject})
	}
	return commits, nil
}

func (r Runner) SquashMerge(ctx context.Context, base, branch, message string, pull bool) error {
	if _, err := r.Run(ctx, "checkout", base); err != nil {
		return err
	}
	if pull {
		if _, err := r.Run(ctx, "pull", "--ff-only"); err != nil {
			return err
		}
	}
	if _, err := r.Run(ctx, "merge", "--squash", branch); err != nil {
		return err
	}
	_, err := r.Run(ctx, "commit", "-m", message)
	return err
}
