package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aki-0421/loop/internal/runstate"
)

func TestRunRendererLineModePrintsAuditCommand(t *testing.T) {
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     time.Now(),
		done:        make(chan struct{}),
	}
	renderer.AgentEvent(runstate.Event{"type": "agent.command", "command": "make test"})
	if !strings.Contains(out.String(), "make test") {
		t.Fatalf("line renderer should print audit command: %q", out.String())
	}
}

func TestRunRendererDedupesConsecutiveActivity(t *testing.T) {
	renderer := &runRenderer{
		enabled:     true,
		interactive: true,
		started:     time.Now(),
		done:        make(chan struct{}),
	}
	renderer.addActivity("command: git status --short")
	renderer.addActivity("command: git status --short")
	renderer.addActivity("command: make test")

	if len(renderer.activity) != 2 {
		t.Fatalf("activity = %#v, want consecutive duplicate removed", renderer.activity)
	}
}

func TestRunRendererInteractiveUsesScreenRefresh(t *testing.T) {
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:     true,
		interactive: true,
		writer:      &out,
		started:     time.Now(),
		runID:       "run",
		agent:       "codex",
		base:        "develop",
		logs:        ".loop/runs/run",
		stage:       string(runstate.StageAgentRunning),
		done:        make(chan struct{}),
	}
	renderer.render()
	got := out.String()
	if !strings.Contains(got, "\x1b[H") {
		t.Fatalf("interactive renderer should reposition cursor, got %q", got)
	}
	if !strings.Contains(got, "╦   ╔═╗ ╔═╗ ╔═╗") || !strings.Contains(got, "Ctrl+C cancel") {
		t.Fatalf("interactive renderer should draw focused dashboard, got %q", got)
	}
}

func TestRunRendererWideDashboardShowsPanelsAndProgress(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	todoPath := filepath.Join(t.TempDir(), "todo.txt")
	if err := os.WriteFile(todoPath, []byte("- [x] Inspect startup path\n- [>] Add lazy loading\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	renderer := &runRenderer{
		started:      now.Add(-5 * time.Minute),
		runID:        "run",
		agent:        "codex",
		repo:         "loop",
		instruction:  "task.md",
		base:         "main",
		branch:       "wip/0001",
		goal:         "Reduce startup latency without sacrificing functionality.",
		stage:        string(runstate.StageAgentRunning),
		stageDetail:  "agent running",
		iteration:    "0001",
		todoPath:     todoPath,
		activity:     []string{"command: go test ./..."},
		activityLog:  []rendererLogLine{{At: now, Text: "command: go test ./..."}},
		events:       []rendererEvent{{At: now, Status: "active", Title: "Implementing", Detail: "agent running"}},
		messageCount: 1,
		inputTokens:  1536,
		outputTokens: 512,
		mergeCount:   2,
		latestMsg:    "Inspecting startup path before editing.",
		maxIter:      3,
	}

	frameLines := renderer.frame(130, 32)
	frame := strings.Join(frameLines, "\n")
	for _, want := range []string{"╦   ╔═╗", "task.md", "2K in", "512 out", "2 merged", "1/2 todo", "Add lazy loading", "Inspecting startup path", "Ctrl+C cancel"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame missing %q:\n%s", want, frame)
		}
	}
	if !strings.Contains(stripANSISequences(frameLines[len(frameLines)-2]), "Ctrl+C cancel") {
		t.Fatalf("footer should be fixed on the second-last row:\n%s", frame)
	}
	for i := 0; i < 3; i++ {
		if strings.TrimSpace(stripANSISequences(frameLines[i])) != "" {
			t.Fatalf("logo should have three blank rows above it:\n%s", frame)
		}
	}
	if !strings.Contains(stripANSISequences(frameLines[3]), "╦   ╔═╗") {
		t.Fatalf("logo should start after three blank rows:\n%s", frame)
	}
	metricsLine, latestLine, todoLine := -1, -1, -1
	for i, line := range frameLines {
		plain := stripANSISequences(line)
		switch {
		case strings.Contains(plain, "2K in"):
			metricsLine = i
		case strings.Contains(plain, "Inspecting startup path before editing."):
			latestLine = i
		case strings.Contains(plain, "Inspect startup path"):
			todoLine = i
		}
	}
	if latestLine != metricsLine+2 || strings.TrimSpace(stripANSISequences(frameLines[metricsLine+1])) != "" || todoLine <= latestLine {
		t.Fatalf("latest message should sit one blank row below metrics and above todos, metrics=%d latest=%d todo=%d:\n%s", metricsLine, latestLine, todoLine, frame)
	}
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "╦   ╔═╗") {
			if strings.Index(line, "╦") < 20 {
				t.Fatalf("logo should be horizontally centered, got:\n%s", frame)
			}
			break
		}
	}
	var todoIndents []int
	checkedCenter := false
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "Inspect startup path") || strings.Contains(line, "Add lazy loading") {
			if idx := strings.Index(line, "Inspect"); idx >= 0 {
				visible := strings.TrimRight(stripANSISequences(line), " ")
				left := displayWidth(visible) - displayWidth(strings.TrimLeft(visible, " "))
				right := 130 - displayWidth(visible)
				if diff := absInt(left - right); diff > 1 {
					t.Fatalf("todo block should be horizontally centered, left=%d right=%d:\n%s", left, right, frame)
				}
				checkedCenter = true
			}
			todoIndents = append(todoIndents, strings.Index(line, strings.Fields(strings.TrimSpace(stripANSISequences(line)))[1]))
		}
	}
	if len(todoIndents) != 2 || todoIndents[0] != todoIndents[1] {
		t.Fatalf("todo rows should share the same left edge, indents=%v:\n%s", todoIndents, frame)
	}
	if !checkedCenter {
		t.Fatalf("todo block center check did not find expected row:\n%s", frame)
	}
}

func TestRunRendererDashboardListsMaximumTodosWithHiddenBelow(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	now := time.Now()
	todos := []todoItem{
		{Done: true, Status: "done", Text: "Todo 1"},
		{Done: true, Status: "done", Text: "Todo 2"},
		{Text: "Todo 3"},
		{Text: "Todo 4"},
		{Status: "active", Text: "Todo 5"},
		{Text: "Todo 6"},
		{Text: "Todo 7"},
		{Text: "Todo 8"},
	}
	lines := renderDashboard(rendererSnapshot{
		Started:      now.Add(-time.Minute),
		Now:          now,
		Instruction:  "task.md",
		Todos:        todos,
		InputTokens:  100,
		OutputTokens: 50,
		MergeCount:   1,
		LatestMsg:    "Latest agent message.",
	}, 100, 18)
	frame := strings.Join(lines, "\n")

	if len(lines) != 18 {
		t.Fatalf("line count = %d, want 18:\n%s", len(lines), frame)
	}
	for i := 0; i < 3; i++ {
		if strings.TrimSpace(stripANSISequences(lines[i])) != "" {
			t.Fatalf("logo should have three blank rows above it:\n%s", frame)
		}
	}
	if !strings.Contains(stripANSISequences(lines[9]), "2/8 todo") {
		t.Fatalf("metrics should include completed TODO progress:\n%s", frame)
	}
	if strings.TrimSpace(stripANSISequences(lines[10])) != "" {
		t.Fatalf("metrics and latest message should be separated by one blank row:\n%s", frame)
	}
	if !strings.Contains(stripANSISequences(lines[15]), "6 hidden below") {
		t.Fatalf("last TODO row should report hidden items:\n%s", frame)
	}
	if strings.Contains(frame, "Todo 3") || strings.Contains(frame, "Todo 8") {
		t.Fatalf("hidden TODOs should not be rendered when hidden-below row is needed:\n%s", frame)
	}
	if !strings.Contains(stripANSISequences(lines[16]), "Ctrl+C cancel") {
		t.Fatalf("footer should be fixed on the second-last row:\n%s", frame)
	}
	if strings.TrimSpace(stripANSISequences(lines[17])) != "" {
		t.Fatalf("last row should remain empty:\n%s", frame)
	}
}

func TestPlanningSpinnerAdvancesWithRendererTick(t *testing.T) {
	started := time.Now()
	first := spinnerSymbol(rendererSnapshot{Started: started, Now: started})
	second := spinnerSymbol(rendererSnapshot{Started: started, Now: started.Add(rendererTickInterval)})
	if first == second {
		t.Fatalf("spinner should advance on each renderer tick, got %q then %q", first, second)
	}
}

func TestRunRendererAppliesTokenUsageEvents(t *testing.T) {
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &bytes.Buffer{},
		started:     time.Now(),
		done:        make(chan struct{}),
	}

	renderer.AgentEvent(runstate.Event{"type": "agent.started", "command": "codex"})
	renderer.AgentEvent(runstate.Event{"type": "agent.usage", "input_tokens": 1200, "output_tokens": 40, "delta": true})
	renderer.AgentEvent(runstate.Event{"type": "agent.usage", "input_tokens": 800, "output_tokens": 60, "delta": true})

	if renderer.inputTokens != 2000 || renderer.outputTokens != 100 {
		t.Fatalf("delta usage = %d/%d, want 2000/100", renderer.inputTokens, renderer.outputTokens)
	}

	renderer.AgentEvent(runstate.Event{"type": "agent.started", "command": "claude"})
	renderer.AgentEvent(runstate.Event{"type": "agent.usage", "input_tokens": 500, "output_tokens": 25, "estimated": true})

	if renderer.inputTokens != 2500 || renderer.outputTokens != 125 {
		t.Fatalf("snapshot usage = %d/%d, want 2500/125", renderer.inputTokens, renderer.outputTokens)
	}
	if !renderer.tokensEstimated {
		t.Fatal("estimated token usage should mark renderer totals as estimated")
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
