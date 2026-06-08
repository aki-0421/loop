package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/runstate"
)

func commandBranch(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop branch <rename>")}
	}
	switch args[0] {
	case "rename":
		return commandBranchRename(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown branch subcommand %q", args[0])}
	}
}

func commandBranchRename(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("branch rename", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	kind := fs.String("kind", "", "branch kind")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "kind": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() == 0 {
		return codedError{2, fmt.Errorf("usage: loop branch rename [--kind <kind>] <kind>/<slug>|<slug words...>")}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	if strings.TrimSpace(resolvedDir) == "" {
		return codedError{2, fmt.Errorf("iteration directory is required; run inside an agent iteration or pass --iteration-dir")}
	}
	if err := rejectBranchRenameAfterIntegration(resolvedDir); err != nil {
		return codedError{2, err}
	}

	root, err := loadBranchCommandRoot(ctx, g)
	if err != nil {
		return codedError{1, err}
	}
	proposal := strings.Join(fs.Args(), " ")
	desired, err := gitx.FinalBranchName(*kind, proposal)
	if err != nil {
		return codedError{2, fmt.Errorf("%w (allowed kinds: %s)", err, strings.Join(gitx.BranchKinds(), ", "))}
	}

	runtime, err := readRuntimeMap(resolvedDir)
	if err != nil {
		return codedError{1, err}
	}
	_, repairingPR := runtime["repair_pull_request"]
	if repairingPR {
		return codedError{2, fmt.Errorf("pending PR repair iterations keep the existing PR branch; do not run loop branch rename")}
	}
	tracked := trackedBranchFromRuntime(runtime, "", "")
	workDir := firstNonEmpty(runtimeString(runtime, "workdir"), os.Getenv("LOOP_WORKDIR"), root)
	if workDir == "" {
		workDir = "."
	}
	runner := gitx.Runner{Dir: workDir}
	if tracked.Current == "" {
		current, err := runner.CurrentBranch(ctx)
		if err != nil {
			return codedError{1, err}
		}
		tracked.Current = current
		if tracked.Initial == "" {
			tracked.Initial = current
		}
	}
	current, err := runner.CurrentBranch(ctx)
	if err != nil {
		return codedError{1, err}
	}
	if current != tracked.Current {
		return codedError{2, fmt.Errorf("current branch is %q, but loop runtime tracks %q; use `loop branch rename ...` instead of direct Git branch changes", current, tracked.Current)}
	}
	actual := desired
	if desired != tracked.Current {
		actual, err = runner.UniqueBranchName(ctx, desired, iterationIDForDir(resolvedDir))
		if err != nil {
			return codedError{1, err}
		}
		if err := runner.RenameBranch(ctx, tracked.Current, actual); err != nil {
			return codedError{1, err}
		}
	}
	if _, err := updateTrackedBranch(resolvedDir, actual); err != nil {
		return codedError{1, err}
	}
	if err := updateRunStateCurrentBranch(resolvedDir, actual); err != nil {
		return codedError{1, err}
	}
	appendBranchRenameEvent(resolvedDir, tracked.Current, actual)
	return printResult(g, map[string]any{"branch": actual, "renamed": actual != tracked.Current}, actual+"\n")
}

func loadBranchCommandRoot(ctx context.Context, g globals) (string, error) {
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return "", err
	}
	if _, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent, NoColor: g.NoColor}}); err != nil {
		return "", err
	}
	return root, nil
}

func iterationIDForDir(iterationDir string) string {
	_, iterationID := artifactdb.ParseIterationDir(iterationDir)
	if iterationID != "" {
		return iterationID
	}
	return filepath.Base(filepath.Clean(iterationDir))
}

func appendBranchRenameEvent(iterationDir, from, to string) {
	_ = runstate.AppendEvent(filepath.Join(iterationDir, "agent-events.jsonl"), runstate.Event{
		"type": "git.branch.renamed",
		"from": from,
		"to":   to,
	})
}
