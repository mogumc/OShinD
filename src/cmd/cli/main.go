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
		printError(fmt.Sprintf("unknown command: %s", cmd))
		fmt.Fprintln(os.Stderr, "Run 'oshind help' for usage.")
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
	fmt.Println(renderDivider("Commands"))
	printCmd("  download, dl", "  <url> [options]", "Start a download")
	printCmd("  probe", "        <url> [options]", "Probe server info")
	printCmd("  has-resume", "   <dir> <file>", "Check resume state")
	printCmd("  clear-resume", " <dir> <file>", "Clear resume state")
	printCmd("  version", "", "Show version")
	printCmd("  help", "", "Show this help")
	fmt.Println()
	fmt.Println(renderDivider("Options"))
	printOpt("-o, --output <dir>", "Output directory (default: .)")
	printOpt("-c, --connections <n>", "Max connections (default: 4)")
	printOpt("-s, --chunk-size <size>", "Chunk size, e.g. 8m, 1m")
	printOpt("-t, --timeout <duration>", "Request timeout (default: 30s)")
	printOpt("-r, --retry <n>", "Retry count (default: 3)")
	printOpt("-H, --header <key:value>", "Custom HTTP header")
	printOpt("-m, --multi-source <url>", "Additional download source")
	printOpt("-u, --user <username>", "FTP/SFTP username")
	printOpt("-p, --password <password>", "FTP/SFTP password")
	printOpt("--ftp-port <port>", "FTP port (default: 21)")
	printOpt("--sftp-port <port>", "SFTP port (default: 22)")
	printOpt("--skip-tls-verify", "Skip TLS verification")
	printOpt("--no-checksum", "Skip checksum verification")
	printOpt("--no-resume", "Disable resume support")
	printOpt("--checksum-type <type>", "Checksum algorithm (md5/sha1/sha256)")
	printOpt("--checksum-value <value>", "Expected checksum value")
	printOpt("--checksum <type:value>", "Checksum as type:value")
}

func printPlainUsage() {
	fmt.Println("OShinD v" + version)
	fmt.Println()
	fmt.Println("Usage: oshind <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  download, dl   <url> [options]   Start a download")
	fmt.Println("  probe          <url> [options]   Probe server info")
	fmt.Println("  has-resume     <dir> <file>      Check resume state")
	fmt.Println("  clear-resume   <dir> <file>      Clear resume state")
	fmt.Println("  version                          Show version")
	fmt.Println("  help                             Show this help")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -o, --output <dir>              Output directory (default: .)")
	fmt.Println("  -c, --connections <n>           Max connections (default: 4)")
	fmt.Println("  -s, --chunk-size <size>         Chunk size, e.g. 8m, 1m")
	fmt.Println("  -t, --timeout <duration>        Request timeout (default: 30s)")
	fmt.Println("  -r, --retry <n>                 Retry count (default: 3)")
	fmt.Println("  -H, --header <key:value>        Custom HTTP header")
	fmt.Println("  -m, --multi-source <url>        Additional download source")
	fmt.Println("  -u, --user <username>           FTP/SFTP username")
	fmt.Println("  -p, --password <password>       FTP/SFTP password")
	fmt.Println("  --ftp-port <port>               FTP port (default: 21)")
	fmt.Println("  --sftp-port <port>              SFTP port (default: 22)")
	fmt.Println("  --skip-tls-verify               Skip TLS verification")
	fmt.Println("  --no-checksum                   Skip checksum verification")
	fmt.Println("  --no-resume                     Disable resume support")
	fmt.Println("  --checksum-type <type>          Checksum algorithm (md5/sha1/sha256)")
	fmt.Println("  --checksum-value <value>        Expected checksum value")
	fmt.Println("  --checksum <type:value>         Checksum as type:value")
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
		printError("url is required")
		fmt.Fprintln(os.Stderr, "Usage: oshind download <url> [options]")
		os.Exit(1)
	}

	rawURL := args[0]
	config := parseArgs(args[1:])

	if isInteractive() {
		fmt.Println(InfoStyle.Render("↓ ") + fmt.Sprintf("Downloading: %s", rawURL))
	} else {
		fmt.Printf("Download: %s\n", rawURL)
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
		printError(fmt.Sprintf("tui error: %v", runErr))
		os.Exit(1)
	}

	// 从最终 model 获取结果
	m := finalModel.(progressModel)
	if m.err != nil {
		printError(fmt.Sprintf("download failed: %v", m.err))
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
		printError(fmt.Sprintf("download failed: %v", err))
		os.Exit(1)
	}
	printDownloadSummary(task)
}

// ── probe 命令 ──

func handleProbe(args []string) {
	if len(args) < 1 {
		printError("url is required")
		fmt.Fprintln(os.Stderr, "Usage: oshind probe <url> [options]")
		os.Exit(1)
	}

	rawURL := args[0]
	config := parseArgs(args[1:])

	if isInteractive() {
		fmt.Println(InfoStyle.Render("⟳ ") + fmt.Sprintf("Probing: %s", rawURL))
	} else {
		fmt.Printf("Probing: %s\n", rawURL)
	}

	result, err := downloader.ProbeFull(rawURL, config)
	if err != nil {
		printError(fmt.Sprintf("probe failed: %v", err))
		os.Exit(1)
	}

	fmt.Println()

	if isInteractive() {
		fmt.Println(renderDivider("Probe Result"))
		fmt.Println()
		fmt.Println(renderKV("URL", result.URL))

		if result.Metadata != nil {
			if result.Metadata.FileName != "" {
				fmt.Println(renderKV("File", result.Metadata.FileName))
			}
			if result.Metadata.Size > 0 {
				fmt.Println(renderKV("Size", formatBytes(result.Metadata.Size)))
			}
			if result.Metadata.ContentType != "" {
				fmt.Println(renderKV("Type", result.Metadata.ContentType))
			}
			fmt.Println(renderKV("Resume", fmt.Sprintf("%v", result.Metadata.SupportResume)))
			if result.Metadata.Checksum != "" {
				if result.Metadata.ChecksumType != "" {
					fmt.Println(renderKV("Checksum", fmt.Sprintf("%s:%s", result.Metadata.ChecksumType, result.Metadata.Checksum)))
				} else {
					fmt.Println(renderKV("Checksum", result.Metadata.Checksum))
				}
			}
		}
		if result.ServerInfo != nil {
			fmt.Println(renderKV("Server", fmt.Sprintf("%s:%s (%s)", result.ServerInfo.Host, result.ServerInfo.Port, result.ServerInfo.Scheme)))
		}
		if result.EstimatedSpeed > 0 {
			fmt.Println(renderKV("Speed", fmt.Sprintf("%s/s", formatBytes(int64(result.EstimatedSpeed)))))
		}
		fmt.Println()
	} else {
		fmt.Printf("URL:        %s\n", result.URL)
		if result.Metadata != nil {
			if result.Metadata.FileName != "" {
				fmt.Printf("File:       %s\n", result.Metadata.FileName)
			}
			if result.Metadata.Size > 0 {
				fmt.Printf("Size:       %s\n", formatBytes(result.Metadata.Size))
			}
			if result.Metadata.ContentType != "" {
				fmt.Printf("Type:       %s\n", result.Metadata.ContentType)
			}
			fmt.Printf("Resume:     %v\n", result.Metadata.SupportResume)
			if result.Metadata.Checksum != "" {
				if result.Metadata.ChecksumType != "" {
					fmt.Println(renderKV("Checksum", fmt.Sprintf("%s:%s", result.Metadata.ChecksumType, result.Metadata.Checksum)))
				} else {
					fmt.Println(renderKV("Checksum", result.Metadata.Checksum))
				}
			}
		}
		if result.ServerInfo != nil {
			fmt.Printf("Server:     %s:%s (%s)\n", result.ServerInfo.Host, result.ServerInfo.Port, result.ServerInfo.Scheme)
		}
		if result.EstimatedSpeed > 0 {
			fmt.Printf("Speed:      %s/s\n", formatBytes(int64(result.EstimatedSpeed)))
		}
	}
}

// ── has-resume 命令 ──

func handleHasResume(args []string) {
	if len(args) < 2 {
		printError("output_dir and file_name are required")
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
			fmt.Println(WarningStyle.Render("  ⚠ ") + "No resume state found")
		} else {
			fmt.Println("No resume state found")
		}
		return
	}

	if isInteractive() {
		fmt.Println()
		fmt.Println(renderDivider("Resume State"))
		fmt.Println()
		fmt.Println(renderKV("URL", state.URL))
		fmt.Println(renderKV("File", state.FileName))
		fmt.Println(renderKV("Size", formatBytes(state.TotalSize)))
		fmt.Println(renderKV("ChunkSize", formatBytes(state.ChunkSize)))
		fmt.Println(renderKV("Chunks", fmt.Sprintf("%d", len(state.Chunks))))

		if len(state.Chunks) > 0 {
			fmt.Println()
			chunkHeader := fmt.Sprintf("  %-6s  %-20s  %-20s", "ID", "Start", "End")
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
				fmt.Println(InfoStyle.Render(fmt.Sprintf("    ... (%d more)", len(state.Chunks)-12)))
			}
		}
		fmt.Println()
	} else {
		fmt.Printf("Resume state found:\n")
		fmt.Printf("  URL:       %s\n", state.URL)
		fmt.Printf("  File:      %s\n", state.FileName)
		fmt.Printf("  Size:      %s\n", formatBytes(state.TotalSize))
		fmt.Printf("  ChunkSize: %s\n", formatBytes(state.ChunkSize))
		fmt.Printf("  Chunks:    %d\n", len(state.Chunks))
		for i, c := range state.Chunks {
			fmt.Printf("    [%d] %s - %s\n", c.ID, formatBytes(c.Start), formatBytes(c.End))
			if i > 10 {
				fmt.Printf("    ... (%d more)\n", len(state.Chunks)-11)
				break
			}
		}
	}
}

// ── clear-resume 命令 ──

func handleClearResume(args []string) {
	if len(args) < 2 {
		printError("output_dir and file_name are required")
		fmt.Fprintln(os.Stderr, "Usage: oshind clear-resume <dir> <file>")
		os.Exit(1)
	}

	outputDir := args[0]
	fileName := args[1]
	outputPath := filepath.Join(outputDir, fileName)
	oshinPath := downloader.GetOShinStatePath(outputPath)

	if err := downloader.RemoveOShinState(oshinPath); err != nil {
		printError(fmt.Sprintf("clear failed: %v", err))
		os.Exit(1)
	}

	if isInteractive() {
		fmt.Println(SuccessStyle.Render("  ✓ ") + "Resume state cleared")
	} else {
		fmt.Println("Resume state cleared")
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
