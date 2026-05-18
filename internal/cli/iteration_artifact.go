package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/gitx"
)

type iterationArtifact struct {
	Name       string
	File       string
	EnvPath    string
	Writable   bool
	Repository bool
	Database   bool
}

var iterationArtifacts = map[string]iterationArtifact{
	"effective-config": {Name: "effective-config", File: "effective-config.yaml"},
	"runtime":          {Name: "runtime", File: "runtime.json", Database: true},
	"prompt":           {Name: "prompt", File: "prompt.md"},
	"plan":             {Name: "plan", File: "plan.md", Writable: true, Database: true},
	"todo":             {Name: "todo", File: "todo.md", Writable: true, Database: true},
	"worklog":          {Name: "worklog", File: "worklog.md", Writable: true, Database: true},
	"validation":       {Name: "validation", File: "validation.md", Database: true},
	"summary":          {Name: "summary", File: "summary.md", Writable: true, Database: true},
	"result":           {Name: "result", File: "result.json", Writable: true, Database: true},
	"events":           {Name: "events", File: "agent-events.jsonl"},
	"errors":           {Name: "errors", File: "errors.log"},
	"pr-title":         {Name: "pr-title", File: "pr-title.txt", Writable: true, Database: true},
	"pr-body":          {Name: "pr-body", File: "pr-body.md", Writable: true, Database: true},
	"pr-template":      {Name: "pr-template", Repository: true},
	"instruction":      {Name: "instruction", EnvPath: "LOOP_INSTRUCTION_FILE"},
}

var iterationArtifactAliases = map[string]string{
	"effective_config": "effective-config",
	"config":           "effective-config",
	"agent-events":     "events",
	"agent_events":     "events",
	"pr_title":         "pr-title",
	"pr_body":          "pr-body",
	"pull-request":     "pr-body",
	"pr_template":      "pr-template",
	"task":             "instruction",
}

func commandIteration(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop iteration <path|read|write|append> <artifact> [--iteration-dir <dir>] | loop iteration result [flags]")}
	}
	switch args[0] {
	case "path", "read":
		return commandIterationReadPath(ctx, g, args[0], args[1:])
	case "write", "append":
		return commandIterationWriteAppend(ctx, g, args[0], args[1:])
	case "result":
		return commandIterationResult(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown iteration subcommand %q", args[0])}
	}
}

func commandIterationReadPath(ctx context.Context, g globals, action string, args []string) error {
	fs := flag.NewFlagSet("iteration "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", os.Getenv("LOOP_ITERATION_DIR"), "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", "", "run id")
	iteration := fs.String("iteration", "latest", "iteration id")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 1 {
		return codedError{2, fmt.Errorf("usage: loop iteration %s <artifact> [--iteration-dir <dir>]", action)}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	path, artifact, err := resolveIterationArtifact(ctx, resolvedDir, fs.Arg(0))
	if err != nil {
		if action == "read" && isPullRequestTemplateArtifact(fs.Arg(0)) {
			data := defaultPullRequestTemplate()
			if g.JSON {
				return printResult(g, map[string]any{"artifact": "pr-template", "content": data, "fallback": true}, "")
			}
			fmt.Print(data)
			return nil
		}
		return codedError{2, err}
	}
	if action == "path" {
		if artifact.Database {
			return codedError{2, fmt.Errorf("artifact %q is stored in the loop artifact database; use `loop iteration read %s` or `loop iteration write %s`", artifact.Name, artifact.Name, artifact.Name)}
		}
		return printResult(g, map[string]any{"artifact": artifact.Name, "path": path}, path+"\n")
	}
	if artifact.Database {
		data, err := artifactdb.Read(resolvedDir, artifact.Name)
		if err != nil {
			return codedError{1, err}
		}
		if g.JSON {
			return printResult(g, map[string]any{"artifact": artifact.Name, "content": data}, "")
		}
		fmt.Print(data)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return codedError{1, err}
	}
	if g.JSON {
		return printResult(g, map[string]any{"artifact": artifact.Name, "path": path, "content": string(data)}, "")
	}
	fmt.Print(string(data))
	return nil
}

func commandIterationWriteAppend(ctx context.Context, g globals, action string, args []string) error {
	fs := flag.NewFlagSet("iteration "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", os.Getenv("LOOP_ITERATION_DIR"), "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", "", "run id")
	iteration := fs.String("iteration", "latest", "iteration id")
	sourceFile := fs.String("file", "", "source file, or - for stdin")
	value := fs.String("value", "", "literal content")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true, "file": true, "value": true})
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
		return codedError{2, fmt.Errorf("usage: loop iteration %s <artifact> [--iteration-dir <dir>] [--file <path>|--value <text>]", action)}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	path, artifact, err := resolveIterationArtifact(ctx, resolvedDir, fs.Arg(0))
	if err != nil {
		return codedError{2, err}
	}
	if !artifact.Writable {
		return codedError{2, fmt.Errorf("artifact %q is read-only", artifact.Name)}
	}
	data, err := readArtifactInput(*sourceFile, *value, valueSet)
	if err != nil {
		return codedError{2, err}
	}
	if artifact.Database {
		var err error
		if action == "append" {
			err = artifactdb.Append(resolvedDir, artifact.Name, string(data))
		} else {
			err = artifactdb.Write(resolvedDir, artifact.Name, string(data))
		}
		if err != nil {
			return codedError{1, err}
		}
		verb := "wrote"
		if action == "append" {
			verb = "appended"
		}
		return printResult(g, map[string]any{"artifact": artifact.Name, "action": action}, fmt.Sprintf("%s %s\n", verb, artifact.Name))
	}
	if err := writeIterationArtifact(path, data, action == "append"); err != nil {
		return codedError{1, err}
	}
	verb := "wrote"
	if action == "append" {
		verb = "appended"
	}
	return printResult(g, map[string]any{"artifact": artifact.Name, "path": path, "action": action}, fmt.Sprintf("%s %s\n", verb, path))
}

func resolveIterationDir(ctx context.Context, g globals, iterationDir, runID, iteration string) (string, error) {
	if strings.TrimSpace(iterationDir) != "" {
		return iterationDir, nil
	}
	if strings.TrimSpace(runID) == "" {
		return "", nil
	}
	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(config.LoadOptions{CWD: root, ConfigPath: g.ConfigPath, Overrides: config.Overrides{Agent: g.Agent, NoColor: g.NoColor}})
	if err != nil {
		return "", err
	}
	iter := strings.TrimSpace(iteration)
	if iter == "" || iter == "latest" {
		iter, err = latestIteration(filepath.Join(root, cfg.Logs.Dir, runID, "iterations"))
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(root, cfg.Logs.Dir, runID, "iterations", iter), nil
}

func resolveIterationArtifact(ctx context.Context, iterationDir, name string) (string, iterationArtifact, error) {
	artifact, err := lookupIterationArtifact(name)
	if err != nil {
		return "", artifact, err
	}
	if artifact.Repository {
		root, err := gitx.RepoRoot(ctx, ".")
		if err != nil {
			return "", artifact, err
		}
		path, _, ok := findPullRequestTemplate(root)
		if !ok {
			return "", artifact, errors.New("pull request template not found")
		}
		return path, artifact, nil
	}
	if artifact.EnvPath != "" {
		path := strings.TrimSpace(os.Getenv(artifact.EnvPath))
		if path == "" {
			return "", artifact, fmt.Errorf("artifact %q is unavailable", artifact.Name)
		}
		return path, artifact, nil
	}
	if strings.TrimSpace(iterationDir) == "" {
		return "", artifact, errors.New("iteration directory is required; pass --iteration-dir, pass --run, or run inside an agent iteration")
	}
	return filepath.Join(iterationDir, artifact.File), artifact, nil
}

func lookupIterationArtifact(name string) (iterationArtifact, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.TrimSuffix(key, ".md")
	key = strings.TrimSuffix(key, ".txt")
	key = strings.TrimSuffix(key, ".json")
	key = strings.TrimSuffix(key, ".yaml")
	key = strings.TrimSuffix(key, ".jsonl")
	key = strings.TrimSuffix(key, ".log")
	if alias, ok := iterationArtifactAliases[key]; ok {
		key = alias
	}
	artifact, ok := iterationArtifacts[key]
	if !ok {
		return iterationArtifact{}, fmt.Errorf("unknown iteration artifact %q", name)
	}
	return artifact, nil
}

func isPullRequestTemplateArtifact(name string) bool {
	artifact, err := lookupIterationArtifact(name)
	return err == nil && artifact.Name == "pr-template"
}

func defaultPullRequestTemplate() string {
	return fallbackPRBody("", "")
}

func readArtifactInput(sourceFile, value string, valueSet bool) ([]byte, error) {
	if sourceFile != "" && valueSet {
		return nil, errors.New("--file and --value cannot be used together")
	}
	if sourceFile == "-" {
		return io.ReadAll(os.Stdin)
	}
	if sourceFile != "" {
		return os.ReadFile(sourceFile)
	}
	if valueSet {
		return []byte(value), nil
	}
	return io.ReadAll(os.Stdin)
}

func writeIterationArtifact(path string, data []byte, appendMode bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if !appendMode {
		f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
		if err != nil {
			return err
		}
		tmp := f.Name()
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if err := f.Chmod(0o644); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func findPullRequestTemplate(root string) (string, string, bool) {
	for _, rel := range []string{
		filepath.Join(".github", "PULL_REQUEST_TEMPLATE.md"),
		"PULL_REQUEST_TEMPLATE.md",
		filepath.Join("docs", "PULL_REQUEST_TEMPLATE.md"),
	} {
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path)
		if err == nil {
			return path, string(data), true
		}
	}
	for _, rel := range []string{
		filepath.Join(".github", "PULL_REQUEST_TEMPLATE"),
		"PULL_REQUEST_TEMPLATE",
		filepath.Join("docs", "PULL_REQUEST_TEMPLATE"),
	} {
		matches, err := filepath.Glob(filepath.Join(root, rel, "*.md"))
		if err != nil || len(matches) == 0 {
			continue
		}
		sort.Strings(matches)
		data, err := os.ReadFile(matches[0])
		if err == nil {
			return matches[0], string(data), true
		}
	}
	return "", "", false
}
