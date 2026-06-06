package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/pr"
	"github.com/aki-0421/loop/internal/runstate"
	"github.com/aki-0421/loop/internal/workflow"
)

type runRenderer struct {
	enabled     bool
	interactive bool
	writer      io.Writer
	started     time.Time
	runID       string
	agent       string
	repo        string
	instruction string
	base        string
	branch      string
	goal        string
	logs        string
	maxIter     int
	color       bool

	mu                    sync.Mutex
	stage                 string
	stageDetail           string
	iteration             string
	iterationDir          string
	agentCommand          string
	agentExit             string
	current               string
	runningCommand        string
	tasks                 []taskItem
	activity              []string
	activityLog           []rendererLogLine
	events                []rendererEvent
	commitCount           int
	mergeCount            int
	messageCount          int
	inputTokens           int
	outputTokens          int
	tokensEstimated       bool
	usageBaseInputTokens  int
	usageBaseOutputTokens int
	latestMsg             string
	confirmation          *rendererConfirmation
	pendingPullRequests   []rendererPendingPullRequest
	sleeping              bool
	sleepSince            time.Time
	sleepTitle            string
	sleepStatus           string
	sleepDetail           string
	sleepFetchRequested   chan struct{}
	gracefulShutdown      bool
	prMode                bool
	prReviewMode          string
	prReviewModeLocked    bool
	done                  chan struct{}
	ticker                *time.Ticker
	inputCancel           context.CancelFunc
	inputDone             chan struct{}
	inputRunning          bool
	titleEnabled          bool
	drawMu                sync.Mutex
	lastFrame             []string
	lastFrameWidth        int
	lastFrameHeight       int
	lastTitle             string
}

type rendererConfirmation struct {
	Title        string
	TargetBranch string
	MainBranch   string
	Until        time.Time
}

type rendererPendingPullRequest struct {
	PR     string
	Title  string
	Branch string
}

type taskItem struct {
	ID      string
	Done    bool
	Status  string
	Text    string
	TaskDir string
	Todos   []taskTodoDisplay
}

type taskTodoDisplay struct {
	Status string
	Text   string
}

type rendererLogLine struct {
	At   time.Time
	Text string
}

type rendererEvent struct {
	At     time.Time
	Status string
	Title  string
	Detail string
}

func newRunRenderer(g globals, runID, agentName, repoName, instructionFile, baseBranch, goal, logs string, maxIterations int) *runRenderer {
	interactive := terminalControlSupported()
	return &runRenderer{
		enabled:             !g.JSON,
		interactive:         interactive,
		writer:              os.Stderr,
		started:             time.Now(),
		runID:               runID,
		agent:               agentName,
		repo:                repoName,
		instruction:         instructionFile,
		base:                baseBranch,
		goal:                goal,
		logs:                logs,
		maxIter:             maxIterations,
		color:               interactive && !g.NoColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		sleepFetchRequested: make(chan struct{}, 1),
		done:                make(chan struct{}),
		titleEnabled:        interactive,
	}
}

func (r *runRenderer) Start(ctx context.Context) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	r.stage = string(runstate.StageCreated)
	r.stageDetail = "created"
	r.mu.Unlock()
	if r.interactive {
		fmt.Fprint(r.writer, "\x1b[?1049h\x1b[?25l\x1b[?7l")
		r.render()
	} else {
		r.line("run", fmt.Sprintf("%s agent=%s base=%s logs=%s", r.runID, r.agent, r.base, r.logs))
	}
	r.setTitle()
	r.ticker = time.NewTicker(rendererTickInterval)
	r.startInput(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				r.setCurrent("cancelling")
				r.render()
				return
			case <-r.done:
				return
			case <-r.ticker.C:
				r.render()
			}
		}
	}()
}

func (r *runRenderer) Stop(status runstate.Stage, summary string) {
	if !r.enabled {
		return
	}
	if r.ticker != nil {
		r.ticker.Stop()
	}
	r.stopInput()
	select {
	case <-r.done:
	default:
		close(r.done)
	}
	r.Stage(status, summary)
	if r.interactive {
		r.render()
		fmt.Fprint(r.writer, "\x1b[?7h\x1b[?25h\x1b[?1049l")
	}
	r.clearTitle()
}

func (r *runRenderer) EnablePRReviewMode(mode string) {
	if !r.enabled {
		return
	}
	mode = normalizeRendererReviewMode(mode)
	r.mu.Lock()
	r.prMode = mode != ""
	r.prReviewMode = mode
	r.mu.Unlock()
}

func (r *runRenderer) CurrentPRReviewMode() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prReviewMode
}

func (r *runRenderer) LockPRReviewMode(locked bool) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	r.prReviewModeLocked = locked
	r.mu.Unlock()
	r.render()
}

func (r *runRenderer) LockCurrentPRReviewMode() string {
	if r == nil {
		return ""
	}
	if !r.enabled {
		return r.CurrentPRReviewMode()
	}
	r.mu.Lock()
	r.prReviewModeLocked = true
	mode := r.prReviewMode
	r.mu.Unlock()
	r.render()
	return mode
}

func (r *runRenderer) cyclePRReviewMode() {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	if !r.prMode || r.prReviewMode == "" {
		r.mu.Unlock()
		return
	}
	if r.prReviewModeLocked {
		r.addEventLocked(rendererEvent{At: time.Now(), Status: "blocked", Title: "Review Mode Locked", Detail: rendererReviewModeLabel(r.prReviewMode)})
		r.mu.Unlock()
		r.render()
		return
	}
	r.prReviewMode = nextRendererReviewMode(r.prReviewMode)
	label := rendererReviewModeLabel(r.prReviewMode)
	r.addEventLocked(rendererEvent{At: time.Now(), Status: "active", Title: "Review Mode", Detail: label})
	r.mu.Unlock()
	r.render()
}

func (r *runRenderer) startInput(ctx context.Context) {
	r.mu.Lock()
	prMode := r.prMode
	r.mu.Unlock()
	if !prMode {
		return
	}
	if !rendererInputSupported(r) {
		return
	}
	inputCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.inputCancel = cancel
	r.inputDone = done
	r.mu.Lock()
	r.inputRunning = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.inputRunning = false
			r.mu.Unlock()
			close(done)
		}()
		_ = watchRendererInput(inputCtx, r)
	}()
}

func (r *runRenderer) stopInput() {
	if r.inputCancel == nil {
		return
	}
	r.inputCancel()
	if r.inputDone != nil {
		select {
		case <-r.inputDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	r.inputCancel = nil
	r.inputDone = nil
	r.mu.Lock()
	r.inputRunning = false
	r.mu.Unlock()
}

func (r *runRenderer) inputActive() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inputRunning
}

func (r *runRenderer) handleInputByte(ctx context.Context, b byte) {
	switch b {
	case 0x03:
		if state := runInterruptFromContext(ctx); state != nil {
			if state.GracefulRequested() {
				state.Force()
			} else {
				state.RequestGraceful()
			}
		}
	case 'r', 'R':
		r.cyclePRReviewMode()
	default:
		r.requestSleepFetchIfSleeping()
	}
}

func (r *runRenderer) requestSleepFetchIfSleeping() {
	if r == nil {
		return
	}
	r.mu.Lock()
	sleeping := r.sleeping
	r.mu.Unlock()
	if !sleeping {
		return
	}
	select {
	case r.sleepFetchRequested <- struct{}{}:
	default:
	}
}

func (r *runRenderer) waitForSleepFetch(ctx context.Context, d time.Duration) (bool, error) {
	if r == nil || !r.inputActive() {
		return false, errRendererInputUnavailable
	}
	if d <= 0 {
		return false, nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		if state := runInterruptFromContext(ctx); state != nil && !state.GracefulRequested() {
			state.RequestGraceful()
		}
		return false, ctx.Err()
	case <-timer.C:
		return false, nil
	case <-r.sleepFetchRequested:
		return true, nil
	}
}

func (r *runRenderer) Iteration(iterationID string) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	r.iteration = iterationID
	r.tasks = nil
	r.clearSleepLocked()
	if r.branch == "" || strings.HasPrefix(r.branch, "wip/") {
		r.branch = "wip/" + iterationID
	}
	r.mu.Unlock()
	r.render()
}

func (r *runRenderer) IterationDirectory(iterationDir string) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	r.iterationDir = strings.TrimSpace(iterationDir)
	r.mu.Unlock()
}

func (r *runRenderer) TasksPlanned(tasks []workflow.Task) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	added := 0
	for _, task := range tasks {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			continue
		}
		if r.taskIndexLocked(id) >= 0 {
			continue
		}
		r.tasks = append(r.tasks, taskItem{ID: id, Status: "pending", Text: taskDisplayTitle(task)})
		added++
	}
	total := len(r.tasks)
	if added > 0 {
		r.addEventLocked(rendererEvent{At: time.Now(), Status: "active", Title: "Tasks Planned", Detail: fmt.Sprintf("%d total", total)})
	}
	r.mu.Unlock()
	if added > 0 {
		if !r.interactive {
			r.line("tasks", fmt.Sprintf("%d planned", total))
		}
		r.render()
	}
}

func (r *runRenderer) TaskStarted(task workflow.Task) {
	if !r.enabled {
		return
	}
	id := strings.TrimSpace(task.ID)
	title := taskDisplayTitle(task)
	r.mu.Lock()
	r.upsertTaskLocked(id, title, "active", false)
	r.current = "running task: " + title
	r.addEventLocked(rendererEvent{At: time.Now(), Status: "active", Title: "Task Started", Detail: title})
	r.mu.Unlock()
	if !r.interactive {
		r.line("task", "started "+title)
	}
	r.render()
}

func (r *runRenderer) TaskDirectory(task workflow.Task, taskDir string) {
	if !r.enabled {
		return
	}
	id := strings.TrimSpace(task.ID)
	if id == "" || strings.TrimSpace(taskDir) == "" {
		return
	}
	r.mu.Lock()
	if idx := r.taskIndexLocked(id); idx >= 0 {
		r.tasks[idx].TaskDir = taskDir
	} else {
		r.tasks = append(r.tasks, taskItem{ID: id, Status: "pending", Text: taskDisplayTitle(task), TaskDir: taskDir})
	}
	r.mu.Unlock()
}

func (r *runRenderer) TaskCompleted(task workflow.Task) {
	if !r.enabled {
		return
	}
	id := strings.TrimSpace(task.ID)
	title := taskDisplayTitle(task)
	r.mu.Lock()
	r.upsertTaskLocked(id, title, "done", true)
	r.current = "completed task: " + title
	r.addEventLocked(rendererEvent{At: time.Now(), Status: "done", Title: "Task Completed", Detail: title})
	r.mu.Unlock()
	if !r.interactive {
		r.line("task", "completed "+title)
	}
	r.render()
}

func (r *runRenderer) TaskFailed(task workflow.Task, err error) {
	if !r.enabled {
		return
	}
	id := strings.TrimSpace(task.ID)
	title := taskDisplayTitle(task)
	detail := title
	if err != nil {
		detail = title + ": " + err.Error()
	}
	r.mu.Lock()
	r.upsertTaskLocked(id, title, "blocked", false)
	r.current = "failed task: " + title
	r.addEventLocked(rendererEvent{At: time.Now(), Status: "blocked", Title: "Task Failed", Detail: detail})
	r.mu.Unlock()
	if !r.interactive {
		r.line("task", "failed "+detail)
	}
	r.render()
}

func (r *runRenderer) Stage(stage runstate.Stage, detail string) {
	if !r.enabled {
		return
	}
	if detail == "" {
		detail = strings.ReplaceAll(string(stage), "_", " ")
	}
	r.mu.Lock()
	r.clearSleepLocked()
	r.stage = string(stage)
	r.stageDetail = detail
	r.current = detail
	r.addEventLocked(rendererEvent{
		At:     time.Now(),
		Status: eventStatusForStage(string(stage)),
		Title:  phaseLabel(string(stage)),
		Detail: detail,
	})
	r.mu.Unlock()
	if r.interactive {
		r.render()
	} else {
		r.line("stage", fmt.Sprintf("%s %s", stage, detail))
	}
	r.setTitle()
}

func (r *runRenderer) GracefulShutdownRequested() {
	if !r.enabled {
		return
	}
	detail := "finishing current iteration before exit"
	r.mu.Lock()
	r.gracefulShutdown = true
	r.current = detail
	r.addEventLocked(rendererEvent{
		At:     time.Now(),
		Status: "active",
		Title:  "Graceful Shutdown",
		Detail: detail,
	})
	r.mu.Unlock()
	if !r.interactive {
		r.line("shutdown", detail+"; press Ctrl+C again to exit immediately")
	}
	r.render()
}

func (r *runRenderer) AgentEvent(event runstate.Event) {
	if !r.enabled || event == nil {
		return
	}
	r.mu.Lock()
	r.messageCount++
	r.mu.Unlock()
	typ, _ := event["type"].(string)
	switch typ {
	case "agent.started":
		r.startAgentUsageWindow()
		cmd, _ := event["command"].(string)
		args := stringifyEventValue(event["args"])
		r.setAgentCommand(strings.TrimSpace(cmd + " " + args))
		r.addEvent("active", "Agent Started", "adapter process launched")
	case "agent.usage":
		if r.applyTokenUsage(event) {
			r.addEvent("active", "Usage", r.tokenUsageLine())
			if !r.interactive {
				r.line("usage", r.tokenUsageLine())
			}
		}
	case "agent.stream", "agent.message":
		text, _ := event["text"].(string)
		if text != "" {
			r.setLatestMessage(text)
			r.addActivity(text)
			if typ == "agent.message" && !r.interactive {
				r.line("message", text)
			}
		}
	case "agent.command":
		cmd, _ := event["command"].(string)
		args := stringifyEventValue(event["args"])
		text := strings.TrimSpace(cmd + " " + args)
		if text != "" {
			r.setRunningCommand("running command: " + text)
			r.addActivity("command: " + text)
			r.addEvent("active", "Command", text)
			if !r.interactive {
				r.line("command", text)
			}
		}
	case "agent.file_read":
		path, _ := event["path"].(string)
		if path != "" {
			r.setRunningCommand("reading file: " + path)
			r.addActivity("read: " + path)
			r.addEvent("active", "Read", path)
			if !r.interactive {
				r.line("read", path)
			}
		}
	case "agent.exited":
		r.setAgentExit(fmt.Sprintf("exit=%v", event["exit_code"]))
		r.addEvent("done", "Agent Exited", fmt.Sprintf("exit=%v", event["exit_code"]))
	case "agent.rate_limit_wait":
		waitMs, _ := intEventField(event, "wait_ms")
		resetAt, _ := event["reset_at"].(string)
		detail := "sleeping; waiting before retrying agent after rate limit"
		if waitMs > 0 {
			detail = fmt.Sprintf("sleeping; waiting %s before retrying agent after rate limit", formatDuration(time.Duration(waitMs)*time.Millisecond))
		}
		if strings.TrimSpace(resetAt) != "" {
			detail = "sleeping; waiting for agent rate limit reset"
			if waitMs > 0 {
				detail = fmt.Sprintf("sleeping; waiting %s for agent rate limit reset", formatDuration(time.Duration(waitMs)*time.Millisecond))
			}
			detail += " at " + strings.TrimSpace(resetAt)
		}
		r.sleepWaitingForAgentRateLimit(detail)
	case "run.cancelled_cleanup.started", "run.cancelled_cleanup.completed":
		r.setCurrent(typ)
		r.addActivity(typ)
		r.addEvent("blocked", "Cleanup", typ)
		if !r.interactive {
			r.line("cleanup", typ)
		}
	}
	r.render()
}

func (r *runRenderer) Branch(branch string) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	r.branch = branch
	r.mu.Unlock()
	r.render()
}

func (r *runRenderer) ConfirmTargetBranch(ctx context.Context, targetBranch, mainBranch string, delay time.Duration) error {
	if !r.enabled {
		return nil
	}
	targetBranch = strings.TrimSpace(targetBranch)
	mainBranch = strings.TrimSpace(mainBranch)
	until := time.Now().Add(delay)
	r.mu.Lock()
	r.current = "confirming target branch"
	r.confirmation = &rendererConfirmation{
		Title:        "Confirm Target Branch",
		TargetBranch: targetBranch,
		MainBranch:   mainBranch,
		Until:        until,
	}
	r.addEventLocked(rendererEvent{
		At:     time.Now(),
		Status: "blocked",
		Title:  "Confirm Target Branch",
		Detail: fmt.Sprintf("%s is not %s", targetBranch, mainBranch),
	})
	r.mu.Unlock()
	if r.interactive {
		r.render()
	} else {
		r.line("confirm", fmt.Sprintf("target branch %s is not main branch %s; continuing in %s", targetBranch, mainBranch, formatDuration(delay)))
	}
	err := targetBranchConfirmationSleep(ctx, delay)
	r.mu.Lock()
	r.confirmation = nil
	r.current = ""
	r.mu.Unlock()
	r.render()
	return err
}

func (r *runRenderer) Commits(count int) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	r.commitCount = count
	r.mu.Unlock()
	r.render()
}

func (r *runRenderer) Merged(count int) {
	if !r.enabled {
		return
	}
	r.mu.Lock()
	r.mergeCount = count
	r.mu.Unlock()
	r.render()
}

func (r *runRenderer) MemorySync(repo string, initial bool) {
	if !r.enabled {
		return
	}
	detail := "checking GitHub memory"
	if initial {
		detail = "fetching initial GitHub memory"
	}
	if strings.TrimSpace(repo) != "" {
		detail += " for " + strings.TrimSpace(repo)
	}
	r.mu.Lock()
	r.stageDetail = detail
	r.current = detail
	if len(r.activity) == 0 || r.activity[len(r.activity)-1] != detail {
		r.activity = append(r.activity, detail)
		r.activityLog = append(r.activityLog, rendererLogLine{At: time.Now(), Text: detail})
		if len(r.activity) > 20 {
			r.activity = append([]string(nil), r.activity[len(r.activity)-20:]...)
		}
		if len(r.activityLog) > 20 {
			r.activityLog = append([]rendererLogLine(nil), r.activityLog[len(r.activityLog)-20:]...)
		}
	}
	r.addEventLocked(rendererEvent{
		At:     time.Now(),
		Status: "active",
		Title:  "Memory Sync",
		Detail: detail,
	})
	r.mu.Unlock()
	if r.interactive {
		r.render()
	} else {
		r.line("memory", detail)
	}
	r.setTitle()
}

func (r *runRenderer) SleepWaitingForGitHub() {
	if !r.enabled {
		return
	}
	detail := "sleeping; waiting for GitHub Issue/PR updates"
	r.mu.Lock()
	now := time.Now()
	r.sleeping = true
	if r.sleepSince.IsZero() {
		r.sleepSince = now
	}
	r.sleepTitle = "GitHub Sleep Mode"
	r.sleepStatus = "Waiting for GitHub Issue/PR updates"
	r.sleepDetail = detail
	r.stage = "sleeping"
	r.stageDetail = detail
	r.current = detail
	if len(r.activity) == 0 || r.activity[len(r.activity)-1] != detail {
		r.activity = append(r.activity, detail)
		r.activityLog = append(r.activityLog, rendererLogLine{At: time.Now(), Text: detail})
		if len(r.activity) > 20 {
			r.activity = append([]string(nil), r.activity[len(r.activity)-20:]...)
		}
		if len(r.activityLog) > 20 {
			r.activityLog = append([]rendererLogLine(nil), r.activityLog[len(r.activityLog)-20:]...)
		}
	}
	r.addEventLocked(rendererEvent{
		At:     now,
		Status: "active",
		Title:  "Sleep",
		Detail: detail,
	})
	r.mu.Unlock()
	if r.interactive {
		r.render()
	} else {
		r.line("sleep", detail)
	}
	r.setTitle()
}

func (r *runRenderer) SleepWaitingForPullRequests(pending []runstate.PendingPullRequest) {
	if !r.enabled {
		return
	}
	items := rendererPendingPullRequestsFromState(pending)
	detail := "sleeping; waiting for human PR review"
	if len(items) > 0 {
		detail = "sleeping; waiting for human PR review: " + rendererPendingPullRequestSummary(items)
	}
	r.mu.Lock()
	now := time.Now()
	r.sleeping = true
	if r.sleepSince.IsZero() {
		r.sleepSince = now
	}
	r.sleepTitle = "Waiting For PR Review"
	r.sleepStatus = "Waiting for human PR review"
	r.sleepDetail = detail
	r.pendingPullRequests = items
	r.stage = "sleeping"
	r.stageDetail = detail
	r.current = detail
	if len(r.activity) == 0 || r.activity[len(r.activity)-1] != detail {
		r.activity = append(r.activity, detail)
		r.activityLog = append(r.activityLog, rendererLogLine{At: time.Now(), Text: detail})
		if len(r.activity) > 20 {
			r.activity = append([]string(nil), r.activity[len(r.activity)-20:]...)
		}
		if len(r.activityLog) > 20 {
			r.activityLog = append([]rendererLogLine(nil), r.activityLog[len(r.activityLog)-20:]...)
		}
	}
	r.addEventLocked(rendererEvent{
		At:     now,
		Status: "active",
		Title:  "PR Review Wait",
		Detail: detail,
	})
	r.mu.Unlock()
	if r.interactive {
		r.render()
	} else {
		r.line("sleep", detail)
	}
	r.setTitle()
}

func (r *runRenderer) sleepWaitingForAgentRateLimit(detail string) {
	if !r.enabled {
		return
	}
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "sleeping; waiting before retrying agent after rate limit"
	}
	r.mu.Lock()
	now := time.Now()
	r.sleeping = true
	if r.sleepSince.IsZero() {
		r.sleepSince = now
	}
	r.sleepTitle = "Agent Rate Limit"
	r.sleepStatus = "Waiting for reset"
	r.sleepDetail = detail
	r.stage = "sleeping"
	r.stageDetail = detail
	r.current = detail
	if len(r.activity) == 0 || r.activity[len(r.activity)-1] != detail {
		r.activity = append(r.activity, detail)
		r.activityLog = append(r.activityLog, rendererLogLine{At: time.Now(), Text: detail})
		if len(r.activity) > 20 {
			r.activity = append([]string(nil), r.activity[len(r.activity)-20:]...)
		}
		if len(r.activityLog) > 20 {
			r.activityLog = append([]rendererLogLine(nil), r.activityLog[len(r.activityLog)-20:]...)
		}
	}
	r.addEventLocked(rendererEvent{
		At:     now,
		Status: "active",
		Title:  "Rate Limit Wait",
		Detail: detail,
	})
	r.mu.Unlock()
	if r.interactive {
		r.render()
	} else {
		r.line("sleep", detail)
	}
	r.setTitle()
}

func (r *runRenderer) PendingPullRequests(pending []runstate.PendingPullRequest) {
	if !r.enabled {
		return
	}
	items := rendererPendingPullRequestsFromState(pending)
	r.mu.Lock()
	r.pendingPullRequests = items
	r.mu.Unlock()
	if !r.interactive && len(items) > 0 {
		r.line("pending-pr", rendererPendingPullRequestSummary(items))
	}
	r.render()
}

func (r *runRenderer) SleepFetchRequested() {
	if !r.enabled {
		return
	}
	detail := "fetching GitHub updates after keypress"
	r.mu.Lock()
	r.sleepDetail = detail
	r.stageDetail = detail
	r.current = detail
	r.addEventLocked(rendererEvent{
		At:     time.Now(),
		Status: "active",
		Title:  "Sleep Fetch",
		Detail: detail,
	})
	r.mu.Unlock()
	if r.interactive {
		r.render()
	} else {
		r.line("sleep", detail)
	}
	r.setTitle()
}

func (r *runRenderer) clearSleepLocked() {
	r.sleeping = false
	r.sleepSince = time.Time{}
	r.sleepTitle = ""
	r.sleepStatus = ""
	r.sleepDetail = ""
}

func (r *runRenderer) startAgentUsageWindow() {
	r.mu.Lock()
	r.usageBaseInputTokens = r.inputTokens
	r.usageBaseOutputTokens = r.outputTokens
	r.mu.Unlock()
}

func (r *runRenderer) applyTokenUsage(event runstate.Event) bool {
	input, hasInput := intEventField(event, "input_tokens")
	output, hasOutput := intEventField(event, "output_tokens")
	if !hasInput && !hasOutput {
		return false
	}
	delta, _ := event["delta"].(bool)
	estimated, _ := event["estimated"].(bool)
	r.mu.Lock()
	beforeInput := r.inputTokens
	beforeOutput := r.outputTokens
	beforeEstimated := r.tokensEstimated
	if delta {
		r.inputTokens += maxInt(0, input)
		r.outputTokens += maxInt(0, output)
	} else {
		if hasInput {
			next := r.usageBaseInputTokens + maxInt(0, input)
			if next > r.inputTokens {
				r.inputTokens = next
			}
		}
		if hasOutput {
			next := r.usageBaseOutputTokens + maxInt(0, output)
			if next > r.outputTokens {
				r.outputTokens = next
			}
		}
	}
	if estimated {
		r.tokensEstimated = true
	}
	changed := r.inputTokens != beforeInput || r.outputTokens != beforeOutput || r.tokensEstimated != beforeEstimated
	r.mu.Unlock()
	return changed
}

func (r *runRenderer) tokenUsageLine() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("%s in, %s out", formatTokenCount(r.inputTokens, r.tokensEstimated), formatTokenCount(r.outputTokens, r.tokensEstimated))
}

func (r *runRenderer) setAgentCommand(command string) {
	r.mu.Lock()
	r.agentCommand = command
	r.current = "agent started"
	r.mu.Unlock()
	if !r.interactive {
		r.line("agent", command)
	}
}

func (r *runRenderer) setAgentExit(text string) {
	r.mu.Lock()
	r.agentExit = text
	r.current = "agent " + text
	r.mu.Unlock()
	if !r.interactive {
		r.line("agent", text)
	}
}

func (r *runRenderer) setCurrent(text string) {
	r.mu.Lock()
	r.current = text
	r.mu.Unlock()
}

func (r *runRenderer) setRunningCommand(text string) {
	r.mu.Lock()
	r.runningCommand = text
	r.mu.Unlock()
}

func (r *runRenderer) setLatestMessage(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.mu.Lock()
	r.latestMsg = text
	r.mu.Unlock()
}

func (r *runRenderer) addActivity(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.activity) > 0 && r.activity[len(r.activity)-1] == text {
		return
	}
	r.activity = append(r.activity, text)
	r.activityLog = append(r.activityLog, rendererLogLine{At: time.Now(), Text: text})
	if len(r.activity) > 20 {
		r.activity = append([]string(nil), r.activity[len(r.activity)-20:]...)
	}
	if len(r.activityLog) > 20 {
		r.activityLog = append([]rendererLogLine(nil), r.activityLog[len(r.activityLog)-20:]...)
	}
}

func (r *runRenderer) addEvent(status, title, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addEventLocked(rendererEvent{At: time.Now(), Status: status, Title: title, Detail: detail})
}

func (r *runRenderer) addEventLocked(event rendererEvent) {
	if strings.TrimSpace(event.Title) == "" {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	r.events = append(r.events, event)
	if len(r.events) > 12 {
		r.events = append([]rendererEvent(nil), r.events[len(r.events)-12:]...)
	}
}

func (r *runRenderer) render() {
	if !r.enabled || !r.interactive {
		return
	}
	r.drawMu.Lock()
	defer r.drawMu.Unlock()
	width, height := terminalSize()
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	lines := r.frame(width, height)
	nextFrame := make([]string, len(lines))
	fullRender := len(r.lastFrame) != len(lines) || r.lastFrameWidth != width || r.lastFrameHeight != height
	for i, line := range lines {
		nextFrame[i] = trimRendererLineRight(fitLine(line, width))
		if !fullRender && r.lastFrame[i] == nextFrame[i] {
			continue
		}
		fmt.Fprintf(r.writer, "\x1b[%d;1H%s\x1b[K", i+1, nextFrame[i])
	}
	r.lastFrame = nextFrame
	r.lastFrameWidth = width
	r.lastFrameHeight = height
	if fullRender {
		fmt.Fprint(r.writer, "\x1b[J")
	}
	r.writeTitleLocked()
}

func (r *runRenderer) frame(width, height int) []string {
	r.mu.Lock()
	snapshot := rendererSnapshot{
		Started:            r.started,
		RunID:              r.runID,
		Agent:              r.agent,
		Repo:               r.repo,
		Instruction:        r.instruction,
		Base:               r.base,
		Branch:             r.branch,
		Goal:               r.goal,
		Logs:               r.logs,
		MaxIter:            r.maxIter,
		Color:              r.color,
		Stage:              r.stage,
		StageDetail:        r.stageDetail,
		Iteration:          r.iteration,
		IterationDir:       r.iterationDir,
		AgentCommand:       r.agentCommand,
		AgentExit:          r.agentExit,
		Current:            r.current,
		RunningCommand:     r.runningCommand,
		Activity:           append([]string(nil), r.activity...),
		ActivityLog:        append([]rendererLogLine(nil), r.activityLog...),
		Events:             append([]rendererEvent(nil), r.events...),
		CommitCount:        r.commitCount,
		MergeCount:         r.mergeCount,
		MessageCount:       r.messageCount,
		InputTokens:        r.inputTokens,
		OutputTokens:       r.outputTokens,
		TokensEstimated:    r.tokensEstimated,
		LatestMsg:          r.latestMsg,
		Tasks:              append([]taskItem(nil), r.tasks...),
		Confirmation:       cloneRendererConfirmation(r.confirmation),
		PendingPRs:         append([]rendererPendingPullRequest(nil), r.pendingPullRequests...),
		Sleeping:           r.sleeping,
		SleepSince:         r.sleepSince,
		SleepTitle:         r.sleepTitle,
		SleepStatus:        r.sleepStatus,
		SleepDetail:        r.sleepDetail,
		GracefulShutdown:   r.gracefulShutdown,
		PRMode:             r.prMode,
		PRReviewMode:       r.prReviewMode,
		PRReviewModeLocked: r.prReviewModeLocked,
		Now:                time.Now(),
	}
	r.mu.Unlock()

	refreshRendererPullRequest(&snapshot)
	tasks := append([]taskItem(nil), snapshot.Tasks...)
	refreshVisibleTaskTodos(tasks)
	if snapshot.Stage != string(runstate.StagePullRequest) {
		if currentTask := firstOpenTask(tasks); currentTask != "" {
			snapshot.Current = currentTask
		}
	}
	snapshot.Tasks = tasks
	return renderDashboard(snapshot, width, height)
}

func refreshRendererPullRequest(snapshot *rendererSnapshot) {
	if snapshot == nil || snapshot.Stage != string(runstate.StagePullRequest) || strings.TrimSpace(snapshot.IterationDir) == "" {
		return
	}
	if state, ok, err := readPRState(snapshot.IterationDir); err == nil && ok {
		snapshot.PullRequest = rendererPullRequest{
			PR:     state.PR,
			Title:  state.Title,
			Branch: state.Branch,
			Base:   state.Base,
			Status: state.Status,
		}
	}
	if checks, ok, err := readPRChecks(snapshot.IterationDir); err == nil && ok {
		snapshot.PullRequestChecks = rendererPullRequestChecks{
			Status:    checks.Status,
			CheckedAt: checks.CheckedAt,
			Pending:   checks.Pending,
			NoChecks:  checks.NoChecks,
			Error:     checks.Error,
			Checks:    rendererPullRequestChecksFromPR(checks.Checks),
			Stdout:    checks.Stdout,
			Stderr:    checks.Stderr,
		}
	}
	if strings.TrimSpace(snapshot.PullRequest.Branch) == "" {
		snapshot.PullRequest.Branch = snapshot.Branch
	}
	if strings.TrimSpace(snapshot.PullRequest.Base) == "" {
		snapshot.PullRequest.Base = snapshot.Base
	}
	if strings.TrimSpace(snapshot.PullRequest.Status) == "" {
		snapshot.PullRequest.Status = "preparing"
	}
	if strings.TrimSpace(snapshot.Current) == "" || strings.HasPrefix(strings.TrimSpace(snapshot.Current), "running task:") {
		snapshot.Current = "merge agent running"
	}
}

func normalizeRendererReviewMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case config.ReviewModeAutoMerge:
		return config.ReviewModeAutoMerge
	case config.ReviewModeParallelHumanReview:
		return config.ReviewModeParallelHumanReview
	case config.ReviewModeSerialHumanReview:
		return config.ReviewModeSerialHumanReview
	default:
		return ""
	}
}

func nextRendererReviewMode(mode string) string {
	switch normalizeRendererReviewMode(mode) {
	case config.ReviewModeAutoMerge:
		return config.ReviewModeParallelHumanReview
	case config.ReviewModeParallelHumanReview:
		return config.ReviewModeSerialHumanReview
	default:
		return config.ReviewModeAutoMerge
	}
}

func rendererPullRequestChecksFromPR(checks []pr.CheckStatus) []rendererPullRequestCheck {
	if len(checks) == 0 {
		return nil
	}
	out := make([]rendererPullRequestCheck, 0, len(checks))
	for _, check := range checks {
		name := strings.TrimSpace(check.Name)
		if name == "" {
			name = strings.TrimSpace(check.Workflow)
		}
		if name == "" {
			continue
		}
		out = append(out, rendererPullRequestCheck{
			Bucket:      strings.TrimSpace(check.Bucket),
			CompletedAt: strings.TrimSpace(check.CompletedAt),
			Description: strings.TrimSpace(check.Description),
			Event:       strings.TrimSpace(check.Event),
			Link:        strings.TrimSpace(check.Link),
			Name:        name,
			StartedAt:   strings.TrimSpace(check.StartedAt),
			State:       strings.TrimSpace(check.State),
			Workflow:    strings.TrimSpace(check.Workflow),
		})
	}
	return out
}

func (r *runRenderer) taskIndexLocked(id string) int {
	for i, task := range r.tasks {
		if task.ID == id {
			return i
		}
	}
	return -1
}

func (r *runRenderer) upsertTaskLocked(id, title, status string, done bool) {
	if strings.TrimSpace(title) == "" {
		title = id
	}
	if idx := r.taskIndexLocked(id); idx >= 0 {
		r.tasks[idx].Text = title
		r.tasks[idx].Status = status
		r.tasks[idx].Done = done
		return
	}
	r.tasks = append(r.tasks, taskItem{ID: id, Text: title, Status: status, Done: done})
}

func taskDisplayTitle(task workflow.Task) string {
	for _, value := range []string{task.Title, task.ID, task.Description} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return "task"
}

func rendererPendingPullRequestsFromState(pending []runstate.PendingPullRequest) []rendererPendingPullRequest {
	if len(pending) == 0 {
		return nil
	}
	out := make([]rendererPendingPullRequest, 0, len(pending))
	for _, item := range pending {
		prID := strings.TrimSpace(item.PR)
		if prID == "" {
			continue
		}
		out = append(out, rendererPendingPullRequest{
			PR:     prID,
			Title:  strings.TrimSpace(item.Title),
			Branch: strings.TrimSpace(item.Branch),
		})
	}
	return out
}

func rendererPendingPullRequestSummary(items []rendererPendingPullRequest) string {
	var parts []string
	for _, item := range items {
		text := pendingPullRequestDisplayText(item)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "; ")
}

func cloneRendererConfirmation(in *rendererConfirmation) *rendererConfirmation {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func firstOpenTask(tasks []taskItem) string {
	for _, item := range tasks {
		if item.Status == "active" || item.Status == "blocked" || item.Status == "retrying" {
			return item.Text
		}
	}
	for _, item := range tasks {
		if !item.Done {
			return item.Text
		}
	}
	return ""
}

func refreshVisibleTaskTodos(tasks []taskItem) {
	active := activeTaskIndex(tasks)
	for i := range tasks {
		if !shouldRenderTaskTodos(tasks[i], i, active) {
			continue
		}
		items := readTaskTodosForDisplay(tasks[i].TaskDir)
		if len(items) == 0 {
			continue
		}
		tasks[i].Todos = items
	}
}

func readTaskTodosForDisplay(taskDir string) []taskTodoDisplay {
	taskDir = strings.TrimSpace(taskDir)
	if taskDir == "" {
		return nil
	}
	data, err := os.ReadFile(taskTodoPath(taskDir))
	if err != nil {
		return nil
	}
	var todos taskTodoFile
	if err := json.Unmarshal(data, &todos); err != nil {
		return nil
	}
	items := make([]taskTodoDisplay, 0, len(todos.Items))
	for _, item := range todos.Items {
		subject, err := buildLoopCommitSubject(item.Type, item.CommitMessage, loopCommitMessageMaxLength)
		if err != nil {
			subject = strings.TrimSpace(item.Title)
		}
		if subject != "" {
			items = append(items, taskTodoDisplay{Status: item.Status, Text: subject})
		}
	}
	return items
}

func (r *runRenderer) line(label, message string) {
	r.drawMu.Lock()
	defer r.drawMu.Unlock()
	elapsed := formatDuration(time.Since(r.started))
	fmt.Fprintf(r.writer, "[%s] %-7s %s\n", elapsed, label, message)
}

func (r *runRenderer) setTitle() {
	if !r.titleEnabled {
		return
	}
	r.drawMu.Lock()
	defer r.drawMu.Unlock()
	r.writeTitleLocked()
}

func (r *runRenderer) writeTitleLocked() {
	if !r.titleEnabled {
		return
	}
	r.mu.Lock()
	stage := r.stage
	iter := r.iteration
	r.mu.Unlock()
	title := fmt.Sprintf("loop %s | %s | %s | %s", stage, iter, r.agent, formatDuration(time.Since(r.started)))
	if title == r.lastTitle {
		return
	}
	r.lastTitle = title
	fmt.Fprintf(r.writer, "\x1b]2;%s\a", title)
}

func (r *runRenderer) clearTitle() {
	if !r.titleEnabled {
		return
	}
	r.drawMu.Lock()
	defer r.drawMu.Unlock()
	r.lastTitle = ""
	fmt.Fprint(r.writer, "\x1b]2;\a")
}

func terminalControlSupported() bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func stringifyEventValue(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case []string:
		return strings.Join(typed, " ")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(typed)
	}
}

func intEventField(event runstate.Event, key string) (int, bool) {
	value, ok := event[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return int(n), true
		}
	}
	return 0, false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int(d%time.Hour) / int(time.Minute)
	s := int(d%time.Minute) / int(time.Second)
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func fitLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	if displayWidth(s) > width {
		return truncateVisible(s, width, true)
	}
	return s
}

func trimRendererLineRight(s string) string {
	return strings.TrimRight(s, " ")
}

func truncateDisplay(s string, width int) string {
	if width <= 0 {
		return s
	}
	s = strings.TrimSpace(s)
	if displayWidth(s) <= width {
		return s
	}
	if width <= 14 {
		return truncateVisible(s, width, false)
	}
	return truncateVisible(s, width-14, false) + "...[truncated]"
}

func truncateVisible(s string, width int, ellipsis bool) string {
	if width <= 0 {
		return ""
	}
	suffix := ""
	limit := width
	if ellipsis && width > 3 {
		suffix = "..."
		limit = width - 3
	}
	var b strings.Builder
	visible := 0
	hasANSI := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			hasANSI = true
			start := i
			i += 2
			for i < len(runes) {
				if runes[i] >= '@' && runes[i] <= '~' {
					break
				}
				i++
			}
			if i < len(runes) {
				b.WriteString(string(runes[start : i+1]))
			}
			continue
		}
		runeWidth := runeDisplayWidth(runes[i])
		if runeWidth > 0 && visible+runeWidth > limit {
			break
		}
		b.WriteRune(runes[i])
		visible += runeWidth
	}
	if hasANSI {
		b.WriteString(ansiReset)
	}
	b.WriteString(suffix)
	return b.String()
}
