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
	Worklog      string
	Validation   string
	Summary      string
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
	InstructionPath    string
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
		"Use `loop iteration`, `loop memory`, and `loop commit` commands for runtime context, artifacts, and commits.",
	}
	if pullRequestMode {
		lines = append(lines, "Before writing pull request artifacts, read template text with `loop iteration read pr-template`.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func Assemble(req Request) string {
	if req.Language == "" {
		req.Language = "en"
	}
	var b strings.Builder
	writeSection(&b, "Loop Instruction", HarnessContract(req.PullRequestMode))
	writeVerbatimInstructionSection(&b, req.InstructionPath, req.InstructionContent)
	if strings.TrimSpace(req.Goal) != "" {
		writeSection(&b, "Run Goal", req.Goal)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func WritePrompt(path string, req Request) (string, error) {
	text := Assemble(req)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return text, os.WriteFile(path, []byte(text), 0o644)
}

func writeSection(b *strings.Builder, title, body string) {
	fmt.Fprintf(b, "## %s\n\n%s\n\n", title, strings.TrimSpace(body))
}

func writeVerbatimInstructionSection(b *strings.Builder, path, content string) {
	fmt.Fprint(b, "## User Instruction (Verbatim)\n\n")
	if strings.TrimSpace(path) != "" {
		fmt.Fprintf(b, "Source: `%s`\n\n", strings.TrimSpace(path))
	}
	if content == "" {
		fmt.Fprint(b, "(Instruction file is empty.)\n\n")
		return
	}
	fence := "```"
	if strings.Contains(content, fence) {
		fence = "````"
	}
	fmt.Fprintf(b, "%smarkdown\n", fence)
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "%s\n\n", fence)
}
