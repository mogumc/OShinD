# OShinD

[English](docs/README-en.md)

跨平台多线程下载器，支持 HTTP/HTTPS/FTP/SFTP 协议，提供 CLI 和 FFI 两种使用方式。

## 特性

- **多协议支持** — HTTP/HTTPS、FTP、SFTP
- **多线程分片下载** — 最多 64 个并发连接
- **断点续传** — 自动保存下载状态，中断后可继续
- **Ctrl+C 暂停** — 按 Ctrl+C 暂停下载，输出详细摘要，重新运行即可续传
- **自定义请求头** — 支持自定义 HTTP 头，适用于鉴权、防盗链等场景
- **多来源下载** — 支持多个 CDN 同时下载同一文件
- **文件校验** — 自动探测并校验 MD5/SHA256
- **Protobuf 状态持久化** — 高效的二进制状态文件格式

## 快速开始

```bash
# 构建 CLI
cd src && go build -o oshind.exe ./cmd/cli/

# 下载文件
./oshind.exe dl https://example.com/file.zip

# 使用 8 个并发 + 自定义请求头
./oshind.exe dl https://example.com/file.zip -c 8 -H "Authorization: Bearer token"
```

## 文档

| 文档 | 说明 |
|------|------|
| [CLI 使用指南](docs/cli-guide.md) | 命令行参数、使用示例、Ctrl+C 暂停、断点续传、校验 |
| [FFI 接口文档](docs/ffi-api.md) | C/Python/Swift 接口、函数签名、集成示例 |
| [构建指南](docs/build-guide.md) | 环境要求、构建步骤、交叉编译、故障排除 |
| [架构设计](docs/architecture.md) | 模块说明、下载流程、并发模型、状态持久化 |

## 构建

```bash
# 使用 Makefile（推荐）
cd src && make build          # CLI + FFI
cd src && make build-cli      # 仅 CLI
cd src && make build-ffi      # 仅 FFI DLL
```

详见 [构建指南](docs/build-guide.md)。

## 许可证

[AGPL-3.0](LICENSE)
