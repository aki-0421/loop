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
	sleeping              bool
	sleepSince            time.Time
	sleepDetail           string
	gracefulShutdown      bool
	done                  chan struct{}
	ticker                *time.Ticker
	titleEnabled          bool
	drawMu                sync.Mutex
}

type rendererConfirmation struct {
	Title        string
	TargetBranch string
	MainBranch   string
	Until        time.Time
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

func newRunRenderer(g globals, runID, agentName, repoName, instructionFile, baseBranch, goal, logs string, maxIterations int, dryRun bool) *runRenderer {
	interactive := terminalControlSupported()
	return &runRenderer{
		enabled:      !g.JSON && !dryRun,
		interactive:  interactive,
		writer:       os.Stderr,
		started:      time.Now(),
		runID:        runID,
		agent:        agentName,
		repo:         repoName,
		instruction:  instructionFile,
		base:         baseBranch,
		goal:         goal,
		logs:         logs,
		maxIter:      maxIterations,
		color:        interactive && !g.NoColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		done:         make(chan struct{}),
		titleEnabled: interactive,
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
		fmt.Fprint(r.writer, "\x1b[?1049h\x1b[?25l")
		r.render()
	} else {
		r.line("run", fmt.Sprintf("%s agent=%s base=%s logs=%s", r.runID, r.agent, r.base, r.logs))
	}
	r.setTitle()
	r.ticker = time.NewTicker(rendererTickInterval)
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
	select {
	case <-r.done:
	default:
		close(r.done)
	}
	r.Stage(status, summary)
	if r.interactive {
		r.render()
		fmt.Fprint(r.writer, "\x1b[?25h\x1b[?1049l")
	}
	r.clearTitle()
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
		r.latestMsg = fmt.Sprintf("%d tasks planned", total)
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
	r.latestMsg = title
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
	r.latestMsg = title
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
	r.latestMsg = detail
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
	r.latestMsg = "graceful shutdown requested; press Ctrl+C again to exit immediately"
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
	r.latestMsg = "target branch is not the main branch"
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
	r.latestMsg = detail
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
	r.sleepDetail = detail
	r.stage = "sleeping"
	r.stageDetail = detail
	r.current = detail
	r.latestMsg = detail
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

func (r *runRenderer) SleepFetchRequested() {
	if !r.enabled {
		return
	}
	detail := "fetching GitHub updates after keypress"
	r.mu.Lock()
	r.sleepDetail = detail
	r.stageDetail = detail
	r.current = detail
	r.latestMsg = detail
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
	fmt.Fprint(r.writer, "\x1b[H")
	for i, line := range lines {
		fmt.Fprint(r.writer, fitLine(line, width), "\x1b[K")
		if i < len(lines)-1 {
			fmt.Fprint(r.writer, "\n")
		}
	}
	fmt.Fprint(r.writer, "\x1b[J")
	r.setTitle()
}

func (r *runRenderer) frame(width, height int) []string {
	r.mu.Lock()
	snapshot := rendererSnapshot{
		Started:          r.started,
		RunID:            r.runID,
		Agent:            r.agent,
		Repo:             r.repo,
		Instruction:      r.instruction,
		Base:             r.base,
		Branch:           r.branch,
		Goal:             r.goal,
		Logs:             r.logs,
		MaxIter:          r.maxIter,
		Color:            r.color,
		Stage:            r.stage,
		StageDetail:      r.stageDetail,
		Iteration:        r.iteration,
		AgentCommand:     r.agentCommand,
		AgentExit:        r.agentExit,
		Current:          r.current,
		RunningCommand:   r.runningCommand,
		Activity:         append([]string(nil), r.activity...),
		ActivityLog:      append([]rendererLogLine(nil), r.activityLog...),
		Events:           append([]rendererEvent(nil), r.events...),
		CommitCount:      r.commitCount,
		MergeCount:       r.mergeCount,
		MessageCount:     r.messageCount,
		InputTokens:      r.inputTokens,
		OutputTokens:     r.outputTokens,
		TokensEstimated:  r.tokensEstimated,
		LatestMsg:        r.latestMsg,
		Tasks:            append([]taskItem(nil), r.tasks...),
		Confirmation:     cloneRendererConfirmation(r.confirmation),
		Sleeping:         r.sleeping,
		SleepSince:       r.sleepSince,
		SleepDetail:      r.sleepDetail,
		GracefulShutdown: r.gracefulShutdown,
		Now:              time.Now(),
	}
	r.mu.Unlock()

	tasks := append([]taskItem(nil), snapshot.Tasks...)
	refreshActiveTaskTodos(tasks)
	if currentTask := firstOpenTask(tasks); currentTask != "" {
		snapshot.Current = currentTask
	}
	snapshot.Tasks = tasks
	return renderDashboard(snapshot, width, height)
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

func refreshActiveTaskTodos(tasks []taskItem) {
	active := activeTaskIndex(tasks)
	if active < 0 || active >= len(tasks) {
		return
	}
	taskDir := strings.TrimSpace(tasks[active].TaskDir)
	if taskDir == "" {
		return
	}
	data, err := os.ReadFile(taskTodoPath(taskDir))
	if err != nil {
		return
	}
	var todos taskTodoFile
	if err := json.Unmarshal(data, &todos); err != nil {
		return
	}
	items := make([]taskTodoDisplay, 0, len(todos.Items))
	for _, item := range todos.Items {
		subject, err := buildLoopCommitSubject(item.Type, item.CommitMessage, loopCommitMessageMaxLength)
		if err != nil {
			subject = strings.TrimSpace(item.Title)
		}
		if subject == "" {
			continue
		}
		items = append(items, taskTodoDisplay{Status: item.Status, Text: subject})
	}
	tasks[active].Todos = items
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
	r.mu.Lock()
	stage := r.stage
	iter := r.iteration
	r.mu.Unlock()
	fmt.Fprintf(r.writer, "\x1b]2;loop %s | %s | %s | %s\a", stage, iter, r.agent, formatDuration(time.Since(r.started)))
}

func (r *runRenderer) clearTitle() {
	if !r.titleEnabled {
		return
	}
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
