package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/workflow"
)

func commandHandoff(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop handoff <write|read|list> ...")}
	}
	switch args[0] {
	case "write":
		return commandHandoffWrite(ctx, g, args[1:])
	case "read":
		return commandHandoffRead(ctx, g, args[1:])
	case "list":
		return commandHandoffList(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown handoff subcommand %q", args[0])}
	}
}

func commandHandoffWrite(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("handoff write", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id for task-result")
	sourceFile := fs.String("file", "", "source file, or - for stdin")
	value := fs.String("value", "", "literal JSON payload")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true, "dir": true, "run": true, "iteration": true,
		"task": true, "file": true, "value": true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	valueSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "value" {
			valueSet = true
		}
	})
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop handoff write <task-tree|task-result|review-result> [--task <id>] [--file <path>|--value <json>]")}
	}
	kind := normalizeHandoffKind(fs.Arg(0))
	if kind == "" {
		return codedError{2, fmt.Errorf("handoff kind must be task-tree, task-result, or review-result")}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	if strings.TrimSpace(resolvedDir) == "" {
		return codedError{2, errors.New("iteration directory is required")}
	}
	data, err := readArtifactInput(*sourceFile, *value, valueSet)
	if err != nil {
		return codedError{2, err}
	}
	task := ""
	if kind == "task-result" {
		task = strings.TrimSpace(*taskID)
		if task == "" {
			return codedError{2, errors.New("--task is required for task-result handoffs")}
		}
	}
	normalized, err := validateRoleHandoff(kind, task, data)
	if err != nil {
		return codedError{2, err}
	}
	run, iter := artifactdb.ParseIterationDir(resolvedDir)
	run = firstNonEmpty(run, strings.TrimSpace(*runID))
	iter = firstNonEmpty(iter, strings.TrimSpace(*iteration))
	if run == "" || iter == "" || iter == "latest" {
		return codedError{2, errors.New("run id and concrete iteration id are required")}
	}
	globalPath := artifactdb.GlobalDBPathForIteration(resolvedDir)
	if globalPath == "" {
		return codedError{2, errors.New("iteration directory must be under .loop/runs")}
	}
	if err := artifactdb.WriteRoleHandoff(globalPath, run, iter, kind, task, string(normalized)); err != nil {
		return codedError{1, err}
	}
	if err := writeRoleHandoffAudit(resolvedDir, kind, task, normalized); err != nil {
		return codedError{1, err}
	}
	cleanupTransientHandoffSourceFile(ctx, *sourceFile, kind)
	return printResult(g, map[string]any{"handoff": kind, "task": task}, fmt.Sprintf("wrote %s handoff\n", kind))
}

func commandHandoffRead(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("handoff read", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	taskID := fs.String("task", os.Getenv("LOOP_TASK_ID"), "task id for task-result")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "task": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop handoff read <task-tree|task-result|review-result> [--task <id>]")}
	}
	kind := normalizeHandoffKind(fs.Arg(0))
	task := ""
	if kind == "task-result" {
		task = strings.TrimSpace(*taskID)
	}
	handoff, err := readRoleHandoffForFlags(ctx, g, *iterDir, *runID, *iteration, kind, task)
	if err != nil {
		return codedError{1, err}
	}
	if g.JSON {
		return printResult(g, map[string]any{"handoff": kind, "task": task, "content": handoff.Payload}, "")
	}
	fmt.Print(handoff.Payload)
	return nil
}

func commandHandoffList(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("handoff list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	kind := fs.String("kind", "", "handoff kind")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "kind": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	if strings.TrimSpace(resolvedDir) == "" {
		return codedError{2, errors.New("iteration directory is required")}
	}
	run, iter := artifactdb.ParseIterationDir(resolvedDir)
	globalPath := artifactdb.GlobalDBPathForIteration(resolvedDir)
	handoffs, err := artifactdb.ListRoleHandoffs(globalPath, run, iter, normalizeHandoffKind(*kind))
	if err != nil {
		return codedError{1, err}
	}
	var lines strings.Builder
	for _, handoff := range handoffs {
		label := handoff.Kind
		if handoff.TaskID != "" {
			label += " " + handoff.TaskID
		}
		lines.WriteString(label + "\n")
	}
	return printResult(g, map[string]any{"handoffs": handoffs}, lines.String())
}

func readRoleHandoffForFlags(ctx context.Context, g globals, iterDir, runID, iteration, kind, task string) (artifactdb.RoleHandoff, error) {
	resolvedDir, err := resolveIterationDir(ctx, g, iterDir, runID, iteration)
	if err != nil {
		return artifactdb.RoleHandoff{}, err
	}
	run, iter := artifactdb.ParseIterationDir(resolvedDir)
	run = firstNonEmpty(run, strings.TrimSpace(runID))
	iter = firstNonEmpty(iter, strings.TrimSpace(iteration))
	globalPath := artifactdb.GlobalDBPathForIteration(resolvedDir)
	return artifactdb.ReadRoleHandoff(globalPath, run, iter, kind, task)
}

func validateRoleHandoff(kind, taskID string, data []byte) ([]byte, error) {
	switch kind {
	case "task-tree":
		tree, err := workflow.DecodeTaskTree(data)
		if err != nil {
			return nil, err
		}
		return workflow.MarshalIndent(tree)
	case "task-result":
		result, err := workflow.DecodeTaskResult(data)
		if err != nil {
			return nil, err
		}
		if taskID != "" && result.TaskID != taskID {
			return nil, fmt.Errorf("task-result task_id %q does not match --task %q", result.TaskID, taskID)
		}
		return workflow.MarshalIndent(result)
	case "review-result":
		result, err := workflow.DecodeReviewResult(data)
		if err != nil {
			return nil, err
		}
		return workflow.MarshalIndent(result)
	default:
		return nil, fmt.Errorf("unknown handoff kind %q", kind)
	}
}

func writeRoleHandoffAudit(iterDir, kind, taskID string, data []byte) error {
	var path string
	switch kind {
	case "task-tree":
		path = filepath.Join(iterDir, "task-tree.json")
	case "review-result":
		path = filepath.Join(iterDir, "review-result.json")
	case "task-result":
		return writeTaskResultAudit(iterDir, strings.TrimSpace(os.Getenv("LOOP_TASK_DIR")), taskID, data)
	default:
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeTaskResultAudit(iterDir, taskDir, taskID string, data []byte) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("task id is required for task-result audit")
	}
	dir := strings.TrimSpace(taskDir)
	if dir != "" {
		ok, err := taskDirBelongsToIteration(iterDir, dir)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("task directory %q is not under %s/tasks", dir, iterDir)
		}
	} else {
		found, err := findTaskAuditDir(iterDir, taskID)
		if err != nil {
			return err
		}
		dir = found
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "task-result.json"), data, 0o644)
}

func taskDirBelongsToIteration(iterDir, taskDir string) (bool, error) {
	tasksDir, err := filepath.Abs(filepath.Join(iterDir, "tasks"))
	if err != nil {
		return false, err
	}
	absTaskDir, err := filepath.Abs(taskDir)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(tasksDir, absTaskDir)
	if err != nil {
		return false, err
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)), nil
}

func findTaskAuditDir(iterDir, taskID string) (string, error) {
	tasksDir := filepath.Join(iterDir, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return "", fmt.Errorf("find task directory for %q: %w", taskID, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(tasksDir, entry.Name())
		data, err := os.ReadFile(filepath.Join(dir, "task.json"))
		if err != nil {
			continue
		}
		var task workflow.Task
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&task); err != nil {
			continue
		}
		if task.ID == taskID {
			return dir, nil
		}
	}
	return "", fmt.Errorf("task directory for task-result %q was not found under %s", taskID, tasksDir)
}

func normalizeHandoffKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "task-tree", "task_tree", "planner", "plan":
		return "task-tree"
	case "task-result", "task_result", "task", "code", "coding":
		return "task-result"
	case "review-result", "review_result", "review":
		return "review-result"
	default:
		return ""
	}
}

func cleanupTransientHandoffSourceFile(ctx context.Context, sourceFile, kind string) {
	sourceFile = strings.TrimSpace(sourceFile)
	if sourceFile == "" || sourceFile == "-" {
		return
	}
	if filepath.Base(sourceFile) != defaultHandoffFileName(kind) {
		return
	}
	abs, err := filepath.Abs(sourceFile)
	if err != nil {
		return
	}
	cleanAbs := filepath.Clean(abs)
	parts := strings.Split(filepath.ToSlash(cleanAbs), "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == ".loop" && (parts[i+1] == "runs" || parts[i+1] == "tmp" || parts[i+1] == "locks") {
			return
		}
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return
	}
	rel, err := filepath.Rel(root, cleanAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return
	}
	if gitPathIsTracked(ctx, root, rel) {
		return
	}
	_ = os.Remove(cleanAbs)
}

func defaultHandoffFileName(kind string) string {
	switch kind {
	case "task-tree":
		return "task-tree.json"
	case "task-result":
		return "task-result.json"
	case "review-result":
		return "review-result.json"
	default:
		return ""
	}
}

func gitPathIsTracked(ctx context.Context, root, path string) bool {
	_, err := (gitx.Runner{Dir: root}).Run(ctx, "ls-files", "--error-unmatch", "--", filepath.ToSlash(path))
	return err == nil
}
