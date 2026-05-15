// OShinD - Command Line Interface
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mogumc/oshind/pkg/downloader"
	"github.com/mogumc/oshind/types"
)

var version = "1.0.0"

// ── 子命令路由 ──

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	cmd := os.Args[1]

	switch cmd {
	case "download", "dl":
		handleDownload(os.Args[2:])
	case "probe":
		handleProbe(os.Args[2:])
	case "has-resume":
		handleHasResume(os.Args[2:])
	case "clear-resume":
		handleClearResume(os.Args[2:])
	case "version", "-v", "--version":
		printVersion()
	case "help", "-h", "--help":
		printUsage()
	default:
		printError(fmt.Sprintf("%s: %s", errUnknownCommand, cmd))
		fmt.Fprintln(os.Stderr, errRunHelp)
		os.Exit(1)
	}
}

// ── 使用说明 ──
func printUsage() {
	if !isInteractive() {
		printPlainUsage()
		return
	}

	fmt.Println(TitleStyle.Render("OShinD - v" + version))
	fmt.Println()
	fmt.Println(renderDivider(hSecCommands))
	printCmd("  download, dl", "  <url> [options]", hCmdDownload)
	printCmd("  probe", "        <url> [options]", hCmdProbe)
	printCmd("  has-resume", "   <dir> <file>", hCmdHasResume)
	printCmd("  clear-resume", " <dir> <file>", hCmdClear)
	printCmd("  version", "", hCmdVersion)
	printCmd("  help", "", hCmdHelp)
	fmt.Println()
	fmt.Println(renderDivider(hSecOptions))
	printOpt("-o, --output <dir>", hOptOutput)
	printOpt("-c, --connections <n>", hOptConn)
	printOpt("-s, --chunk-size <size>", hOptChunk)
	printOpt("-t, --timeout <duration>", hOptTimeout)
	printOpt("-r, --retry <n>", hOptRetry)
	printOpt("-H, --header <key:value>", hOptHeader)
	printOpt("-m, --multi-source <url>", hOptMulti)
	printOpt("-u, --user <username>", hOptUser)
	printOpt("-p, --password <password>", hOptPass)
	printOpt("--ftp-port <port>", hOptFtpPort)
	printOpt("--sftp-port <port>", hOptSftpPort)
	printOpt("--skip-tls-verify", hOptNoTLS)
	printOpt("--no-checksum", hOptNoCheck)
	printOpt("--no-resume", hOptNoResume)
	printOpt("--checksum-type <type>", hOptCkType)
	printOpt("--checksum-value <value>", hOptCkValue)
	printOpt("--checksum <type:value>", hOptCkBoth)
}

func printPlainUsage() {
	fmt.Println("OShinD v" + version)
	fmt.Println()
	fmt.Println("Usage: oshind <command> [options]")
	fmt.Println()
	fmt.Println(hSecCommands + ":")
	fmt.Printf("  download, dl   <url> [options]   %s\n", hCmdDownload)
	fmt.Printf("  probe          <url> [options]   %s\n", hCmdProbe)
	fmt.Printf("  has-resume     <dir> <file>      %s\n", hCmdHasResume)
	fmt.Printf("  clear-resume   <dir> <file>      %s\n", hCmdClear)
	fmt.Printf("  version                          %s\n", hCmdVersion)
	fmt.Printf("  help                             %s\n", hCmdHelp)
	fmt.Println()
	fmt.Println(hSecOptions + ":")
	fmt.Printf("  -o, --output <dir>              %s\n", hOptOutput)
	fmt.Printf("  -c, --connections <n>           %s\n", hOptConn)
	fmt.Printf("  -s, --chunk-size <size>         %s\n", hOptChunk)
	fmt.Printf("  -t, --timeout <duration>        %s\n", hOptTimeout)
	fmt.Printf("  -r, --retry <n>                 %s\n", hOptRetry)
	fmt.Printf("  -H, --header <key:value>        %s\n", hOptHeader)
	fmt.Printf("  -m, --multi-source <url>        %s\n", hOptMulti)
	fmt.Printf("  -u, --user <username>           %s\n", hOptUser)
	fmt.Printf("  -p, --password <password>       %s\n", hOptPass)
	fmt.Printf("  --ftp-port <port>               %s\n", hOptFtpPort)
	fmt.Printf("  --sftp-port <port>              %s\n", hOptSftpPort)
	fmt.Printf("  --skip-tls-verify               %s\n", hOptNoTLS)
	fmt.Printf("  --no-checksum                   %s\n", hOptNoCheck)
	fmt.Printf("  --no-resume                     %s\n", hOptNoResume)
	fmt.Printf("  --checksum-type <type>          %s\n", hOptCkType)
	fmt.Printf("  --checksum-value <value>        %s\n", hOptCkValue)
	fmt.Printf("  --checksum <type:value>         %s\n", hOptCkBoth)
}

func printCmd(cmd, args, desc string) {
	fmt.Println(HelpCmdStyle.Render(cmd) + HelpDescStyle.Render(args) + HelpDescStyle.Render("    "+desc))
}

func printOpt(opt, desc string) {
	fmt.Println(InfoStyle.Render(opt) + "    " + HelpDescStyle.Render(desc))
}

func printVersion() {
	if isInteractive() {
		fmt.Println(TitleStyle.Render("OShinD") + SubtitleStyle.Render(" v"+version))
	} else {
		fmt.Printf("OShinD %s\n", version)
	}
}

// ── download 命令 ──

func handleDownload(args []string) {
	if len(args) < 1 {
		printError(errURLRequired)
		fmt.Fprintln(os.Stderr, "Usage: oshind download <url> [options]")
		os.Exit(1)
	}

	rawURL := args[0]
	config := parseArgs(args[1:])

	if isInteractive() {
		fmt.Println(InfoStyle.Render("↓ ") + fmt.Sprintf("%s: %s", sumDownload, rawURL))
	} else {
		fmt.Printf("%s: %s\n", sumDownload, rawURL)
	}

	e := downloader.NewEngine(config)

	// TTY: bubbletea 模式 | 非 TTY: 简单滚动输出
	if isInteractive() {
		runBubbleTeaDownload(e, rawURL, config)
	} else {
		runSimpleDownload(e, rawURL, config)
	}
}

// runBubbleTeaDownload 交互终端
func runBubbleTeaDownload(e *downloader.Engine, rawURL string, config *types.DownloadConfig) {
	model, linesChan, statusChan, doneChan := newProgressModel()

	// 在 goroutine 中执行下载，完成后通知 bubbletea 退出
	go func() {
		var reporter *ProgressReporter

		onReady := func(task *types.DownloadTask) {
			reporter = NewProgressReporter(task, statusChan)
			reporter.OnReport = func(lines []string) {
				cp := make([]string, len(lines))
				copy(cp, lines)
				select {
				case linesChan <- cp:
				default:
				}
			}
			// bubbletea 管理 screen buffer
			reporter.OnStop = func(_ int) {}
			reporter.Start()
		}

		task, err := e.Download(context.Background(), rawURL, config, onReady)

		// 停止 reporter
		if reporter != nil {
			reporter.Stop()
		}

		doneChan <- &doneMsg{task: task, err: err}
	}()

	// 运行 bubbletea
	p := tea.NewProgram(model, tea.WithOutput(os.Stdout))
	finalModel, runErr := p.Run()
	if runErr != nil {
		printError(fmt.Sprintf("%s: %v", errTUIError, runErr))
		os.Exit(1)
	}

	// 从最终 model 获取结果
	m := finalModel.(progressModel)
	if m.err != nil {
		printError(fmt.Sprintf("%s: %v", errDownloadFailed, m.err))
		os.Exit(1)
	}
	if m.result != nil {
		printDownloadSummary(m.result)
	}
}

// runSimpleDownload 非交互终端：直接滚动输出
func runSimpleDownload(e *downloader.Engine, rawURL string, config *types.DownloadConfig) {
	var reporter *ProgressReporter

	onReady := func(task *types.DownloadTask) {
		reporter = NewProgressReporter(task, nil)
		reporter.OnReport = simpleOnReport
		reporter.OnStop = func(_ int) {}
		reporter.Start()
	}

	task, err := e.Download(context.Background(), rawURL, config, onReady)

	if reporter != nil {
		reporter.Stop()
	}

	if err != nil {
		printError(fmt.Sprintf("%s: %v", errDownloadFailed, err))
		os.Exit(1)
	}
	printDownloadSummary(task)
}

// ── probe 命令 ──

// probeModel bubbletea model，用于 probe 命令的 TUI 状态显示
type probeModel struct {
	status     types.TaskStatus
	result     *downloader.ProbeResult
	statusChan chan types.TaskStatus
	doneChan   chan probeDoneMsg
	err        error
	quitting   bool
}

type probeDoneMsg struct {
	result *downloader.ProbeResult
	err    error
}

func (m probeModel) Init() tea.Cmd {
	return waitForProbeUpdate(m.statusChan, m.doneChan)
}

func (m probeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		m.status = msg.status
		return m, waitForProbeUpdate(m.statusChan, m.doneChan)
	case probeDoneMsg:
		m.result = msg.result
		m.err = msg.err
		m.quitting = true
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

func (m probeModel) View() string {
	if m.quitting {
		return ""
	}
	if m.status == 0 {
		return ""
	}

	switch m.status {
	case types.TaskStatusProbing:
		return "\n  🔍 " + statusProbing + "\n"
	case types.TaskStatusCompleted:
		return "\n  ✅ " + statusCompleted + "\n"
	case types.TaskStatusFailed:
		return "\n  ❌ " + statusFailed + "\n"
	}
	return ""
}

// waitForProbeUpdate probe 专用的 channel 等待，优先消费 status
func waitForProbeUpdate(statusChan chan types.TaskStatus, doneChan chan probeDoneMsg) tea.Cmd {
	return func() tea.Msg {
		select {
		case status := <-statusChan:
			return statusMsg{status: status}
		case msg := <-doneChan:
			return probeDoneMsg{result: msg.result, err: msg.err}
		}
	}
}

func handleProbe(args []string) {
	if len(args) < 1 {
		printError(errURLRequired)
		fmt.Fprintln(os.Stderr, "Usage: oshind probe <url> [options]")
		os.Exit(1)
	}

	rawURL := args[0]
	config := parseArgs(args[1:])

	if isInteractive() {
		fmt.Println(InfoStyle.Render("⟳ ") + fmt.Sprintf("%s: %s", sumProbe, rawURL))
	} else {
		fmt.Printf("%s: %s\n", sumProbe, rawURL)
	}

	// TTY: bubbletea 模式 | 非 TTY: 简单阻塞
	if isInteractive() {
		runBubbleTeaProbe(rawURL, config)
	} else {
		runSimpleProbe(rawURL, config)
	}
}

// runBubbleTeaProbe 交互终端 probe
func runBubbleTeaProbe(rawURL string, config *types.DownloadConfig) {
	statusChan := make(chan types.TaskStatus, 8)
	doneChan := make(chan probeDoneMsg, 1)

	model := probeModel{
		statusChan: statusChan,
		doneChan:   doneChan,
	}

	go func() {
		// 发送 Probing 状态
		select {
		case statusChan <- types.TaskStatusProbing:
		default:
		}

		result, err := downloader.ProbeFull(rawURL, config)

		doneChan <- probeDoneMsg{result: result, err: err}
	}()

	p := tea.NewProgram(model, tea.WithOutput(os.Stdout))
	finalModel, runErr := p.Run()
	if runErr != nil {
		printError(fmt.Sprintf("%s: %v", errTUIError, runErr))
		os.Exit(1)
	}

	m := finalModel.(probeModel)
	if m.err != nil {
		printError(fmt.Sprintf("%s: %v", errProbeFailed, m.err))
		os.Exit(1)
	}
	printProbeResult(m.result)
}

// runSimpleProbe 非交互终端 probe
func runSimpleProbe(rawURL string, config *types.DownloadConfig) {
	result, err := downloader.ProbeFull(rawURL, config)
	if err != nil {
		printError(fmt.Sprintf("%s: %v", errProbeFailed, err))
		os.Exit(1)
	}
	printProbeResult(result)
}

// printProbeResult 输出探测结果
func printProbeResult(result *downloader.ProbeResult) {
	if result == nil {
		return
	}

	fmt.Println()

	if isInteractive() {
		fmt.Println(renderDivider(probeLabel))
		fmt.Println()
		fmt.Println(renderKV(probeURL, result.URL))

		if result.Metadata != nil {
			if result.Metadata.FileName != "" {
				fmt.Println(renderKV(probeFile, result.Metadata.FileName))
			}
			if result.Metadata.Size > 0 {
				fmt.Println(renderKV(probeSize, formatBytes(result.Metadata.Size)))
			}
			if result.Metadata.ContentType != "" {
				fmt.Println(renderKV(probeType, result.Metadata.ContentType))
			}
			fmt.Println(renderKV(probeResume, fmt.Sprintf("%v", result.Metadata.SupportResume)))
			if result.Metadata.Checksum != "" {
				if result.Metadata.ChecksumType != "" {
					fmt.Println(renderKV(sumChecksum, fmt.Sprintf("%s:%s", result.Metadata.ChecksumType, result.Metadata.Checksum)))
				} else {
					fmt.Println(renderKV(sumChecksum, result.Metadata.Checksum))
				}
			}
		}
		if result.ServerInfo != nil {
			fmt.Println(renderKV(probeServer, fmt.Sprintf("%s:%s (%s)", result.ServerInfo.Host, result.ServerInfo.Port, result.ServerInfo.Scheme)))
		}
		if result.EstimatedSpeed > 0 {
			fmt.Println(renderKV(probeSpeed, fmt.Sprintf("%s/s", formatBytes(int64(result.EstimatedSpeed)))))
		}
		fmt.Println()
	} else {
		fmt.Printf("%-11s%s\n", probeURL+":", result.URL)
		if result.Metadata != nil {
			if result.Metadata.FileName != "" {
				fmt.Printf("%-11s%s\n", probeFile+":", result.Metadata.FileName)
			}
			if result.Metadata.Size > 0 {
				fmt.Printf("%-11s%s\n", probeSize+":", formatBytes(result.Metadata.Size))
			}
			if result.Metadata.ContentType != "" {
				fmt.Printf("%-11s%s\n", probeType+":", result.Metadata.ContentType)
			}
			fmt.Printf("%-11s%v\n", probeResume+":", result.Metadata.SupportResume)
			if result.Metadata.Checksum != "" {
				if result.Metadata.ChecksumType != "" {
					fmt.Printf("%-11s%s:%s\n", sumChecksum+":", result.Metadata.ChecksumType, result.Metadata.Checksum)
				} else {
					fmt.Printf("%-11s%s\n", sumChecksum+":", result.Metadata.Checksum)
				}
			}
		}
		if result.ServerInfo != nil {
			fmt.Printf("%-11s%s:%s (%s)\n", probeServer+":", result.ServerInfo.Host, result.ServerInfo.Port, result.ServerInfo.Scheme)
		}
		if result.EstimatedSpeed > 0 {
			fmt.Printf("%-11s%s/s\n", probeSpeed+":", formatBytes(int64(result.EstimatedSpeed)))
		}
	}
}

// ── has-resume 命令 ──

func handleHasResume(args []string) {
	if len(args) < 2 {
		printError(errDirFileRequired)
		fmt.Fprintln(os.Stderr, "Usage: oshind has-resume <dir> <file>")
		os.Exit(1)
	}

	outputDir := args[0]
	fileName := args[1]
	outputPath := filepath.Join(outputDir, fileName)
	oshinPath := downloader.GetOShinStatePath(outputPath)

	state, _ := downloader.LoadOShinState(oshinPath)
	if state == nil {
		if isInteractive() {
			fmt.Println(WarningStyle.Render("  ⚠ ") + sumNoResume)
		} else {
			fmt.Println(sumNoResume)
		}
		return
	}

	if isInteractive() {
		fmt.Println()
		fmt.Println(renderDivider(sumResumeState))
		fmt.Println()
		fmt.Println(renderKV(probeURL, state.URL))
		fmt.Println(renderKV(probeFile, state.FileName))
		if len(state.ET) > 0 {
			fmt.Println(renderKV(sumChecksum, state.ET))
		}
		fmt.Println(renderKV(probeSize, formatBytes(state.TotalSize)))
		fmt.Println(renderKV(sumChunkSize, formatBytes(state.ChunkSize)))
		fmt.Println(renderKV(sumChunks, fmt.Sprintf("%d", len(state.Chunks))))

		if len(state.Chunks) > 0 {
			fmt.Println()
			chunkHeader := fmt.Sprintf("  %-6s  %-20s  %-20s", resumeID, resumeStart, resumeEnd)
			fmt.Println(HeaderStyle.Render(chunkHeader))
			limit := len(state.Chunks)
			if limit > 12 {
				limit = 12
			}
			for i := 0; i < limit; i++ {
				c := state.Chunks[i]
				fmt.Printf("  %-6d  %-20s  %-20s\n", c.ID, formatBytes(c.Start), formatBytes(c.End))
			}
			if len(state.Chunks) > 12 {
				fmt.Println(InfoStyle.Render(fmt.Sprintf("    "+resumeMore, len(state.Chunks)-12)))
			}
		}
		fmt.Println()
	} else {
		fmt.Printf("%s\n", sumResumeFound)
		fmt.Printf("  %-10s%s\n", probeURL+":", state.URL)
		fmt.Printf("  %-10s%s\n", probeFile+":", state.FileName)
		fmt.Printf("  %-10s%s\n", probeSize+":", formatBytes(state.TotalSize))
		fmt.Printf("  %-10s%s\n", sumChunkSize+":", formatBytes(state.ChunkSize))
		fmt.Printf("  %-10s%d\n", sumChunks+":", len(state.Chunks))
		for i, c := range state.Chunks {
			fmt.Printf("    [%d] %s - %s\n", c.ID, formatBytes(c.Start), formatBytes(c.End))
			if i > 10 {
				fmt.Printf("    "+resumeMore+"\n", len(state.Chunks)-11)
				break
			}
		}
	}
}

// ── clear-resume 命令 ──

func handleClearResume(args []string) {
	if len(args) < 2 {
		printError(errDirFileRequired)
		fmt.Fprintln(os.Stderr, "Usage: oshind clear-resume <dir> <file>")
		os.Exit(1)
	}

	outputDir := args[0]
	fileName := args[1]
	outputPath := filepath.Join(outputDir, fileName)
	oshinPath := downloader.GetOShinStatePath(outputPath)

	if err := downloader.RemoveOShinState(oshinPath); err != nil {
		printError(fmt.Sprintf("%s: %v", errClearFailed, err))
		os.Exit(1)
	}

	if isInteractive() {
		fmt.Println(SuccessStyle.Render("  ✓ ") + sumCleared)
	} else {
		fmt.Println(sumCleared)
	}
}

// ── 参数解析 ──

func parseArgs(args []string) *types.DownloadConfig {
	config := types.DefaultConfig()

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "-o", "--output":
			if i+1 < len(args) {
				config.OutputDir = args[i+1]
				i++
			}
		case "-c", "--connections":
			if i+1 < len(args) {
				var n int
				if _, err := fmt.Sscanf(args[i+1], "%d", &n); err == nil && n > 0 {
					config.MaxConnections = n
				}
				i++
			}
		case "-s", "--chunk-size":
			if i+1 < len(args) {
				config.ChunkSize = parseSize(args[i+1])
				i++
			}
		case "-t", "--timeout":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil {
					config.Timeout = d
				}
				i++
			}
		case "-r", "--retry":
			if i+1 < len(args) {
				var n int
				if _, err := fmt.Sscanf(args[i+1], "%d", &n); err == nil && n >= 0 {
					config.Retry = n
				}
				i++
			}
		case "-u", "--user":
			if i+1 < len(args) {
				config.FTPConfig.Username = args[i+1]
				i++
			}
		case "-p", "--password":
			if i+1 < len(args) {
				config.FTPConfig.Password = args[i+1]
				i++
			}
		case "--ftp-port":
			if i+1 < len(args) {
				var n int
				if _, err := fmt.Sscanf(args[i+1], "%d", &n); err == nil && n > 0 {
					config.FTPConfig.Port = n
				}
				i++
			}
		case "--sftp-port":
			if i+1 < len(args) {
				var n int
				if _, err := fmt.Sscanf(args[i+1], "%d", &n); err == nil && n > 0 {
					config.FTPConfig.Port = n
				}
				i++
			}
		case "--skip-tls-verify":
			config.TLSConfig.InsecureSkipVerify = true
		case "--no-checksum":
			config.AutoChecksum = false
		case "--no-resume":
			config.NoResume = true
		case "-m", "--multi-source":
			if i+1 < len(args) {
				config.MultiSources = append(config.MultiSources, args[i+1])
				i++
			}
		case "--checksum-type":
			if i+1 < len(args) {
				config.ChecksumType = args[i+1]
				i++
			}
		case "--checksum-value":
			if i+1 < len(args) {
				config.ChecksumValue = args[i+1]
				i++
			}
		case "--checksum":
			if i+1 < len(args) {
				parts := strings.SplitN(args[i+1], ":", 2)
				if len(parts) == 2 {
					config.ChecksumType = parts[0]
					config.ChecksumValue = parts[1]
				}
				i++
			}
		case "-H", "--header":
			if i+1 < len(args) {
				parts := strings.SplitN(args[i+1], ":", 2)
				if len(parts) == 2 {
					config.Headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				}
				i++
			}
		}
	}

	return config
}

func parseSize(s string) int64 {
	if len(s) == 0 {
		return 8 * 1024 * 1024
	}

	last := s[len(s)-1]
	var multiplier int64 = 1
	var numStr string

	switch last {
	case 'g', 'G':
		multiplier = 1024 * 1024 * 1024
		numStr = s[:len(s)-1]
	case 'm', 'M':
		multiplier = 1024 * 1024
		numStr = s[:len(s)-1]
	case 'k', 'K':
		multiplier = 1024
		numStr = s[:len(s)-1]
	default:
		numStr = s
	}

	var val int64
	if _, err := fmt.Sscanf(numStr, "%d", &val); err != nil || val <= 0 {
		return 8 * 1024 * 1024
	}
	return val * multiplier
}
