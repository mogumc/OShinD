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
	cancelFuncs    map[string]context.CancelFunc
	reporters      map[string]*ProgressReporter
	httpDownloader *HTTPDownloader
	ftpDownloader  *FTPDownloader
	sftpDownloader *SFTPDownloader
	verifier       *Verifier
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

// Download 创建并执行下载任务
func (e *Engine) Download(ctx context.Context, rawURL string, config *types.DownloadConfig, onReport func(lines []string), onStop func(maxLines int)) (*types.DownloadTask, error) {
	if config == nil {
		config = types.DefaultConfig()
	}

	types.ValidateConfig(config)

	protocol, err := DetectProtocol(rawURL)
	if err != nil {
		return nil, fmt.Errorf("unsupported protocol: %w", err)
	}

	task := types.NewDownloadTask(rawURL, protocol, config)
	task.FileName = ExtractFileInfo(rawURL)

	// 独立的可取消 context，不受父 ctx 影响
	taskCtx, taskCancel := context.WithCancel(ctx)

	e.mu.Lock()
	e.tasks[task.ID] = task
	e.cancelFuncs[task.ID] = taskCancel
	e.mu.Unlock()

	// 不在此处启动 reporter，由各协议 Download() 在预下载消息输出完毕后启动
	reporter := NewProgressReporter(task, 500*time.Millisecond)
	reporter.OnReport = onReport
	reporter.OnStop = onStop
	e.mu.Lock()
	e.reporters[task.ID] = reporter
	e.mu.Unlock()

	// 根据协议执行下载
	switch protocol {
	case types.ProtocolHTTP, types.ProtocolHTTPS:
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
		if checkedPath != outputPath {
			task.FileName = filepath.Base(checkedPath)
		}

		if err := e.httpDownloader.Download(taskCtx, task, reporter); err != nil {
			e.cleanupReporter(task.ID)
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return task, fmt.Errorf("HTTP download failed: %w", err)
		}
	case types.ProtocolFTP:
		if err := e.ftpDownloader.Download(taskCtx, task, reporter); err != nil {
			e.cleanupReporter(task.ID)
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return task, fmt.Errorf("FTP download failed: %w", err)
		}

	case types.ProtocolSFTP:
		if err := e.sftpDownloader.Download(taskCtx, task, reporter); err != nil {
			e.cleanupReporter(task.ID)
			task.SetStatus(types.TaskStatusFailed)
			e.cleanupCancelFunc(task.ID)
			return task, fmt.Errorf("SFTP download failed: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported protocol: %v", protocol)
	}

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

	task.OutputPath = outputPath
	task.FileSize = task.Metadata.Size

	task.SetStatus(types.TaskStatusCompleted)
	e.cleanupCancelFunc(task.ID)

	return task, nil
}

// SubmitDownload 异步提交下载任务
// onReport: 外部进度输出回调（传 nil 表示不输出进度）
// onStop: 外部进度停止回调（ANSI 清行，传 nil 表示不处理）
func (e *Engine) SubmitDownload(rawURL string, config *types.DownloadConfig, onReport func(lines []string), onStop func(maxLines int)) (string, error) {
	if config == nil {
		config = types.DefaultConfig()
	}

	types.ValidateConfig(config)

	protocol, err := DetectProtocol(rawURL)
	if err != nil {
		return "", fmt.Errorf("unsupported protocol: %w", err)
	}

	task := types.NewDownloadTask(rawURL, protocol, config)
	task.FileName = ExtractFileInfo(rawURL)

	taskCtx, taskCancel := context.WithCancel(context.Background())

	e.mu.Lock()
	e.tasks[task.ID] = task
	e.cancelFuncs[task.ID] = taskCancel

	reporter := NewProgressReporter(task, 500*time.Millisecond)
	reporter.OnReport = onReport
	reporter.OnStop = onStop
	e.reporters[task.ID] = reporter
	e.mu.Unlock()

	go func() {
		// 根据协议执行下载
		switch protocol {
		case types.ProtocolHTTP, types.ProtocolHTTPS:
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

			// 下载前检查已存在文件（NoResume 时跳过）
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

		task.OutputPath = outputPath
		task.FileSize = task.Metadata.Size

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
		if cancel, ok := e.cancelFuncs[id]; ok {
			cancel()
			delete(e.cancelFuncs, id)
		}
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

	// 先设置 PAUSED 再取消，http.go 检测到 ctx.Done() 时看到的是 PAUSED 而非 FAILED
	task.SetStatus(types.TaskStatusPaused)

	e.StopReporter(id)

	cancel()

	return nil
}

// ResumeTask 恢复暂停/失败的任务，移除旧任务后重新提交下载（自动检测 .oshin 断点状态）
func (e *Engine) ResumeTask(id string, onReport func(lines []string), onStop func(maxLines int)) (string, error) {
	e.mu.RLock()
	task, ok := e.tasks[id]
	e.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("task %s not found", id)
	}

	status := task.GetStatus()
	if status != types.TaskStatusPaused && status != types.TaskStatusFailed {
		return "", fmt.Errorf("task %s is not resumable (status: %s)", id, status)
	}

	// 标记为恢复中（FFI 轮询时可见）
	task.SetStatus(types.TaskStatusResuming)

	// RemoveTask 会清理 task 引用，先保存原始 URL 和 Config
	rawURL := task.URL
	config := task.Config

	// 移除旧任务后重新提交，.oshin 状态文件会被自动检测并恢复
	e.RemoveTask(id)

	newID, err := e.SubmitDownload(rawURL, config, onReport, onStop)
	if err != nil {
		return "", fmt.Errorf("resume failed: %w", err)
	}

	return newID, nil
}

// CancelTask 取消任务（触发 context 取消，下载 goroutine 通过 ctx.Done() 退出）
func (e *Engine) CancelTask(id string) error {
	e.mu.RLock()
	cancel, ok := e.cancelFuncs[id]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("task %s not found or already completed", id)
	}
	cancel()
	return nil
}

// cleanupCancelFunc 任务完成后释放取消函数引用
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

// cleanupReporter 停止并移除进度报告器
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
	if task.FileName != "" {
		return filepath.Join(task.Config.OutputDir, task.FileName)
	}
	fileName := ExtractFileInfo(task.URL)
	return filepath.Join(task.Config.OutputDir, fileName)
}
