package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func RunFakeAgentFromEnv() int {
	mode := getenv("LOOP_FAKE_AGENT_MODE", "completed")
	iterationDir := os.Getenv("LOOP_ITERATION_DIR")
	if iterationDir != "" {
		_ = os.MkdirAll(iterationDir, 0o755)
	}

	switch mode {
	case "invalid_json":
		_ = writeFakeArtifact(iterationDir, "result", "{invalid json\n")
		return 0
	case "dirty":
		_ = os.WriteFile(filepath.Join(getenv("LOOP_WORKDIR", "."), "loop-fake-dirty.txt"), []byte("dirty\n"), 0o644)
		writeFakeResult(iterationDir, "completed")
		return 0
	case "blocked":
		writeFakeResult(iterationDir, "blocked")
		return 1
	case "no_change":
		writeFakeResult(iterationDir, "no_change")
		return 0
	case "completed_unrenamed":
		workDir := getenv("LOOP_WORKDIR", ".")
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed without branch rename at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = git(workDir, "add", "loop-fake-change.txt")
		_ = git(workDir, "commit", "-m", "F: run fake agent behavior")
		_ = writeFakeArtifact(iterationDir, "summary", "# Iteration Summary\n\n- Fake agent completed without renaming.\n")
		writeFakeResult(iterationDir, "completed", fakeCommit(workDir))
		return 0
	default:
		workDir := getenv("LOOP_WORKDIR", ".")
		_ = loopBranchRename(workDir, "test/fake-agent")
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = git(workDir, "add", "loop-fake-change.txt")
		_ = git(workDir, "commit", "-m", "F: run fake agent behavior")
		_ = writeFakeArtifact(iterationDir, "summary", "# Iteration Summary\n\n- Fake agent completed.\n")
		writeFakeResult(iterationDir, "completed", fakeCommit(workDir))
		return 0
	}
}

func loopBranchRename(dir, branch string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "branch", "rename", branch)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	return cmd.Run()
}

func writeFakeResult(iterationDir, status string, commits ...map[string]any) {
	validationStatus := "passed"
	if status == "blocked" {
		validationStatus = "skipped"
	}
	if commits == nil {
		commits = []map[string]any{}
	}
	result := map[string]any{
		"schema_version":    1,
		"status":            status,
		"summary_sentence":  "Run fake agent behavior",
		"should_fully_stop": status != "completed",
		"goal_evaluation":   "Fake agent produced a deterministic test result.",
		"branch":            map[string]any{"initial_name": "wip/0001", "kind": "test", "slug": "fake-agent", "final_name": "test/fake-agent"},
		"commits":           commits,
		"validation":        map[string]any{"status": validationStatus, "commands": []map[string]any{}},
		"artifacts":         map[string]any{"summary": "summary"},
		"assumptions":       []string{},
		"blocked_reason":    "",
	}
	if status == "blocked" {
		result["blocked_reason"] = "Fake agent blocked by requested mode."
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	_ = writeFakeArtifact(iterationDir, "result", string(append(b, '\n')))
}

func writeFakeArtifact(iterationDir, name, content string) error {
	if iterationDir != "" {
		return artifactdb.Write(iterationDir, name, content)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "iteration", "write", name, "--value", content)
	cmd.Env = os.Environ()
	return cmd.Run()
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

func fakeCommit(dir string) map[string]any {
	cmd := exec.Command("git", "log", "-1", "--format=%H")
	cmd.Dir = dir
	out, err := cmd.Output()
	sha := ""
	if err == nil {
		sha = string(bytesTrimSpace(out))
	}
	return map[string]any{"sha": sha, "message": "F: run fake agent behavior"}
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 {
		last := b[len(b)-1]
		if last != ' ' && last != '\n' && last != '\t' && last != '\r' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
