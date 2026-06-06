package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/pr"
	"github.com/aki-0421/loop/internal/runstate"
	"github.com/aki-0421/loop/internal/workflow"
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

func TestRunRendererLineModePrintsAgentMessage(t *testing.T) {
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     time.Now(),
		done:        make(chan struct{}),
	}
	renderer.AgentEvent(runstate.Event{"type": "agent.message", "text": "Inspecting renderer behavior before editing."})
	if !strings.Contains(out.String(), "Inspecting renderer behavior before editing.") {
		t.Fatalf("line renderer should print agent message: %q", out.String())
	}
	if renderer.latestMsg != "Inspecting renderer behavior before editing." {
		t.Fatalf("latest message = %q", renderer.latestMsg)
	}
}

func TestRunRendererLineModePrintsTokenUsage(t *testing.T) {
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     time.Now(),
		done:        make(chan struct{}),
	}

	renderer.AgentEvent(runstate.Event{"type": "agent.started", "command": "codex"})
	renderer.AgentEvent(runstate.Event{"type": "agent.usage", "input_tokens": 26985, "output_tokens": 26, "delta": true})

	got := out.String()
	if !strings.Contains(got, "usage") || !strings.Contains(got, "27K in, 26 out") {
		t.Fatalf("line renderer should print token usage: %q", got)
	}
}

func TestRunRendererInteractiveModeDisablesAutoWrap(t *testing.T) {
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:             true,
		interactive:         true,
		writer:              &out,
		started:             time.Now(),
		done:                make(chan struct{}),
		sleepFetchRequested: make(chan struct{}, 1),
	}

	renderer.Start(context.Background())
	renderer.Stop(runstate.StageCompleted, "done")

	got := out.String()
	if !strings.Contains(got, "\x1b[?1049h\x1b[?25l\x1b[?7l") {
		t.Fatalf("interactive renderer should disable auto-wrap on start:\n%q", got)
	}
	if !strings.Contains(got, "\x1b[?7h\x1b[?25h\x1b[?1049l") {
		t.Fatalf("interactive renderer should restore auto-wrap on stop:\n%q", got)
	}
}

func TestRunRendererUsesAbsoluteRowsInsteadOfNewlines(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	t.Setenv("LINES", "24")
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:     true,
		interactive: true,
		writer:      &out,
		started:     time.Now(),
		done:        make(chan struct{}),
		stage:       string(runstate.StageCreated),
		stageDetail: "created",
	}

	renderer.render()

	got := out.String()
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("interactive render should not rely on terminal newline handling:\n%q", got)
	}
	if !strings.Contains(got, "\x1b[1;1H") || !strings.Contains(got, "\x1b[24;1H") {
		t.Fatalf("interactive render should address rows explicitly:\n%q", got)
	}
	if strings.Contains(got, strings.Repeat(" ", 80)+"\x1b[K") {
		t.Fatalf("interactive render should clear with ESC[K instead of full-width blank writes:\n%q", got)
	}
}

func TestRunRendererOnlyDrawsChangedRows(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	t.Setenv("LINES", "24")
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:     true,
		interactive: true,
		writer:      &out,
		started:     time.Now(),
		done:        make(chan struct{}),
		stage:       string(runstate.StageCreated),
		stageDetail: "created",
	}

	renderer.render()
	out.Reset()
	renderer.render()
	if got := out.String(); got != "" {
		t.Fatalf("unchanged frame should not redraw rows:\n%q", got)
	}

	renderer.latestMsg = "Agent update."
	renderer.render()
	got := out.String()
	if !strings.Contains(got, "\x1b[3;1H") || !strings.Contains(got, "Agent update.") {
		t.Fatalf("changed agent message row should redraw:\n%q", got)
	}
	for _, unchangedRow := range []string{"\x1b[1;1H", "\x1b[2;1H", "\x1b[4;1H", "\x1b[23;1H"} {
		if strings.Contains(got, unchangedRow) {
			t.Fatalf("unchanged row %q should not redraw:\n%q", unchangedRow, got)
		}
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

	want := "fetching initial GitHub memory for acme/app"
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
		tasks:        []taskItem{{ID: "inspect", Done: true, Status: "done", Text: "Inspect startup path"}, {ID: "lazy-load", Status: "active", Text: "Add lazy loading"}},
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
	for _, want := range []string{"╦   ╔═╗", "prompt.md", "2K in", "512 out", "2 merged", "1/2 tasks", "Add lazy loading", "Inspecting startup path", "Ctrl+C gracefully stops after this iteration"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame missing %q:\n%s", want, frame)
		}
	}
	assertFrameBounds(t, frameLines, 130, 32)

	shortLines := renderDashboard(rendererSnapshot{
		Started:      now.Add(-time.Minute),
		Now:          now,
		Tasks:        []taskItem{{Done: true, Text: "Done"}, {Text: "Open"}},
		Current:      strings.Repeat("long message ", 20),
		LatestMsg:    strings.Repeat("latest agent message ", 8),
		InputTokens:  100,
		OutputTokens: 50,
	}, 70, 8)
	shortFrame := strings.Join(shortLines, "\n")
	for _, want := range []string{"prompt.md", "100 in", "latest agent message", "1/2 tasks", "Done", "Open"} {
		if !strings.Contains(shortFrame, want) {
			t.Fatalf("compact frame missing %q:\n%s", want, shortFrame)
		}
	}
	assertFrameFits(t, shortLines, 70, 8)
}

func TestRunRendererShowsPRReviewModeMetric(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	now := time.Now()
	lines := renderDashboard(rendererSnapshot{
		Started:      now.Add(-time.Minute),
		Now:          now,
		Color:        true,
		PRMode:       true,
		PRReviewMode: config.ReviewModeParallelHumanReview,
		Tasks:        []taskItem{{Done: true, Text: "Done"}},
		InputTokens:  100,
		OutputTokens: 50,
	}, 140, 20)
	frame := strings.Join(lines, "\n")
	plain := stripANSISequences(frame)
	if !strings.Contains(plain, "parallel review") {
		t.Fatalf("frame missing review mode metric:\n%s", plain)
	}
	if !strings.Contains(frame, ansiYellow+ansiBold+"parallel review"+ansiReset) {
		t.Fatalf("parallel review metric should be colorized:\n%s", frame)
	}
	if !strings.Contains(plain, "r cycles review mode") {
		t.Fatalf("footer missing review mode shortcut:\n%s", plain)
	}
	assertFrameBounds(t, lines, 140, 20)
}

func TestRunRendererCyclesPRReviewModeAndLocksDuringIntegration(t *testing.T) {
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:             true,
		interactive:         false,
		writer:              &out,
		started:             time.Now(),
		done:                make(chan struct{}),
		sleepFetchRequested: make(chan struct{}, 1),
	}
	renderer.EnablePRReviewMode(config.ReviewModeAutoMerge)
	renderer.handleInputByte(context.Background(), 'r')
	if got := renderer.CurrentPRReviewMode(); got != config.ReviewModeParallelHumanReview {
		t.Fatalf("first cycle mode = %q, want %s", got, config.ReviewModeParallelHumanReview)
	}
	renderer.handleInputByte(context.Background(), 'R')
	if got := renderer.CurrentPRReviewMode(); got != config.ReviewModeSerialHumanReview {
		t.Fatalf("second cycle mode = %q, want %s", got, config.ReviewModeSerialHumanReview)
	}
	renderer.LockPRReviewMode(true)
	renderer.handleInputByte(context.Background(), 'r')
	if got := renderer.CurrentPRReviewMode(); got != config.ReviewModeSerialHumanReview {
		t.Fatalf("locked cycle changed mode to %q", got)
	}
	frame := stripANSISequences(strings.Join(renderer.frame(120, 20), "\n"))
	if !strings.Contains(frame, "serial review") || !strings.Contains(frame, "review mode locked") {
		t.Fatalf("locked frame missing review mode state:\n%s", frame)
	}
}

func TestRunRendererPrimaryMessageOnlyShowsAgentMessages(t *testing.T) {
	now := time.Now()
	lines := renderDashboard(rendererSnapshot{
		Started:     now.Add(-time.Minute),
		Now:         now,
		Current:     "switched to parallel review",
		StageDetail: "graceful shutdown requested; press Ctrl+C again to exit immediately",
	}, 70, 8)
	if got := strings.TrimSpace(stripANSISequences(lines[2])); got != "" {
		t.Fatalf("primary message row should be blank without an agent message, got %q:\n%s", got, strings.Join(lines, "\n"))
	}

	lines = renderDashboard(rendererSnapshot{
		Started:   now.Add(-time.Minute),
		Now:       now,
		LatestMsg: "Agent-only message.",
		Current:   "switched to serial review",
	}, 70, 8)
	primary := strings.TrimSpace(stripANSISequences(lines[2]))
	if !strings.Contains(primary, "Agent-only message.") {
		t.Fatalf("primary message should show latest agent message, got %q:\n%s", primary, strings.Join(lines, "\n"))
	}
	if strings.Contains(primary, "switched to serial review") {
		t.Fatalf("primary message should ignore current renderer state, got %q:\n%s", primary, strings.Join(lines, "\n"))
	}
}

func TestRunRendererAlwaysReservesTwoAgentMessageRows(t *testing.T) {
	now := time.Now()
	base := rendererSnapshot{
		Started: now.Add(-time.Minute),
		Now:     now,
		Tasks:   []taskItem{{Status: "active", Text: "Stable task row"}},
	}
	cases := []struct {
		name string
		msg  string
	}{
		{name: "empty"},
		{name: "single", msg: "Short agent update."},
		{name: "wrapped", msg: strings.Repeat("Long agent update ", 10)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := base
			snapshot.LatestMsg = tc.msg
			lines := renderDashboard(snapshot, 70, 8)
			primaryRow := strings.TrimSpace(stripANSISequences(lines[2]))
			reservedRow := strings.TrimSpace(stripANSISequences(lines[3]))
			taskRow := strings.TrimSpace(stripANSISequences(lines[4]))
			if tc.msg != "" && primaryRow == "" {
				t.Fatalf("first agent message row should contain the message:\n%s", strings.Join(lines, "\n"))
			}
			if tc.name == "single" && reservedRow != "" {
				t.Fatalf("single-line agent message should keep the second row reserved and blank, got %q:\n%s", reservedRow, strings.Join(lines, "\n"))
			}
			if !strings.Contains(taskRow, "Stable task row") {
				t.Fatalf("task row should remain fixed after two agent message rows, got %q:\n%s", taskRow, strings.Join(lines, "\n"))
			}
			assertFrameBounds(t, lines, 70, 8)
		})
	}
}

func TestRunRendererControlsDoNotOverwriteAgentMessage(t *testing.T) {
	renderer := &runRenderer{
		enabled:             true,
		interactive:         true,
		writer:              &bytes.Buffer{},
		started:             time.Now(),
		done:                make(chan struct{}),
		sleepFetchRequested: make(chan struct{}, 1),
	}
	renderer.AgentEvent(runstate.Event{"type": "agent.message", "text": "Agent is inspecting the repository."})
	renderer.EnablePRReviewMode(config.ReviewModeAutoMerge)
	renderer.handleInputByte(context.Background(), 'r')
	renderer.GracefulShutdownRequested()

	frame := stripANSISequences(strings.Join(renderer.frame(120, 24), "\n"))
	if !strings.Contains(frame, "Agent is inspecting the repository.") {
		t.Fatalf("agent message should remain visible after renderer controls:\n%s", frame)
	}
	if strings.Contains(frame, "switched to parallel review") || strings.Contains(frame, "graceful shutdown requested") {
		t.Fatalf("renderer controls should not appear in the agent message area:\n%s", frame)
	}
}

func TestRunRendererLockedReviewFooterStaysWithinContentColumn(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	now := time.Now()
	lines := renderDashboard(rendererSnapshot{
		Started:            now.Add(-time.Minute),
		Now:                now,
		Color:              true,
		PRMode:             true,
		PRReviewMode:       config.ReviewModeAutoMerge,
		PRReviewModeLocked: true,
	}, 80, 24)

	footer := stripANSISequences(lines[len(lines)-2])
	if !strings.Contains(footer, "review mode locked") {
		t.Fatalf("locked footer missing review state:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(footer, "Ctrl+C gracefully stops after this iteration") {
		t.Fatalf("locked footer should keep the Ctrl+C hint intact:\n%q", footer)
	}
	if strings.Contains(footer, "[truncated]") {
		t.Fatalf("locked footer should use dashboard ellipsis rules, not log truncation:\n%q", footer)
	}
	if strings.HasPrefix(footer, "review mode locked") {
		t.Fatalf("locked footer should stay centered within the content column:\n%q", footer)
	}
	assertFrameBounds(t, lines, 80, 24)
}

func TestRunRendererEnablePRReviewModeDoesNotRenderBeforeStart(t *testing.T) {
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:             true,
		interactive:         true,
		writer:              &out,
		started:             time.Now(),
		done:                make(chan struct{}),
		sleepFetchRequested: make(chan struct{}, 1),
	}

	renderer.EnablePRReviewMode(config.ReviewModeAutoMerge)

	if got := out.String(); got != "" {
		t.Fatalf("EnablePRReviewMode rendered before Start:\n%q", got)
	}
	if got := renderer.CurrentPRReviewMode(); got != config.ReviewModeAutoMerge {
		t.Fatalf("review mode = %q, want %s", got, config.ReviewModeAutoMerge)
	}
}

func TestRunRendererSerializesTitleWritesWithFrame(t *testing.T) {
	writer := newBlockingRendererWriter("auto merge")
	renderer := &runRenderer{
		enabled:             true,
		interactive:         true,
		writer:              writer,
		started:             time.Now(),
		done:                make(chan struct{}),
		titleEnabled:        true,
		sleepFetchRequested: make(chan struct{}, 1),
		prMode:              true,
		prReviewMode:        config.ReviewModeAutoMerge,
	}

	renderDone := make(chan struct{})
	go func() {
		renderer.render()
		close(renderDone)
	}()

	select {
	case <-writer.blocked:
	case <-time.After(time.Second):
		t.Fatal("renderer did not reach the blocked metrics write")
	}

	titleDone := make(chan struct{})
	go func() {
		renderer.setTitle()
		close(titleDone)
	}()

	select {
	case <-titleDone:
		close(writer.release)
		<-renderDone
		t.Fatal("title write completed while frame write was in progress")
	case <-time.After(50 * time.Millisecond):
	}

	close(writer.release)
	select {
	case <-renderDone:
	case <-time.After(time.Second):
		t.Fatal("render did not finish after releasing writer")
	}
	select {
	case <-titleDone:
	case <-time.After(time.Second):
		t.Fatal("title write did not finish after render completed")
	}
}

func TestRunRendererShowsTargetBranchConfirmation(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	now := time.Now()
	lines := renderDashboard(rendererSnapshot{
		Started: now.Add(-time.Minute),
		Now:     now,
		Color:   false,
		Confirmation: &rendererConfirmation{
			Title:        "Confirm Target Branch",
			TargetBranch: "feat/preview",
			MainBranch:   "main",
			Until:        now.Add(5 * time.Second),
		},
	}, 100, 20)
	frame := stripANSISequences(strings.Join(lines, "\n"))

	for _, want := range []string{"Confirm Target Branch", "Target branch: feat/preview", "Main branch: main", "Continuing in 00:05"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("confirmation frame missing %q:\n%s", want, frame)
		}
	}
	assertFrameBounds(t, lines, 100, 20)
}

func TestRunRendererShowsGitHubSleepMode(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	var out bytes.Buffer
	now := time.Now()
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     now.Add(-2 * time.Minute),
		done:        make(chan struct{}),
		stage:       string(runstate.StageIntegrating),
		iteration:   "0001",
	}

	renderer.SleepWaitingForGitHub()

	if !strings.Contains(out.String(), "waiting for GitHub Issue/PR updates") {
		t.Fatalf("line renderer should print sleep status: %q", out.String())
	}
	frameLines := renderer.frame(100, 20)
	frame := stripANSISequences(strings.Join(frameLines, "\n"))
	for _, want := range []string{"GitHub Sleep Mode", "Waiting for GitHub Issue/PR updates", "Press any key to fetch now"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("sleep frame missing %q:\n%s", want, frame)
		}
	}
	assertFrameBounds(t, frameLines, 100, 20)
}

func TestRunRendererShowsPullRequestReviewWaitMode(t *testing.T) {
	t.Setenv("TERM", "xterm")
	var out bytes.Buffer
	now := time.Now()
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     now.Add(-2 * time.Minute),
		done:        make(chan struct{}),
		stage:       string(runstate.StagePullRequest),
		iteration:   "0001",
	}

	renderer.SleepWaitingForPullRequests([]runstate.PendingPullRequest{{
		PR:     "https://github.com/acme/app/pull/42",
		Title:  "Add review dashboard",
		Branch: "feat/review-dashboard",
		Status: "waiting_for_human",
	}})

	if !strings.Contains(out.String(), "waiting for human PR review") {
		t.Fatalf("line renderer should print PR wait status: %q", out.String())
	}
	frameLines := renderer.frame(110, 22)
	frame := stripANSISequences(strings.Join(frameLines, "\n"))
	for _, want := range []string{"Waiting For PR Review", "Waiting for human PR review", "Review Pending", "#42 Add review dashboard", "Polling every 5m"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("PR wait frame missing %q:\n%s", want, frame)
		}
	}
	assertFrameBounds(t, frameLines, 110, 22)
}

func TestRunRendererShowsPendingPullRequestsDuringWork(t *testing.T) {
	t.Setenv("TERM", "xterm")
	now := time.Now()
	lines := renderDashboard(rendererSnapshot{
		Started: now.Add(-time.Minute),
		Now:     now,
		Stage:   string(runstate.StageCoding),
		Current: "coding tasks",
		Tasks: []taskItem{{
			ID:     "continue-safe-work",
			Status: "active",
			Text:   "Continue non-overlapping implementation",
		}},
		PendingPRs: []rendererPendingPullRequest{{
			PR:     "42",
			Title:  "Add review dashboard",
			Branch: "feat/review-dashboard",
		}},
		LatestMsg: "Implementing a non-overlapping task.",
	}, 120, 24)
	frame := stripANSISequences(strings.Join(lines, "\n"))
	for _, want := range []string{"Review Pending", "#42 Add review dashboard", "Continue non-overlapping implementation"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("work frame missing %q:\n%s", want, frame)
		}
	}
	if !containsAny(frame, []string{"◐", "◓", "◑", "◒"}) {
		t.Fatalf("work frame should show a rotating circle status:\n%s", frame)
	}
	assertFrameBounds(t, lines, 120, 24)
}

func TestRunRendererShowsPullRequestProgressInsteadOfTasks(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	now := time.Now()
	iterDir := t.TempDir()
	if err := writePRState(iterDir, prState{
		SchemaVersion: 1,
		Status:        "created",
		PR:            "https://github.com/acme/app/pull/42",
		Branch:        "feature/render-pr-progress",
		Base:          "develop",
		Title:         "Show PR progress in renderer",
	}); err != nil {
		t.Fatal(err)
	}
	if err := writePRChecks(iterDir, prChecksArtifact{
		SchemaVersion: 1,
		Status:        "failed",
		PR:            "https://github.com/acme/app/pull/42",
		Branch:        "feature/render-pr-progress",
		CheckedAt:     "2026-06-06T10:00:00Z",
		ExitCode:      1,
		Error:         "exit status 1",
		Checks: []pr.CheckStatus{
			{Name: "unit", Workflow: "test", Bucket: "fail", State: "FAILURE", StartedAt: "2026-06-06T09:58:43Z", CompletedAt: "2026-06-06T10:00:00Z", Link: "https://github.com/acme/app/actions/runs/1/job/2"},
			{Name: "lint", Workflow: "quality", Bucket: "pass", State: "SUCCESS", StartedAt: "2026-06-06T09:59:16Z", CompletedAt: "2026-06-06T10:00:00Z"},
			{Name: "deploy-preview", Workflow: "preview", Bucket: "pending", State: "QUEUED"},
		},
		Stdout: "Refreshing checks status every 5 seconds. Press Ctrl+C to quit.\n",
		Stderr: "unit test failed: missing dependency\n",
	}); err != nil {
		t.Fatal(err)
	}

	renderer := &runRenderer{
		started:      now.Add(-time.Minute),
		base:         "develop",
		branch:       "feature/render-pr-progress",
		stage:        string(runstate.StagePullRequest),
		stageDetail:  "merge agent running",
		iterationDir: iterDir,
		latestMsg:    "merge agent running",
		tasks: []taskItem{{
			ID:     "renderer",
			Status: "active",
			Text:   "Old task should not be visible",
			Todos:  []taskTodoDisplay{{Status: "active", Text: "Old TODO should not be visible"}},
		}},
	}

	frameLines := renderer.frame(120, 28)
	frame := stripANSISequences(strings.Join(frameLines, "\n"))
	for _, want := range []string{
		"Pull Request",
		"feature/render-pr-progress -> develop",
		"#42 Show PR progress in renderer",
		"[!] test / unit (1m17s)",
		"[x] quality / lint (44s)",
		"- preview / deploy-preview",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("pull request frame missing %q:\n%s", want, frame)
		}
	}
	for _, hidden := range []string{"Branch:", "PR:", "Title:", "Status:", "Checked:", "exit status 1", "unit test failed"} {
		if strings.Contains(frame, hidden) {
			t.Fatalf("pull request frame should not show label %q:\n%s", hidden, frame)
		}
	}
	for _, hidden := range []string{"Old task should not be visible", "Old TODO should not be visible"} {
		if strings.Contains(frame, hidden) {
			t.Fatalf("pull request frame should hide task content %q:\n%s", hidden, frame)
		}
	}
	assertFrameBounds(t, frameLines, 120, 28)
}

func TestRunRendererPullRequestFrameKeepsSectionSpacing(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	now := time.Now()
	lines := pullRequestBlock(rendererSnapshot{
		Started: now.Add(-time.Minute),
		Now:     now,
		PullRequest: rendererPullRequest{
			Status: "created",
			PR:     "42",
			Branch: "feature/render-pr-progress",
			Base:   "develop",
			Title:  "Show PR progress in renderer",
		},
		PullRequestChecks: rendererPullRequestChecks{
			Checks: []rendererPullRequestCheck{
				{Name: "unit", Workflow: "test", Bucket: "pass", State: "SUCCESS", StartedAt: "2026-06-06T09:58:43Z", CompletedAt: "2026-06-06T10:00:00Z"},
			},
		},
	}, symbolsForEnvironment(), 100, 72, 12)

	plain := make([]string, 0, len(lines))
	for _, line := range lines {
		plain = append(plain, strings.TrimSpace(stripANSISequences(line)))
	}
	want := []string{
		"Pull Request",
		"",
		"feature/render-pr-progress -> develop",
		"",
		"#42 Show PR progress in renderer",
		"",
		"[x] test / unit (1m17s)",
	}
	if strings.Join(plain, "\n") != strings.Join(want, "\n") {
		t.Fatalf("pull request block spacing mismatch:\n%s", strings.Join(plain, "\n"))
	}
}

func TestRunRendererPullRequestFrameStylesIDAndCreatingState(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	now := time.Now()
	lines := renderDashboard(rendererSnapshot{
		Started: now.Add(-time.Minute),
		Now:     now,
		Color:   true,
		Stage:   string(runstate.StagePullRequest),
		PullRequest: rendererPullRequest{
			Status: "created",
			PR:     "42",
			Branch: "feature/render-pr-progress",
			Base:   "develop",
			Title:  "Show PR progress in renderer",
		},
		PullRequestChecks: rendererPullRequestChecks{Status: "pending", Pending: true},
	}, 120, 24)
	frame := strings.Join(lines, "\n")
	if !strings.Contains(frame, ansiDim+"#42"+ansiReset+" Show PR progress in renderer") {
		t.Fatalf("pull request id should be gray and title should remain plain:\n%s", frame)
	}

	creatingLines := renderDashboard(rendererSnapshot{
		Started: now.Add(-time.Minute),
		Now:     now,
		Stage:   string(runstate.StagePullRequest),
		PullRequest: rendererPullRequest{
			Status: "preparing",
			Branch: "feature/render-pr-progress",
			Base:   "develop",
		},
	}, 100, 22)
	creatingFrame := stripANSISequences(strings.Join(creatingLines, "\n"))
	if !strings.Contains(creatingFrame, "Creating pull request...") {
		t.Fatalf("creating PR frame missing progress message:\n%s", creatingFrame)
	}
	if strings.Contains(creatingFrame, "Waiting for check results") {
		t.Fatalf("creating PR frame should not show check-wait copy before the PR exists:\n%s", creatingFrame)
	}
}

func TestRunRendererShowsSleepFetchRequested(t *testing.T) {
	var out bytes.Buffer
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     time.Now(),
		done:        make(chan struct{}),
		stage:       "sleeping",
	}

	renderer.SleepFetchRequested()

	if !strings.Contains(out.String(), "fetching GitHub updates after keypress") {
		t.Fatalf("line renderer should print sleep fetch status: %q", out.String())
	}
}

func TestRunRendererDashboardListsMaximumTasksWithHiddenBelow(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	now := time.Now()
	tasks := []taskItem{
		{Done: true, Status: "done", Text: "Task 1"},
		{Done: true, Status: "done", Text: "Task 2"},
		{Text: "Task 3"},
		{Text: "Task 4"},
		{Status: "active", Text: "Task 5"},
		{Text: "Task 6"},
		{Text: "Task 7"},
		{Text: "Task 8"},
	}
	lines := renderDashboard(rendererSnapshot{
		Started:      now.Add(-time.Minute),
		Now:          now,
		Instruction:  "",
		Tasks:        tasks,
		InputTokens:  100,
		OutputTokens: 50,
		MergeCount:   1,
		LatestMsg:    "Latest agent message.",
	}, 100, 18)
	frame := strings.Join(lines, "\n")

	assertFrameBounds(t, lines, 100, 18)
	for _, want := range []string{"2/8 tasks", "Task 1", "7 hidden below", "Ctrl+C gracefully stops after this iteration"} {
		if !strings.Contains(stripANSISequences(frame), want) {
			t.Fatalf("frame missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "Task 2") || strings.Contains(frame, "Task 8") {
		t.Fatalf("hidden tasks should not be rendered when hidden-below row is needed:\n%s", frame)
	}
}

func TestRunRendererShowsGracefulShutdownInstructions(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	now := time.Now()
	lines := renderDashboard(rendererSnapshot{
		Started:          now.Add(-time.Minute),
		Now:              now,
		LatestMsg:        "Working through the current task.",
		GracefulShutdown: true,
	}, 110, 20)
	frame := stripANSISequences(strings.Join(lines, "\n"))
	for _, want := range []string{"Working through the current task.", "Finishing current iteration before exit", "Press Ctrl+C again to exit immediately"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame missing %q:\n%s", want, frame)
		}
	}
}

func TestRunRendererShowsRoleTasks(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	var out bytes.Buffer
	now := time.Now()
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     now,
		done:        make(chan struct{}),
		stage:       string(runstate.StageCoding),
		iteration:   "0001",
	}

	first := workflow.Task{ID: "api", Title: "Add API route"}
	second := workflow.Task{ID: "tests", Title: "Add request tests"}
	renderer.TasksPlanned([]workflow.Task{first, second})
	renderer.TaskStarted(first)
	renderer.TaskCompleted(first)
	renderer.TaskStarted(second)

	if got := out.String(); !strings.Contains(got, "2 planned") || !strings.Contains(got, "started Add request tests") {
		t.Fatalf("line renderer missing task updates:\n%s", got)
	}
	frameLines := renderer.frame(100, 22)
	frame := stripANSISequences(strings.Join(frameLines, "\n"))
	for _, want := range []string{"1/2 tasks", "Add API route", "Add request tests"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("task frame missing %q:\n%s", want, frame)
		}
	}
	assertFrameBounds(t, frameLines, 100, 22)
}

func TestRunRendererShowsActiveTaskTodosIndented(t *testing.T) {
	t.Setenv("LOOP_ASCII", "")
	t.Setenv("TERM", "xterm-256color")
	now := time.Now()
	lines := renderDashboard(rendererSnapshot{
		Started: now.Add(-time.Minute),
		Now:     now,
		Tasks: []taskItem{{
			ID:     "api",
			Status: "active",
			Text:   "Add API route",
			Todos: []taskTodoDisplay{
				{Status: "active", Text: "F: add parser support"},
				{Status: "pending", Text: "T: cover invalid input"},
				{Status: "done", Text: "D: update workflow docs"},
			},
		}},
		InputTokens:  100,
		OutputTokens: 50,
		LatestMsg:    "Working task TODOs.",
	}, 100, 24)
	frame := stripANSISequences(strings.Join(lines, "\n"))
	for _, want := range []string{"Add API route", "◐ F: add parser support", "◦ T: cover invalid input", "✓ D: update workflow docs"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("task TODO frame missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "▶ F: add parser support") {
		t.Fatalf("task TODO frame should use spinner instead of triangle:\n%s", frame)
	}
	assertFrameBounds(t, lines, 100, 24)
}

func TestRunRendererShowsTodosForParallelActiveTasks(t *testing.T) {
	t.Setenv("LOOP_ASCII", "")
	t.Setenv("TERM", "xterm-256color")
	now := time.Now()
	root := t.TempDir()
	apiDir := filepath.Join(root, "api")
	invoiceDir := filepath.Join(root, "invoice")
	writeTaskTodosForRendererTest(t, apiDir, "api", []taskTodoItem{
		{Status: "done", Type: "F", Title: "Scope department users", Acceptance: []string{"Department users are scoped."}, CommitMessage: "secure department users API scope", CommitSHA: "abc123", CommitSubject: "F: secure department users API scope"},
		{Status: "active", Type: "F", Title: "Restore vendor filter", Acceptance: []string{"Vendor filters restore scoped IDs."}, CommitMessage: "scope vendor filter id restoration"},
	})
	writeTaskTodosForRendererTest(t, invoiceDir, "invoice", []taskTodoItem{
		{Status: "active", Type: "F", Title: "Secure upload route", Acceptance: []string{"Uploads are tenant scoped."}, CommitMessage: "secure vendor invoice upload route"},
		{Status: "pending", Type: "T", Title: "Cover invoice permissions", Acceptance: []string{"Permission tests cover invoices."}, CommitMessage: "cover vendor invoice permissions"},
	})
	renderer := &runRenderer{
		started: now.Add(-time.Minute),
		tasks: []taskItem{
			{ID: "api", Status: "active", Text: "Secure department users and vendor filter APIs", TaskDir: apiDir},
			{ID: "invoice", Status: "active", Text: "Secure vendor invoice upload and download routes", TaskDir: invoiceDir},
			{ID: "audit", Status: "pending", Text: "Audit remaining scope gaps and run quality gates"},
		},
		inputTokens:  100,
		outputTokens: 50,
		latestMsg:    "Working parallel task TODOs.",
	}

	frameLines := renderer.frame(120, 28)
	frame := stripANSISequences(strings.Join(frameLines, "\n"))
	for _, want := range []string{
		"Secure department users and vendor filter APIs",
		"F: secure department users API scope",
		"F: scope vendor filter id restoration",
		"Secure vendor invoice upload and download routes",
		"F: secure vendor invoice upload route",
		"T: cover vendor invoice permissions",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("parallel task TODO frame missing %q:\n%s", want, frame)
		}
	}
	assertFrameBounds(t, frameLines, 120, 28)
}

func TestRunRendererPlannerShowsCompactStatusAndTruncatedCommand(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	var out bytes.Buffer
	now := time.Now()
	renderer := &runRenderer{
		enabled:     true,
		interactive: false,
		writer:      &out,
		started:     now,
		done:        make(chan struct{}),
		stage:       string(runstate.StagePlanning),
		stageDetail: "planner agent running",
		latestMsg:   "The loop CLI in this checkout does not expose loop memory recent, and loop issue access is unavailable. " + strings.Repeat("message ", 16),
	}

	renderer.AgentEvent(runstate.Event{
		"type":    "agent.command",
		"command": "/bin/zsh",
		"args":    []string{"-lc", "sed -n '1,240p' apps/web/app/layout.tsx && sed -n '1,240p' apps/web/app/page.tsx && echo super-long-tail-marker " + strings.Repeat("command ", 16)},
	})

	frameLines := renderer.frame(90, 24)
	frame := stripANSISequences(strings.Join(frameLines, "\n"))
	var latestLine, commandLine string
	for _, line := range frameLines {
		line = strings.TrimSpace(stripANSISequences(line))
		if strings.Contains(line, "The loop CLI") {
			latestLine = line
		}
		if strings.Contains(line, "running command:") {
			commandLine = line
		}
	}
	for _, want := range []string{"- planning...", "running command: /bin/zsh -lc", "The loop CLI"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("planner frame missing %q:\n%s", want, frame)
		}
	}
	if commandLine == "" || latestLine == "" {
		t.Fatalf("planner frame should include latest message and running command:\n%s", frame)
	}
	if displayWidth(commandLine) >= displayWidth(latestLine) {
		t.Fatalf("running command should use a shorter ellipsis width than latest message:\nlatest=%q\ncommand=%q", latestLine, commandLine)
	}
	if strings.Contains(frame, "super-long-tail-marker") {
		t.Fatalf("running command should be truncated to the content width:\n%s", frame)
	}
	assertFrameBounds(t, frameLines, 90, 24)
}

func TestRunRendererDashboardWrapsWideLatestMessageWithinContentWidth(t *testing.T) {
	t.Setenv("LOOP_ASCII", "1")
	now := time.Now()
	cjkWord := string([]rune{0x65e5, 0x672c, 0x8a9e})
	message := strings.Repeat(cjkWord, 20)

	lines := renderDashboard(rendererSnapshot{
		Started:   now.Add(-time.Minute),
		Now:       now,
		LatestMsg: message,
	}, 100, 24)

	assertFrameBounds(t, lines, 100, 24)
	latestRows := 0
	for _, line := range lines {
		plain := strings.TrimSpace(stripANSISequences(line))
		if strings.Contains(plain, cjkWord) {
			latestRows++
			if displayWidth(plain) > 84 {
				t.Fatalf("latest message row width = %d, want <= 84: %q", displayWidth(plain), plain)
			}
		}
	}
	if latestRows < 2 {
		t.Fatalf("wide latest message should wrap inside the dashboard content width:\n%s", strings.Join(lines, "\n"))
	}
}

func TestDisplayWidthCountsCJKRunesAsWide(t *testing.T) {
	cjkWord := string([]rune{0x65e5, 0x672c, 0x8a9e})
	if got, want := displayWidth("a"+cjkWord+"b"), 8; got != want {
		t.Fatalf("displayWidth() = %d, want %d", got, want)
	}
	truncated := truncateVisible(cjkWord+cjkWord, 5, true)
	if width := displayWidth(truncated); width > 5 {
		t.Fatalf("truncateVisible() width = %d, want <= 5: %q", width, truncated)
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

func containsAny(text string, wants []string) bool {
	for _, want := range wants {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

func writeTaskTodosForRendererTest(t *testing.T, taskDir, taskID string, items []taskTodoItem) {
	t.Helper()
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(taskTodoFile{SchemaVersion: 1, TaskID: taskID, Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskTodoPath(taskDir), data, 0o644); err != nil {
		t.Fatal(err)
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

type blockingRendererWriter struct {
	mu      sync.Mutex
	once    sync.Once
	writes  []string
	needle  string
	blocked chan struct{}
	release chan struct{}
}

func newBlockingRendererWriter(needle string) *blockingRendererWriter {
	return &blockingRendererWriter{
		needle:  needle,
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *blockingRendererWriter) Write(p []byte) (int, error) {
	text := string(p)
	if strings.Contains(text, w.needle) {
		w.once.Do(func() {
			close(w.blocked)
			<-w.release
		})
	}
	w.mu.Lock()
	w.writes = append(w.writes, text)
	w.mu.Unlock()
	return len(p), nil
}
