package downloader

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mogumc/oshind/types"
)

// checkExistingFile 检查已存在的同名文件是否与待下载文件一致
func checkExistingFile(outputPath string, task *types.DownloadTask) (skip bool, finalPath string, err error) {
	fi, statErr := os.Stat(outputPath)
	if statErr != nil || fi == nil {
		// 文件不存在，正常下载
		return false, outputPath, nil
	}

	// 文件存在，确定有效校验值
	var checksumType, checksumValue string

	// 优先级：用户指定 > probe 得到的 Checksum > ETag MD5
	if task.Config.ChecksumType != "" && task.Config.ChecksumValue != "" {
		checksumType = task.Config.ChecksumType
		checksumValue = task.Config.ChecksumValue
	} else if task.Metadata.Checksum != "" {
		checksumType = "md5"
		checksumValue = task.Metadata.Checksum
	} else if task.Metadata.ETag != "" && isMD5Hex(task.Metadata.ETag) {
		checksumType = "md5"
		checksumValue = task.Metadata.ETag
	}

	if checksumType != "" && checksumValue != "" {
		// 有校验值：计算文件校验和并比较
		verifier := NewVerifier()
		actual, calcErr := verifier.CalculateChecksum(outputPath, checksumType)
		if calcErr != nil {
			return false, "", fmt.Errorf("failed to calculate checksum: %w", calcErr)
		}
		if strings.EqualFold(actual, checksumValue) {
			fmt.Printf("  [i] File already exists and checksum matches, skipping download\n")
			fmt.Printf("      Path: %s (%s)\n", outputPath, formatBytes(fi.Size()))
			return true, outputPath, nil
		}
		// 校验不一致，重命名
		fmt.Printf("  [!] File exists but checksum mismatch (expected %s, got %s), renaming\n",
			checksumValue, actual)
		newPath := findAvailablePath(outputPath)
		if err := os.Rename(outputPath, newPath); err != nil {
			return false, "", fmt.Errorf("failed to rename existing file: %w", err)
		}
		fmt.Printf("      Renamed to: %s\n", newPath)
		return false, newPath, nil
	}

	// 无校验值：比较文件大小
	if task.Metadata.Size > 0 && fi.Size() == task.Metadata.Size {
		fmt.Printf("  [i] File already exists and size matches, skipping download\n")
		fmt.Printf("      Path: %s (%s)\n", outputPath, formatBytes(fi.Size()))
		return true, outputPath, nil
	}

	// 大小不一致，重命名
	fmt.Printf("  [!] File exists but size mismatch (expected %s, got %s), renaming\n",
		formatBytes(task.Metadata.Size), formatBytes(fi.Size()))
	newPath := findAvailablePath(outputPath)
	if err := os.Rename(outputPath, newPath); err != nil {
		return false, "", fmt.Errorf("failed to rename existing file: %w", err)
	}
	fmt.Printf("      Renamed to: %s\n", newPath)
	return false, newPath, nil
}

// validateResumeFile 校验续传时临时文件的一致性
func validateResumeFile(state *OShinState, tempPath string, task *types.DownloadTask) bool {
	fi, err := os.Stat(tempPath)
	if err != nil || fi == nil {
		return false
	}

	// 校验逻辑（优先级递减）：
	// 1. state 有 checksum → 计算临时文件 checksum 比较
	if state.ET != "" {
		parts := strings.SplitN(state.ET, ":", 2)
		if len(parts) == 2 {
			checksumType, checksumValue := parts[0], parts[1]
			verifier := NewVerifier()
			actual, calcErr := verifier.CalculateChecksum(tempPath, checksumType)
			if calcErr == nil && strings.EqualFold(actual, checksumValue) {
				return true
			}
			// checksum 不匹配
			fmt.Printf("  [!] Resume checksum mismatch (expected %s, got %s)\n", checksumValue, actual)
			return false
		}
	}

	// 2. 无 checksum 但有 ETag MD5 → 计算临时文件前 chunk_size 字节的 MD5
	if task.Metadata.ETag != "" && isMD5Hex(task.Metadata.ETag) && state.ChunkSize > 0 {
		partialMD5, calcErr := CalculatePartialMD5(tempPath, state.ChunkSize)
		if calcErr == nil && strings.EqualFold(partialMD5, task.Metadata.ETag) {
			return true
		}
		// partial MD5 不匹配（可能是下载过程中断导致前段数据不一致）
		// 这种情况下不作为硬性失败，继续下一步
	}

	// 3. 只比较文件大小
	if fi.Size() == state.TotalSize {
		return true
	}

	// 所有校验都失败
	fmt.Printf("  [!] Resume validation failed: temp file size %s, expected %s\n",
		formatBytes(fi.Size()), formatBytes(state.TotalSize))
	return false
}

// findAvailablePath 查找可用的重命名路径
// file.zip → file.zip.1 → file.zip.2 ...
func findAvailablePath(outputPath string) string {
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s.%d", outputPath, i)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// isPermanentFailure 判断是否为永久性失败（服务器限制连接数）
func isPermanentFailure(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// 403 Forbidden
	if strings.Contains(errStr, "forbidden") || strings.Contains(errStr, "403") {
		return true
	}
	// 连接被拒绝
	if strings.Contains(errStr, "connection refused") {
		return true
	}
	// Too many connections
	if strings.Contains(errStr, "too many") {
		return true
	}
	return false
}

// HTTPDownloader HTTP/HTTPS 下载器
type HTTPDownloader struct {
	client         *http.Client
	transport      *http.Transport         // 主 transport（用于快速关闭空闲连接）
	clients        map[string]*http.Client // 多来源下载时，每个 URL 对应一个 client
	mu             sync.Mutex              // 保护 maxConnTime 和 fastFailClient 的并发访问
	maxConnTime    time.Duration           // 最长成功连接时间（用于自适应超时）
	fastFailClient *http.Client            // 快速失败客户端（用于快速重试）
	weightedURLs   []string                // 加权 URL 列表（主 URL 占比更高）
	urlIndex       int64                   // 当前轮询索引（原子操作）
	tlsConfig      *tls.Config             // TLS 配置（用于快速失败客户端）
}

// NewHTTPDownloader 创建新的 HTTP 下载器
func NewHTTPDownloader(config *types.DownloadConfig) *HTTPDownloader {
	if config == nil {
		config = types.DefaultConfig()
	}

	types.ValidateConfig(config)

	downloader := &HTTPDownloader{
		transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   config.MaxConnections,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: config.Timeout,
		},
		clients:     make(map[string]*http.Client),
		maxConnTime: config.Timeout,
	}

	// 应用 TLS 配置（跳过证书验证等）
	if config.TLSConfig != nil && config.TLSConfig.InsecureSkipVerify {
		tlsCfg := &tls.Config{
			InsecureSkipVerify: true,
		}
		downloader.transport.TLSClientConfig = tlsCfg
		downloader.tlsConfig = tlsCfg
	}

	downloader.client = &http.Client{
		Timeout:   config.Timeout,
		Transport: downloader.transport,
	}

	downloader.fastFailClient = &http.Client{
		Timeout: 3 * time.Second,
	}

	// 为多来源 URL 创建独立的 client（共享同一个 transport，便于统一关闭）
	for _, url := range config.MultiSources {
		downloader.clients[url] = &http.Client{
			Timeout:   config.Timeout,
			Transport: downloader.transport,
		}
	}

	return downloader
}

// buildWeightedURLs 构建加权 URL 列表
// 主 URL 出现 2 次，其他 URL 各出现 1 次，确保主 URL 占比高于其他 URL
func buildWeightedURLs(primary string, others []string) []string {
	if len(others) == 0 {
		return []string{primary}
	}
	// 主 URL 出现 2 次，其他各 1 次
	urls := make([]string, 0, 2+len(others))
	urls = append(urls, primary, primary)
	urls = append(urls, others...)
	return urls
}

// nextURL 从加权 URL 列表中轮询获取下一个 URL（线程安全）
func (d *HTTPDownloader) nextURL() string {
	if len(d.weightedURLs) == 0 {
		return ""
	}
	idx := atomic.AddInt64(&d.urlIndex, 1) - 1
	return d.weightedURLs[idx%int64(len(d.weightedURLs))]
}

// Download 执行 HTTP/HTTPS 下载
// reporter 参数用于在所有预下载消息（resume提示等）输出完毕后才启动进度显示
func (d *HTTPDownloader) Download(ctx context.Context, task *types.DownloadTask, reporter *ProgressReporter) error {
	task.SetStatus(types.TaskStatusDownloading)

	outputPath := d.getOutputPath(task)
	oshinPath := GetOShinStatePath(outputPath)
	tempPath := GetTempPath(outputPath)

	// 尝试从 .oshin 文件恢复下载状态（除非指定了 --no-resume）
	resumedFromState := false
	var existingState *OShinState
	if !task.Config.NoResume {
		existingState, _ = LoadOShinState(oshinPath)
	}
	if existingState != nil {
		if task.Metadata.Size <= 0 {
			task.Metadata.Size = existingState.TotalSize
		}
		if !validateResumeFile(existingState, tempPath, task) {
			fmt.Printf("  [!] Resume validation failed, starting fresh download\n")
			RemoveOShinState(oshinPath)
			os.Remove(tempPath)
			existingState = nil
		}
	}
	if existingState != nil && existingState.TotalSize == task.Metadata.Size {
		chunkCount := int((existingState.TotalSize + existingState.ChunkSize - 1) / existingState.ChunkSize)
		completedSet := make(map[int]*OShinChunk)
		for i := range existingState.Chunks {
			completedSet[existingState.Chunks[i].ID] = &existingState.Chunks[i]
		}

		task.Chunks = make([]*types.ChunkInfo, chunkCount)
		var totalDownloaded int64
		for i := 0; i < chunkCount; i++ {
			// 默认按 ChunkSize 计算范围
			start := int64(i) * existingState.ChunkSize
			end := start + existingState.ChunkSize - 1
			if end >= existingState.TotalSize {
				end = existingState.TotalSize - 1
			}

			chunkStatus := types.ChunkStatusPending

			if cs, ok := completedSet[i]; ok {
				chunkStatus = types.ChunkStatusCompleted
				// 优先使用 state 中保存的 Start/End（Protobuf V3 格式）
				// 回退到计算值（JSON V2 旧格式只保存了 End）
				if cs.Start > 0 {
					start = cs.Start
				}
				if cs.End > 0 {
					end = cs.End
				}
				totalDownloaded += end - start + 1
			}

			task.Chunks[i] = &types.ChunkInfo{
				Index:  i,
				Start:  start,
				End:    end,
				Status: chunkStatus,
			}
		}
		if len(existingState.URLs) > 1 {
			task.Config.MultiSources = existingState.URLs[1:]
		}
		if existingState.ET != "" {
			parts := strings.SplitN(existingState.ET, ":", 2)
			if len(parts) == 2 {
				task.Config.ChecksumType = parts[0]
				task.Config.ChecksumValue = parts[1]
			}
		}
		resumedFromState = true
		completedCount := len(completedSet)
		fmt.Printf("  [+] Resumed from .oshin state (%d/%d chunks completed, %s)\n",
			completedCount, chunkCount, formatBytes(totalDownloaded))
	} else {
		if err := d.initChunks(task); err != nil {
			return fmt.Errorf("failed to init chunks: %w", err)
		}
	}

	// 在所有预下载消息（resume/init 提示）输出完毕后再启动进度显示
	// 避免 reporter 的 ANSI 清行与这些消息交错导致显示混乱
	if reporter != nil {
		reporter.Start()
	}

	// 计算实际并发数：min(分片数, 最大连接数)
	chunkCount := len(task.Chunks)
	effectiveConcurrency := task.Config.MaxConnections
	if chunkCount < effectiveConcurrency {
		effectiveConcurrency = chunkCount
	}
	if effectiveConcurrency < 1 {
		effectiveConcurrency = 1
	}

	// 打开或创建临时文件（支持续传）
	var tempFile *os.File
	var err error
	if resumedFromState {
		tempFile, err = os.OpenFile(tempPath, os.O_RDWR, 0644)
		if err != nil {
			fmt.Printf("  [!] Temp file missing, starting fresh download\n")
			resumedFromState = false
			tempFile, err = os.Create(tempPath)
		}
	} else {
		tempFile, err = os.Create(tempPath)
	}
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}
	defer func() {
		tempFile.Close()
	}()

	// 预分配文件空间（如果支持）
	if task.Metadata.Size > 0 {
		fi, statErr := tempFile.Stat()
		if statErr == nil && fi.Size() >= task.Metadata.Size {
		} else {
			if err := tempFile.Truncate(task.Metadata.Size); err != nil {
				return fmt.Errorf("failed to allocate file space: %w", err)
			}
		}
	}

	// 创建取消上下文（仅用于外部取消，如 Ctrl+C）
	// 分片下载失败不取消 context，让其他 worker 继续工作
	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 构建加权 URL 列表（主 URL 占比高于其他 URL）
	d.weightedURLs = buildWeightedURLs(task.URL, task.Config.MultiSources)
	d.urlIndex = 0

	// 初始化进度跟踪
	// 注意：不设置 ActiveThreads 初始值，由 goroutine 的 Inc/Dec 自然管理
	// 避免 SetActiveThreads(N) + N 个 goroutine 各 IncActiveThreads() 导致计数翻倍
	task.Progress.SetRemainingChunks(int32(chunkCount))

	// 创建状态保存器（每 5 秒自动保存，分片完成时立即保存）
	stateSaver := NewStateSaver(task, oshinPath, 5*time.Second)
	stateSaver.Start()
	defer stateSaver.Stop()

	var wg sync.WaitGroup
	var workerFailed atomic.Bool
	nextChunkIdx := 0
	var idxMu sync.Mutex

	for w := 0; w < effectiveConcurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task.Progress.IncActiveThreads()
			defer task.Progress.DecActiveThreads()

			for {
				select {
				case <-downloadCtx.Done():
					return
				default:
				}

				idxMu.Lock()
				if nextChunkIdx >= len(task.Chunks) {
					idxMu.Unlock()
					return
				}
				chunk := task.Chunks[nextChunkIdx]
				nextChunkIdx++
				idxMu.Unlock()

				if chunk.Status == types.ChunkStatusCompleted {
					task.Progress.SetRemainingChunks(int32(len(task.Chunks)) - int32(nextChunkIdx))
					continue
				}

				if err := d.downloadChunk(downloadCtx, task, chunk, tempFile); err != nil {
					if !workerFailed.Load() && isPermanentFailure(err) {
						workerFailed.Store(true)
						fmt.Printf("\n  [!] Server connection limit detected, falling back to queue mode\n")
					}
					task.Progress.IncFailedChunks()
					stateSaver.MarkDirty()
					continue
				}
				stateSaver.MarkDirty()
				stateSaver.Save()
				task.Progress.SetRemainingChunks(int32(len(task.Chunks)) - int32(nextChunkIdx))
			}
		}()
	}

	// 等待所有 worker 完成（带强制超时，防止 Ctrl+C 后长时间阻塞）
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// 先停止进度报告器，清除 ANSI 进度显示区，避免后续消息与进度输出交错
		if reporter != nil {
			reporter.Stop()
		}
		// 强制退出：关闭所有空闲连接，打断阻塞在 body.Read() 上的 worker
		fmt.Printf("\n  [!] Force closing connections after timeout\n")
		d.transport.CloseIdleConnections()
		<-done
	}

	stateSaver.Stop()

	// 检查是否被中断（Ctrl+C），中断时保留 .oshin 状态文件用于续传
	if downloadCtx.Err() != nil {
		// 如果是用户主动暂停，状态已设为 PAUSED，不需要再改为 FAILED
		if task.GetStatus() != types.TaskStatusPaused {
			task.SetStatus(types.TaskStatusFailed)
		}
		return downloadCtx.Err()
	}

	// 检查是否所有分片都失败
	failedCount := task.Progress.GetFailedChunks()
	if failedCount == int32(chunkCount) {
		task.SetStatus(types.TaskStatusFailed)
		return fmt.Errorf("all %d chunks failed", chunkCount)
	}

	// 下载完成，重命名临时文件
	tempFile.Close()
	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	// 清理 .oshin 状态文件
	RemoveOShinState(oshinPath)

	task.SetStatus(types.TaskStatusCompleted)
	return nil
}

// initChunks 初始化分片信息
func (d *HTTPDownloader) initChunks(task *types.DownloadTask) error {
	if task.Metadata.Size <= 0 {
		metadata, err := Probe(task.URL, task.Config)
		if err != nil {
			return fmt.Errorf("failed to probe file: %w", err)
		}
		task.Metadata = metadata
	}

	// 如果文件大小仍未知，使用单片下载
	if task.Metadata.Size <= 0 {
		task.Chunks = []*types.ChunkInfo{
			{
				Index:  0,
				Start:  0,
				End:    -1, // 表示下载到末尾
				Status: types.ChunkStatusPending,
			},
		}
		return nil
	}

	// 如果不支持断点续传或文件小于分片大小，使用单片
	if !task.Metadata.SupportResume || task.Metadata.Size < task.Config.ChunkSize {
		task.Chunks = []*types.ChunkInfo{
			{
				Index:  0,
				Start:  0,
				End:    task.Metadata.Size - 1,
				Status: types.ChunkStatusPending,
			},
		}
		return nil
	}

	// 计算分片数量
	chunkCount := int((task.Metadata.Size + task.Config.ChunkSize - 1) / task.Config.ChunkSize)
	task.Chunks = make([]*types.ChunkInfo, chunkCount)

	if len(task.Config.MultiSources) > 0 {
		task.MultiSource = true
	}

	// 初始化分片信息（不再分配 SourceURL，下载时统一使用 task.URL）
	for i := 0; i < chunkCount; i++ {
		start := int64(i) * task.Config.ChunkSize
		end := start + task.Config.ChunkSize - 1
		if end >= task.Metadata.Size {
			end = task.Metadata.Size - 1
		}

		task.Chunks[i] = &types.ChunkInfo{
			Index:  i,
			Start:  start,
			End:    end,
			Status: types.ChunkStatusPending,
		}
	}

	return nil
}

// downloadChunk 下载单个分片
func (d *HTTPDownloader) downloadChunk(ctx context.Context, task *types.DownloadTask, chunk *types.ChunkInfo, tempFile *os.File) error {
	task.UpdateChunkStatus(chunk.Index, types.ChunkStatusDownloading)

	downloadURL := d.nextURL()
	if downloadURL == "" {
		downloadURL = task.URL
	}

	// 重试逻辑
	var lastErr error
	for retry := 0; retry <= task.Config.Retry; retry++ {
		select {
		case <-ctx.Done():
			task.UpdateChunkStatus(chunk.Index, types.ChunkStatusFailed)
			return ctx.Err()
		default:
		}

		if retry > 0 {
			delay := task.Config.RetryDelay * time.Duration(retry)
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
			time.Sleep(delay)
		}

		// 创建 HTTP 请求
		req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
		if err != nil {
			lastErr = fmt.Errorf("failed to create request: %w", err)
			continue
		}

		// 设置 Range 请求头（支持断点续传）
		startPos := chunk.Start + chunk.LocalOffset
		if chunk.End >= 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", startPos, chunk.End))
		} else {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startPos))
		}

		req.Header.Set("User-Agent", "OShinD/1.0")
		req.Header.Set("Content-Type", detectContentType(task.FileName))
		applyHeaders(req, task.Config.Headers)

		chunk.Headers = make(map[string]string)
		for key := range req.Header {
			chunk.Headers[key] = req.Header.Get(key)
		}

		client := d.getAdaptiveClient(downloadURL, retry > 0)

		connStart := time.Now()

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			continue
		}

		// 检查响应状态码
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden {
				task.UpdateChunkStatus(chunk.Index, types.ChunkStatusFailed)
				return fmt.Errorf("forbidden: server connection limit")
			}
			lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			continue
		}

		// 下载数据
		err = d.readChunkData(ctx, task, chunk, resp.Body, tempFile)
		resp.Body.Close()

		if err == nil {
			connDuration := time.Since(connStart)
			d.updateMaxConnTime(connDuration)
			return nil
		}
		lastErr = err
	}

	// 所有重试都失败
	task.UpdateChunkStatus(chunk.Index, types.ChunkStatusFailed)
	return fmt.Errorf("chunk %d failed after %d retries: %w", chunk.Index, task.Config.Retry, lastErr)
}

// readChunkData 读取分片数据并写入文件
func (d *HTTPDownloader) readChunkData(ctx context.Context, task *types.DownloadTask, chunk *types.ChunkInfo, body io.Reader, tempFile *os.File) error {
	buf := make([]byte, 32*1024) // 32KB 缓冲区
	offset := chunk.Start + chunk.LocalOffset

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, readErr := body.Read(buf)
		if n > 0 {
			_, writeErr := tempFile.WriteAt(buf[:n], offset)
			if writeErr != nil {
				return fmt.Errorf("failed to write chunk: %w", writeErr)
			}
			offset += int64(n)
			chunk.LocalOffset += int64(n)
			chunk.Downloaded += int64(n)
			task.Progress.AddDownloaded(int64(n))
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("failed to read chunk: %w", readErr)
		}
	}

	// 更新分片状态
	task.UpdateChunkStatus(chunk.Index, types.ChunkStatusCompleted)
	return nil
}

// getOutputPath 获取输出文件路径
func (d *HTTPDownloader) getOutputPath(task *types.DownloadTask) string {
	if task.FileName != "" {
		return filepath.Join(task.Config.OutputDir, task.FileName)
	}

	// 从 URL 提取文件名
	fileName := ExtractFileInfo(task.URL)
	return filepath.Join(task.Config.OutputDir, fileName)
}

// ProgressReporter 进度报告器
type ProgressReporter struct {
	task            *types.DownloadTask
	interval        time.Duration
	stopChan        chan struct{}
	lastOutputLines int       // 上次输出的行数（用于清除）
	maxOutputLines  int       // 历史最大输出行数（用于可靠清除，防止活跃线程减少时残留旧内容）
	started         bool      // 是否已输出过至少一次
	stopOnce        sync.Once // 确保 Stop() 只执行一次
}

// NewProgressReporter 创建新的进度报告器
func NewProgressReporter(task *types.DownloadTask, interval time.Duration) *ProgressReporter {
	return &ProgressReporter{
		task:     task,
		interval: interval,
		stopChan: make(chan struct{}),
	}
}

// Start 开始报告进度（立即输出一次初始进度，然后定时刷新）
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

// Stop 停止报告进度（清除进度显示区，不留残影）
func (r *ProgressReporter) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopChan)
		if r.maxOutputLines > 0 && r.started {
			fmt.Printf("\033[%dA\033[J", r.maxOutputLines)
		}
	})
}

// report 报告当前进度
// 每帧输出格式：
//
//	[=====================>               ] 65.3% | 31.61 MB/s | ETA: 12s
//	Threads: 4/4  |  Remaining: 8 chunks  |  Failed: 0
//	── Active Threads ──
//	[T0] Chunk#3  [########........]  62.3%  | 8.2 MB/s
//	[T1] Chunk#7  [######..........]  31.1%  | 7.8 MB/s
func (r *ProgressReporter) report() {
	if r.task.Metadata.Size <= 0 {
		return
	}

	downloaded := r.task.Progress.GetDownloaded()
	total := r.task.Metadata.Size

	// 计算全局瞬时速度（基于 ProgressInfo 的滚动窗口）
	speed := r.task.Progress.CalculateSpeed()

	var eta time.Duration
	if speed > 0 && downloaded < total {
		remaining := float64(total-downloaded) / speed
		eta = time.Duration(remaining * float64(time.Second))
	}

	// 清除上一帧输出（使用历史最大行数，确保活跃线程减少时也能完整清除旧内容）
	if r.maxOutputLines > 0 && r.started {
		fmt.Printf("\033[%dA\033[J", r.maxOutputLines)
	}

	lines := 0

	// 第1行：进度条 + 百分比 + 速度 + ETA
	progress := float64(downloaded) / float64(total) * 100
	bar := r.buildProgressBar(40)
	if speed > 0 {
		fmt.Printf("  %s %5.1f%% | %s/s | ETA: %s\n", bar, progress, formatBytes(int64(speed)), formatDuration(eta))
	} else {
		fmt.Printf("  %s %5.1f%% | connecting...\n", bar, progress)
	}
	lines++

	// 第2行：线程统计
	activeThreads := r.task.Progress.GetActiveThreads()
	remainingChunks := r.task.Progress.GetRemainingChunks()
	failedChunks := r.task.Progress.GetFailedChunks()
	fmt.Printf("  Threads: %d/%d  |  Remaining: %d chunks  |  Failed: %d\n",
		activeThreads, r.task.Config.MaxConnections, remainingChunks, failedChunks)
	lines++

	// 第3行起：活跃线程详情（只显示 DOWNLOADING 状态的分片，无速度列）
	// 通过线程安全方法获取活跃分片快照，避免与下载 goroutine 写操作竞争
	activeChunks := r.task.GetActiveChunks()

	if len(activeChunks) > 0 {
		fmt.Printf("  ── Active Threads ──\n")
		lines++
		for i, chunk := range activeChunks {
			chunkSize := chunk.End - chunk.Start + 1
			chunkProgress := 0.0
			if chunkSize > 0 {
				chunkProgress = float64(chunk.Downloaded) / float64(chunkSize) * 100
			}
			miniBar := buildMiniBar(chunkProgress, 20)
			fmt.Printf("  [T%d] Chunk#%-3d %s %5.1f%%\n",
				i, chunk.Index, miniBar, chunkProgress)
			lines++
		}
	}

	r.lastOutputLines = lines
	if lines > r.maxOutputLines {
		r.maxOutputLines = lines
	}
	r.started = true
}

// buildProgressBar 构建基于字节百分比的主进度条
func (r *ProgressReporter) buildProgressBar(width int) string {
	downloaded := r.task.Progress.GetDownloaded()
	total := r.task.Metadata.Size
	if total <= 0 {
		return "[" + strings.Repeat(" ", width) + "]"
	}

	ratio := float64(downloaded) / float64(total)
	filled := int(ratio * float64(width))
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

// buildFastFailClient 构建快速失败客户端
// 调用方须持有 d.mu 锁
func (d *HTTPDownloader) buildFastFailClient() *http.Client {
	timeout := 3 * time.Second
	if d.maxConnTime > timeout {
		timeout = d.maxConnTime
	}

	transport := &http.Transport{}
	if d.tlsConfig != nil {
		transport.TLSClientConfig = d.tlsConfig
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

// updateMaxConnTime 更新最大成功连接时间（线程安全）
func (d *HTTPDownloader) updateMaxConnTime(duration time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if duration > d.maxConnTime {
		d.maxConnTime = duration
		if d.fastFailClient != nil {
			d.fastFailClient.CloseIdleConnections()
		}
		d.fastFailClient = d.buildFastFailClient()
	}
}

// getAdaptiveClient 获取自适应客户端（线程安全）
func (d *HTTPDownloader) getAdaptiveClient(downloadURL string, isRetry bool) *http.Client {
	if isRetry {
		d.mu.Lock()
		ffc := d.fastFailClient
		d.mu.Unlock()
		if ffc != nil {
			return ffc
		}
	}

	// 否则使用普通客户端
	client := d.client
	if c, ok := d.clients[downloadURL]; ok {
		client = c
	}
	return client
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

// mimeTypes 文件扩展名 → MIME 类型映射
var mimeTypes = map[string]string{
	".apk":  "application/vnd.android.package-archive",
	".zip":  "application/zip",
	".rar":  "application/x-rar-compressed",
	".7z":   "application/x-7z-compressed",
	".tar":  "application/x-tar",
	".gz":   "application/gzip",
	".exe":  "application/x-msdownload",
	".msi":  "application/x-msdownload",
	".dmg":  "application/x-apple-diskimage",
	".iso":  "application/x-iso9660-image",
	".pdf":  "application/pdf",
	".mp4":  "video/mp4",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",
	".mp3":  "audio/mpeg",
	".flac": "audio/flac",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".html": "text/html",
	".css":  "text/css",
	".js":   "application/javascript",
	".json": "application/json",
	".xml":  "application/xml",
	".txt":  "text/plain",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// detectContentType 根据文件扩展名推断 Content-Type
func detectContentType(filename string) string {
	ext := strings.ToLower(filename)
	if dotIdx := strings.LastIndex(ext, "."); dotIdx >= 0 {
		ext = ext[dotIdx:]
		if ct, ok := mimeTypes[ext]; ok {
			return ct
		}
	}
	return "application/octet-stream"
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
