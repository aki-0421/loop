package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Skill struct {
	Name string
	Path string
}

type Paths struct {
	IterationDir string
	Prompt       string
	Plan         string
	Todo         string
	Validation   string
	Result       string
	EventLog     string
	StdoutLog    string
	StderrLog    string
	PRTitle      string
	PRBody       string
}

type Request struct {
	Language           string
	IterationID        string
	BaseBranch         string
	CurrentBranch      string
	Agent              string
	PullRequestMode    bool
	Skills             []Skill
	Paths              Paths
	RecentSummaries    []MemoryItem
	SearchResults      []MemoryItem
	InstructionContent string
	Goal               string
	EffectiveConfig    string
	SchemaSummary      string
}

type MemoryItem struct {
	Path    string
	Title   string
	Content string
}

func HarnessContract(pullRequestMode bool) string {
	lines := []string{
		"Use the `loop` skill for this single loop iteration.",
		"Use `loop iteration`, `loop memory`, `loop issue`, and `loop commit` commands for runtime context, artifacts, GitHub Issues, and commits.",
	}
	if pullRequestMode {
		lines = append(lines, "Before writing pull request artifacts, read template text with `loop iteration read pr-template`; create, check, repair, and merge the PR with `loop pr` before writing a completed result.")
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
