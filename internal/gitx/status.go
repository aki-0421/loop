package gitx

import (
	"context"
	"strings"
)

type CleanOptions struct {
	IgnoreRuntime bool
	RuntimePaths  []string
}

type CleanResult struct {
	Clean bool
	Dirty []StatusEntry
}

type StatusEntry struct {
	Code string
	Path string
	Orig string
}

func (r Runner) CheckClean(ctx context.Context, opts CleanOptions) (CleanResult, error) {
	out, err := r.Run(ctx, "status", "--porcelain=v1", "--untracked-files=all", "-z")
	if err != nil {
		return CleanResult{}, err
	}
	entries := parseStatus(out)
	filtered := entries[:0]
	for _, entry := range entries {
		if opts.ignore(entry.Path) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return CleanResult{Clean: len(filtered) == 0, Dirty: filtered}, nil
}

func parseStatus(out string) []StatusEntry {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	entries := make([]StatusEntry, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if part == "" {
			continue
		}
		if len(part) < 4 {
			entries = append(entries, StatusEntry{Path: part})
			continue
		}
		entry := StatusEntry{Code: part[:2], Path: part[3:]}
		if strings.HasPrefix(entry.Code, "R") || strings.HasPrefix(entry.Code, "C") {
			if i+1 < len(parts) {
				entry.Orig = parts[i+1]
				i++
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func (o CleanOptions) ignore(path string) bool {
	prefixes := append([]string(nil), o.RuntimePaths...)
	if o.IgnoreRuntime {
		prefixes = append(prefixes,
			".loop/runs/",
			".loop/worktrees/",
			".loop/tmp/",
			".loop/locks/",
			".loop/loop.db",
			".loop/loop.db-wal",
			".loop/loop.db-shm",
		)
	}
	for _, prefix := range prefixes {
		prefix = strings.TrimPrefix(prefix, "./")
		if prefix == "" {
			continue
		}
		if strings.HasSuffix(prefix, "/") {
			if strings.HasPrefix(path, prefix) {
				return true
			}
			continue
		}
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
