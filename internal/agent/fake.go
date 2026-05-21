package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
)

func RunFakeAgentFromEnv() int {
	mode := fakeModeFromEnv()
	iterationDir := os.Getenv("LOOP_ITERATION_DIR")
	if iterationDir != "" {
		_ = os.MkdirAll(iterationDir, 0o755)
	}

	switch mode {
	case "invalid_json":
		_ = writeFakeRawResultHandoff(iterationDir, "{invalid json\n")
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
	case "needs_repair":
		writeFakeResult(iterationDir, "needs_repair")
		return 0
	case "validation_fix":
		workDir := getenv("LOOP_WORKDIR", ".")
		_ = loopBranchRename(workDir, "test/fake-agent")
		changePath := filepath.Join(workDir, "validation-ok.txt")
		_ = os.WriteFile(changePath, []byte("validation repaired at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = git(workDir, "add", "validation-ok.txt")
		_ = git(workDir, "commit", "-m", "F: repair validation fixture")
		writeFakeResult(iterationDir, "completed", fakeCommit(workDir))
		return 0
	case "completed_unrenamed":
		workDir := getenv("LOOP_WORKDIR", ".")
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed without branch rename at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = git(workDir, "add", "loop-fake-change.txt")
		_ = git(workDir, "commit", "-m", "F: run fake agent behavior")
		writeFakeResult(iterationDir, "completed", fakeCommit(workDir))
		return 0
	default:
		workDir := getenv("LOOP_WORKDIR", ".")
		_ = loopBranchRename(workDir, "test/fake-agent")
		changePath := filepath.Join(workDir, "loop-fake-change.txt")
		_ = os.WriteFile(changePath, []byte("fake agent completed at "+time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
		_ = git(workDir, "add", "loop-fake-change.txt")
		_ = git(workDir, "commit", "-m", "F: run fake agent behavior")
		writeFakeResult(iterationDir, "completed", fakeCommit(workDir))
		return 0
	}
}

func fakeModeFromEnv() string {
	sequence := strings.TrimSpace(os.Getenv("LOOP_FAKE_AGENT_SEQUENCE"))
	if sequence == "" {
		return getenv("LOOP_FAKE_AGENT_MODE", "completed")
	}
	var modes []string
	for _, item := range strings.Split(sequence, ",") {
		if mode := strings.TrimSpace(item); mode != "" {
			modes = append(modes, mode)
		}
	}
	if len(modes) == 0 {
		return getenv("LOOP_FAKE_AGENT_MODE", "completed")
	}
	index := nextFakeInvocationIndex()
	if index >= len(modes) {
		index = len(modes) - 1
	}
	return modes[index]
}

func nextFakeInvocationIndex() int {
	path := strings.TrimSpace(os.Getenv("LOOP_FAKE_AGENT_COUNT_FILE"))
	if path == "" {
		if n, err := strconv.Atoi(strings.TrimLeft(getenv("LOOP_ITERATION_ID", "1"), "0")); err == nil && n > 0 {
			return n - 1
		}
		return 0
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := os.ReadFile(path)
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	_ = os.WriteFile(path, []byte(strconv.Itoa(count+1)+"\n"), 0o644)
	return count
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
	_ = writeFakeResultHandoff(iterationDir, status, commits...)
}

func writeFakeResultHandoff(iterationDir, status string, commits ...map[string]any) error {
	validationStatus := "passed"
	if status == "blocked" {
		validationStatus = "skipped"
	}
	commitList := make([]map[string]any, 0, len(commits))
	for _, commit := range commits {
		if commit != nil {
			commitList = append(commitList, commit)
		}
	}
	result := map[string]any{
		"schema_version":       1,
		"status":               status,
		"summary_sentence":     "Run fake agent behavior",
		"should_fully_stop":    status != "completed",
		"goal_evaluation":      "Fake agent produced a deterministic test result.",
		"branch":               fakeBranchResult(status),
		"commits":              commitList,
		"validation":           map[string]any{"status": validationStatus, "commands": []map[string]any{}},
		"artifacts":            map[string]any{},
		"assumptions":          []string{},
		"blocked_reason":       "",
		"follow_up_issue_refs": []string{},
	}
	if status == "blocked" {
		result["blocked_reason"] = "Fake agent blocked by requested mode."
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return writeFakeRawResultHandoff(iterationDir, string(append(data, '\n')))
}

func writeFakeRawResultHandoff(iterationDir, resultJSON string) error {
	globalPath := artifactdb.GlobalDBPathForIteration(iterationDir)
	if globalPath == "" {
		return nil
	}
	runID := getenv("LOOP_RUN_ID", "")
	iterationID := getenv("LOOP_ITERATION_ID", "")
	if runID == "" || iterationID == "" {
		parsedRunID, parsedIterationID := artifactdb.ParseIterationDir(iterationDir)
		if runID == "" {
			runID = parsedRunID
		}
		if iterationID == "" {
			iterationID = parsedIterationID
		}
	}
	if runID == "" || iterationID == "" {
		return nil
	}
	return artifactdb.WriteResultHandoff(globalPath, runID, iterationID, resultJSON)
}

func fakeBranchResult(status string) map[string]any {
	initial := getenv("LOOP_INITIAL_BRANCH", "wip/0001")
	current := currentBranch(getenv("LOOP_WORKDIR", "."))
	if current == "" {
		current = getenv("LOOP_CURRENT_BRANCH", initial)
	}
	kind := "test"
	slug := "fake-agent"
	final := ""
	if status == "completed" && current != "" && current != initial {
		final = current
		if parsedKind, parsedSlug, ok := splitBranch(current); ok {
			kind = parsedKind
			slug = parsedSlug
		}
	}
	branch := map[string]any{"initial_name": initial, "kind": kind, "slug": slug}
	if final != "" {
		branch["final_name"] = final
	}
	return branch
}

func currentBranch(dir string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(bytesTrimSpace(out))
}

func splitBranch(branch string) (string, string, bool) {
	kind, slug, ok := strings.Cut(strings.TrimSpace(branch), "/")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(slug) == "" {
		return "", "", false
	}
	return strings.TrimSpace(kind), strings.TrimSpace(slug), true
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
