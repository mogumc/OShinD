# 架构设计

## 项目结构

```
OShinD/
├── src/                        # Go 源码
│   ├── cmd/
│   │   ├── cli/main.go         # CLI 入口
│   │   └── ffi/main.go         # FFI 接口入口
│   ├── pkg/downloader/
│   │   ├── engine.go           # 任务调度引擎
│   │   ├── http.go             # HTTP/HTTPS 下载器 + ProgressReporter
│   │   ├── ftp.go              # FTP 下载器
│   │   ├── sftp.go             # SFTP 下载器
│   │   ├── probe.go            # 服务器探测
│   │   ├── state.go            # 状态持久化 (Protobuf V1)
│   │   ├── verifier.go         # 文件校验
│   │   └── proto/oshin_state.proto
│   ├── types/types.go          # 核心数据结构
│   ├── Makefile                # 构建脚本
│   ├── go.mod
│   └── go.sum
├── docs/                       # 文档
├── .github/workflows/          # CI/CD
├── README.md
└── LICENSE
```

---

## 核心模块

### 1. Engine (engine.go)

任务调度引擎，负责：
- 任务生命周期管理
- 协议分发（HTTP/FTP/SFTP）
- 并发控制
- 资源清理

```go
type Engine struct {
    mu             sync.RWMutex
    tasks          map[string]*types.DownloadTask      // 任务列表
    cancelFuncs    map[string]context.CancelFunc       // 取消函数
    reporters      map[string]*ProgressReporter        // 进度报告器
    httpDownloader *HTTPDownloader
    ftpDownloader  *FTPDownloader
    sftpDownloader *SFTPDownloader
    verifier       *Verifier
}
```

**关键方法**:
- `Download()` - 同步执行下载（CLI 使用）
- `SubmitDownload()` - 异步提交下载（FFI 使用）
- `CancelTask()` - 取消任务
- `PauseTask()` - 暂停任务（状态设为 PAUSED）
- `ResumeTask()` - 恢复暂停/失败的任务（创建新任务，状态设为 RESUMING）
- `GetTask()` - 获取任务状态
- `ListTasks()` - 列出所有任务
- `RemoveTask()` - 移除任务
- `StopReporter()` - 停止进度报告器

---

### 2. HTTPDownloader (http.go)

HTTP/HTTPS 下载器，实现：
- 多线程分片下载
- 自适应超时
- 多来源下载（加权轮询）
- 断点续传

```go
type HTTPDownloader struct {
    client         *http.Client
    transport      *http.Transport          // 主 transport
    clients        map[string]*http.Client  // 多来源下载时的 client 映射
    mu             sync.Mutex
    maxConnTime    time.Duration            // 最长成功连接时间
    fastFailClient *http.Client             // 快速失败客户端
    weightedURLs   []string                 // 加权 URL 列表
    urlIndex       int64                    // 当前轮询索引（原子操作）
    tlsConfig      *tls.Config              // TLS 配置
}
```

**下载流程**:
1. 探测服务器（Probe）
2. 加载/创建分片（支持从 .oshin 状态文件恢复）
3. 启动 Worker Pool（goroutine + atomic 原子索引）
4. 下载分片
5. 校验文件
6. 清理状态

---

### 3. ProgressReporter (http.go)

进度报告器，负责：
- 实时计算下载速度（基于已完成分片数）
- 格式化进度显示（spinner 动画 + 进度条）
- 支持 ANSI 清行（终端环境）

```go
type ProgressReporter struct {
    task            *types.DownloadTask
    interval        time.Duration
    stopChan        chan struct{}
    lastOutputLines int
    maxOutputLines  int       // 历史最大输出行数
    started         bool      // 是否已输出过至少一次
    stopOnce        sync.Once
    frameCount      int       // 动画帧计数
    OnReport        func(lines []string)  // 回调：输出进度行
    OnStop          func(maxLines int)    // 回调：停止时清除进度区
}
```

**显示格式**:
```
  ⠋ [=====================>               ] 65.3% | 31.61 MB/s | ETA: 12s
  Threads: 4/4  |  Remaining: 8 chunks  |  Failed: 0
  ── Active Threads ──
  [T0] Chunk#3  [########........]  62.3%
  [T1] Chunk#7  [######..........]  31.1%
```

**进度计算**:
- 总进度：`已完成分片数 / 总分片数 * 100%`
- 速度：基于 `ProgressInfo` 的滚动窗口计算瞬时速度

---

### 4. State (state.go)

状态持久化，使用 Protobuf V1 二进制格式：

**状态文件格式**:
```protobuf
message OShinState {
  int32                version         = 1;  // 版本号
  repeated string       urls            = 2;  // 下载源 URL 列表
  string               file_name       = 3;  // 输出文件名
  int64                total_size      = 4;  // 文件总大小（字节）
  int64                chunk_size      = 5;  // 分片大小（字节）
  string               et              = 6;  // 校验信息，格式 "type:value"
  repeated Chunk       chunks          = 7;  // 已完成的分片列表
  map<string, string>  headers         = 8;  // HTTP 自定义请求头
  string               protocol        = 9;  // 下载协议
  int32                connections     = 10; // 最大并发连接数
}

message Chunk {
  int32  id    = 1;  // 分片索引
  int64  start = 2;  // 起始字节位置（含）
  int64  end   = 3;  // 结束字节位置（含）
}
```

---

### 5. Types (types.go)

核心数据结构：

```go
// 任务状态
type TaskStatus int
const (
    TaskStatusPending     TaskStatus = 0  // 待开始
    TaskStatusProbing     TaskStatus = 1  // 探测中
    TaskStatusDownloading TaskStatus = 2  // 下载中
    TaskStatusVerifying   TaskStatus = 3  // 校验中
    TaskStatusCompleted   TaskStatus = 4  // 已完成
    TaskStatusFailed      TaskStatus = 5  // 失败
    TaskStatusPaused      TaskStatus = 6  // 已暂停
    TaskStatusResuming    TaskStatus = 7  // 恢复中
)

// 下载任务
type DownloadTask struct {
    mu          sync.RWMutex
    ID          string
    URL         string
    Protocol    Protocol
    FileName    string
    OutputPath  string
    FileSize    int64
    Metadata    *FileMetadata
    Config      *DownloadConfig
    Chunks      []*ChunkInfo
    Progress    *ProgressInfo
    Status      TaskStatus
    CreatedAt   time.Time
    UpdatedAt   time.Time
    MultiSource bool
    Verify      *VerifyResult
}

// 下载配置
type DownloadConfig struct {
    MaxConnections  int
    ChunkSize       int64
    Timeout         time.Duration
    Retry           int
    RetryDelay      time.Duration
    Headers         map[string]string
    OutputDir       string
    TempDir         string
    ChecksumType    string
    ChecksumValue   string
    AutoChecksum    bool
    TLSConfig       *TLSConfig
    FTPConfig       *FTPConfig
    MultiSources    []string
    AdaptiveTimeout bool
    NoResume        bool
}
```

---

## 下载流程

```
┌─────────────────────────────────────────────────────────────┐
│                      CLI / FFI                              │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       v
┌─────────────────────────────────────────────────────────────┐
│                     Engine                                  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │   Submit    │  │   Cancel    │  │   Pause     │        │
│  │   Resume    │  │   Remove    │  │   List      │        │
│  └─────────────┘  └─────────────┘  └─────────────┘        │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       v
┌─────────────────────────────────────────────────────────────┐
│                   Probe                                     │
│  - 检测协议                                                  │
│  - 获取文件大小                                              │
│  - 检查续传支持                                              │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       v
┌─────────────────────────────────────────────────────────────┐
│               HTTPDownloader / FTPDownloader / SFTPDownloader│
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │  分片创建   │  │  Worker Pool │  │  下载分片   │        │
│  │  (恢复)     │  │  (atomic)   │  │  (重试)     │        │
│  └─────────────┘  └─────────────┘  └─────────────┘        │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       v
┌─────────────────────────────────────────────────────────────┐
│              ProgressReporter                               │
│  - 实时进度（基于已完成分片数）                               │
│  - 速度计算（滚动窗口）                                      │
│  - spinner 动画                                              │
│  - ANSI 清行（终端）                                         │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       v
┌─────────────────────────────────────────────────────────────┐
│                   Verifier                                  │
│  - MD5/SHA256 校验                                          │
│  - 自动探测                                                  │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       v
┌─────────────────────────────────────────────────────────────┐
│                    State                                     │
│  - 保存进度到 .oshin 文件（Protobuf V1）                     │
│  - 支持断点续传                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## 并发模型

### Worker Pool

```go
// 使用 goroutine + atomic 原子索引实现
// 有效并发数 = min(分片数, 最大连接数)
effectiveConcurrency := min(chunkCount, config.MaxConnections)

var nextIndex int64  // 原子索引

// 每个 Worker 从共享索引获取分片
for i := 0; i < effectiveConcurrency; i++ {
    go func() {
        for {
            idx := int(atomic.AddInt64(&nextIndex, 1) - 1)
            if idx >= len(task.Chunks) {
                return
            }
            downloadChunk(task.Chunks[idx])
        }
    }()
}
```

---

## 断点续传机制

1. **保存时机**: 每个分片完成后保存到 .oshin 文件
2. **保存内容**: 已完成的分片列表、URL、校验信息等
3. **恢复流程**: 
   - 加载 .oshin 文件
   - 校验临时文件一致性（checksum/文件大小）
   - 跳过已完成的分片
   - 继续下载剩余分片
4. **状态清理**: 下载完成后删除 .oshin 文件

**任务状态流转**:
```
PENDING → PROBING → DOWNLOADING → VERIFYING → COMPLETED
                                 ↘ FAILED
                → PAUSED (Ctrl+C) → RESUMING → DOWNLOADING → ...
```

**Ctrl+C 暂停处理**:
- **TTY 模式**: bubbletea 捕获 `ctrl+c` 作为 KeyMsg 或 SIGINT → `tea.ErrInterrupted`，触发 `printInterruptSummary` 输出暂停摘要
- **非 TTY 模式**: signal.Notify 监听 SIGINT，同样触发 `printInterruptSummary`
- 暂停摘要包含：URL、文件名、大小、已下载进度、分片状态、校验和、协议、状态文件路径、续传提示

---

## 自适应超时

```go
// 基于历史连接时间动态调整超时
func (d *HTTPDownloader) getAdaptiveClient(url string, isRetry bool) *http.Client {
    if isRetry && d.fastFailClient != nil {
        return d.fastFailClient  // 重试时使用快速失败客户端
    }
    return d.client
}

// 记录最长成功连接时间
func (d *HTTPDownloader) updateMaxConnTime(duration time.Duration) {
    d.mu.Lock()
    defer d.mu.Unlock()
    if duration > d.maxConnTime {
        d.maxConnTime = duration
    }
}
```

---

## 内存管理

### FFI 字符串

所有 FFI 返回的字符串都需要手动释放：

```c
char* result = OShinD_GetTaskStatus(taskId);
// 使用 result
OShinD_FreeString(result);  // 必须释放
```

### 分片快照

使用 `GetChunkSnapshots()` 获取线程安全的分片快照：

```go
// 错误方式（数据竞争）
chunk := task.GetChunk(0)
println(chunk.Downloaded)

// 正确方式（线程安全）
snapshots := task.GetChunkSnapshots()
println(snapshots[0].Downloaded)
```
