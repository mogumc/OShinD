# CLI 使用指南

## 安装

```bash
# 从源码构建
go build -o oshind ./cmd/cli/

# Windows
go build -o oshind.exe ./cmd/cli/
```

## 命令结构

```
oshind <command> [arguments]
```

### 可用命令

| 命令 | 别名 | 说明 |
|------|------|------|
| `download` | `dl` | 下载文件 |
| `probe` | - | 探测服务器信息 |
| `version` | `-v`, `--version` | 显示版本 |
| `help` | `-h`, `--help` | 显示帮助 |

---

## 下载文件

### 基本用法

```bash
oshind download <url> [options]
oshind dl <url> [options]
```

### 示例

```bash
# 基本下载
oshind dl https://example.com/file.zip

# 指定输出目录
oshind dl https://example.com/file.zip -o ./downloads/file.zip

# 指定输出目录（自动使用原始文件名）
oshind dl https://example.com/file.zip -o ./downloads/

# 使用 8 个并发连接
oshind dl https://example.com/file.zip -c 8

# 设置分片大小为 4MB
oshind dl https://example.com/file.zip -s 4m

# 自定义请求头
oshind dl https://example.com/file.zip -H "Authorization: Bearer token123"
oshind dl https://example.com/file.zip -H "Referer: https://example.com" -H "Cookie: session=abc"

# 强制全新下载（忽略断点续传）
oshind dl https://example.com/file.zip --no-resume

# 设置校验和
oshind dl https://example.com/file.zip --checksum md5:abc123def456789...
oshind dl https://example.com/file.zip --checksum sha256:abc123...

# 多来源下载
oshind dl https://cdn1.example.com/file.zip -m https://cdn2.example.com/file.zip
```

### FTP/SFTP 下载

```bash
# FTP 下载
oshind dl ftp://example.com/file.zip -u username -p password

# FTP 指定端口
oshind dl ftp://example.com/file.zip -u user -p pass --ftp-port 2121

# SFTP 下载
oshind dl sftp://example.com/file.zip -u root -p password

# SFTP 跳过 TLS 验证
oshind dl sftp://example.com/file.zip -u root --skip-tls-verify
```

### TCP 下载

```bash
# TCP 原始数据下载
oshind dl tcp://example.com:8080 -o ./download/data.bin
```

---

## 参数说明

### 通用参数

| 参数 | 简写 | 说明 | 默认值 |
|------|------|------|--------|
| `--output` | `-o` | 输出路径 | 当前目录 |
| `--connections` | `-c` | 并发连接数 (1-64) | 4 |
| `--chunk-size` | `-s` | 分片大小 | 8m |
| `--timeout` | `-t` | 请求超时 | 30s |
| `--retry` | `-r` | 重试次数 | 3 |

### HTTP 专用参数

| 参数 | 说明 |
|------|------|
| `-H, --header <k:v>` | 自定义请求头（可重复使用） |
| `-m, --multi-source <url>` | 额外下载源（可重复使用） |
| `--no-resume` | 强制全新下载 |
| `--no-checksum` | 禁用自动校验 |
| `--checksum <type:value>` | 设置校验和 |

### FTP/SFTP 参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-u, --user` | 用户名 | - |
| `-p, --password` | 密码 | - |
| `--ftp-port` | FTP 端口 | 21 |
| `--sftp-port` | SFTP 端口 | 22 |
| `--skip-tls-verify` | 跳过 TLS 验证 | false |

### 分片大小格式

支持以下格式：
- `64k` - 64 KB
- `1m` - 1 MB
- `4m` - 4 MB
- `1g` - 1 GB
- 纯数字表示字节数

---

## 探测服务器

```bash
oshind probe <url>
```

探测服务器信息，显示：
- 文件大小
- 是否支持断点续传
- 支持的校验类型
- 服务器标识 (ETag)

### 示例

```bash
$ oshind probe https://example.com/file.zip
[i] Probing server...
    URL:        https://example.com/file.zip
    Size:       1.2 GB (1,200,000,000 bytes)
    Resume:     Supported
    ETag:       "abc123"
    Checksum:   MD5 available
```

---

## 断点续传

OShinD 自动处理断点续传：

1. 下载过程中会保存状态到 `.oshin` 文件
2. 中断后重新执行相同命令，会自动检测并继续下载
3. 使用 `--no-resume` 强制重新下载
4. 使用 `probe` 命令查看续传状态

### 状态文件

状态文件保存在输出文件同目录，格式为 `.oshin`：
```
downloads/
├── file.zip
└── file.zip.oshin
```

---

## 校验

### 自动校验

默认情况下，OShinD 会自动探测服务器提供的校验信息并进行校验。

### 手动指定校验和

```bash
# MD5 校验
oshind dl https://example.com/file.zip --checksum md5:abc123def456...

# SHA256 校验
oshind dl https://example.com/file.zip --checksum sha256:abc123...

# 禁用校验
oshind dl https://example.com/file.zip --no-checksum
```

---

## 中断与控制

### Ctrl+C 中断

按下 `Ctrl+C` 会：
1. 停止当前下载
2. 保存下载状态
3. 显示当前进度
4. 可以重新执行命令继续下载

### 进度显示

下载过程中会显示：
```
  [i] Downloading file.zip
      Progress: 45.2% (540.0 MB / 1.2 GB)
      Speed:    12.5 MB/s
      Threads:  4/4 active
      ETA:      1m 12s
```
