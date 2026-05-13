// OShinD - Foreign Function Interface
package main

// #include <stdlib.h>
import "C"

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/mogumc/oshind/pkg/downloader"
	"github.com/mogumc/oshind/types"
)

var (
	version    = "1.3.0"
	engine     *downloader.Engine
	engineOnce sync.Once
)

// ChunkStatusJSON 分片状态 JSON 结构
type ChunkStatusJSON struct {
	Index      int               `json:"index"`
	Start      int64             `json:"start"`
	End        int64             `json:"end"`
	Status     string            `json:"status"`
	Downloaded int64             `json:"downloaded"`
	Speed      float64           `json:"speed"`             // 当前分片下载速度（字节/秒）
	Headers    map[string]string `json:"headers,omitempty"` // 下载请求头
	RetryCount int               `json:"retry_count"`       // 重试次数
	Error      string            `json:"error,omitempty"`   // 错误信息
}

// TaskStatusJSON 任务状态 JSON 结构
type TaskStatusJSON struct {
	ID              string            `json:"id"`
	URL             string            `json:"url"`
	FileName        string            `json:"file_name"`
	Status          string            `json:"status"`
	Progress        float64           `json:"progress"`
	Speed           float64           `json:"speed"` // 总速度（所有分片速度之和）
	Downloaded      int64             `json:"downloaded"`
	Total           int64             `json:"total"`
	Chunks          []ChunkStatusJSON `json:"chunks"`
	Protocol        string            `json:"protocol"`         // 协议类型
	MultiSource     bool              `json:"multi_source"`     // 是否多来源下载
	ActiveThreads   int32             `json:"active_threads"`   // 当前活跃线程数
	RemainingChunks int32             `json:"remaining_chunks"` // 剩余分块数
	FailedChunks    int32             `json:"failed_chunks"`    // 失败分块数
	CreatedAt       string            `json:"created_at"`       // 创建时间
	UpdatedAt       string            `json:"updated_at"`       // 更新时间
}

// initEngine 初始化下载引擎（单例）
func initEngine() *downloader.Engine {
	engineOnce.Do(func() {
		engine = downloader.NewEngine(nil)
	})
	return engine
}

// buildTaskStatusJSON 构建任务状态 JSON（内部函数，避免重复代码）
func buildTaskStatusJSON(task *types.DownloadTask) TaskStatusJSON {
	totalSpeed := task.Progress.CalculateSpeed()

	snapshots := task.GetChunkSnapshots()
	chunks := make([]ChunkStatusJSON, len(snapshots))
	for i, snap := range snapshots {
		chunks[i] = ChunkStatusJSON{
			Index:      snap.Index,
			Start:      snap.Start,
			End:        snap.End,
			Status:     snap.Status.String(),
			Downloaded: snap.Downloaded,
			Headers:    snap.Headers,
			RetryCount: snap.RetryCount,
		}
		if snap.Error != nil {
			chunks[i].Error = snap.Error.Error()
		}
	}

	progressPct := 0.0
	if task.Metadata.Size > 0 {
		progressPct = float64(task.Progress.GetDownloaded()) / float64(task.Metadata.Size) * 100
	}

	return TaskStatusJSON{
		ID:              task.ID,
		URL:             task.URL,
		FileName:        task.FileName,
		Status:          task.GetStatus().String(),
		Progress:        progressPct,
		Speed:           totalSpeed,
		Downloaded:      task.Progress.GetDownloaded(),
		Total:           task.Metadata.Size,
		Chunks:          chunks,
		Protocol:        task.Protocol.String(),
		MultiSource:     task.MultiSource,
		ActiveThreads:   task.Progress.GetActiveThreads(),
		RemainingChunks: task.Progress.GetRemainingChunks(),
		FailedChunks:    task.Progress.GetFailedChunks(),
		CreatedAt:       task.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       task.UpdatedAt.Format(time.RFC3339),
	}
}

//export OShinD_Version
func OShinD_Version() *C.char {
	return C.CString(version)
}

//export OShinD_Download
func OShinD_Download(url *C.char, outputDir *C.char, connections C.int) *C.char {
	goURL := C.GoString(url)
	goOutputDir := C.GoString(outputDir)

	config := types.DefaultConfig()
	if goOutputDir != "" {
		config.OutputDir = goOutputDir
	}
	if connections > 0 {
		config.MaxConnections = int(connections)
	}

	e := initEngine()
	taskID, err := e.SubmitDownload(goURL, config)
	if err != nil {
		return C.CString("")
	}
	return C.CString(taskID)
}

//export OShinD_DownloadWithResume
func OShinD_DownloadWithResume(url *C.char, outputDir *C.char, connections C.int, noResume C.int) *C.char {
	goURL := C.GoString(url)
	goOutputDir := C.GoString(outputDir)

	config := types.DefaultConfig()
	if goOutputDir != "" {
		config.OutputDir = goOutputDir
	}
	if connections > 0 {
		config.MaxConnections = int(connections)
	}
	config.NoResume = noResume != 0

	e := initEngine()
	taskID, err := e.SubmitDownload(goURL, config)
	if err != nil {
		return C.CString("")
	}
	return C.CString(taskID)
}

//export OShinD_DownloadMultiSource
func OShinD_DownloadMultiSource(url *C.char, outputDir *C.char, connections C.int, sources **C.char, sourceCount C.int) *C.char {
	goURL := C.GoString(url)
	goOutputDir := C.GoString(outputDir)

	config := types.DefaultConfig()
	if goOutputDir != "" {
		config.OutputDir = goOutputDir
	}
	if connections > 0 {
		config.MaxConnections = int(connections)
	}

	// 解析多来源 URL
	if sourceCount > 0 && sources != nil {
		cSlice := (*[1 << 30]*C.char)(unsafe.Pointer(sources))[:sourceCount:sourceCount]
		for _, cStr := range cSlice {
			config.MultiSources = append(config.MultiSources, C.GoString(cStr))
		}
	}

	e := initEngine()
	taskID, err := e.SubmitDownload(goURL, config)
	if err != nil {
		return C.CString("")
	}
	return C.CString(taskID)
}

// OShinD_DownloadWithHeaders 带自定义请求头的下载
// headersJson: JSON 格式的请求头，如 {"User-Agent":"xxx","Referer":"xxx"}
//
//export OShinD_DownloadWithHeaders
func OShinD_DownloadWithHeaders(url *C.char, outputDir *C.char, connections C.int, noResume C.int, headersJson *C.char) *C.char {
	goURL := C.GoString(url)
	goOutputDir := C.GoString(outputDir)

	config := types.DefaultConfig()
	if goOutputDir != "" {
		config.OutputDir = goOutputDir
	}
	if connections > 0 {
		config.MaxConnections = int(connections)
	}
	config.NoResume = noResume != 0

	// 解析自定义请求头
	if headersJson != nil {
		goHeadersJson := C.GoString(headersJson)
		if goHeadersJson != "" {
			headers := make(map[string]string)
			if err := json.Unmarshal([]byte(goHeadersJson), &headers); err == nil {
				config.Headers = headers
			}
		}
	}

	e := initEngine()
	taskID, err := e.SubmitDownload(goURL, config)
	if err != nil {
		return C.CString("")
	}
	return C.CString(taskID)
}

//export OShinD_GetTaskStatus
func OShinD_GetTaskStatus(taskID *C.char) *C.char {
	goTaskID := C.GoString(taskID)

	e := initEngine()
	task, ok := e.GetTask(goTaskID)
	if !ok {
		return C.CString("{}")
	}

	status := buildTaskStatusJSON(task)
	data, _ := json.Marshal(status)
	return C.CString(string(data))
}

//export OShinD_GetChunkStatus
func OShinD_GetChunkStatus(taskID *C.char, chunkIndex C.int) *C.char {
	goTaskID := C.GoString(taskID)

	e := initEngine()
	task, ok := e.GetTask(goTaskID)
	if !ok {
		return C.CString("{}")
	}

	// 使用线程安全快照，避免数据竞争
	snapshots := task.GetChunkSnapshots()
	idx := int(chunkIndex)
	if idx < 0 || idx >= len(snapshots) {
		return C.CString("{}")
	}

	snap := snapshots[idx]
	status := ChunkStatusJSON{
		Index:      snap.Index,
		Start:      snap.Start,
		End:        snap.End,
		Status:     snap.Status.String(),
		Downloaded: snap.Downloaded,
		Headers:    snap.Headers,
		RetryCount: snap.RetryCount,
	}
	if snap.Error != nil {
		status.Error = snap.Error.Error()
	}

	data, _ := json.Marshal(status)
	return C.CString(string(data))
}

//export OShinD_CancelTask
func OShinD_CancelTask(taskID *C.char) C.int {
	goTaskID := C.GoString(taskID)

	e := initEngine()

	// 先停止 reporter
	e.StopReporter(goTaskID)

	if err := e.CancelTask(goTaskID); err != nil {
		return 0
	}
	return 1
}

// OShinD_PauseTask 暂停下载并返回中断状态 JSON
// 与 OShinD_GetTaskStatus 格式相同，但状态为 PAUSED
//
//export OShinD_PauseTask
func OShinD_PauseTask(taskID *C.char) *C.char {
	goTaskID := C.GoString(taskID)

	e := initEngine()

	// 调用 PauseTask：设置状态为 PAUSED + 取消下载
	if err := e.PauseTask(goTaskID); err != nil {
		result := TaskStatusJSON{ID: goTaskID, Status: "not_found"}
		data, _ := json.Marshal(result)
		return C.CString(string(data))
	}

	// 读取暂停后的任务状态
	task, ok := e.GetTask(goTaskID)
	if !ok {
		result := TaskStatusJSON{ID: goTaskID, Status: "not_found"}
		data, _ := json.Marshal(result)
		return C.CString(string(data))
	}

	status := buildTaskStatusJSON(task)
	data, _ := json.Marshal(status)
	return C.CString(string(data))
}

//export OShinD_RemoveTask
func OShinD_RemoveTask(taskID *C.char) C.int {
	goTaskID := C.GoString(taskID)

	e := initEngine()
	if e.RemoveTask(goTaskID) {
		return 1
	}
	return 0
}

//export OShinD_FreeString
func OShinD_FreeString(str *C.char) {
	C.free(unsafe.Pointer(str))
}

// OShinStateJSON .oshin 状态文件查询结果
type OShinStateJSON struct {
	Exists    bool             `json:"exists"`
	V         int              `json:"v,omitempty"`
	URL       string           `json:"url,omitempty"`
	FileName  string           `json:"file_name,omitempty"`
	TotalSize int64            `json:"total_size,omitempty"`
	ChunkSize int64            `json:"chunk_size,omitempty"`
	ET        string           `json:"et,omitempty"`
	Chunks    []OShinChunkJSON `json:"chunks,omitempty"`
}

// OShinChunkJSON .oshin 状态文件中的分片信息
type OShinChunkJSON struct {
	ID    int   `json:"id"`
	Start int64 `json:"start"` // 起始字节位置（含）
	End   int64 `json:"end"`   // 结束字节位置（含）
}

//export OShinD_HasResumeState
func OShinD_HasResumeState(outputDir *C.char, fileName *C.char) *C.char {
	goOutputDir := C.GoString(outputDir)
	goFileName := C.GoString(fileName)

	outputPath := filepath.Join(goOutputDir, goFileName)
	oshinPath := downloader.GetOShinStatePath(outputPath)

	state, _ := downloader.LoadOShinState(oshinPath)
	if state == nil {
		result := OShinStateJSON{Exists: false}
		data, _ := json.Marshal(result)
		return C.CString(string(data))
	}

	result := OShinStateJSON{
		Exists:    true,
		V:         state.V,
		URL:       state.URL,
		FileName:  state.FileName,
		TotalSize: state.TotalSize,
		ChunkSize: state.ChunkSize,
		ET:        state.ET,
	}
	result.Chunks = make([]OShinChunkJSON, len(state.Chunks))
	for i, c := range state.Chunks {
		result.Chunks[i] = OShinChunkJSON{
			ID:    c.ID,
			Start: c.Start,
			End:   c.End,
		}
	}

	data, _ := json.Marshal(result)
	return C.CString(string(data))
}

//export OShinD_ClearResumeState
func OShinD_ClearResumeState(outputDir *C.char, fileName *C.char) C.int {
	goOutputDir := C.GoString(outputDir)
	goFileName := C.GoString(fileName)

	outputPath := filepath.Join(goOutputDir, goFileName)
	oshinPath := downloader.GetOShinStatePath(outputPath)

	if err := downloader.RemoveOShinState(oshinPath); err != nil {
		return 0
	}
	return 1
}

func main() {}
