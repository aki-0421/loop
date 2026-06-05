package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Request struct {
	Language        string
	PullRequestMode bool
}

func HarnessContract(pullRequestMode bool) string {
	lines := []string{
		"Use the `loop` skill for this CLI-orchestrated role run.",
		"Use `loop iteration`, `loop issue`, and `loop handoff` commands for runtime context, GitHub Issues, and role handoff JSON.",
		"Do not run Git or GitHub commands directly; use loop-owned commands for branch renames, commits, pull requests, task merges, and handoffs.",
		"Run `loop role instruction` for the current role-specific operating rules when you need to refresh the role contract.",
		"Use `loop help` for the agent-facing command reference and `loop help handoff write` for the current handoff schema and command flags if needed.",
	}
	if pullRequestMode {
		lines = append(lines, "In pull request mode, review agents create, check, and merge pull requests through loop-owned PR commands before approval.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func Assemble(req Request) string {
	if req.Language == "" {
		req.Language = "en"
	}
	var b strings.Builder
	writeSection(&b, "Loop Instruction", HarnessContract(req.PullRequestMode))
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func WritePrompt(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func writeSection(b *strings.Builder, title, body string) {
	fmt.Fprintf(b, "## %s\n\n%s\n\n", title, strings.TrimSpace(body))
}
