package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type helpFlag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Default     string `json:"default,omitempty"`
}

type helpCommand struct {
	Path        []string   `json:"path"`
	Usage       string     `json:"usage"`
	Summary     string     `json:"summary"`
	Description string     `json:"description,omitempty"`
	Flags       []helpFlag `json:"flags,omitempty"`
	Agent       bool       `json:"-"`
	AgentOnly   bool       `json:"-"`
}

type helpSummary struct {
	Command string `json:"command"`
	Summary string `json:"summary"`
}

type helpArtifact struct {
	Name     string `json:"name"`
	Readable bool   `json:"readable"`
	Writable bool   `json:"writable"`
	Storage  string `json:"storage"`
}

func commandHelp(ctx context.Context, g globals, args []string) error {
	_ = ctx
	if len(args) > 0 && args[0] == "agent" {
		return commandAgentHelp(g, args[1:])
	}
	if len(args) == 0 {
		summaries := helpSummaries()
		var text strings.Builder
		text.WriteString("Loop commands:\n")
		for _, summary := range summaries {
			fmt.Fprintf(&text, "  %-44s %s\n", summary.Command, summary.Summary)
		}
		text.WriteString("\nRun `loop help <command>` for details.\n")
		return printResult(g, map[string]any{"commands": summaries}, text.String())
	}

	cmd, ok := lookupHelpCommand(args)
	if !ok {
		if _, agentOnly := lookupAgentOnlyHelpCommand(args); agentOnly {
			return codedError{2, fmt.Errorf("agent-only help topic %q; run `loop help agent %s`", strings.Join(args, " "), strings.Join(args, " "))}
		}
		return codedError{2, fmt.Errorf("unknown help topic %q; run `loop help`", strings.Join(args, " "))}
	}

	subcommands := childHelpSummaries(cmd.Path)
	artifacts := []helpArtifact(nil)
	if hasIterationArtifactHelp(cmd.Path) {
		artifacts = helpArtifacts()
	}

	value := map[string]any{
		"command":     "loop " + strings.Join(cmd.Path, " "),
		"usage":       cmd.Usage,
		"summary":     cmd.Summary,
		"description": cmd.Description,
	}
	if len(cmd.Flags) > 0 {
		value["flags"] = cmd.Flags
	}
	if len(subcommands) > 0 {
		value["subcommands"] = subcommands
	}
	if len(artifacts) > 0 {
		value["artifacts"] = artifacts
	}

	var text strings.Builder
	fmt.Fprintf(&text, "Usage: %s\n\n", cmd.Usage)
	fmt.Fprintf(&text, "%s\n", cmd.Summary)
	if strings.TrimSpace(cmd.Description) != "" {
		fmt.Fprintf(&text, "\n%s\n", cmd.Description)
	}
	if len(cmd.Flags) > 0 {
		text.WriteString("\nFlags:\n")
		for _, flag := range cmd.Flags {
			if flag.Default == "" {
				fmt.Fprintf(&text, "  %-28s %s\n", flag.Name, flag.Description)
			} else {
				fmt.Fprintf(&text, "  %-28s %s (default %s)\n", flag.Name, flag.Description, flag.Default)
			}
		}
	}
	if len(subcommands) > 0 {
		text.WriteString("\nSubcommands:\n")
		for _, summary := range subcommands {
			fmt.Fprintf(&text, "  %-44s %s\n", summary.Command, summary.Summary)
		}
	}
	if len(artifacts) > 0 {
		text.WriteString("\nArtifacts:\n")
		for _, artifact := range artifacts {
			mode := "read-only"
			if artifact.Writable {
				mode = "read/write"
			}
			fmt.Fprintf(&text, "  %-18s %-10s %s\n", artifact.Name, mode, artifact.Storage)
		}
	}
	return printResult(g, value, text.String())
}

func helpSummaries() []helpSummary {
	commands := humanHelpCommands()
	summaries := make([]helpSummary, 0, len(commands))
	for _, cmd := range commands {
		summaries = append(summaries, helpSummary{
			Command: "loop " + strings.Join(cmd.Path, " "),
			Summary: cmd.Summary,
		})
	}
	sortHelpSummaries(summaries)
	return summaries
}

func childHelpSummaries(path []string) []helpSummary {
	commands := humanHelpCommands()
	var out []helpSummary
	for _, cmd := range commands {
		if len(cmd.Path) != len(path)+1 {
			continue
		}
		matches := true
		for i := range path {
			if cmd.Path[i] != path[i] {
				matches = false
				break
			}
		}
		if matches {
			out = append(out, helpSummary{
				Command: "loop " + strings.Join(cmd.Path, " "),
				Summary: cmd.Summary,
			})
		}
	}
	sortHelpSummaries(out)
	return out
}

func lookupHelpCommand(path []string) (helpCommand, bool) {
	key := strings.Join(path, " ")
	for _, cmd := range humanHelpCommands() {
		if strings.Join(cmd.Path, " ") == key {
			return cmd, true
		}
	}
	return helpCommand{}, false
}

func lookupAgentOnlyHelpCommand(path []string) (helpCommand, bool) {
	key := strings.Join(path, " ")
	for _, cmd := range allHelpCommands() {
		if cmd.AgentOnly && strings.Join(cmd.Path, " ") == key {
			return cmd, true
		}
	}
	return helpCommand{}, false
}

func humanHelpCommands() []helpCommand {
	all := allHelpCommands()
	out := make([]helpCommand, 0, len(all))
	for _, cmd := range all {
		if !cmd.AgentOnly {
			out = append(out, cmd)
		}
	}
	return out
}

func agentHelpCommands() []helpCommand {
	all := allHelpCommands()
	out := make([]helpCommand, 0, len(all))
	for _, cmd := range all {
		if cmd.Agent {
			out = append(out, cmd)
		}
	}
	return out
}

func sortHelpSummaries(items []helpSummary) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].Command < items[j].Command
	})
}

func hasIterationArtifactHelp(path []string) bool {
	if len(path) == 1 && path[0] == "iteration" {
		return true
	}
	if len(path) == 2 && path[0] == "iteration" {
		switch path[1] {
		case "path", "read", "write", "append":
			return true
		}
	}
	return false
}

func helpArtifacts() []helpArtifact {
	names := make([]string, 0, len(iterationArtifacts))
	for name := range iterationArtifacts {
		names = append(names, name)
	}
	sort.Strings(names)

	artifacts := make([]helpArtifact, 0, len(names))
	for _, name := range names {
		artifact := iterationArtifacts[name]
		storage := "file"
		switch {
		case artifact.Database:
			storage = "artifact database"
		case artifact.Repository:
			storage = "repository file"
		case artifact.EnvPath != "":
			storage = "environment file"
		}
		artifacts = append(artifacts, helpArtifact{
			Name:     artifact.Name,
			Readable: true,
			Writable: artifact.Writable,
			Storage:  storage,
		})
	}
	return artifacts
}

func commandAgentHelp(g globals, args []string) error {
	if len(args) == 0 {
		commands := agentHelpCommands()
		value := map[string]any{
			"agent":     true,
			"commands":  compactHelpSummaries(commands),
			"artifacts": helpArtifacts(),
		}
		return printResult(g, value, renderAgentHelp(commands))
	}

	cmd, ok := lookupAgentHelpCommand(args)
	if !ok {
		return codedError{2, fmt.Errorf("unknown agent help topic %q; run `loop help agent`", strings.Join(args, " "))}
	}
	subcommands := agentChildHelpSummaries(cmd.Path)
	artifacts := []helpArtifact(nil)
	if hasIterationArtifactHelp(cmd.Path) {
		artifacts = helpArtifacts()
	}
	value := map[string]any{
		"agent":       true,
		"command":     "loop " + strings.Join(cmd.Path, " "),
		"usage":       cmd.Usage,
		"summary":     cmd.Summary,
		"description": cmd.Description,
	}
	if len(cmd.Flags) > 0 {
		value["flags"] = cmd.Flags
	}
	if len(subcommands) > 0 {
		value["subcommands"] = subcommands
	}
	if len(artifacts) > 0 {
		value["artifacts"] = artifacts
	}
	return printResult(g, value, renderAgentCommandHelp(cmd, subcommands, artifacts))
}

func lookupAgentHelpCommand(path []string) (helpCommand, bool) {
	key := strings.Join(path, " ")
	for _, cmd := range agentHelpCommands() {
		if strings.Join(cmd.Path, " ") == key {
			return cmd, true
		}
	}
	return helpCommand{}, false
}

func compactHelpSummaries(commands []helpCommand) []helpSummary {
	summaries := make([]helpSummary, 0, len(commands))
	for _, cmd := range commands {
		summaries = append(summaries, helpSummary{
			Command: "loop " + strings.Join(cmd.Path, " "),
			Summary: cmd.Summary,
		})
	}
	sortHelpSummaries(summaries)
	return summaries
}

func agentChildHelpSummaries(path []string) []helpSummary {
	commands := agentHelpCommands()
	var out []helpSummary
	for _, cmd := range commands {
		if len(cmd.Path) != len(path)+1 {
			continue
		}
		matches := true
		for i := range path {
			if cmd.Path[i] != path[i] {
				matches = false
				break
			}
		}
		if matches {
			out = append(out, helpSummary{
				Command: "loop " + strings.Join(cmd.Path, " "),
				Summary: cmd.Summary,
			})
		}
	}
	sortHelpSummaries(out)
	return out
}

func renderAgentHelp(commands []helpCommand) string {
	sort.Slice(commands, func(i, j int) bool {
		return strings.Join(commands[i].Path, " ") < strings.Join(commands[j].Path, " ")
	})
	var b strings.Builder
	b.WriteString("agent-help-v1\n")
	for _, cmd := range commands {
		fmt.Fprintf(&b, "cmd:%s;%s\n", compactUsage(cmd.Usage), compactText(cmd.Summary))
	}
	fmt.Fprintf(&b, "artifacts:%s\n", compactArtifacts(helpArtifacts()))
	b.WriteString("detail:loop help agent <command...>\n")
	return b.String()
}

func renderAgentCommandHelp(cmd helpCommand, subcommands []helpSummary, artifacts []helpArtifact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cmd:%s\n", compactUsage(cmd.Usage))
	fmt.Fprintf(&b, "summary:%s\n", compactText(cmd.Summary))
	if len(cmd.Flags) > 0 {
		fmt.Fprintf(&b, "flags:%s\n", compactFlags(cmd.Flags))
	}
	if len(subcommands) > 0 {
		b.WriteString("sub:")
		for i, sub := range subcommands {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%s;%s", compactUsage(sub.Command), compactText(sub.Summary))
		}
		b.WriteByte('\n')
	}
	if len(artifacts) > 0 {
		fmt.Fprintf(&b, "artifacts:%s\n", compactArtifacts(artifacts))
	}
	return b.String()
}

func compactFlags(flags []helpFlag) string {
	var b strings.Builder
	for i, flag := range flags {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(compactUsage(flag.Name))
		if flag.Default != "" {
			b.WriteString("(d=")
			b.WriteString(compactText(flag.Default))
			b.WriteByte(')')
		}
	}
	return b.String()
}

func compactArtifacts(artifacts []helpArtifact) string {
	var b strings.Builder
	for i, artifact := range artifacts {
		if i > 0 {
			b.WriteByte(',')
		}
		mode := "r"
		if artifact.Writable {
			mode = "rw"
		}
		fmt.Fprintf(&b, "%s:%s:%s", artifact.Name, mode, compactStorage(artifact.Storage))
	}
	return b.String()
}

func compactStorage(storage string) string {
	switch storage {
	case "artifact database":
		return "db"
	case "repository file":
		return "repo"
	case "environment file":
		return "env"
	default:
		return "file"
	}
}

func compactUsage(text string) string {
	text = strings.ReplaceAll(text, "<", "")
	text = strings.ReplaceAll(text, ">", "")
	return compactText(text)
}

func compactText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func allHelpCommands() []helpCommand {
	return []helpCommand{
		{
			Path:        []string{"help"},
			Usage:       "loop help [command...]",
			Summary:     "Show available commands or detailed command help",
			Description: "Use `loop help` for the command list and `loop help <command...>` for details.",
		},
		{
			Path:    []string{"init"},
			Usage:   "loop init [flags]",
			Summary: "Create repository-local loop files",
			Flags: []helpFlag{
				{Name: "--force", Description: "overwrite generated files"},
				{Name: "--agent <name>", Description: "set the default agent", Default: "codex"},
				{Name: "--skills", Description: "install the default skill", Default: "true"},
				{Name: "--sync-agent-skills", Description: "sync skills into configured agent targets", Default: "false"},
				{Name: "--base <branch>", Description: "set the base branch", Default: "auto"},
			},
		},
		{
			Path:        []string{"run"},
			Usage:       "loop run <instruction.md> [flags]",
			Summary:     "Run one or more automated coding iterations",
			Description: "Builds each iteration prompt, launches the configured agent, validates the result, and integrates completed work.",
			Flags: []helpFlag{
				{Name: "--goal <text>", Description: "natural-language stop condition"},
				{Name: "--max-iterations <n>", Description: "maximum iterations; 0 means unlimited", Default: "config value"},
				{Name: "--pr", Description: "use pull request integration", Default: "config value"},
				{Name: "--base <branch>", Description: "base branch for integration", Default: "config value"},
				{Name: "--worktree", Description: "run each iteration in a Git worktree", Default: "config value"},
				{Name: "--resume <run-id>", Description: "resume an existing run"},
				{Name: "--from-iteration <n>", Description: "resume from a specific iteration"},
				{Name: "--keep-branches <mode>", Description: "branch cleanup mode", Default: "config value"},
				{Name: "--keep-worktrees <mode>", Description: "worktree cleanup mode", Default: "config value"},
				{Name: "--dry-run", Description: "build prompt and state files without launching the agent", Default: "false"},
			},
		},
		{
			Path:        []string{"commit"},
			Usage:       "loop commit <type> <message>",
			Summary:     "Create a validated iteration commit",
			Description: "`<type>` is F, T, R, D, S, V, or C, with common lowercase aliases accepted. The CLI stages repository changes and creates `<TYPE>: <message>`.",
			Agent:       true,
			AgentOnly:   true,
		},
		{
			Path:    []string{"resume"},
			Usage:   "loop resume <run-id> [flags]",
			Summary: "Resume a stored run",
			Flags: []helpFlag{
				{Name: "--from-iteration <n>", Description: "resume from iteration n", Default: "latest incomplete"},
				{Name: "--repair", Description: "attempt repair before resuming", Default: "true"},
			},
		},
		{
			Path:    []string{"status"},
			Usage:   "loop status [run-id] [flags]",
			Summary: "Show run state and next action",
		},
		{
			Path:    []string{"logs"},
			Usage:   "loop logs <run-id> [flags]",
			Summary: "Print iteration logs or a specific runtime file",
			Flags: []helpFlag{
				{Name: "--iteration <n>", Description: "select an iteration", Default: "latest"},
				{Name: "--follow", Description: "follow agent event stream", Default: "false"},
				{Name: "--file <name>", Description: "print a specific file from the iteration directory"},
			},
		},
		{
			Path:        []string{"iteration"},
			Usage:       "loop iteration <path|read|write|append|result> ...",
			Summary:     "Read, write, and build named iteration artifacts",
			Description: "If `--iteration-dir` is omitted, commands resolve the current agent iteration automatically.",
			Agent:       true,
			AgentOnly:   true,
		},
		{
			Path:        []string{"iteration", "path"},
			Usage:       "loop iteration path <artifact> [--iteration-dir <dir>|--run <run-id> --iteration <n>]",
			Summary:     "Print the filesystem path for file-backed iteration artifacts",
			Description: "Database-backed artifacts intentionally do not expose a writable path; use read, write, or append instead.",
			Flags:       iterationLocatorFlags(),
			Agent:       true,
			AgentOnly:   true,
		},
		{
			Path:        []string{"iteration", "read"},
			Usage:       "loop iteration read <artifact> [--iteration-dir <dir>|--run <run-id> --iteration <n>]",
			Summary:     "Print an iteration artifact",
			Description: "`pr-template` prints the repository pull request template when present and a CLI-owned fallback otherwise.",
			Flags:       iterationLocatorFlags(),
			Agent:       true,
			AgentOnly:   true,
		},
		{
			Path:        []string{"iteration", "write"},
			Usage:       "loop iteration write <artifact> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--file <path>|--value <text>]",
			Summary:     "Replace a writable iteration artifact",
			Description: "Input is read from --file, --value, or stdin.",
			Flags: append(iterationLocatorFlags(),
				helpFlag{Name: "--file <path>", Description: "source file, or - for stdin"},
				helpFlag{Name: "--value <text>", Description: "literal content"},
			),
			Agent:     true,
			AgentOnly: true,
		},
		{
			Path:        []string{"iteration", "append"},
			Usage:       "loop iteration append <artifact> [--iteration-dir <dir>|--run <run-id> --iteration <n>] [--file <path>|--value <text>]",
			Summary:     "Append text to a writable iteration artifact",
			Description: "Input is read from --file, --value, or stdin.",
			Flags: append(iterationLocatorFlags(),
				helpFlag{Name: "--file <path>", Description: "source file, or - for stdin"},
				helpFlag{Name: "--value <text>", Description: "literal content"},
			),
			Agent:     true,
			AgentOnly: true,
		},
		{
			Path:        []string{"iteration", "result"},
			Usage:       "loop iteration result [--iteration-dir <dir>|--run <run-id> --iteration <n>] --summary <text> --should-stop <bool> --goal-evaluation <text> [flags]",
			Summary:     "Build valid iteration result JSON",
			Description: "Prints result JSON by default, or writes the result artifact with --write. The command infers mechanical fields and returns actionable errors for contract problems.",
			Flags: append(iterationLocatorFlags(),
				helpFlag{Name: "--write", Description: "write generated JSON to the result artifact", Default: "false"},
				helpFlag{Name: "--status <status>", Description: "result status", Default: "completed"},
				helpFlag{Name: "--summary <text>", Description: "required summary sentence"},
				helpFlag{Name: "--should-stop <bool>", Description: "required stop decision"},
				helpFlag{Name: "--goal-evaluation <text>", Description: "required explanation of the stop decision"},
				helpFlag{Name: "--validation-status <status>", Description: "validation status"},
				helpFlag{Name: "--validation-command <value>", Description: "repeatable command as JSON or name|command|exit_code|required"},
				helpFlag{Name: "--assumption <text>", Description: "repeatable recorded assumption"},
				helpFlag{Name: "--blocked-reason <text>", Description: "required when --status blocked"},
				helpFlag{Name: "--error <text>", Description: "required when --status failed"},
				helpFlag{Name: "--branch-kind <kind>", Description: "override inferred branch kind"},
				helpFlag{Name: "--branch-slug <slug>", Description: "override inferred branch slug"},
				helpFlag{Name: "--branch-final <name>", Description: "override proposed final branch name"},
			),
			Agent:     true,
			AgentOnly: true,
		},
		{
			Path:    []string{"skills"},
			Usage:   "loop skills <list|install|sync|doctor>",
			Summary: "Manage repository skills",
		},
		{
			Path:    []string{"skills", "list"},
			Usage:   "loop skills list",
			Summary: "Show discovered repository skills",
		},
		{
			Path:    []string{"skills", "install"},
			Usage:   "loop skills install <name-or-path> [--force]",
			Summary: "Install a built-in or local skill",
			Flags: []helpFlag{
				{Name: "--force", Description: "overwrite an existing skill", Default: "false"},
			},
		},
		{
			Path:    []string{"skills", "sync"},
			Usage:   "loop skills sync",
			Summary: "Copy or symlink configured skill targets",
		},
		{
			Path:    []string{"skills", "doctor"},
			Usage:   "loop skills doctor",
			Summary: "Validate skill front matter",
		},
		{
			Path:    []string{"memory"},
			Usage:   "loop memory <recent|search|compact>",
			Summary: "Inspect and search iteration memory",
			Agent:   true,
		},
		{
			Path:    []string{"memory", "recent"},
			Usage:   "loop memory recent [--run <run-id>] [--limit 30]",
			Summary: "Print recent iteration summaries",
			Flags: []helpFlag{
				{Name: "--run <run-id>", Description: "limit to one run"},
				{Name: "--limit <n>", Description: "maximum summaries", Default: "config value"},
			},
			Agent: true,
		},
		{
			Path:    []string{"memory", "search"},
			Usage:   "loop memory search <query> [--run <run-id>] [--iteration <id>] [--artifact <name>] [--limit <n>]",
			Summary: "Search SQLite-backed artifacts",
			Flags: []helpFlag{
				{Name: "--run <run-id>", Description: "limit to one run"},
				{Name: "--iteration <id>", Description: "limit to one iteration"},
				{Name: "--artifact <name>", Description: "limit to one artifact"},
				{Name: "--limit <n>", Description: "maximum matches", Default: "config value"},
			},
			Agent: true,
		},
		{
			Path:    []string{"memory", "compact"},
			Usage:   "loop memory compact [--run <run-id>]",
			Summary: "Rebuild the global SQLite search index",
			Flags: []helpFlag{
				{Name: "--run <run-id>", Description: "limit to one run"},
			},
			Agent: true,
		},
		{
			Path:        []string{"doctor"},
			Usage:       "loop doctor",
			Summary:     "Check Git, config, agent command, validation commands, and PR tooling",
			Description: "Checks local prerequisites and configured integrations.",
		},
		{
			Path:        []string{"version"},
			Usage:       "loop version",
			Summary:     "Print build version, commit, and date",
			Description: "`loop --version` and `loop -v` are aliases.",
		},
	}
}

func iterationLocatorFlags() []helpFlag {
	return []helpFlag{
		{Name: "--iteration-dir <dir>", Description: "explicit iteration directory"},
		{Name: "--dir <dir>", Description: "alias for --iteration-dir"},
		{Name: "--run <run-id>", Description: "resolve an iteration under the configured log directory"},
		{Name: "--iteration <n>", Description: "iteration id", Default: "$LOOP_ITERATION_ID or latest"},
	}
}
