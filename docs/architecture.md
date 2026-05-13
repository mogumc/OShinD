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
│   │   ├── state.go            # 状态持久化 (Protobuf V3 + JSON V2/V1)
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
    tasks          map[string]*DownloadTask      // 任务列表
    cancelFuncs    map[string]context.CancelFunc  // 取消函数
    reporters      map[string]*ProgressReporter   // 进度报告器
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
- `PauseTask()` - 暂停任务
- `GetTask()` - 获取任务状态

---

### 2. HTTPDownloader (http.go)

HTTP/HTTPS 下载器，实现：
- 多线程分片下载
- 自适应超时
- 慢启动
- 工作窃取
- 断点续传

```go
type HTTPDownloader struct {
    config        *DownloadConfig
    transport     *http.Transport
    fastFailClient *http.Client
    mu            sync.Mutex  // 保护 maxConnTime/fastFailClient
}
```

**下载流程**:
1. 探测服务器（Probe）
2. 创建分片（Chunks）
3. 启动 Worker Pool
4. 下载分片
5. 校验文件
6. 清理状态

---

### 3. ProgressReporter (http.go)

进度报告器，负责：
- 实时计算下载速度
- 格式化进度显示
- 支持 ANSI 清行

```go
type ProgressReporter struct {
    task           *DownloadTask
    interval       time.Duration
    done           chan struct{}
    maxOutputLines int  // 历史最大输出行数
}
```

**显示格式**:
```
  [i] Downloading file.zip
      Progress: 45.2% (540.0 MB / 1.2 GB)
      Speed:    12.5 MB/s
      Threads:  4/4 active
      ETA:      1m 12s
```

---

### 4. State (state.go)

状态持久化，支持：
- Protobuf V3（二进制格式）
- JSON V2（精简格式）
- JSON V1（完整格式）
- 自动兼容旧格式

**状态文件格式**:
```protobuf
message OShinState {
    int32 v = 1;
    string url = 2;
    string file_name = 3;
    int64 total_size = 4;
    int64 chunk_size = 5;
    string et = 6;
    repeated OShinChunk chunks = 7;
}

message OShinChunk {
    int32 id = 1;
    int64 start = 2;
    int64 end = 3;
}
```

---

### 5. Types (types.go)

核心数据结构：

```go
// 下载任务
type DownloadTask struct {
    ID         string
    URL        string
    FileName   string
    Protocol   Protocol
    Config     *DownloadConfig
    Metadata   FileMetadata
    Progress   *ProgressInfo
    Chunks     []*ChunkInfo
    Status     TaskStatus
    // ...
}

// 下载配置
type DownloadConfig struct {
    MaxConnections int
    ChunkSize      int64
    Timeout        time.Duration
    Retry          int
    Headers        map[string]string
    // ...
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
│               HTTPDownloader                                │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │  分片创建   │  │  Worker Pool │  │  下载分片   │        │
│  └─────────────┘  └─────────────┘  └─────────────┘        │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       v
┌─────────────────────────────────────────────────────────────┐
│              ProgressReporter                               │
│  - 实时进度                                                  │
│  - 速度计算                                                  │
│  - ANSI 显示                                                 │
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
│  - 保存进度到 .oshin 文件                                    │
│  - 支持断点续传                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## 并发模型

### Worker Pool

```go
// 有效并发数 = min(分片数, 最大连接数)
effectiveConcurrency = min(chunkCount, MaxConnections)

// 每个 Worker 从共享 index 队列获取分片
for chunkIdx := range indexQueue {
    downloadChunk(chunkIdx)
}
```

### 工作窃取

当一个 Worker 完成后，可以窃取其他慢 Worker 的分片：

```go
// 慢 Worker 检测
if time.Since(chunk.StartTime) > slowThreshold {
    // 释放分片到队列
    indexQueue <- chunk.Index
}
```

---

## 断点续传机制

1. **保存时机**: 每个分片完成后保存
2. **保存内容**: 已完成的分片列表
3. **恢复流程**: 
   - 加载 .oshin 文件
   - 跳过已完成的分片
   - 继续下载剩余分片
4. **状态清理**: 下载完成后删除 .oshin 文件

---

## 自适应超时

```go
// 基于历史连接时间动态调整超时
if connTime < avgTime * 0.5 {
    // 快速连接，减少超时
    timeout = minTimeout
} else if connTime > avgTime * 2 {
    // 慢连接，增加超时
    timeout = maxTimeout
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
