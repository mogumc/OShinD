// OShinD - Command Line Interface
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mogumc/oshind/pkg/downloader"
	"github.com/mogumc/oshind/types"
)

var version = "1.3.0"

func main() {
	// 解析命令行参数
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// 获取命令
	cmd := os.Args[1]

	switch cmd {
	case "download", "dl":
		handleDownload(os.Args[2:])
	case "probe":
		handleProbe(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("OShinD v%s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

// printUsage 打印使用说明
func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  oshind download <url> [options]   Download a file")
	fmt.Println("  oshind probe <url> [options]      Probe server and show download info")
	fmt.Println("  oshind version                    Show version")
	fmt.Println("  oshind help                       Show this help")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -o, --output <path>        Output file or directory path")
	fmt.Println("  -c, --connections <n>      Max connections (1-64, default: 4)")
	fmt.Println("  -s, --chunk-size <size>    Chunk size in bytes or human-readable (e.g. 4m, 512k, default: 8m)")
	fmt.Println("  -t, --timeout <dur>        Timeout duration (default: 30s)")
	fmt.Println("  -r, --retry <n>            Retry count (default: 3)")
	fmt.Println("  -u, --user <user>          FTP/SFTP username")
	fmt.Println("  -p, --password <pass>      FTP/SFTP password")
	fmt.Println("      --ftp-port <port>      FTP port (default: 21)")
	fmt.Println("      --sftp-port <port>     SFTP port (default: 22)")
	fmt.Println("      --skip-tls-verify      Skip TLS certificate verification")
	fmt.Println("      --no-checksum          Disable automatic checksum verification")
	fmt.Println("      --no-resume            Force fresh download, ignore .oshin state")
	fmt.Println("  -m, --multi-source <url>   Additional source URL (can be repeated)")
	fmt.Println("      --checksum-type <type> Checksum type (md5/sha256)")
	fmt.Println("      --checksum-value <hex> Expected checksum value")
	fmt.Println("      --checksum <type:value> Set checksum type and value (e.g. md5:abc123...)")
	fmt.Println("  -H, --header <k:v>        Custom HTTP header (can be repeated)")
}

// handleDownload 处理下载命令
func handleDownload(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: URL is required")
		os.Exit(1)
	}

	// 解析参数
	rawURL := args[0]
	config := parseArgs(args[1:])

	// 创建引擎
	engine := downloader.NewEngine(config)

	// 创建可取消的上下文，监听 SIGINT/SIGTERM
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 显示协议信息
	protocol, err := downloader.DetectProtocol(rawURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Protocol:    %s\n", protocol)
	fmt.Printf("URL:         %s\n", rawURL)
	fmt.Printf("Output:      %s\n", config.OutputDir)
	fmt.Printf("Connections: %d\n", config.MaxConnections)
	if len(config.MultiSources) > 0 {
		fmt.Printf("Sources:     %d (multi-source enabled)\n", len(config.MultiSources)+1)
	}
	if len(config.Headers) > 0 {
		fmt.Printf("Headers:     %d custom\n", len(config.Headers))
	}
	fmt.Println()

	// 执行下载（engine.Download内部会启动ProgressReporter显示进度）
	task, err := engine.Download(ctx, rawURL, config)

	// 检查是否因信号中断退出
	if err != nil && ctx.Err() != nil {
		// 被 SIGINT/SIGTERM 中断
		// 先停止进度报告器（停止 goroutine + 清除 ANSI 进度显示）
		// 再打印中断状态，防止 ProgressReporter 继续覆盖输出
		engine.StopReporter(task.ID)
		fmt.Println()
		printInterruptStatus(task)
		os.Exit(130) // 标准 Ctrl+C 退出码
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\nDownload failed: %v\n", err)
		os.Exit(1)
	}

	// 显示最终结果
	fmt.Printf("\n\n")
	fmt.Println("========================================")
	fmt.Println("  Download completed!")
	fmt.Println("========================================")
	fmt.Printf("  File:     %s\n", task.FileName)
	fmt.Printf("  Path:     %s\n", task.OutputPath)
	fmt.Printf("  Size:     %s\n", formatBytes(task.FileSize))
	fmt.Printf("  Status:   %s\n", task.GetStatus())

	// 显示校验结果
	if task.Verify != nil {
		if task.Verify.Skipped {
			fmt.Printf("  Verify:   skipped (no checksum available)\n")
		} else if task.Verify.Passed {
			fmt.Printf("  Verify:   PASSED [%s]\n", task.Verify.Method)
			fmt.Printf("    Expected: %s\n", task.Verify.Expected)
			fmt.Printf("    Actual:   %s\n", task.Verify.Actual)
		} else {
			fmt.Printf("  Verify:   FAILED [%s]\n", task.Verify.Method)
			fmt.Printf("    Expected: %s\n", task.Verify.Expected)
			fmt.Printf("    Actual:   %s\n", task.Verify.Actual)
		}
	}
	fmt.Println("========================================")
}

// printInterruptStatus 打印中断时的下载状态
func printInterruptStatus(task *types.DownloadTask) {
	fmt.Println("========================================")
	fmt.Println("  Download Interrupted")
	fmt.Println("========================================")

	if task == nil {
		fmt.Println("  No active task.")
		return
	}

	fmt.Printf("  URL:    %s\n", task.URL)
	fmt.Printf("  File:   %s\n", task.FileName)
	fmt.Printf("  Status: %s\n", task.GetStatus())

	if task.Metadata.Size > 0 {
		downloaded := task.Progress.GetDownloaded()
		progress := float64(downloaded) / float64(task.Metadata.Size) * 100
		fmt.Printf("  Size:   %s / %s (%.1f%%)\n",
			formatBytes(downloaded),
			formatBytes(task.Metadata.Size),
			progress)
	} else {
		downloaded := task.Progress.GetDownloaded()
		fmt.Printf("  Size:   %s (total unknown)\n", formatBytes(downloaded))
	}

	// 显示当前速度（使用全局速度计算）
	speed := task.Progress.CalculateSpeed()
	fmt.Printf("  Speed:  %s/s\n", formatBytes(int64(speed)))

	// 显示线程和分块统计
	fmt.Printf("  Active Threads: %d\n", task.Progress.GetActiveThreads())
	fmt.Printf("  Remaining Chunks: %d\n", task.Progress.GetRemainingChunks())
	fmt.Printf("  Failed Chunks: %d\n", task.Progress.GetFailedChunks())

	// 显示 .oshin 状态文件路径
	outputPath := task.Config.OutputDir + "/" + task.FileName
	oshinPath := downloader.GetOShinStatePath(outputPath)
	if _, err := os.Stat(oshinPath); err == nil {
		fmt.Printf("  State File: %s (resume available)\n", oshinPath)
	}

	// 显示分片统计（使用线程安全快照）
	snapshots := task.GetChunkSnapshots()
	completed, downloading, pending, failed := 0, 0, 0, 0
	for _, snap := range snapshots {
		switch snap.Status {
		case types.ChunkStatusCompleted:
			completed++
		case types.ChunkStatusDownloading:
			downloading++
		case types.ChunkStatusPending:
			pending++
		case types.ChunkStatusFailed:
			failed++
		}
	}
	fmt.Printf("  Chunks: %d total", len(snapshots))
	if completed > 0 {
		fmt.Printf("  [done: %d]", completed)
	}
	if downloading > 0 {
		fmt.Printf("  [active: %d]", downloading)
	}
	if pending > 0 {
		fmt.Printf("  [pending: %d]", pending)
	}
	if failed > 0 {
		fmt.Printf("  [failed: %d]", failed)
	}
	fmt.Println()
	fmt.Println("========================================")
}

// handleProbe 处理探测命令
func handleProbe(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: URL is required")
		os.Exit(1)
	}

	// 解析参数
	rawURL := args[0]
	config := parseArgs(args[1:])

	fmt.Printf("Probing:     %s\n", rawURL)
	fmt.Println()

	// 执行完整探测
	result, err := downloader.ProbeFull(rawURL, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Probe failed: %v\n", err)
		os.Exit(1)
	}

	// 显示探测结果
	fmt.Println("=== Probe Results ===")
	if result.ServerInfo != nil {
		fmt.Printf("Host:        %s\n", result.ServerInfo.Host)
		fmt.Printf("Port:        %s\n", result.ServerInfo.Port)
		fmt.Printf("Scheme:      %s\n", result.ServerInfo.Scheme)
	}
	fmt.Println()

	if result.Metadata != nil {
		fmt.Println("=== File Info ===")
		if result.Metadata.Size > 0 {
			fmt.Printf("Size:        %s\n", formatBytes(result.Metadata.Size))
		} else {
			fmt.Println("Size:        Unknown")
		}
		fmt.Printf("Content-Type:%s\n", result.Metadata.ContentType)
		fmt.Printf("Resume:      %v\n", result.Metadata.SupportResume)
		if result.Metadata.ETag != "" {
			fmt.Printf("ETag:        %s\n", result.Metadata.ETag)
		}
		if result.Metadata.Checksum != "" {
			fmt.Printf("MD5:         %s\n", result.Metadata.Checksum)
		}
	}
	fmt.Println()

	if result.SpeedTestError != "" {
		fmt.Printf("Speed test:  Failed (%s)\n", result.SpeedTestError)
	} else {
		fmt.Printf("Speed test:  %s/s\n", formatBytes(int64(result.EstimatedSpeed)))
	}

	if result.Error != "" {
		fmt.Printf("Error:       %s\n", result.Error)
	}
}

// parseArgs 解析命令行参数
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
			// 快捷参数：同时指定类型和值，格式为 "type:value" (例如 "md5:abc123...")
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

// formatBytes 格式化字节数为可读格式
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

// parseSize 解析带单位的大小字符串（如 "4m", "512k", "1g"）
func parseSize(s string) int64 {
	if len(s) == 0 {
		return 8 * 1024 * 1024 // 默认 8MB
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
