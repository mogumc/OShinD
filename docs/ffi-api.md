# FFI 接口文档

OShinD 提供 C 兼容的 FFI 接口，用于移动端（iOS/Android）集成。

## 构建 DLL

```bash
# Windows
CGO_ENABLED=1 GOOS=windows go build -buildmode=c-shared -o oshind.dll ./cmd/ffi/

# macOS
CGO_ENABLED=1 GOOS=darwin go build -buildmode=c-shared -o oshind.dylib ./cmd/ffi/

# Linux
CGO_ENABLED=1 GOOS=linux go build -buildmode=c-shared -o oshind.so ./cmd/ffi/
```

产物：
- `oshind.dll` / `oshind.dylib` / `oshind.so` - 动态库
- `oshind.h` - C 头文件

---

## 函数列表

### 版本信息

#### `OShinD_Version`

```c
char* OShinD_Version(void)
```

返回版本号字符串。

**返回值**: 版本号，如 `"1.3.0"`

---

### 下载任务

#### `OShinD_Download`

```c
char* OShinD_Download(char* url, char* outputDir, int connections)
```

异步启动下载任务。

**参数**:
- `url` - 下载 URL
- `outputDir` - 输出目录
- `connections` - 并发连接数 (1-64)

**返回值**: 任务 ID（用于后续操作），失败返回空字符串

---

#### `OShinD_DownloadWithResume`

```c
char* OShinD_DownloadWithResume(char* url, char* outputDir, int connections, int noResume)
```

下载任务（支持续传控制）。

**参数**:
- `url` - 下载 URL
- `outputDir` - 输出目录
- `connections` - 并发连接数
- `noResume` - 是否强制全新下载 (0=使用续传, 1=强制全新)

**返回值**: 任务 ID

---

#### `OShinD_DownloadWithHeaders`

```c
char* OShinD_DownloadWithHeaders(char* url, char* outputDir, int connections, int noResume, char* headersJson)
```

下载任务（支持自定义请求头）。

**参数**:
- `url` - 下载 URL
- `outputDir` - 输出目录
- `connections` - 并发连接数
- `noResume` - 是否强制全新下载
- `headersJson` - JSON 格式的请求头，如 `{"User-Agent":"Mozilla/5.0","Referer":"https://example.com"}`

**返回值**: 任务 ID

---

#### `OShinD_DownloadMultiSource`

```c
char* OShinD_DownloadMultiSource(char* url, char* outputDir, int connections, char** sources, int sourceCount)
```

多来源下载。

**参数**:
- `url` - 主 URL
- `outputDir` - 输出目录
- `connections` - 并发连接数
- `sources` - 额外来源 URL 数组
- `sourceCount` - 额外来源数量

**返回值**: 任务 ID

---

### 任务控制

#### `OShinD_GetTaskStatus`

```c
char* OShinD_GetTaskStatus(char* taskID)
```

查询任务状态。

**参数**:
- `taskID` - 任务 ID

**返回值**: JSON 格式的任务状态

**JSON 结构**:
```json
{
  "id": "task-id",
  "url": "https://example.com/file.zip",
  "file_name": "file.zip",
  "status": "DOWNLOADING",
  "progress": 45.2,
  "speed": 12500000,
  "downloaded": 540000000,
  "total": 1200000000,
  "chunks": [...],
  "protocol": "HTTPS",
  "active_threads": 4,
  "remaining_chunks": 2,
  "failed_chunks": 0,
  "created_at": "2026-05-13T12:00:00Z",
  "updated_at": "2026-05-13T12:01:30Z"
}
```

**状态值**:
- `PENDING` - 待开始
- `PROBING` - 探测中
- `DOWNLOADING` - 下载中
- `VERIFYING` - 校验中
- `COMPLETED` - 已完成
- `FAILED` - 失败
- `PAUSED` - 已暂停

---

#### `OShinD_GetChunkStatus`

```c
char* OShinD_GetChunkStatus(char* taskID, int chunkIndex)
```

查询分片状态。

**参数**:
- `taskID` - 任务 ID
- `chunkIndex` - 分片索引

**返回值**: JSON 格式的分片状态

**JSON 结构**:
```json
{
  "index": 0,
  "start": 0,
  "end": 8388607,
  "status": "COMPLETED",
  "downloaded": 8388608,
  "speed": 3125000,
  "retry_count": 0,
  "headers": {"Range": "bytes=0-8388607"}
}
```

---

#### `OShinD_PauseTask`

```c
char* OShinD_PauseTask(char* taskID)
```

暂停下载任务。

**参数**:
- `taskID` - 任务 ID

**返回值**: 暂停后的任务状态 JSON

---

#### `OShinD_CancelTask`

```c
int OShinD_CancelTask(char* taskID)
```

取消下载任务。

**参数**:
- `taskID` - 任务 ID

**返回值**: 1=成功, 0=失败

---

#### `OShinD_RemoveTask`

```c
int OShinD_RemoveTask(char* taskID)
```

移除任务（清理资源）。

**参数**:
- `taskID` - 任务 ID

**返回值**: 1=成功, 0=失败

---

### 续传状态

#### `OShinD_HasResumeState`

```c
char* OShinD_HasResumeState(char* outputDir, char* fileName)
```

查询是否有续传状态。

**参数**:
- `outputDir` - 输出目录
- `fileName` - 文件名

**返回值**: JSON 格式的续传状态

**JSON 结构**:
```json
{
  "exists": true,
  "v": 3,
  "url": "https://example.com/file.zip",
  "file_name": "file.zip",
  "total_size": 1200000000,
  "chunk_size": 8388608,
  "chunks": [
    {"id": 0, "start": 0, "end": 8388607},
    {"id": 1, "start": 8388608, "end": 16777215}
  ]
}
```

---

#### `OShinD_ClearResumeState`

```c
int OShinD_ClearResumeState(char* outputDir, char* fileName)
```

清除续传状态。

**参数**:
- `outputDir` - 输出目录
- `fileName` - 文件名

**返回值**: 1=成功, 0=失败

---

### 内存管理

#### `OShinD_FreeString`

```c
void OShinD_FreeString(char* str)
```

释放由 FFI 函数返回的字符串内存。

**重要**: 所有返回 `char*` 的函数都需要调用此函数释放内存。

---

## Python 集成示例

### 基本封装

```python
import ctypes
import json

class OShinD:
    def __init__(self, dll_path="oshind.dll"):
        self.lib = ctypes.CDLL(dll_path)
        self._setup_signatures()
    
    def _setup_signatures(self):
        self.lib.OShinD_Version.argtypes = []
        self.lib.OShinD_Version.restype = ctypes.c_char_p
        
        self.lib.OShinD_DownloadWithHeaders.argtypes = [
            ctypes.c_char_p, ctypes.c_char_p, ctypes.c_int, ctypes.c_int, ctypes.c_char_p
        ]
        self.lib.OShinD_DownloadWithHeaders.restype = ctypes.c_char_p
        
        self.lib.OShinD_GetTaskStatus.argtypes = [ctypes.c_char_p]
        self.lib.OShinD_GetTaskStatus.restype = ctypes.c_char_p
        
        self.lib.OShinD_PauseTask.argtypes = [ctypes.c_char_p]
        self.lib.OShinD_PauseTask.restype = ctypes.c_char_p
        
        self.lib.OShinD_CancelTask.argtypes = [ctypes.c_char_p]
        self.lib.OShinD_CancelTask.restype = ctypes.c_int
        
        self.lib.OShinD_RemoveTask.argtypes = [ctypes.c_char_p]
        self.lib.OShinD_RemoveTask.restype = ctypes.c_int
    
    def version(self):
        return self.lib.OShinD_Version().decode()
    
    def download(self, url, output_dir, connections=4, headers=None):
        headers_json = json.dumps(headers) if headers else "{}"
        task_id = self.lib.OShinD_DownloadWithHeaders(
            url.encode(), output_dir.encode(), connections, 0, headers_json.encode()
        )
        return task_id.decode() if task_id else None
    
    def get_status(self, task_id):
        raw = self.lib.OShinD_GetTaskStatus(task_id.encode())
        return json.loads(raw.decode()) if raw else {}
    
    def pause(self, task_id):
        raw = self.lib.OShinD_PauseTask(task_id.encode())
        return json.loads(raw.decode()) if raw else {}
    
    def cancel(self, task_id):
        return self.lib.OShinD_CancelTask(task_id.encode()) == 1
    
    def remove(self, task_id):
        return self.lib.OShinD_RemoveTask(task_id.encode()) == 1


# 使用示例
oshind = OShinD("oshind.dll")
print(f"版本: {oshind.version()}")

# 带请求头下载
task_id = oshind.download(
    "https://example.com/file.zip",
    "./downloads",
    headers={"User-Agent": "Mozilla/5.0", "Authorization": "Bearer token"}
)
print(f"任务 ID: {task_id}")

# 轮询状态
import time
while True:
    status = oshind.get_status(task_id)
    print(f"进度: {status['progress']:.1f}% - {status['status']}")
    if status["status"] in ("COMPLETED", "FAILED", "PAUSED"):
        break
    time.sleep(0.5)
```

---

## iOS 集成示例

### Swift 桥接

```swift
import Foundation

class OShinD {
    private let handle: UnsafeMutableRawPointer
    
    init?(dllPath: String) {
        guard let h = dlopen(dllPath, RTLD_LAZY) else { return nil }
        handle = h
    }
    
    func download(url: String, outputDir: String, headers: [String: String] = [:]) -> String? {
        guard let sym = dlsym(handle, "OShinD_DownloadWithHeaders") else { return nil }
        
        typealias DownloadFunc = @convention(c) (UnsafePointer<CChar>, UnsafePointer<CChar>, Int32, Int32, UnsafePointer<CChar>) -> UnsafePointer<CChar>?
        let download = unsafeBitCast(sym, to: DownloadFunc.self)
        
        let headersJson = try? JSONSerialization.data(withJSONObject: headers)
        let headersStr = headersJson.flatMap { String(data: $0, encoding: .utf8) } ?? "{}"
        
        return url.withCString { urlCStr in
            outputDir.withCString { dirCStr in
                headersStr.withCString { headersCStr in
                    download(urlCStr, dirCStr, 4, 0, headersCStr)?.pointee
                }
            }
        }
    }
}
```

---

## 错误处理

所有返回 `char*` 的函数在失败时返回空字符串 `""`。

所有返回 `int` 的函数在失败时返回 `0`。

建议在调用后检查返回值：

```python
task_id = lib.OShinD_DownloadWithHeaders(...)
if not task_id:
    print("下载启动失败")
else:
    print(f"任务 ID: {task_id}")
```
