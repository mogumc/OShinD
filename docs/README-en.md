# OShinD

[中文](../README.md)

Cross-platform multi-threaded downloader supporting HTTP/HTTPS/TCP/FTP/SFTP protocols, available as both CLI and FFI library.

## Features

- **Multi-protocol** — HTTP/HTTPS, TCP, FTP, SFTP
- **Multi-threaded chunked download** — up to 64 concurrent connections
- **Resume support** — auto-saves download state, resumes after interruption
- **Custom headers** — supports custom HTTP headers for auth, anti-hotlinking, etc.
- **Multi-source download** — download from multiple CDNs simultaneously
- **File verification** — auto-detects and verifies MD5/SHA256
- **Protobuf state persistence** — efficient binary state file format

## Quick Start

```bash
# Build CLI
cd src && go build -o oshind ./cmd/cli/

# Download a file
./oshind dl https://example.com/file.zip

# 8 concurrent connections + custom headers
./oshind dl https://example.com/file.zip -c 8 -H "Authorization: Bearer token"
```

## Documentation

| Document | Description |
|----------|-------------|
| [CLI Guide](cli-guide.md) | Command-line arguments, usage examples, resume, verification |
| [FFI API](ffi-api.md) | C/Python/Swift interfaces, function signatures, integration examples |
| [Build Guide](build-guide.md) | Requirements, build steps, cross-compilation, troubleshooting |
| [Architecture](architecture.md) | Module overview, download flow, concurrency model, state persistence |

## Build

```bash
# Using Makefile (recommended)
cd src && make build          # CLI + FFI
cd src && make build-cli      # CLI only
cd src && make build-ffi      # FFI DLL only
```

See [Build Guide](build-guide.md) for details.

## License

[AGPL-3.0](../LICENSE)
