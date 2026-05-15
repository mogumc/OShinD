package downloader

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/mogumc/oshind/types"
)

// Engine 下载引擎
type Engine struct {
	mu             sync.RWMutex
	tasks          map[string]*types.DownloadTask
	cancelFuncs    map[string]context.CancelFunc // 任务取消函数（用于暂停/取消下载）
	reporters      map[string]*ProgressReporter  // 按任务 ID 管理进度报告器（支持并发下载）
	httpDownloader *HTTPDownloader
	ftpDownloader  *FTPDownloader
	sftpDownloader *SFTPDownloader
	verifier       *Verifier
	onReport       func(lines []string) // 外部进度输出回调
	onStop         func(maxLines int)   // 外部进度停止回调（ANSI 清行）
}

// NewEngine 创建新的下载引擎
func NewEngine(config *types.DownloadConfig) *Engine {
	if config == nil {
		config = types.DefaultConfig()
	}

	return &Engine{
		tasks:          make(map[string]*types.DownloadTask),
		cancelFuncs:    make(map[string]context.CancelFunc),
		reporters:      make(map[string]*ProgressReporter),
		httpDownloader: NewHTTPDownloader(config),
		ftpDownloader:  NewFTPDownloader(),
		sftpDownloader: NewSFTPDownloader(),
		verifier:       NewVerifier(),
	}
}

// SetOutputCallbacks 设置外部输出回调（用于 CLI 等需要 human-readable 输出的场景）
// onReport: 接收进度行并输出
// onStop: 接收最大行数，用于清除 ANSI 进度区
func (e *Engine) SetOutputCallbacks(onReport func(lines []string), onStop func(maxLines int)) {
	e.onReport = onReport
	e.onStop = onStop
}

// Download 创建并执行下载任务
func (e *Engine) Download(ctx context.Context, rawURL string, config *types.DownloadConfig) (*types.DownloadTask, error) {
	if config == nil {
		config = types.DefaultConfig()
	}

	// 验证配置
	types.ValidateConfig(config)

	// 检测协议
	protocol, err := DetectProtocol(rawURL)
	if err != nil {
		return nil, fmt.Errorf("unsupported protocol: %w", err)
	}

	// 创建任务
	task := types.NewDownloadTask(rawURL, protocol, config)
	task.FileName = ExtractFileInfo(rawURL)

	// 为任务创建独立的可取消 context（支持外部暂停/取消）
	// 即使父 ctx 被取消，也可以通过 taskCtx 独立控制
	taskCtx, taskCancel := context.WithCancel(ctx)

	// 添加到任务列表
	e.mu.Lock()
	e.tasks[task.ID] = task
	e.cancelFuncs[task.ID] = taskCancel
	e.mu.Unlock()

	// 创建进度报告器（500ms 间隔，小文件也能及时显示）
	// 注意：不在此处启动，由各协议 Download() 在所有预下载消息输出完毕后启动
	reporter := NewProgressReporter(task, 500*time.Millisecond)
	reporter.OnReport = e.onReport
	reporter.OnStop = e.onStop
	e.mu.Lock()
	e.reporters[task.ID] = reporter
	e.mu.Unlock()

	// 根据协议执行下载
	switch protocol {
	case types.ProtocolHTTP, types.ProtocolHTTPS:
		// 探测服务器
		task.SetStatus(types.TaskStatusProbing)
		metadata, err := Probe(rawURL, config)
		if err != nil {
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return task, fmt.Errorf("probe failed: %w", err)
		}
		task.Metadata = metadata

		if metadata.FileName != "" {
			task.FileName = metadata.FileName
		}

		// 下载前检查已存在文件
		outputPath := e.getOutputPath(task)
		skip, checkedPath, checkErr := checkExistingFile(outputPath, task)
		if checkErr != nil {
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return task, checkErr
		}
		if skip {
			task.OutputPath = checkedPath
			task.FileSize = task.Metadata.Size
			task.SetStatus(types.TaskStatusCompleted)
			e.cleanupReporter(task.ID)
			e.cleanupCancelFunc(task.ID)
			return task, nil
		}
		// 如果发生了重命名，更新文件名
		if checkedPath != outputPath {
			task.FileName = filepath.Base(checkedPath)
		}

		// 执行 HTTP 下载（内部会在适当时机启动 reporter）
		if err := e.httpDownloader.Download(taskCtx, task, reporter); err != nil {
			e.cleanupReporter(task.ID)
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return task, fmt.Errorf("HTTP download failed: %w", err)
		}
	case types.ProtocolFTP:
		// FTP 协议，直接执行下载（内部会在适当时机启动 reporter）
		if err := e.ftpDownloader.Download(taskCtx, task, reporter); err != nil {
			e.cleanupReporter(task.ID)
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return task, fmt.Errorf("FTP download failed: %w", err)
		}

	case types.ProtocolSFTP:
		// SFTP 协议，直接执行下载（内部会在适当时机启动 reporter）
		if err := e.sftpDownloader.Download(taskCtx, task, reporter); err != nil {
			e.cleanupReporter(task.ID)
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return task, fmt.Errorf("SFTP download failed: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported protocol: %v", protocol)
	}

	// 下载完成，执行校验
	e.cleanupReporter(task.ID)
	task.SetStatus(types.TaskStatusVerifying)
	outputPath := e.getOutputPath(task)
	verifyResult := VerifyTask(task, outputPath)
	task.Verify = verifyResult
	if !verifyResult.Passed && !verifyResult.Skipped {
		task.SetStatus(types.TaskStatusFailed)
		e.cleanupCancelFunc(task.ID)
		return task, fmt.Errorf("checksum verification failed: expected %s, got %s", verifyResult.Expected, verifyResult.Actual)
	}

	// 填充输出文件信息
	task.OutputPath = outputPath
	task.FileSize = task.Metadata.Size

	// 下载完成
	task.SetStatus(types.TaskStatusCompleted)
	e.cleanupCancelFunc(task.ID)

	return task, nil
}

// SubmitDownload 异步提交下载任务
func (e *Engine) SubmitDownload(rawURL string, config *types.DownloadConfig) (string, error) {
	if config == nil {
		config = types.DefaultConfig()
	}

	// 验证配置
	types.ValidateConfig(config)

	// 检测协议
	protocol, err := DetectProtocol(rawURL)
	if err != nil {
		return "", fmt.Errorf("unsupported protocol: %w", err)
	}

	// 创建任务
	task := types.NewDownloadTask(rawURL, protocol, config)
	task.FileName = ExtractFileInfo(rawURL)

	// 为任务创建独立的可取消 context
	taskCtx, taskCancel := context.WithCancel(context.Background())

	// 添加到任务列表
	e.mu.Lock()
	e.tasks[task.ID] = task
	e.cancelFuncs[task.ID] = taskCancel

	// 创建进度报告器
	reporter := NewProgressReporter(task, 500*time.Millisecond)
	reporter.OnReport = e.onReport
	reporter.OnStop = e.onStop
	e.reporters[task.ID] = reporter
	e.mu.Unlock()

	// 异步执行下载
	go func() {
		// 根据协议执行下载
		switch protocol {
		case types.ProtocolHTTP, types.ProtocolHTTPS:
			// 探测服务器
			task.SetStatus(types.TaskStatusProbing)
			metadata, probeErr := Probe(rawURL, config)
			if probeErr != nil {
				task.SetStatus(types.TaskStatusFailed)
				e.cleanupCancelFunc(task.ID)
				return
			}
			task.Metadata = metadata

			if metadata.FileName != "" {
				task.FileName = metadata.FileName
			}

			// 下载前检查已存在文件（强制全新下载时跳过此检查）
			outputPath := e.getOutputPath(task)
			if !task.Config.NoResume {
				skip, checkedPath, checkErr := checkExistingFile(outputPath, task)
				if checkErr != nil {
					task.SetStatus(types.TaskStatusFailed)
					e.cleanupCancelFunc(task.ID)
					return
				}
				if skip {
					task.OutputPath = checkedPath
					task.FileSize = task.Metadata.Size
					task.SetStatus(types.TaskStatusCompleted)
					e.cleanupReporter(task.ID)
					e.cleanupCancelFunc(task.ID)
					return
				}
				if checkedPath != outputPath {
					task.FileName = filepath.Base(checkedPath)
				}
			}

			// 执行 HTTP 下载
			if dlErr := e.httpDownloader.Download(taskCtx, task, reporter); dlErr != nil {
				e.cleanupReporter(task.ID)
				if task.GetStatus() != types.TaskStatusPaused {
					task.SetStatus(types.TaskStatusFailed)
				}
				e.cleanupCancelFunc(task.ID)
				return
			}

		case types.ProtocolFTP:
			if dlErr := e.ftpDownloader.Download(taskCtx, task, reporter); dlErr != nil {
				e.cleanupReporter(task.ID)
				if task.GetStatus() != types.TaskStatusPaused {
					task.SetStatus(types.TaskStatusFailed)
				}
				e.cleanupCancelFunc(task.ID)
				return
			}

		case types.ProtocolSFTP:
			if dlErr := e.sftpDownloader.Download(taskCtx, task, reporter); dlErr != nil {
				e.cleanupReporter(task.ID)
				if task.GetStatus() != types.TaskStatusPaused {
					task.SetStatus(types.TaskStatusFailed)
				}
				e.cleanupCancelFunc(task.ID)
				return
			}
		}

		// 下载完成，执行校验
		e.cleanupReporter(task.ID)
		task.SetStatus(types.TaskStatusVerifying)
		outputPath := e.getOutputPath(task)
		verifyResult := VerifyTask(task, outputPath)
		task.Verify = verifyResult
		if !verifyResult.Passed && !verifyResult.Skipped {
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return
		}

		// 填充输出文件信息
		task.OutputPath = outputPath
		task.FileSize = task.Metadata.Size

		// 下载完成
		task.SetStatus(types.TaskStatusCompleted)
		e.cleanupCancelFunc(task.ID)
	}()

	return task.ID, nil
}

// GetTask 获取任务
func (e *Engine) GetTask(id string) (*types.DownloadTask, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	task, ok := e.tasks[id]
	return task, ok
}

// ListTasks 列出所有任务
func (e *Engine) ListTasks() []*types.DownloadTask {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tasks := make([]*types.DownloadTask, 0, len(e.tasks))
	for _, task := range e.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

// RemoveTask 移除任务
func (e *Engine) RemoveTask(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.tasks[id]; ok {
		// 清理取消函数
		if cancel, ok := e.cancelFuncs[id]; ok {
			cancel() // 先取消正在运行的下载
			delete(e.cancelFuncs, id)
		}
		// 清理进度报告器
		if reporter, ok := e.reporters[id]; ok {
			if reporter != nil {
				reporter.Stop()
			}
			delete(e.reporters, id)
		}
		delete(e.tasks, id)
		return true
	}
	return false
}

// PauseTask 暂停任务
// 取消下载并将状态设置为 PAUSED（而非 FAILED）
func (e *Engine) PauseTask(id string) error {
	e.mu.RLock()
	cancel, ok := e.cancelFuncs[id]
	task, taskOk := e.tasks[id]
	e.mu.RUnlock()

	if !ok || !taskOk {
		return fmt.Errorf("task %s not found or already completed", id)
	}

	// 先设置状态为 PAUSED，再取消下载
	// 这样 http.go 检测到 ctx.Done() 时会看到 PAUSED 状态
	task.SetStatus(types.TaskStatusPaused)

	// 停止进度报告器
	e.StopReporter(id)

	// 触发取消，下载中的 goroutine 会通过 ctx.Done() 退出
	cancel()

	return nil
}

// ResumeTask 恢复任务（预留接口）
func (e *Engine) ResumeTask(id string) error {
	// TODO: 实现任务恢复
	return fmt.Errorf("resume not implemented yet")
}

// CancelTask 取消/暂停任务
// 调用逻辑和 Ctrl+C 一致：触发 context 取消，所有下载 goroutine 收到信号后退出
func (e *Engine) CancelTask(id string) error {
	e.mu.RLock()
	cancel, ok := e.cancelFuncs[id]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("task %s not found or already completed", id)
	}
	// 触发取消，下载中的 goroutine 会通过 ctx.Done() 退出
	cancel()
	return nil
}

// cleanupCancelFunc 清理任务的取消函数
// 任务完成或失败后调用，释放 map 中的引用
func (e *Engine) cleanupCancelFunc(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.cancelFuncs, id)
}

// StopReporter 停止指定任务的进度报告器
func (e *Engine) StopReporter(taskID string) {
	e.mu.RLock()
	reporter, ok := e.reporters[taskID]
	e.mu.RUnlock()
	if ok && reporter != nil {
		reporter.Stop()
	}
}

// cleanupReporter 停止并移除进度报告器（任务完成/失败后调用）
func (e *Engine) cleanupReporter(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if reporter, ok := e.reporters[id]; ok {
		if reporter != nil {
			reporter.Stop()
		}
		delete(e.reporters, id)
	}
}

// getOutputPath 获取输出文件路径
func (e *Engine) getOutputPath(task *types.DownloadTask) string {
	// 如果指定了文件名，使用指定的文件名
	if task.FileName != "" {
		return filepath.Join(task.Config.OutputDir, task.FileName)
	}

	// 从 URL 提取文件名
	fileName := ExtractFileInfo(task.URL)
	return filepath.Join(task.Config.OutputDir, fileName)
}
