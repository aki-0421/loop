package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aki-0421/loop/internal/assets"
	"gopkg.in/yaml.v3"
)

const (
	DefaultRepoConfig = ".loop/config.yaml"
	DefaultUserConfig = ".config/loop/config.yaml"

	ReviewModeAutoMerge           = "auto_merge"
	ReviewModeParallelHumanReview = "parallel_human_review"
	ReviewModeSerialHumanReview   = "serial_human_review"

	MergeMethodSquash      = "squash"
	MergeMethodMergeCommit = "merge_commit"
)

type Config struct {
	Version    int              `yaml:"version" json:"version"`
	Language   LanguageConfig   `yaml:"language" json:"language"`
	Agent      AgentConfig      `yaml:"agent" json:"agent"`
	Run        RunConfig        `yaml:"run" json:"run"`
	Skills     SkillsConfig     `yaml:"skills" json:"skills"`
	Git        GitConfig        `yaml:"git" json:"git"`
	Validation ValidationConfig `yaml:"validation" json:"validation"`
	Logs       LogsConfig       `yaml:"logs" json:"logs"`
	NoColor    bool             `yaml:"-" json:"-"`
}

type LanguageConfig struct {
	Default            string `yaml:"default" json:"default"`
	AllowSkillOverride bool   `yaml:"allowSkillOverride" json:"allowSkillOverride"`
}

type AgentConfig struct {
	Default  string                   `yaml:"default" json:"default"`
	Adapters map[string]AdapterConfig `yaml:"adapters" json:"adapters"`
}

type AdapterConfig struct {
	Command string            `yaml:"command" json:"command"`
	Args    []string          `yaml:"args" json:"args"`
	Prompt  string            `yaml:"prompt" json:"prompt"`
	Env     map[string]string `yaml:"env" json:"env"`
}

type RunConfig struct {
	MaxIterations           int `yaml:"maxIterations" json:"maxIterations"`
	MaxParallelTasks        int `yaml:"maxParallelTasks" json:"maxParallelTasks"`
	MaxTaskAttempts         int `yaml:"maxTaskAttempts" json:"maxTaskAttempts"`
	MaxRoleAgentRestarts    int `yaml:"maxRoleAgentRestarts" json:"maxRoleAgentRestarts"`
	AgentIdleTimeoutSeconds int `yaml:"agentIdleTimeoutSeconds" json:"agentIdleTimeoutSeconds"`
}

type SkillsConfig struct {
	SourceDir string                 `yaml:"sourceDir" json:"sourceDir"`
	SyncOnRun bool                   `yaml:"syncOnRun" json:"syncOnRun"`
	Targets   map[string]SkillTarget `yaml:"targets" json:"targets"`
}

type SkillTarget struct {
	Mode string `yaml:"mode" json:"mode"`
	Path string `yaml:"path" json:"path"`
}

type GitConfig struct {
	BaseBranch  string            `yaml:"baseBranch" json:"baseBranch"`
	Integration IntegrationConfig `yaml:"integration" json:"integration"`
}

type IntegrationConfig struct {
	Mode        string           `yaml:"mode" json:"mode"`
	MergeMethod string           `yaml:"mergeMethod" json:"mergeMethod"`
	LocalMerge  LocalMergeConfig `yaml:"local_merge" json:"local_merge"`
	PR          PRConfig         `yaml:"pr" json:"pr"`
}

type LocalMergeConfig struct {
	Squash       bool `yaml:"squash" json:"squash"`
	DeleteBranch bool `yaml:"deleteBranch" json:"deleteBranch"`
}

type PRConfig struct {
	Create                        bool   `yaml:"create" json:"create"`
	Push                          bool   `yaml:"push" json:"push"`
	WaitChecks                    bool   `yaml:"waitChecks" json:"waitChecks"`
	ChecksRequiredOnly            bool   `yaml:"checksRequiredOnly" json:"checksRequiredOnly"`
	ChecksStartupDelaySeconds     int    `yaml:"checksStartupDelaySeconds" json:"checksStartupDelaySeconds"`
	ChecksDiscoveryTimeoutSeconds int    `yaml:"checksDiscoveryTimeoutSeconds" json:"checksDiscoveryTimeoutSeconds"`
	ChecksPollIntervalSeconds     int    `yaml:"checksPollIntervalSeconds" json:"checksPollIntervalSeconds"`
	ChecksWatchTimeoutSeconds     int    `yaml:"checksWatchTimeoutSeconds" json:"checksWatchTimeoutSeconds"`
	MergeWhenChecksPass           bool   `yaml:"mergeWhenChecksPass" json:"mergeWhenChecksPass"`
	DeleteBranch                  bool   `yaml:"deleteBranch" json:"deleteBranch"`
	HumanReview                   bool   `yaml:"humanReview" json:"humanReview"`
	ReviewMode                    string `yaml:"reviewMode" json:"reviewMode"`
}

type ValidationConfig struct {
	Commands                      []ValidationCommand `yaml:"commands" json:"commands"`
	AgentMayAddCommands           bool                `yaml:"agentMayAddCommands" json:"agentMayAddCommands"`
	RequireRequiredCommandsToPass bool                `yaml:"requireRequiredCommandsToPass" json:"requireRequiredCommandsToPass"`
}

type ValidationCommand struct {
	Name     string `yaml:"name" json:"name"`
	Run      string `yaml:"run" json:"run"`
	Required bool   `yaml:"required" json:"required"`
}

type LogsConfig struct {
	Dir                  string `yaml:"dir" json:"dir"`
	RetainRawAgentOutput bool   `yaml:"retainRawAgentOutput" json:"retainRawAgentOutput"`
	RedactEnv            bool   `yaml:"redactEnv" json:"redactEnv"`
}

type LoadOptions struct {
	CWD        string
	ConfigPath string
	Overrides  Overrides
	Env        []string
}

type Overrides struct {
	Agent         string
	MaxIterations *int
	BaseBranch    string
	PRMode        *bool
	HumanReview   *bool
	ReviewMode    string
	MergeMethod   string
	NoColor       bool
}

func Defaults() Config {
	cfg, err := Default()
	if err != nil {
		panic(err)
	}
	return cfg
}

func Default() (Config, error) {
	var cfg Config
	data, err := assets.ReadTemplate("loop.config.yaml")
	if err != nil {
		return cfg, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, err
	}
	normalize(&cfg)
	return cfg, Validate(cfg)
}

func DefaultYAML() ([]byte, error) {
	return assets.ReadTemplate("loop.config.yaml")
}

func Load(opts LoadOptions) (Config, error) {
	cwd := opts.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Config{}, err
		}
	}
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	if strings.TrimSpace(envValue(env, "LOOP_MODE")) != "" {
		return Config{}, errors.New("LOOP_MODE has been removed; runs are always fully automated")
	}
	baseBytes, err := DefaultYAML()
	if err != nil {
		return Config{}, err
	}
	merged, err := yamlToMap(baseBytes)
	if err != nil {
		return Config{}, err
	}
	if userPath, ok := userConfigPath(env); ok {
		if err := mergeFile(merged, userPath); err != nil {
			return Config{}, err
		}
	}
	repoConfig := opts.ConfigPath
	if v := envValue(env, "LOOP_CONFIG"); v != "" {
		repoConfig = v
	}
	if repoConfig == "" {
		repoConfig = DefaultRepoConfig
	}
	if !filepath.IsAbs(repoConfig) {
		repoConfig = filepath.Join(cwd, repoConfig)
	}
	if err := mergeFile(merged, repoConfig); err != nil {
		return Config{}, err
	}
	applyEnv(merged, env)
	applyOverrides(merged, opts.Overrides)

	data, err := yaml.Marshal(merged)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	normalize(&cfg)
	if envHas(env, "LOOP_NO_COLOR") || opts.Overrides.NoColor {
		cfg.NoColor = true
	}
	return cfg, Validate(cfg)
}

func WriteEffective(path string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0o644)
}

func Validate(cfg Config) error {
	var errs []string
	if cfg.Version != 1 {
		errs = append(errs, "version must be 1")
	}
	if cfg.Language.Default == "" {
		errs = append(errs, "language.default is required")
	}
	if cfg.Agent.Default == "" {
		errs = append(errs, "agent.default is required")
	}
	if _, ok := cfg.Agent.Adapters[cfg.Agent.Default]; !ok {
		errs = append(errs, "agent.default has no adapter")
	}
	for name, adapter := range cfg.Agent.Adapters {
		if adapter.Command == "" {
			errs = append(errs, "agent.adapters."+name+".command is required")
		}
		if !oneOf(adapter.Prompt, "stdin", "arg") {
			errs = append(errs, "agent.adapters."+name+".prompt must be stdin or arg")
		}
		for _, arg := range adapter.Args {
			if strings.Contains(arg, "{prompt_file}") {
				errs = append(errs, "agent.adapters."+name+".args must not use {prompt_file}")
				break
			}
			if strings.Contains(arg, "{result_file}") {
				errs = append(errs, "agent.adapters."+name+".args must not use {result_file}")
				break
			}
			if strings.Contains(arg, "{iteration_dir}") {
				errs = append(errs, "agent.adapters."+name+".args must not use {iteration_dir}")
				break
			}
		}
	}
	if cfg.Run.MaxIterations < 0 {
		errs = append(errs, "run.maxIterations must be at least 0")
	}
	if cfg.Run.MaxParallelTasks <= 0 {
		errs = append(errs, "run.maxParallelTasks must be positive")
	}
	if cfg.Run.MaxTaskAttempts <= 0 {
		errs = append(errs, "run.maxTaskAttempts must be positive")
	}
	if cfg.Run.MaxRoleAgentRestarts < 0 {
		errs = append(errs, "run.maxRoleAgentRestarts must be at least 0")
	}
	if cfg.Run.AgentIdleTimeoutSeconds < 0 {
		errs = append(errs, "run.agentIdleTimeoutSeconds must be at least 0")
	}
	if cfg.Skills.SourceDir == "" {
		errs = append(errs, "skills.sourceDir is required")
	}
	for agent, target := range cfg.Skills.Targets {
		if !oneOf(target.Mode, "copy", "symlink", "off") {
			errs = append(errs, "skills.targets."+agent+".mode must be copy, symlink, or off")
		}
	}
	if !oneOf(cfg.Git.Integration.Mode, "local_merge", "pr") {
		errs = append(errs, "git.integration.mode must be local_merge or pr")
	}
	if !oneOf(cfg.Git.Integration.MergeMethod, MergeMethodSquash, MergeMethodMergeCommit) {
		errs = append(errs, "git.integration.mergeMethod must be squash or merge_commit")
	}
	if !oneOf(cfg.Git.Integration.PR.ReviewMode, ReviewModeAutoMerge, ReviewModeParallelHumanReview, ReviewModeSerialHumanReview) {
		errs = append(errs, "git.integration.pr.reviewMode must be auto_merge, parallel_human_review, or serial_human_review")
	}
	if cfg.Git.Integration.PR.ChecksStartupDelaySeconds < 0 {
		errs = append(errs, "git.integration.pr.checksStartupDelaySeconds must be non-negative")
	}
	if cfg.Git.Integration.PR.ChecksDiscoveryTimeoutSeconds < 0 {
		errs = append(errs, "git.integration.pr.checksDiscoveryTimeoutSeconds must be non-negative")
	}
	if cfg.Git.Integration.PR.ChecksPollIntervalSeconds < 0 {
		errs = append(errs, "git.integration.pr.checksPollIntervalSeconds must be non-negative")
	}
	if cfg.Git.Integration.PR.ChecksWatchTimeoutSeconds <= 0 {
		errs = append(errs, "git.integration.pr.checksWatchTimeoutSeconds must be positive")
	}
	if cfg.Logs.Dir == "" {
		errs = append(errs, "logs.dir is required")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (c Config) Adapter(name string) (AdapterConfig, bool) {
	if name == "" {
		name = c.Agent.Default
	}
	a, ok := c.Agent.Adapters[name]
	return a, ok
}

func yamlToMap(data []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return m, nil
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return normalizeMap(m), nil
}

func mergeFile(dst map[string]any, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	src, err := yamlToMap(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	deepMerge(dst, src)
	return nil
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if srcMap, ok := v.(map[string]any); ok {
			if dstMap, ok := dst[k].(map[string]any); ok {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[k] = v
	}
}

func normalizeMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		switch typed := v.(type) {
		case map[any]any:
			child := map[string]any{}
			for ck, cv := range typed {
				child[fmt.Sprint(ck)] = cv
			}
			out[k] = normalizeMap(child)
		case map[string]any:
			out[k] = normalizeMap(typed)
		case []any:
			out[k] = normalizeSlice(typed)
		default:
			out[k] = typed
		}
	}
	return out
}

func normalizeSlice(in []any) []any {
	for i, v := range in {
		if m, ok := v.(map[string]any); ok {
			in[i] = normalizeMap(m)
		}
	}
	return in
}

func applyEnv(m map[string]any, env []string) {
	values := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	if v := strings.TrimSpace(values["LOOP_AGENT"]); v != "" {
		setPath(m, v, "agent", "default")
	}
	if v := strings.TrimSpace(values["LOOP_MAX_ITERATIONS"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			setPath(m, n, "run", "maxIterations")
		}
	}
	if v := strings.TrimSpace(values["LOOP_AGENT_IDLE_TIMEOUT_SECONDS"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			setPath(m, n, "run", "agentIdleTimeoutSeconds")
		}
	}
	if v := strings.TrimSpace(values["LOOP_MAX_ROLE_AGENT_RESTARTS"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			setPath(m, n, "run", "maxRoleAgentRestarts")
		}
	}
	if v := strings.TrimSpace(values["LOOP_BASE_BRANCH"]); v != "" {
		setPath(m, v, "git", "baseBranch")
	}
	if v := strings.TrimSpace(values["LOOP_MERGE_METHOD"]); v != "" {
		setPath(m, v, "git", "integration", "mergeMethod")
	}
}

func applyOverrides(m map[string]any, o Overrides) {
	if o.Agent != "" {
		setPath(m, o.Agent, "agent", "default")
	}
	if o.MaxIterations != nil {
		setPath(m, *o.MaxIterations, "run", "maxIterations")
	}
	if o.BaseBranch != "" {
		setPath(m, o.BaseBranch, "git", "baseBranch")
	}
	if o.PRMode != nil {
		mode := "local_merge"
		if *o.PRMode {
			mode = "pr"
		}
		setPath(m, mode, "git", "integration", "mode")
	}
	if o.HumanReview != nil {
		setPath(m, *o.HumanReview, "git", "integration", "pr", "humanReview")
	}
	if strings.TrimSpace(o.ReviewMode) != "" {
		setPath(m, strings.TrimSpace(o.ReviewMode), "git", "integration", "pr", "reviewMode")
	}
	if strings.TrimSpace(o.MergeMethod) != "" {
		setPath(m, strings.TrimSpace(o.MergeMethod), "git", "integration", "mergeMethod")
	}
}

func setPath(m map[string]any, value any, path ...string) {
	cur := m
	for _, part := range path[:len(path)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = value
}

func normalize(c *Config) {
	if c.Agent.Adapters == nil {
		c.Agent.Adapters = map[string]AdapterConfig{}
	}
	if _, ok := c.Agent.Adapters["fake"]; !ok {
		c.Agent.Adapters["fake"] = AdapterConfig{Command: "loop", Args: []string{"__fake-agent"}, Prompt: "stdin", Env: map[string]string{}}
	}
	for name, a := range c.Agent.Adapters {
		a = normalizeCodexAdapter(a)
		a = normalizeClaudeAdapter(a)
		if a.Prompt == "" {
			a.Prompt = "stdin"
		}
		if a.Args == nil {
			a.Args = []string{}
		}
		if a.Env == nil {
			a.Env = map[string]string{}
		}
		c.Agent.Adapters[name] = a
	}
	if c.Skills.Targets == nil {
		c.Skills.Targets = map[string]SkillTarget{}
	}
	if c.Validation.Commands == nil {
		c.Validation.Commands = []ValidationCommand{}
	}
	normalizeIntegrationMergeMethod(c)
	normalizePRReviewMode(c)
}

func normalizeIntegrationMergeMethod(c *Config) {
	method := strings.TrimSpace(c.Git.Integration.MergeMethod)
	if method == "" {
		method = MergeMethodSquash
	}
	c.Git.Integration.MergeMethod = method
	c.Git.Integration.LocalMerge.Squash = method == MergeMethodSquash
}

func normalizePRReviewMode(c *Config) {
	mode := strings.TrimSpace(c.Git.Integration.PR.ReviewMode)
	if mode == "" {
		if c.Git.Integration.PR.HumanReview {
			mode = ReviewModeSerialHumanReview
		} else {
			mode = ReviewModeAutoMerge
		}
	}
	c.Git.Integration.PR.ReviewMode = mode
	c.Git.Integration.PR.HumanReview = mode != ReviewModeAutoMerge
}

func normalizeCodexAdapter(a AdapterConfig) AdapterConfig {
	command := strings.TrimSuffix(strings.ToLower(filepath.Base(a.Command)), ".exe")
	if command != "codex" {
		return a
	}
	if !hasCodexSubcommand(a.Args) {
		a.Args = append([]string{"exec"}, a.Args...)
	}
	if codexExecSubcommand(a.Args) && !hasArg(a.Args, "--json") {
		a.Args = append([]string{a.Args[0], "--json"}, a.Args[1:]...)
	}
	return a
}

func normalizeClaudeAdapter(a AdapterConfig) AdapterConfig {
	command := strings.TrimSuffix(strings.ToLower(filepath.Base(a.Command)), ".exe")
	if command != "claude" || len(a.Args) > 0 {
		return a
	}
	a.Args = []string{"-p", "{prompt}", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions"}
	a.Prompt = "arg"
	return a
}

func codexExecSubcommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return args[0] == "exec" || args[0] == "e"
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func hasCodexSubcommand(args []string) bool {
	subcommands := map[string]bool{
		"exec": true, "e": true, "review": true, "login": true, "logout": true,
		"mcp": true, "plugin": true, "mcp-server": true, "app-server": true,
		"app": true, "completion": true, "sandbox": true, "debug": true,
		"apply": true, "a": true, "resume": true, "fork": true, "cloud": true,
		"exec-server": true, "features": true, "help": true,
	}
	for _, arg := range args {
		if subcommands[arg] {
			return true
		}
	}
	return false
}

func oneOf(value string, allowed ...string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

func envHas(env []string, key string) bool {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func userConfigPath(env []string) (string, bool) {
	home := homeDirFromEnv(env)
	if home == "" {
		return "", false
	}
	return filepath.Join(home, DefaultUserConfig), true
}

func homeDirFromEnv(env []string) string {
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if value := strings.TrimSpace(envValue(env, key)); value != "" {
			return value
		}
	}
	drive := strings.TrimSpace(envValue(env, "HOMEDRIVE"))
	path := strings.TrimSpace(envValue(env, "HOMEPATH"))
	if drive != "" && path != "" {
		return drive + path
	}
	return ""
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
