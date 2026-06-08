package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/gitx"
)

const loopHomeEnv = "LOOP_HOME"

type loopStoragePaths struct {
	Root         string
	RunsDir      string
	WorktreesDir string
	LocksDir     string
	TmpDir       string
	DBPath       string
}

func loopStorageForRepo(ctx context.Context, repoRoot string, cfg config.Config) (loopStoragePaths, error) {
	root, err := loopRuntimeRoot(ctx, repoRoot)
	if err != nil {
		return loopStoragePaths{}, err
	}
	return loopStoragePaths{
		Root:         root,
		RunsDir:      loopRunsDir(root, cfg),
		WorktreesDir: filepath.Join(root, "worktrees"),
		LocksDir:     filepath.Join(root, "locks"),
		TmpDir:       filepath.Join(root, "tmp"),
		DBPath:       filepath.Join(root, artifactdb.GlobalDBName),
	}, nil
}

func loopRuntimeRoot(ctx context.Context, repoRoot string) (string, error) {
	home, err := loopHomeDir()
	if err != nil {
		return "", err
	}
	id, err := loopRepositoryID(ctx, repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "workspaces", id), nil
}

func loopHomeDir() (string, error) {
	if raw := strings.TrimSpace(os.Getenv(loopHomeEnv)); raw != "" {
		if filepath.IsAbs(raw) {
			return filepath.Clean(raw), nil
		}
		abs, err := filepath.Abs(raw)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".loop"), nil
}

func loopRepositoryID(ctx context.Context, repoRoot string) (string, error) {
	identityRoot := loopRepositoryIdentityRoot(ctx, repoRoot)
	abs, err := filepath.Abs(identityRoot)
	if err != nil {
		return "", err
	}
	if evaluated, err := filepath.EvalSymlinks(abs); err == nil {
		abs = evaluated
	}
	name := sanitizeTempPart(filepath.Base(abs))
	sum := sha256.Sum256([]byte(filepath.ToSlash(abs)))
	hash := hex.EncodeToString(sum[:])[:12]
	return name + "-" + hash, nil
}

func loopRepositoryIdentityRoot(ctx context.Context, repoRoot string) string {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		root = "."
	}
	common, err := (gitx.Runner{Dir: root}).Run(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return root
	}
	common = strings.TrimSpace(common)
	if common == "" {
		return root
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return root
}

func loopRunsDir(storageRoot string, cfg config.Config) string {
	dir := strings.TrimSpace(cfg.Logs.Dir)
	if dir == "" {
		dir = "runs"
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	dir = filepath.Clean(dir)
	if dir == filepath.Join(".loop", "runs") {
		dir = "runs"
	}
	return filepath.Join(storageRoot, dir)
}

func displayPath(root, path string) string {
	relPath, err := filepath.Rel(root, path)
	if err != nil || relPath == "." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) || filepath.IsAbs(relPath) {
		return path
	}
	return relPath
}
