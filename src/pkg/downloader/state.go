package downloader

import (
	"fmt"
	"os"
	"sync"
	"time"

	pbtypes "github.com/mogumc/oshind/pkg/downloader/proto"
	"github.com/mogumc/oshind/types"
	"google.golang.org/protobuf/proto"
)

// stateVersion 当前 Protobuf 状态文件版本号
const stateVersion = 1

// OShinState
// 每个 chunk 保存 start+end 起止位置，避免续传时重新计算范围出错
type OShinState struct {
	mu          sync.Mutex        `json:"-"`
	filePath    string            `json:"-"`
	V           int               `json:"v"`
	URL         string            `json:"url"`
	URLs        []string          `json:"urls,omitempty"`
	FileName    string            `json:"file_name"`
	TotalSize   int64             `json:"total_size"`
	ChunkSize   int64             `json:"chunk_size"`
	ET          string            `json:"et,omitempty"` // 校验信息，格式 "md5:..." 或 "sha256:..."
	Chunks      []OShinChunk      `json:"chunks"`
	Headers     map[string]string `json:"headers,omitempty"`     // HTTP 自定义请求头
	Protocol    string            `json:"protocol,omitempty"`    // 下载协议
	Connections int32             `json:"connections,omitempty"` // 最大并发连接数
}

// OShinChunk 单个已完成分片的持久化状态
type OShinChunk struct {
	ID    int   `json:"id"`
	Start int64 `json:"start"` // 起始字节位置（含）
	End   int64 `json:"end"`   // 结束字节位置（含）
}

// SaveOShinState 保存下载状态到 .oshin 文件（Protobuf 二进制格式）
// 只保存已完成的 chunks，每个 chunk 记录 start+end 起止位置
func SaveOShinState(task *types.DownloadTask, filePath string) error {
	// 构建 proto 消息
	pb := &pbtypes.OShinState{
		Version:     stateVersion,
		FileName:    task.FileName,
		TotalSize:   task.Metadata.Size,
		ChunkSize:   task.Config.ChunkSize,
		Protocol:    task.Protocol.String(),
		Connections: int32(task.Config.MaxConnections),
	}

	// 保存 HTTP 自定义请求头
	if len(task.Config.Headers) > 0 {
		pb.Headers = task.Config.Headers
	}

	// urls 列表（主 URL + 额外来源）
	pb.Urls = []string{task.URL}
	if len(task.Config.MultiSources) > 0 {
		pb.Urls = append(pb.Urls, task.Config.MultiSources...)
	}

	// 校验信息
	if task.Config.ChecksumType != "" && task.Config.ChecksumValue != "" {
		pb.Et = task.Config.ChecksumType + ":" + task.Config.ChecksumValue
	}

	// 只保存已完成的 chunks（含 start+end 起止位置）
	for _, chunk := range task.Chunks {
		if chunk.Status == types.ChunkStatusCompleted {
			pb.Chunks = append(pb.Chunks, &pbtypes.Chunk{
				Id:    int32(chunk.Index),
				Start: chunk.Start,
				End:   chunk.End,
			})
		}
	}

	// 序列化为 Protobuf 二进制
	data, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// 直接覆写目标文件（状态文件无需原子性，最坏情况读到旧数据，下次保存恢复）
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// LoadOShinState 从 .oshin 文件加载下载状态
// 自动检测格式：Protobuf 二进制
func LoadOShinState(filePath string) (*OShinState, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 文件不存在，全新下载
		}
		return nil, fmt.Errorf("state file not found: %w", err)
	}

	if len(data) == 0 {
		return nil, nil
	}

	// 尝试 Protobuf 解析
	pb := &pbtypes.OShinState{}
	if err := proto.Unmarshal(data, pb); err == nil && pb.Version >= 1 {
		return protoToState(pb, filePath), nil
	}

	return nil, fmt.Errorf("failed to read state file: %w", err)
}

// 将 Protobuf 消息转换为内部 V2 表示
func protoToState(pb *pbtypes.OShinState, filePath string) *OShinState {
	state := &OShinState{
		filePath:    filePath,
		V:           3,
		URLs:        pb.Urls,
		FileName:    pb.FileName,
		TotalSize:   pb.TotalSize,
		ChunkSize:   pb.ChunkSize,
		ET:          pb.Et,
		Headers:     pb.Headers,
		Protocol:    pb.Protocol,
		Connections: pb.Connections,
	}

	// 主 URL
	if len(pb.Urls) > 0 {
		state.URL = pb.Urls[0]
	}

	// 转换 chunks
	for _, c := range pb.Chunks {
		state.Chunks = append(state.Chunks, OShinChunk{
			ID:    int(c.Id),
			Start: c.Start,
			End:   c.End,
		})
	}

	return state
}

// RemoveOShinState 删除 .oshin 文件（下载完成后）
func RemoveOShinState(filePath string) error {
	if filePath == "" {
		return nil
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove state file: %w", err)
	}
	return nil
}

// GetOShinStatePath 获取 .oshin 文件路径
func GetOShinStatePath(outputPath string) string {
	return outputPath + ".oshin"
}

// GetTempPath 获取临时文件路径
func GetTempPath(outputPath string) string {
	return outputPath + ".tmp"
}

// StateSaver 状态保存器
// 定期将下载状态保存到 .oshin 文件，支持在分片完成时立即保存
type StateSaver struct {
	task     *types.DownloadTask
	filePath string
	interval time.Duration
	stopChan chan struct{}
	dirty    bool      // 标记是否有未保存的变更
	stopOnce sync.Once // 确保 Stop() 只执行一次，防止 close of closed channel
}

// NewStateSaver 创建新的状态保存器
func NewStateSaver(task *types.DownloadTask, filePath string, interval time.Duration) *StateSaver {
	return &StateSaver{
		task:     task,
		filePath: filePath,
		interval: interval,
		stopChan: make(chan struct{}),
	}
}

// Start 启动定期保存
func (s *StateSaver) Start() {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.Save()
			case <-s.stopChan:
				return
			}
		}
	}()
}

// Stop 停止定期保存（使用 sync.Once 确保只执行一次）
func (s *StateSaver) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopChan)
		// 最后保存一次
		s.Save()
	})
}

// MarkDirty 标记有变更需要保存
func (s *StateSaver) MarkDirty() {
	s.dirty = true
}

// Save 执行保存（如果标记了 dirty）
func (s *StateSaver) Save() {
	if s.dirty {
		SaveOShinState(s.task, s.filePath)
		s.dirty = false
	}
}

// ForceSave 强制保存（不论是否有变更）
func (s *StateSaver) ForceSave() {
	s.dirty = true
	s.Save()
}
