package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPrecedenceAndOverrides(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("HOME", repo)
	user := filepath.Join(repo, ".config", "loop", "config.yaml")
	mustWrite(t, user, `version: 1
agent:
  default: custom
  adapters:
    custom:
      command: custom-agent
git:
  baseBranch: main
run:
  maxIterations: 3
`)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
run:
  maxIterations: 7
`)

	cfg, err := Load(LoadOptions{
		CWD:        repo,
		ConfigPath: filepath.Join(repo, ".loop", "config.yaml"),
		Env: []string{
			"LOOP_AGENT=codex",
			"LOOP_MAX_ITERATIONS=9",
			"LOOP_BASE_BRANCH=release",
			"LOOP_NO_COLOR=1",
		},
		Overrides: Overrides{MaxIterations: intPtr(11)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Default != "codex" {
		t.Fatalf("agent env override = %q", cfg.Agent.Default)
	}
	if cfg.Run.MaxIterations != 11 {
		t.Fatalf("flag override max iterations = %d", cfg.Run.MaxIterations)
	}
	if cfg.Git.BaseBranch != "release" {
		t.Fatalf("base branch env override = %q", cfg.Git.BaseBranch)
	}
	if !cfg.NoColor {
		t.Fatal("LOOP_NO_COLOR was not applied")
	}
	if cfg.Git.Integration.Mode != "pr" {
		t.Fatalf("default integration mode = %q, want pr", cfg.Git.Integration.Mode)
	}
	if cfg.Git.Integration.PR.ChecksStartupDelaySeconds != 5 {
		t.Fatalf("default checks startup delay = %d, want 5", cfg.Git.Integration.PR.ChecksStartupDelaySeconds)
	}
	if cfg.Git.Integration.PR.ChecksRequiredOnly {
		t.Fatal("default checks required-only should be false")
	}
	if cfg.Git.Integration.PR.ChecksDiscoveryTimeoutSeconds != 60 {
		t.Fatalf("default checks discovery timeout = %d, want 60", cfg.Git.Integration.PR.ChecksDiscoveryTimeoutSeconds)
	}
	if cfg.Git.Integration.PR.ChecksPollIntervalSeconds != 5 {
		t.Fatalf("default checks poll interval = %d, want 5", cfg.Git.Integration.PR.ChecksPollIntervalSeconds)
	}
	if cfg.Git.Integration.PR.ChecksWatchTimeoutSeconds != 3600 {
		t.Fatalf("default checks watch timeout = %d, want 3600", cfg.Git.Integration.PR.ChecksWatchTimeoutSeconds)
	}
}

func TestRemovedDocumentLinterConfigIsRejected(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("HOME", repo)
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
linter:
  document:
    entry: AGENTS.md
    requiredReachable:
      - docs
    excludes:
      - README.md
`)
	_, err := Load(LoadOptions{CWD: repo, Env: []string{}})
	if err == nil {
		t.Fatal("removed linter config should be rejected")
	}
	if !strings.Contains(err.Error(), "field linter not found") {
		t.Fatalf("error should mention removed linter field: %v", err)
	}
}

func TestLoopConfigEnvOverridesUserPath(t *testing.T) {
	repo := t.TempDir()
	envConfig := filepath.Join(repo, "env-config.yaml")
	mustWrite(t, envConfig, `version: 1
agent:
  default: envagent
  adapters:
    envagent:
      command: envagent
`)
	cfg, err := Load(LoadOptions{CWD: repo, Env: []string{"LOOP_CONFIG=" + envConfig}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Default != "envagent" {
		t.Fatalf("LOOP_CONFIG path was not loaded, got %q", cfg.Agent.Default)
	}
}

func TestCodexAdapterUsesNonInteractiveExec(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Run.MaxIterations != 0 {
		t.Fatalf("default max iterations = %d, want unlimited 0", cfg.Run.MaxIterations)
	}
	if cfg.Git.BaseBranch != "" {
		t.Fatalf("default base branch = %q, want empty", cfg.Git.BaseBranch)
	}
	codex := cfg.Agent.Adapters["codex"]
	if len(codex.Args) < 2 || codex.Args[0] != "exec" || codex.Args[1] != "--json" {
		t.Fatalf("default codex args = %#v, want exec --json first", codex.Args)
	}

	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
agent:
  default: codex
  adapters:
    codex:
      command: codex
      args:
        - -m
        - gpt-5.5
        - -c
        - model_reasoning_effort="xhigh"
      prompt: stdin
`)
	cfg, err = Load(LoadOptions{CWD: repo, Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	codex = cfg.Agent.Adapters["codex"]
	want := []string{"exec", "--json", "-m", "gpt-5.5", "-c", `model_reasoning_effort="xhigh"`}
	if strings.Join(codex.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized codex args = %#v, want %#v", codex.Args, want)
	}

	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
agent:
  default: codex
  adapters:
    codex:
      command: codex
      args: [exec, -m, gpt-5.5]
      prompt: stdin
`)
	cfg, err = Load(LoadOptions{CWD: repo, Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	codex = cfg.Agent.Adapters["codex"]
	want = []string{"exec", "--json", "-m", "gpt-5.5"}
	if strings.Join(codex.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("codex exec args should not be duplicated: %#v", codex.Args)
	}

	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
agent:
  default: codex
  adapters:
    codex:
      command: codex
      args: [exec, --json, -m, gpt-5.5]
      prompt: stdin
`)
	cfg, err = Load(LoadOptions{CWD: repo, Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	codex = cfg.Agent.Adapters["codex"]
	want = []string{"exec", "--json", "-m", "gpt-5.5"}
	if strings.Join(codex.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("codex --json should not be duplicated: %#v", codex.Args)
	}
}

func TestClaudeAdapterUsesNonInteractiveStreamJSON(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	claude := cfg.Agent.Adapters["claude"]
	want := []string{"-p", "{prompt}", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions"}
	if claude.Prompt != "arg" {
		t.Fatalf("default claude prompt = %q, want arg", claude.Prompt)
	}
	if strings.Join(claude.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("default claude args = %#v, want %#v", claude.Args, want)
	}

	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
agent:
  default: claude
  adapters:
    claude:
      command: claude
`)
	cfg, err = Load(LoadOptions{CWD: repo, Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	claude = cfg.Agent.Adapters["claude"]
	if claude.Prompt != "arg" {
		t.Fatalf("normalized claude prompt = %q, want arg", claude.Prompt)
	}
	if strings.Join(claude.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized claude args = %#v, want %#v", claude.Args, want)
	}
}

func TestLoadRejectsUnknownAndInvalidValues(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("HOME", repo)
	_, err := Load(LoadOptions{CWD: repo, Env: []string{"LOOP_MODE=autonomous"}})
	if err == nil || !strings.Contains(err.Error(), "LOOP_MODE has been removed") {
		t.Fatalf("expected removed LOOP_MODE error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
interaction:
  mode: autonomous
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "field interaction not found") {
		t.Fatalf("expected removed interaction section error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
run:
  planMode: false
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "field planMode not found") {
		t.Fatalf("expected removed run.planMode error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
run:
  instructionReload: once
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "field instructionReload not found") {
		t.Fatalf("expected removed run.instructionReload error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
git:
  branch: {}
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "field branch not found") {
		t.Fatalf("expected removed git.branch error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
agent:
  default: custom
  adapters:
    custom:
      command: custom-agent
      prompt: file_arg
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "prompt must be stdin or arg") {
		t.Fatalf("expected removed file_arg error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
agent:
  default: custom
  adapters:
    custom:
      command: custom-agent
      args: ["{prompt_file}"]
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "must not use {prompt_file}") {
		t.Fatalf("expected prompt_file placeholder error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
agent:
  default: custom
  adapters:
    custom:
      command: custom-agent
      args: ["{result_file}"]
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "must not use {result_file}") {
		t.Fatalf("expected result_file placeholder error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
agent:
  default: custom
  adapters:
    custom:
      command: custom-agent
      args: ["{iteration_dir}"]
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "must not use {iteration_dir}") {
		t.Fatalf("expected iteration_dir placeholder error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
git:
  integration:
    mode: direct
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "git.integration.mode") {
		t.Fatalf("expected direct integration validation error, got %v", err)
	}

	repo = t.TempDir()
	mustWrite(t, filepath.Join(repo, ".loop", "config.yaml"), `version: 1
unexpected: true
`)
	_, err = Load(LoadOptions{CWD: repo, Env: []string{}, ConfigPath: filepath.Join(repo, ".loop", "config.yaml")})
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("expected strict decode error, got %v", err)
	}
}

func TestWriteEffective(t *testing.T) {
	repo := t.TempDir()
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Git.BaseBranch = "main"
	path := filepath.Join(repo, ".loop", "runs", "0001", "effective-config.yaml")
	if err := WriteEffective(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "baseBranch: main") {
		t.Fatalf("effective config missing override:\n%s", text)
	}
	if strings.Contains(text, "NoColor") || strings.Contains(text, "noColor") {
		t.Fatalf("runtime-only field leaked into YAML:\n%s", text)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func intPtr(v int) *int {
	return &v
}
