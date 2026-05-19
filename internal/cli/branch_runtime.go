package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/runstate"
)

type trackedBranchState struct {
	Initial string
	Current string
	Renamed bool
}

func readRuntimeMap(iterationDir string) (map[string]any, error) {
	data, err := artifactdb.Read(iterationDir, "runtime")
	if err != nil {
		return nil, err
	}
	runtime := map[string]any{}
	if err := json.Unmarshal([]byte(data), &runtime); err != nil {
		return nil, err
	}
	return runtime, nil
}

func writeRuntimeMap(iterationDir string, runtime map[string]any) error {
	data, err := json.MarshalIndent(runtime, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return artifactdb.Write(iterationDir, "runtime", string(data))
}

func runtimeString(runtime map[string]any, key string) string {
	switch value := runtime[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func runtimeBool(runtime map[string]any, key string) bool {
	switch value := runtime[key].(type) {
	case bool:
		return value
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
		return parsed
	default:
		return false
	}
}

func trackedBranchFromRuntime(runtime map[string]any, fallbackInitial, fallbackCurrent string) trackedBranchState {
	initial := firstNonEmpty(runtimeString(runtime, "initial_branch"), runtimeString(runtime, "branch_initial"), fallbackInitial)
	current := firstNonEmpty(runtimeString(runtime, "current_branch"), runtimeString(runtime, "branch_current"), fallbackCurrent)
	if initial == "" {
		initial = current
	}
	if current == "" {
		current = initial
	}
	renamed := runtimeBool(runtime, "branch_renamed")
	if initial != "" && current != "" && initial != current {
		renamed = true
	}
	return trackedBranchState{Initial: initial, Current: current, Renamed: renamed}
}

func readTrackedBranch(iterationDir, fallbackInitial, fallbackCurrent string) (trackedBranchState, error) {
	runtime, err := readRuntimeMap(iterationDir)
	if err != nil {
		return trackedBranchState{}, err
	}
	return trackedBranchFromRuntime(runtime, fallbackInitial, fallbackCurrent), nil
}

func updateTrackedBranch(iterationDir, newBranch string) (trackedBranchState, error) {
	runtime, err := readRuntimeMap(iterationDir)
	if err != nil {
		return trackedBranchState{}, err
	}
	tracked := trackedBranchFromRuntime(runtime, "", "")
	if tracked.Initial == "" {
		tracked.Initial = tracked.Current
	}
	tracked.Current = strings.TrimSpace(newBranch)
	tracked.Renamed = tracked.Initial != "" && tracked.Current != "" && tracked.Initial != tracked.Current
	runtime["initial_branch"] = tracked.Initial
	runtime["current_branch"] = tracked.Current
	runtime["branch_renamed"] = tracked.Renamed
	if err := writeRuntimeMap(iterationDir, runtime); err != nil {
		return trackedBranchState{}, err
	}
	return tracked, nil
}

func refreshTrackedBranch(ctx context.Context, workDir string, paths *pathSet) error {
	if paths == nil {
		return errors.New("path set is required")
	}
	iterDir := filepath.Dir(paths.Result)
	tracked, err := readTrackedBranch(iterDir, paths.InitialBranch, paths.CurrentBranch)
	if err != nil {
		return err
	}
	if tracked.Current != "" && gitx.IsRepository(ctx, workDir) {
		current, err := (gitx.Runner{Dir: workDir}).CurrentBranch(ctx)
		if err != nil {
			return err
		}
		if current != tracked.Current {
			return fmt.Errorf("current branch is %q, but loop runtime tracks %q; use `loop branch rename ...` instead of direct Git branch changes", current, tracked.Current)
		}
	}
	paths.InitialBranch = tracked.Initial
	paths.CurrentBranch = tracked.Current
	paths.BranchRenamed = tracked.Renamed
	return nil
}

func runStatePathForIteration(iterationDir string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(iterationDir)), "run-state.json")
}

func readRunStateForIteration(iterationDir string) (string, runstate.State, bool, error) {
	statePath := runStatePathForIteration(iterationDir)
	if _, err := os.Stat(statePath); err != nil {
		if os.IsNotExist(err) {
			return statePath, runstate.State{}, false, nil
		}
		return statePath, runstate.State{}, false, err
	}
	state, err := runstate.Read(statePath)
	if err != nil {
		return statePath, runstate.State{}, false, err
	}
	return statePath, state, true, nil
}

func rejectBranchRenameAfterIntegration(iterationDir string) error {
	_, state, ok, err := readRunStateForIteration(iterationDir)
	if err != nil || !ok {
		return err
	}
	switch state.Stage {
	case runstate.StageIntegrating, runstate.StageCompleted, runstate.StageBlocked, runstate.StageFailed, runstate.StageCancelled:
		return fmt.Errorf("branch rename is not allowed once the run stage is %q", state.Stage)
	default:
		return nil
	}
}

func updateRunStateCurrentBranch(iterationDir, branch string) error {
	statePath, state, ok, err := readRunStateForIteration(iterationDir)
	if err != nil || !ok {
		return err
	}
	_, iterationID := artifactdb.ParseIterationDir(iterationDir)
	for i := range state.Iterations {
		if state.Iterations[i].IterationID == iterationID {
			state.Iterations[i].BranchCurrent = branch
			return runstate.Write(statePath, state)
		}
	}
	return nil
}
