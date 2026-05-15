package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mogumc/oshind/types"
)

// ── bubbletea messages ──

// linesMsg ProgressReporter 通过 channel 发送的进度行
type linesMsg struct {
	lines []string
}

// statusMsg 任务状态变化消息
type statusMsg struct {
	status types.TaskStatus
}

// doneMsg 下载完成信号（携带 task 结果）
type doneMsg struct {
	task *types.DownloadTask
	err  error
}

// ── progressModel ──

// progressModel bubbletea model，接收 ProgressReporter 的回调行并渲染
type progressModel struct {
	lines      []string         // 当前帧的进度行
	status     types.TaskStatus // 当前任务状态
	result     *types.DownloadTask
	linesChan  chan []string
	statusChan chan types.TaskStatus
	doneChan   chan *doneMsg
	done       bool
	err        error
	quitting   bool
}

// newProgressModel 创建 progressModel 并返回 channels 供外部注入回调
func newProgressModel() (progressModel, chan []string, chan types.TaskStatus, chan *doneMsg) {
	linesChan := make(chan []string, 16)
	statusChan := make(chan types.TaskStatus, 8)
	doneChan := make(chan *doneMsg, 1)
	return progressModel{
		linesChan:  linesChan,
		statusChan: statusChan,
		doneChan:   doneChan,
	}, linesChan, statusChan, doneChan
}

func (m progressModel) Init() tea.Cmd {
	return waitForUpdate(m.linesChan, m.statusChan, m.doneChan)
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case linesMsg:
		m.lines = msg.lines
		return m, waitForUpdate(m.linesChan, m.statusChan, m.doneChan)
	case statusMsg:
		m.status = msg.status
		return m, waitForUpdate(m.linesChan, m.statusChan, m.doneChan)
	case doneMsg:
		m.done = true
		m.err = msg.err
		m.result = msg.task
		// 更新为任务最终状态，确保 View() 渲染正确的完成/失败信息
		if msg.task != nil {
			m.status = msg.task.GetStatus()
		}
		return m, tea.Quit
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m progressModel) View() string {
	if m.quitting {
		return ""
	}
	if len(m.lines) == 0 && m.status == 0 {
		return ""
	}

	var statusLine string
	if m.status != 0 {
		switch m.status {
		case types.TaskStatusPending:
			statusLine = "  ⏳ " + statusPending
		case types.TaskStatusProbing:
			statusLine = "  🔍 " + statusProbing
		case types.TaskStatusDownloading:
			statusLine = "  ⚡️ " + statusDownloading
		case types.TaskStatusVerifying:
			statusLine = "  ✅ " + statusVerifying
		case types.TaskStatusCompleted:
			statusLine = "  ✅ " + statusCompleted
		case types.TaskStatusFailed:
			statusLine = "  ❌ " + statusFailed
		case types.TaskStatusPaused:
			statusLine = "  ⏸️ " + statusPaused
		case types.TaskStatusResuming:
			statusLine = "  🔄 " + statusResuming
		default:
			statusLine = fmt.Sprintf("  Status: %s", m.status.String())
		}
	}

	var parts []string
	if statusLine != "" {
		parts = append(parts, statusLine)
	}
	if len(m.lines) > 0 {
		parts = append(parts, m.lines...)
	}

	return "\n" + strings.Join(parts, "\n") + "\n"
}

// waitForUpdate 返回一个 tea.Cmd，阻塞直到 linesChan、statusChan 或 doneChan 有数据
func waitForUpdate(linesChan chan []string, statusChan chan types.TaskStatus, doneChan chan *doneMsg) tea.Cmd {
	return func() tea.Msg {
		select {
		case lines := <-linesChan:
			return linesMsg{lines: lines}
		case status := <-statusChan:
			return statusMsg{status: status}
		case msg := <-doneChan:
			return doneMsg{task: msg.task, err: msg.err}
		}
	}
}

// ── TTY 检测 ──

// isInteractive 检测 stdout 是否为交互终端（非管道/重定向）
func isInteractive() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ── 非 TTY 进度回调 ──

// simpleOnReport 非终端模式：直接滚动输出进度行
func simpleOnReport(lines []string) {
	for _, line := range lines {
		fmt.Println(line)
	}
}

// ── 格式化工具 ──

// formatBytes 格式化字节大小（复用 downloader 中的格式逻辑）
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// ── 完成信息渲染 ──

// printSuccess 输出成功信息（TTY 用 lipgloss，非 TTY 用 plain text）
func printSuccess(fileName string, size int64) {
	if isInteractive() {
		msg := fmt.Sprintf("%s: %s", sumSaved, fileName)
		if size > 0 {
			msg = fmt.Sprintf("%s: %s (%s)", sumSaved, fileName, formatBytes(size))
		}
		fmt.Println("\n" + SuccessStyle.Render("✓ ") + SavedStyle.Render(msg))
	} else {
		if size > 0 {
			fmt.Printf("%s: %s (%s)\n", sumSaved, fileName, formatBytes(size))
		} else {
			fmt.Printf("%s: %s\n", sumSaved, fileName)
		}
	}
}

// printDownloadSummary 输出下载完成后的汇总信息，包含 probe 信息和验证结果
func printDownloadSummary(task *types.DownloadTask) {
	if task == nil {
		return
	}

	// 先输出保存信息
	var fileSize int64
	if task.Metadata != nil {
		fileSize = task.Metadata.Size
	}
	printSuccess(task.FileName, fileSize)

	if isInteractive() {
		fmt.Println()
		fmt.Println(renderDivider(sumTitle))
		fmt.Println()

		// probe 信息
		fmt.Println(renderKV(sumURL, task.URL))
		if task.Metadata != nil {
			if task.Metadata.FileName != "" {
				fmt.Println(renderKV(sumFile, task.Metadata.FileName))
			}
			if task.Metadata.Size > 0 {
				fmt.Println(renderKV(sumSize, formatBytes(task.Metadata.Size)))
			}
			if task.Metadata.ContentType != "" {
				fmt.Println(renderKV(sumType, task.Metadata.ContentType))
			}
			fmt.Println(renderKV(sumResume, fmt.Sprintf("%v", task.Metadata.SupportResume)))
			if task.Metadata.Checksum != "" {
				if task.Metadata.ChecksumType != "" {
					fmt.Println(renderKV(sumChecksum, fmt.Sprintf("%s:%s", task.Metadata.ChecksumType, task.Metadata.Checksum)))
				} else {
					fmt.Println(renderKV(sumChecksum, task.Metadata.Checksum))
				}
			}
		}
		fmt.Println(renderKV(sumProtocol, task.Protocol.String()))

		// 验证信息
		if task.Verify != nil {
			fmt.Println()
			fmt.Println(renderDivider(T("验证", "Verification")))
			fmt.Println()
			if task.Verify.Skipped {
				fmt.Println(renderKV(T("状态", "Status"), verifySkipped))
				fmt.Println(renderKV(T("原因", "Reason"), verifyNoChecksum))
			} else {
				fmt.Println(renderKV(verifyMethod, task.Verify.Method))
				fmt.Println(renderKV(verifyExpected, task.Verify.Expected))
				fmt.Println(renderKV(verifyActual, task.Verify.Actual))
				if task.Verify.Passed {
					fmt.Println(renderKV(verifyResult, SuccessStyle.Render(verifyPassed)))
				} else {
					fmt.Println(renderKV(verifyResult, ErrorStyle.Render(verifyFailed)))
				}
			}
		}
		fmt.Println()
	} else {
		fmt.Printf("%s:\n", sumTitle)
		fmt.Printf("  %-10s%s\n", sumURL+":", task.URL)
		if task.Metadata != nil {
			if task.Metadata.FileName != "" {
				fmt.Printf("  %-10s%s\n", sumFile+":", task.Metadata.FileName)
			}
			if task.Metadata.Size > 0 {
				fmt.Printf("  %-10s%s\n", sumSize+":", formatBytes(task.Metadata.Size))
			}
			if task.Metadata.ContentType != "" {
				fmt.Printf("  %-10s%s\n", sumType+":", task.Metadata.ContentType)
			}
			fmt.Printf("  %-10s%v\n", sumResume+":", task.Metadata.SupportResume)
			if task.Metadata.Checksum != "" {
				fmt.Printf("  %-10s%s\n", sumChecksum+":", task.Metadata.Checksum)
			}
		}
		fmt.Printf("  %-10s%s\n", sumProtocol+":", task.Protocol.String())

		if task.Verify != nil {
			fmt.Printf("%s:\n", T("验证", "Verification"))
			if task.Verify.Skipped {
				fmt.Printf("  %-10s%s (%s)\n", T("状态", "Status")+":", verifySkipped, verifyNoChecksum)
			} else {
				fmt.Printf("  %-10s%s\n", verifyMethod+":", task.Verify.Method)
				fmt.Printf("  %-10s%s\n", verifyExpected+":", task.Verify.Expected)
				fmt.Printf("  %-10s%s\n", verifyActual+":", task.Verify.Actual)
				if task.Verify.Passed {
					fmt.Printf("  %-10s%s\n", verifyResult+":", verifyPassed)
				} else {
					fmt.Printf("  %-10s%s\n", verifyResult+":", verifyFailed)
				}
			}
		}
	}
}

// printError 输出错误信息（TTY 用 lipgloss，非 TTY 用 plain text）
func printError(msg string) {
	if isInteractive() {
		fmt.Fprintln(os.Stderr, ErrorStyle.Render("✗ ")+msg)
	} else {
		fmt.Fprintf(os.Stderr, "error: %s\n", msg)
	}
}

// printInfo 输出信息（TTY 用 lipgloss，非 TTY 用 plain text）
func printInfo(label, val string) {
	if isInteractive() {
		fmt.Println(InfoStyle.Render(label) + " " + val)
	} else {
		fmt.Printf("%s %s\n", label, val)
	}
}

// renderDivider 渲染分隔线
func renderDivider(title string) string {
	if title == "" {
		return SeparatorStyle.Render(strings.Repeat("─", 48))
	}
	return HeaderStyle.Render(fmt.Sprintf("─ %s ─", title))
}

// ── CLI 侧进度报告器 ──

// ProgressReporter 从 task.Progress 读取下载状态，格式化输出进度行
// 完全运行在 CLI 侧，downloader 包不感知此结构
type ProgressReporter struct {
	task            *types.DownloadTask
	interval        time.Duration
	stopChan        chan struct{}
	statusChan      chan types.TaskStatus
	lastOutputLines int
	maxOutputLines  int
	started         bool
	stopOnce        sync.Once
	frameCount      int
	lastStatus      types.TaskStatus
	OnReport        func(lines []string)
	OnStop          func(maxLines int)
}

// NewProgressReporter 创建 CLI 侧进度报告器
func NewProgressReporter(task *types.DownloadTask, statusChan chan types.TaskStatus) *ProgressReporter {
	return &ProgressReporter{
		task:       task,
		interval:   500 * time.Millisecond,
		stopChan:   make(chan struct{}),
		statusChan: statusChan,
	}
}

// Start 开始报告进度（立即输出一次，然后定时刷新）
func (r *ProgressReporter) Start() {
	r.report()

	go func() {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				r.report()
			case <-r.stopChan:
				return
			}
		}
	}()
}

// Stop 停止报告进度
func (r *ProgressReporter) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopChan)
		if r.maxOutputLines > 0 && r.started && r.OnStop != nil {
			r.OnStop(r.maxOutputLines)
		}
	})
}

// report 生成当前进度帧并通过 OnReport 回调输出
func (r *ProgressReporter) report() {
	// 检测状态变化
	currentStatus := r.task.GetStatus()
	if currentStatus != r.lastStatus {
		r.lastStatus = currentStatus
		if r.statusChan != nil {
			select {
			case r.statusChan <- currentStatus:
			default:
			}
		}
	}

	totalChunks := len(r.task.Chunks)
	if totalChunks == 0 {
		return
	}

	completedChunks := r.task.GetCompletedChunkCount()
	downloaded := r.task.Progress.GetDownloaded()
	total := r.task.Metadata.Size

	speed := r.task.Progress.CalculateSpeed()

	var eta time.Duration
	if speed > 0 && total > 0 && downloaded < total {
		remaining := float64(total-downloaded) / speed
		eta = time.Duration(remaining * float64(time.Second))
	}

	spinner := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	r.frameCount++
	spinChar := spinner[r.frameCount%len(spinner)]

	var lines []string

	// 第1行：spinner + 进度条 + 速度 + ETA
	progress := float64(completedChunks) / float64(totalChunks) * 100
	bar := buildProgressBar(progress, 40)
	if speed > 0 {
		lines = append(lines, fmt.Sprintf("  %c %s %5.1f%% | %s/s | ETA: %s",
			spinChar, bar, progress, formatBytes(int64(speed)), formatDuration(eta)))
	} else if downloaded > 0 {
		lines = append(lines, fmt.Sprintf("  %c %s %5.1f%% | %s %s",
			spinChar, bar, progress, formatBytes(downloaded), progressDownloaded))
	} else {
		lines = append(lines, fmt.Sprintf("  %c %s %5.1f%% | %s",
			spinChar, bar, progress, progressConnecting))
	}

	// 第2行：线程统计
	activeThreads := r.task.Progress.GetActiveThreads()
	remainingChunks := r.task.Progress.GetRemainingChunks()
	failedChunks := r.task.Progress.GetFailedChunks()
	lines = append(lines, fmt.Sprintf("  %s: %d/%d  |  %s: %d %s  |  %s: %d",
		progressThreads, activeThreads, r.task.Config.MaxConnections,
		progressRemaining, remainingChunks, progressChunks,
		progressFailed, failedChunks))

	// 第3行起：活跃线程详情
	activeChunks := r.task.GetActiveChunks()
	if len(activeChunks) > 0 {
		lines = append(lines, "  ── "+progressActive+" ──")
		for i, chunk := range activeChunks {
			chunkSize := chunk.End - chunk.Start + 1
			chunkProgress := 0.0
			if chunkSize > 0 {
				chunkProgress = float64(chunk.Downloaded) / float64(chunkSize) * 100
			}
			miniBar := buildMiniBar(chunkProgress, 20)
			lines = append(lines, fmt.Sprintf("  [T%d] Chunk#%-3d %s %5.1f%%",
				i, chunk.Index, miniBar, chunkProgress))
		}
	}

	r.lastOutputLines = len(lines)
	if len(lines) > r.maxOutputLines {
		r.maxOutputLines = len(lines)
	}
	r.started = true

	if r.OnReport != nil {
		r.OnReport(lines)
	}
}

// ── 进度条构建 ──

// buildProgressBar 构建主进度条
func buildProgressBar(progress float64, width int) string {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}

	filled := int(progress / 100.0 * float64(width))
	if filled > width {
		filled = width
	}

	var bar strings.Builder
	bar.WriteByte('[')
	for i := 0; i < width; i++ {
		if i < filled {
			bar.WriteByte('=')
		} else if i == filled && filled < width {
			bar.WriteByte('>')
		} else {
			bar.WriteByte(' ')
		}
	}
	bar.WriteByte(']')
	return bar.String()
}

// buildMiniBar 构建小型分片进度条
func buildMiniBar(progress float64, width int) string {
	filled := int(progress / 100.0 * float64(width))
	if filled > width {
		filled = width
	}

	var bar strings.Builder
	bar.WriteByte('[')
	for i := 0; i < width; i++ {
		if i < filled {
			bar.WriteByte('#')
		} else {
			bar.WriteByte('.')
		}
	}
	bar.WriteByte(']')
	return bar.String()
}

// formatDuration 格式化持续时间为可读格式
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, minutes)
}
