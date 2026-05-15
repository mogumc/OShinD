# 构建指南

## 环境要求

- Go 1.25+
- GCC (Windows 需要 MinGW 或类似工具链用于 CGO)
- Protocol Buffers 编译器 (protoc) - 仅在修改 proto 文件时需要

## 快速构建

### CLI 可执行文件

```bash
# Linux/macOS
go build -o oshind ./cmd/cli/

# Windows
go build -o oshind.exe ./cmd/cli/
```

### FFI 动态库

```bash
# Windows (DLL)
CGO_ENABLED=1 GOOS=windows go build -buildmode=c-shared -o oshind.dll ./cmd/ffi/

# macOS (dylib)
CGO_ENABLED=1 GOOS=darwin go build -buildmode=c-shared -o oshind.dylib ./cmd/ffi/

# Linux (so)
CGO_ENABLED=1 GOOS=linux go build -buildmode=c-shared -o oshind.so ./cmd/ffi/
```

产物：
- `oshind.dll` / `oshind.dylib` / `oshind.so` - 动态库
- `oshind.h` - C 头文件

---

## 详细构建步骤

### 1. 克隆项目

```bash
git clone https://github.com/mogumc/oshind.git
cd oshind
```

### 2. 下载依赖

```bash
go mod download
```

### 3. 编译 Protobuf (可选)

如果修改了 `pkg/downloader/proto/oshin_state.proto`，需要重新编译：

```bash
# 安装 protoc-gen-go
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

# 编译 proto 文件
protoc --go_out=. --go_opt=paths=source_relative pkg/downloader/proto/oshin_state.proto
```

### 4. 构建 CLI

```bash
go build -o oshind ./cmd/cli/
```

### 5. 构建 FFI DLL

```bash
CGO_ENABLED=1 GOOS=windows go build -buildmode=c-shared -o oshind.dll ./cmd/ffi/
```

---

## 交叉编译

### 从 Linux 交叉编译 Windows

```bash
# 需要安装 mingw-w64
sudo apt install gcc-mingw-w64

# 构建 Windows DLL
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -buildmode=c-shared -o oshind.dll ./cmd/ffi/
```

### 从 macOS 交叉编译 Linux

```bash
# 需要安装 linux 工具链
# 使用 Docker 或 CI/CD 环境推荐
```

---

## 测试

### 运行测试

```bash
go test ./...
```

### 构建验证

```bash
# 验证构建
go build ./...

# 静态分析
go vet ./...
```

### FFI 测试

使用 Python ctypes 测试 FFI 接口：

```bash
# 先构建 DLL
go build -buildmode=c-shared -o oshind.dll ./cmd/ffi/

# 使用 Python 测试（参考 docs/ffi-api.md 中的示例）
python -c "
import ctypes
lib = ctypes.CDLL('./oshind.dll')
lib.OShinD_Version.argtypes = []
lib.OShinD_Version.restype = ctypes.c_char_p
print('Version:', lib.OShinD_Version().decode())
"
```

---

## 发布流程

### 版本号

版本通过 `ldflags` 在编译时注入，源码中的 `var version = "1.0.0"` 为默认值：

```bash
# Makefile 自动从 git tag 获取版本号
cd src && make build          # 版本从 git describe --tags 获取
make tag VERSION=1.1.0        # 创建 git tag
```

### 发布流程

1. 确认代码已合并到主分支
2. 创建 git tag（版本号不带 `v` 前缀）
3. 推送 tag 触发 GitHub Actions 自动构建

```bash
make tag VERSION=1.1.0
git push origin v1.1.0
```

GitHub Actions 会自动构建多平台 CLI + FFI 产物并创建 Release。

---

## 故障排除

### CGO 编译错误

```
exec: "gcc": executable file not found in $PATH
```

**解决方案**: 安装 GCC
- Windows: 安装 MinGW 或 MSYS2
- Linux: `sudo apt install gcc`
- macOS: `xcode-select --install`

### Protobuf 编译错误

```
protoc: program not found
```

**解决方案**: 安装 protoc
```bash
# macOS
brew install protobuf

# Linux
sudo apt install protobuf-compiler

# Windows
# 下载 https://github.com/protocolbuffers/protobuf/releases
```

### DLL 加载失败

```
OSError: [WinError 193] %1 is not a valid Win32 application
```

**解决方案**: 确保使用匹配的架构 (32/64 位)

---

## 性能优化

### 构建优化

```bash
# 禁用调试信息
go build -ldflags="-s -w" -o oshind ./cmd/cli/

# 启用链接器优化
go build -gcflags="-l" -o oshind ./cmd/cli/
```

### 运行时优化

```bash
# 增加并发数
./oshind dl <url> -c 16

# 增加分片大小
./oshind dl <url> -s 16m
```
