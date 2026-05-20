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

func TestRunRendererShowsInitialMemoryFetch(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	var out bytes.Buffer
	now := time.Now()
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     now,
		done:        make(chan struct{}),
		stage:       string(runstate.StageCreated),
	}

	renderer.MemorySync("acme/app", true)

	want := "fetching initial GitHub PR memory for acme/app"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("line renderer should print initial memory fetch: %q", out.String())
	}
	frame := strings.Join(renderer.frame(100, 24), "\n")
	if !strings.Contains(stripANSISequences(frame), want) {
		t.Fatalf("dashboard frame missing initial memory fetch message:\n%s", frame)
	}
}

func TestRunRendererDashboardKeepsEssentialStateWithinBounds(t *testing.T) {
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
		instruction:  "",
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
	for _, want := range []string{"╦   ╔═╗", "prompt.md", "2K in", "512 out", "2 merged", "1/2 todo", "Add lazy loading", "Inspecting startup path", "Ctrl+C cancel"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame missing %q:\n%s", want, frame)
		}
	}
	assertFrameBounds(t, frameLines, 130, 32)

	shortLines := renderDashboard(rendererSnapshot{
		Started:      now.Add(-time.Minute),
		Now:          now,
		Todos:        []todoItem{{Done: true, Text: "Done"}, {Text: "Open"}},
		Current:      strings.Repeat("long message ", 20),
		InputTokens:  100,
		OutputTokens: 50,
	}, 70, 8)
	shortFrame := strings.Join(shortLines, "\n")
	for _, want := range []string{"loop", "long message", "todo: 1/2 done"} {
		if !strings.Contains(shortFrame, want) {
			t.Fatalf("compact frame missing %q:\n%s", want, shortFrame)
		}
	}
	assertFrameFits(t, shortLines, 70, 8)
}

func TestParseTodoItemsNormalizesCommitTypeColon(t *testing.T) {
	todos := parseTodoItems(strings.NewReader("- [ ] C scaffold Next.js app tooling\n- [>] F: add dashboard shell\n- [x] D update docs\n- [ ] Check setup\n"))

	if len(todos) != 4 {
		t.Fatalf("todo count = %d, want 4", len(todos))
	}
	got := []string{todos[0].Text, todos[1].Text, todos[2].Text, todos[3].Text}
	want := []string{"C: scaffold Next.js app tooling", "F: add dashboard shell", "D: update docs", "Check setup"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("todo text = %#v, want %#v", got, want)
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
		Instruction:  "",
		Todos:        todos,
		InputTokens:  100,
		OutputTokens: 50,
		MergeCount:   1,
		LatestMsg:    "Latest agent message.",
	}, 100, 18)
	frame := strings.Join(lines, "\n")

	assertFrameBounds(t, lines, 100, 18)
	for _, want := range []string{"2/8 todo", "Todo 1", "Todo 2", "6 hidden below", "Ctrl+C cancel"} {
		if !strings.Contains(stripANSISequences(frame), want) {
			t.Fatalf("frame missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "Todo 3") || strings.Contains(frame, "Todo 8") {
		t.Fatalf("hidden TODOs should not be rendered when hidden-below row is needed:\n%s", frame)
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

func assertFrameBounds(t *testing.T, lines []string, width, height int) {
	t.Helper()
	if len(lines) != height {
		t.Fatalf("line count = %d, want %d:\n%s", len(lines), height, strings.Join(lines, "\n"))
	}
	assertFrameFits(t, lines, width, height)
}

func assertFrameFits(t *testing.T, lines []string, width, maxHeight int) {
	t.Helper()
	if len(lines) > maxHeight {
		t.Fatalf("line count = %d, want <= %d:\n%s", len(lines), maxHeight, strings.Join(lines, "\n"))
	}
	for i, line := range lines {
		if displayWidth(line) > width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i, displayWidth(line), width, strings.Join(lines, "\n"))
		}
	}
}
