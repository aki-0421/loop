package cli

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	rendererTickInterval = 200 * time.Millisecond
	spinnerFrameInterval = rendererTickInterval
	logoTopPadding       = 3
)

type rendererSnapshot struct {
	Started         time.Time
	Now             time.Time
	RunID           string
	Agent           string
	Repo            string
	Instruction     string
	Base            string
	Branch          string
	Goal            string
	Logs            string
	MaxIter         int
	Color           bool
	Stage           string
	StageDetail     string
	Iteration       string
	AgentCommand    string
	AgentExit       string
	Current         string
	RunningCommand  string
	Tasks           []taskItem
	Activity        []string
	ActivityLog     []rendererLogLine
	Events          []rendererEvent
	CommitCount     int
	MergeCount      int
	MessageCount    int
	InputTokens     int
	OutputTokens    int
	TokensEstimated bool
	LatestMsg       string
	Confirmation    *rendererConfirmation
	Sleeping        bool
	SleepSince      time.Time
	SleepDetail     string
}

type dashboardSymbols struct {
	Done      string
	Active    string
	Pending   string
	Blocked   string
	Retrying  string
	Bullet    string
	Sep       string
	BarFull   string
	BarEmpty  string
	Join      string
	FooterSep string
}

func renderDashboard(s rendererSnapshot, width, height int) []string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	symbols := symbolsForEnvironment()
	if s.Confirmation != nil {
		return renderConfirmationDashboard(s, symbols, width, height)
	}
	if s.Sleeping {
		return renderSleepDashboard(s, symbols, width, height)
	}
	if width < 72 || height < 18 {
		return renderPlainStatusSnapshot(s, width, height)
	}
	switch {
	case width >= 120:
		return renderWideDashboard(s, symbols, width, height)
	case width >= 90:
		return renderMediumDashboard(s, symbols, width, height)
	default:
		return renderNarrowDashboard(s, symbols, width, height)
	}
}

func renderWideDashboard(s rendererSnapshot, symbols dashboardSymbols, width, height int) []string {
	return renderFocusedDashboard(s, symbols, width, height, false)
}

func renderMediumDashboard(s rendererSnapshot, symbols dashboardSymbols, width, height int) []string {
	return renderFocusedDashboard(s, symbols, width, height, false)
}

func renderNarrowDashboard(s rendererSnapshot, symbols dashboardSymbols, width, height int) []string {
	return renderFocusedDashboard(s, symbols, width, height, true)
}

func renderFocusedDashboard(s rendererSnapshot, symbols dashboardSymbols, width, height int, compact bool) []string {
	contentWidth := minInt(width-8, 84)
	if contentWidth < 48 {
		contentWidth = width - 2
	}
	items := s.Tasks
	done, total := taskProgress(items)
	logo := loopLogo(s)
	filename := valueOr(s.Instruction, "prompt.md")
	metrics := fmt.Sprintf("%s  ·  %s in  ·  %s out  ·  %d merged  ·  %s",
		formatDuration(s.Now.Sub(s.Started)),
		formatTokenCount(s.InputTokens, s.TokensEstimated),
		formatTokenCount(s.OutputTokens, s.TokensEstimated),
		s.MergeCount,
		formatTaskProgressMetric(done, total),
	)
	latest := strings.TrimSpace(s.LatestMsg)
	if latest == "" {
		latest = "waiting for agent message..."
	}
	latest = ellipsize(latest, contentWidth)

	lines := []string{}
	for i := 0; i < logoTopPadding; i++ {
		lines = append(lines, "")
	}
	for _, line := range logo {
		lines = append(lines, centerLine(line, width))
	}
	lines = append(lines,
		"",
		centerLine(colorize(s, ansiDim, filename), width),
		"",
		centerLine(metrics, width),
		"",
		centerLine(colorize(s, ansiDim, latest), width),
		"",
	)

	taskLimit := maxTaskRows(height, len(lines))
	if total > 0 {
		lines = append(lines, taskListBlock(s, symbols, width, contentWidth, taskLimit, items)...)
	} else if taskLimit > 0 {
		lines = append(lines, statusBlock(s, width, contentWidth, taskLimit)...)
	}

	return fitCanvasLines(lines, colorize(s, ansiDim, footerText(s, symbols)), width, height)
}

func renderConfirmationDashboard(s rendererSnapshot, symbols dashboardSymbols, width, height int) []string {
	contentWidth := minInt(width-8, 84)
	if contentWidth < 32 {
		contentWidth = width - 2
	}
	confirmation := s.Confirmation
	title := "Confirm Target Branch"
	target := ""
	main := ""
	until := s.Now
	if confirmation != nil {
		title = valueOr(confirmation.Title, title)
		target = confirmation.TargetBranch
		main = confirmation.MainBranch
		until = confirmation.Until
	}
	remaining := until.Sub(s.Now).Round(time.Second)
	if remaining < 0 {
		remaining = 0
	}
	lines := []string{}
	for i := 0; i < logoTopPadding; i++ {
		lines = append(lines, "")
	}
	for _, line := range loopLogo(s) {
		lines = append(lines, centerLine(line, width))
	}
	lines = append(lines,
		"",
		centerLine(colorize(s, ansiYellow+ansiBold, title), width),
		"",
		centerLine(ellipsize("Target branch: "+target, contentWidth), width),
		centerLine(ellipsize("Main branch: "+main, contentWidth), width),
		"",
		centerLine(colorize(s, ansiDim, "Continuing in "+formatDuration(remaining)+". Press Ctrl+C to cancel."), width),
	)
	return fitCanvasLines(lines, colorize(s, ansiDim, footerText(s, symbols)), width, height)
}

func renderSleepDashboard(s rendererSnapshot, symbols dashboardSymbols, width, height int) []string {
	contentWidth := minInt(width-8, 84)
	if contentWidth < 32 {
		contentWidth = width - 2
	}
	detail := strings.TrimSpace(s.SleepDetail)
	if detail == "" {
		detail = "waiting for GitHub Issue/PR updates"
	}
	detail = strings.TrimPrefix(detail, "sleeping; ")
	sleepSince := s.SleepSince
	if sleepSince.IsZero() {
		sleepSince = s.Started
	}
	asleepFor := formatDuration(s.Now.Sub(sleepSince))
	lines := []string{}
	for i := 0; i < logoTopPadding; i++ {
		lines = append(lines, "")
	}
	for _, line := range loopLogo(s) {
		lines = append(lines, centerLine(line, width))
	}
	lines = append(lines,
		"",
		centerLine(colorize(s, ansiCyan+ansiBold, "GitHub Sleep Mode"), width),
		"",
		centerLine(spinnerSymbol(s)+" Waiting for GitHub Issue/PR updates", width),
		centerLine(colorize(s, ansiDim, ellipsize(detail, contentWidth)), width),
		"",
		centerLine(colorize(s, ansiDim, "Polling every 5m. Press any key to fetch now. Ctrl+C to cancel. Asleep for "+asleepFor+"."), width),
	)
	return fitCanvasLines(lines, colorize(s, ansiDim, footerText(s, symbols)), width, height)
}

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
	ansiWhite   = "\x1b[37m"
	ansiGray    = "\x1b[90m"
)

func loopLogo(s rendererSnapshot) []string {
	return []string{
		colorize(s, ansiBold+ansiCyan, `╦   ╔═╗ ╔═╗ ╔═╗`),
		colorize(s, ansiBold+ansiMagenta, `║   ║ ║ ║ ║ ╠═╝`),
		colorize(s, ansiBold+ansiYellow, `╩═╝ ╚═╝ ╚═╝ ╩  `),
	}
}

func statusText(s rendererSnapshot) string {
	if strings.TrimSpace(s.Stage) == "planning" {
		return spinnerSymbol(s) + " planning..."
	}
	text := strings.TrimSpace(s.Current)
	if text == "" {
		text = strings.TrimSpace(s.StageDetail)
	}
	if text == "" {
		text = strings.ReplaceAll(strings.TrimSpace(s.Stage), "_", " ")
	}
	if text == "" {
		text = "planning"
	}
	return spinnerSymbol(s) + " " + text
}

func statusBlock(s rendererSnapshot, width, contentWidth, limit int) []string {
	if limit <= 0 {
		return nil
	}
	lines := []string{centerLine(colorize(s, ansiYellow, statusText(s)), width)}
	command := strings.TrimSpace(s.RunningCommand)
	if command == "" || limit < 3 {
		return lines
	}
	lines = append(lines, "")
	lines = append(lines, centerLine(colorize(s, ansiDim, ellipsize(command, commandDisplayWidth(contentWidth))), width))
	return lines
}

func commandDisplayWidth(contentWidth int) int {
	if contentWidth <= 0 {
		return 0
	}
	commandWidth := contentWidth * 3 / 4
	if commandWidth < 32 {
		commandWidth = minInt(contentWidth, 32)
	}
	if commandWidth >= contentWidth && contentWidth > 1 {
		commandWidth = contentWidth - 1
	}
	return commandWidth
}

func spinnerSymbol(s rendererSnapshot) string {
	spinner := []string{"◐", "◓", "◑", "◒"}
	if os.Getenv("LOOP_ASCII") == "1" || os.Getenv("TERM") == "dumb" {
		spinner = []string{"-", "\\", "|", "/"}
	}
	elapsed := s.Now.Sub(s.Started)
	if elapsed < 0 {
		elapsed = 0
	}
	index := int(elapsed/spinnerFrameInterval) % len(spinner)
	return spinner[index]
}

func formatTokenCount(tokens int, estimated bool) string {
	if tokens < 0 {
		tokens = 0
	}
	prefix := ""
	if estimated {
		prefix = "~"
	}
	switch {
	case tokens >= 1_000_000_000_000:
		return fmt.Sprintf("%s%.1fT", prefix, float64(tokens)/1_000_000_000_000)
	case tokens >= 1_000_000_000:
		return fmt.Sprintf("%s%.1fB", prefix, float64(tokens)/1_000_000_000)
	case tokens >= 1_000_000:
		return fmt.Sprintf("%s%.1fM", prefix, float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%s%dK", prefix, int(float64(tokens)/1_000+0.5))
	default:
		return fmt.Sprintf("%s%d", prefix, tokens)
	}
}

func formatTaskProgressMetric(done, total int) string {
	if total <= 0 {
		return "0 tasks"
	}
	return fmt.Sprintf("%d/%d tasks", done, total)
}

func ellipsize(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if displayWidth(text) <= width {
		return text
	}
	return truncateVisible(text, width, true)
}

func taskListBlock(s rendererSnapshot, symbols dashboardSymbols, screenWidth, blockWidth, limit int, items []taskItem) []string {
	if limit <= 0 {
		return nil
	}
	_, total := taskProgress(items)
	if total == 0 {
		return []string{centerLine(colorize(s, ansiDim, "waiting for task tree"), screenWidth)}
	}
	maxBlockWidth := blockWidth
	if maxBlockWidth > 68 {
		maxBlockWidth = 68
	}

	type taskLine struct {
		line string
	}
	rows := []taskLine{}
	markerWidth := 0
	active := activeTaskIndex(items)
	for i := 0; i < len(items); i++ {
		marker := styledTaskMarker(s, symbols, items[i], i == active)
		if w := displayWidth(marker); w > markerWidth {
			markerWidth = w
		}
	}
	if markerWidth == 0 {
		markerWidth = 1
	}

	for i := 0; i < len(items); i++ {
		item := items[i]
		marker := styledTaskMarker(s, symbols, item, i == active)
		gap := "  "
		available := maxBlockWidth - markerWidth - displayWidth(gap)
		if available < 10 {
			available = 10
		}
		text := ellipsize(item.Text, available)
		rows = append(rows, taskLine{line: padRightPreserve(marker, markerWidth) + gap + text})
		if i == active {
			for _, todo := range item.Todos {
				todoMarker := styledTodoMarker(s, symbols, todo)
				todoGap := "  "
				todoPrefix := strings.Repeat(" ", markerWidth) + todoGap + "  " + todoMarker + " "
				todoAvailable := maxBlockWidth - displayWidth(todoPrefix)
				if todoAvailable < 8 {
					todoAvailable = 8
				}
				rows = append(rows, taskLine{line: todoPrefix + ellipsize(todo.Text, todoAvailable)})
			}
		}
	}
	hiddenBelow := 0
	if len(rows) > limit {
		visibleRows := limit - 1
		if visibleRows < 0 {
			visibleRows = 0
		}
		hiddenBelow = len(rows) - visibleRows
		rows = rows[:visibleRows]
	}
	if hiddenBelow > 0 {
		rows = append(rows, taskLine{line: colorize(s, ansiDim, fmt.Sprintf("%d hidden below", hiddenBelow))})
	}

	blockWidth = 1
	for _, row := range rows {
		if w := displayWidth(row.line); w > blockWidth {
			blockWidth = w
		}
	}
	if blockWidth > maxBlockWidth {
		blockWidth = maxBlockWidth
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, centeredBlockLine(row.line, screenWidth, blockWidth))
	}
	return lines
}

func styledTaskMarker(s rendererSnapshot, symbols dashboardSymbols, item taskItem, active bool) string {
	marker := taskMarker(s, item, active, symbols)
	switch {
	case item.Done || item.Status == "done":
		return colorize(s, ansiGreen, marker)
	case item.Status == "blocked":
		return colorize(s, ansiRed+ansiBold, marker)
	case item.Status == "retrying":
		return colorize(s, ansiMagenta+ansiBold, marker)
	case active || item.Status == "active":
		return colorize(s, ansiYellow+ansiBold, marker)
	default:
		return colorize(s, ansiGray, marker)
	}
}

func styledTodoMarker(s rendererSnapshot, symbols dashboardSymbols, item taskTodoDisplay) string {
	marker := taskTodoMarker(item, symbols)
	switch item.Status {
	case "done":
		return colorize(s, ansiGreen, marker)
	case "active":
		return colorize(s, ansiYellow+ansiBold, marker)
	default:
		return colorize(s, ansiGray, marker)
	}
}

func maxTaskRows(height, used int) int {
	limit := height - 2 - used
	if limit < 0 {
		return 0
	}
	return limit
}

func fitCanvasLines(content []string, footer string, width, height int) []string {
	if height <= 0 {
		return nil
	}
	hasFooter := strings.TrimSpace(stripANSISequences(footer)) != ""
	content = trimTrailingBlankLines(content)
	contentLimit := height
	if hasFooter && height >= 2 {
		contentLimit = height - 2
	}
	if len(content) > contentLimit {
		content = content[:contentLimit]
	}
	lines := make([]string, 0, height)
	lines = append(lines, content...)
	if hasFooter && height >= 2 {
		for len(lines) < height-2 {
			lines = append(lines, "")
		}
		lines = append(lines, centerLine(footer, width))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = padRightPreserve(lines[i], width)
	}
	return lines
}

func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(stripANSISequences(lines[end-1])) == "" {
		end--
	}
	return append([]string(nil), lines[:end]...)
}

func centerLine(text string, width int) string {
	if width <= 0 {
		return text
	}
	text = truncateDisplay(text, width)
	padding := width - displayWidth(text)
	if padding <= 0 {
		return text
	}
	left := padding / 2
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", padding-left)
}

func centeredBlockLine(text string, screenWidth, blockWidth int) string {
	text = padRightPreserve(text, blockWidth)
	padding := screenWidth - blockWidth
	if padding <= 0 {
		return fitLine(text, screenWidth)
	}
	left := padding / 2
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", padding-left)
}

func colorize(s rendererSnapshot, style, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return style + text + ansiReset
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func renderPlainStatusSnapshot(s rendererSnapshot, width, height int) []string {
	if s.Sleeping {
		current := strings.TrimSpace(s.SleepDetail)
		if current == "" {
			current = "waiting for GitHub Issue/PR updates"
		}
		lines := []string{
			fmt.Sprintf("loop sleeping iter=%s elapsed=%s", iterationDisplay(s), formatDuration(s.Now.Sub(s.Started))),
			truncateDisplay(current, width),
		}
		if height > 2 {
			lines = append(lines, "polling GitHub every 5m")
		}
		if len(lines) > height {
			return lines[:height]
		}
		return lines
	}
	iter := iterationDisplay(s)
	current := strings.TrimSpace(s.Current)
	if current == "" {
		current = s.StageDetail
	}
	if current == "" {
		current = "waiting"
	}
	lines := []string{
		fmt.Sprintf("loop %s iter=%s elapsed=%s", s.Stage, iter, formatDuration(s.Now.Sub(s.Started))),
		truncateDisplay(current, width),
	}
	if height > 2 {
		lines = append(lines, fmt.Sprintf("tokens: %s in, %s out", formatTokenCount(s.InputTokens, s.TokensEstimated), formatTokenCount(s.OutputTokens, s.TokensEstimated)))
	}
	if height > 3 {
		done, total := taskProgress(s.Tasks)
		if total > 0 {
			lines = append(lines, fmt.Sprintf("tasks: %d/%d done", done, total))
		}
	}
	if len(lines) > height {
		return lines[:height]
	}
	return lines
}

func taskProgress(tasks []taskItem) (int, int) {
	done := 0
	for _, item := range tasks {
		if item.Done || item.Status == "done" {
			done++
		}
	}
	return done, len(tasks)
}

func activeTaskIndex(tasks []taskItem) int {
	for i, item := range tasks {
		if item.Status == "active" || item.Status == "blocked" || item.Status == "retrying" {
			return i
		}
	}
	for i, item := range tasks {
		if !item.Done {
			return i
		}
	}
	if len(tasks) == 0 {
		return -1
	}
	return len(tasks) - 1
}

func taskMarker(s rendererSnapshot, item taskItem, active bool, symbols dashboardSymbols) string {
	switch {
	case item.Done || item.Status == "done":
		return symbols.Done
	case item.Status == "blocked":
		return symbols.Blocked
	case item.Status == "retrying":
		return symbols.Retrying
	case active || item.Status == "active":
		return spinnerSymbol(s)
	default:
		return symbols.Pending
	}
}

func taskTodoMarker(item taskTodoDisplay, symbols dashboardSymbols) string {
	switch item.Status {
	case "done":
		return symbols.Done
	case "active":
		return symbols.Active
	default:
		return symbols.Pending
	}
}

func footerText(s rendererSnapshot, symbols dashboardSymbols) string {
	return "Ctrl+C cancel"
}

func phaseLabel(stage string) string {
	switch stage {
	case "created", "":
		return "Starting"
	case "branch_created":
		return "Planning"
	case "agent_running":
		return "Implementing"
	case "validating":
		return "Testing"
	case "integrating":
		return "Squashing"
	case "completed":
		return "Completed"
	case "sleeping":
		return "Sleeping"
	case "failed":
		return "Failed"
	case "cancelled", "stopped":
		return "Stopped"
	default:
		return titleWords(strings.ReplaceAll(stage, "_", " "))
	}
}

func eventStatusForStage(stage string) string {
	switch stage {
	case "completed":
		return "done"
	case "failed", "cancelled", "stopped":
		return "blocked"
	default:
		return "active"
	}
}

func iterationDisplay(s rendererSnapshot) string {
	iter := strings.TrimSpace(s.Iteration)
	if iter == "" {
		iter = "0000"
	}
	if s.MaxIter > 0 {
		return fmt.Sprintf("%s/%04d", iter, s.MaxIter)
	}
	return iter
}

func symbolsForEnvironment() dashboardSymbols {
	if os.Getenv("LOOP_ASCII") == "1" || os.Getenv("TERM") == "dumb" {
		return dashboardSymbols{
			Done: "[x]", Active: "[>]", Pending: "[ ]", Blocked: "[!]", Retrying: "[~]",
			Bullet: "-", Sep: "|", BarFull: "#", BarEmpty: "-", Join: ">", FooterSep: " | ",
		}
	}
	return dashboardSymbols{
		Done: "✓", Active: "▶", Pending: "◦", Blocked: "!", Retrying: "↻",
		Bullet: "•", Sep: "·", BarFull: "█", BarEmpty: "░", Join: "›", FooterSep: " · ",
	}
}

func padRightPreserve(text string, width int) string {
	text = fitLine(text, width)
	padding := width - displayWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}

func displayWidth(text string) int {
	return len([]rune(stripANSISequences(text)))
}

func stripANSISequences(text string) string {
	var b strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) {
				if runes[i] >= '@' && runes[i] <= '~' {
					break
				}
				i++
			}
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

func valueOr(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func titleWords(text string) string {
	parts := strings.Fields(text)
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
