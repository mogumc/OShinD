package downloader

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mogumc/oshind/types"
)

// applyHeaders 将自定义请求头应用到 HTTP 请求
// 特殊处理 Host 头：Go 的 http.Request.Host 字段控制实际发送的 Host 头，
// 通过 Header.Set("Host", ...) 设置会被忽略
func applyHeaders(req *http.Request, headers map[string]string) {
	for key, value := range headers {
		if strings.EqualFold(key, "host") {
			req.Host = value
		} else {
			req.Header.Set(key, value)
		}
	}
}

// Probe 通过 HEAD 请求获取服务器资源元信息和断点续传支持情况
func Probe(rawURL string, config *types.DownloadConfig) (*types.FileMetadata, error) {
	if config == nil {
		config = types.DefaultConfig()
	}

	client := newProbeClient(config)

	req, err := http.NewRequest("HEAD", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "OShinD/1.0")

	applyHeaders(req, config.Headers)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()

	metadata := &types.FileMetadata{
		Size:          -1,
		SupportResume: false,
	}

	contentLength := resp.Header.Get("Content-Length")
	if contentLength != "" {
		size, err := strconv.ParseInt(contentLength, 10, 64)
		if err == nil {
			metadata.Size = size
		}
	}

	acceptRange := resp.Header.Get("Accept-Ranges")
	if acceptRange != "" {
		metadata.SupportResume = true
		metadata.AcceptRange = acceptRange
	}

	etag := resp.Header.Get("ETag")
	if etag != "" {
		metadata.ETag = strings.Trim(etag, "\"")
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "" {
		metadata.ContentType = contentType
	}

	contentMD5 := resp.Header.Get("Content-MD5")
	if contentMD5 != "" {
		metadata.Checksum = contentMD5
	}

	if !metadata.SupportResume && metadata.Size > 0 {
		metadata.SupportResume = probeResumeWithRange(rawURL, metadata.Size, config)
	}

	return metadata, nil
}

// ProbeFull 执行完整的下载前探测，返回详细的服务器信息和连接测试结果
func ProbeFull(rawURL string, config *types.DownloadConfig) (*ProbeResult, error) {
	if config == nil {
		config = types.DefaultConfig()
	}

	result := &ProbeResult{
		URL:       rawURL,
		Timestamp: time.Now(),
	}

	metadata, err := Probe(rawURL, config)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	result.Metadata = metadata

	speed, err := probeConnectionSpeed(rawURL, config)
	if err != nil {
		result.SpeedTestError = err.Error()
	} else {
		result.EstimatedSpeed = speed
	}

	result.ServerInfo = parseServerInfo(rawURL)

	return result, nil
}

// ProbeResult 完整探测结果
type ProbeResult struct {
	URL            string              `json:"url"`
	Timestamp      time.Time           `json:"timestamp"`
	Metadata       *types.FileMetadata `json:"metadata"`
	EstimatedSpeed float64             `json:"estimated_speed"` // 估算速度（字节/秒）
	ServerInfo     *ServerInfo         `json:"server_info"`
	Error          string              `json:"error,omitempty"`
	SpeedTestError string              `json:"speed_test_error,omitempty"`
}

// ServerInfo 服务器信息
type ServerInfo struct {
	Host   string `json:"host"`
	Port   string `json:"port"`
	Scheme string `json:"scheme"`
	Path   string `json:"path"`
}

// parseServerInfo 解析 URL 中的服务器信息
func parseServerInfo(rawURL string) *ServerInfo {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	return &ServerInfo{
		Host:   host,
		Port:   port,
		Scheme: u.Scheme,
		Path:   u.Path,
	}
}

// probeConnectionSpeed 通过下载小块数据测试连接速度
func probeConnectionSpeed(rawURL string, config *types.DownloadConfig) (float64, error) {
	client := newProbeClient(config)

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("Range", "bytes=0-262143") // 256KB
	req.Header.Set("User-Agent", "OShinD/1.0")

	applyHeaders(req, config.Headers)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	buf := make([]byte, 256*1024)
	n, _ := io.ReadFull(resp.Body, buf)

	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0, nil
	}

	speed := float64(n) / elapsed
	return speed, nil
}

// probeResumeWithRange 通过 Range 请求探测断点续传支持
func probeResumeWithRange(rawURL string, fileSize int64, config *types.DownloadConfig) bool {
	client := newProbeClient(config)

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return false
	}

	if fileSize >= 0 {
		req.Header.Set("Range", "bytes=0-0")
	}

	req.Header.Set("User-Agent", "OShinD/1.0")

	applyHeaders(req, config.Headers)

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// 206 Partial Content 表示服务器支持 Range 请求
	return resp.StatusCode == http.StatusPartialContent
}

// DetectProtocol 从 URL scheme 自动推断下载协议
func DetectProtocol(rawURL string) (types.Protocol, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http":
		return types.ProtocolHTTP, nil
	case "https":
		return types.ProtocolHTTPS, nil
	case "ftp":
		return types.ProtocolFTP, nil
	case "sftp":
		return types.ProtocolSFTP, nil
	default:
		return 0, fmt.Errorf("unsupported protocol: %s", scheme)
	}
}

// newProbeClient 创建带 TLS 配置的探测用 HTTP 客户端
func newProbeClient(config *types.DownloadConfig) *http.Client {
	transport := &http.Transport{
		ResponseHeaderTimeout: config.Timeout,
	}
	if config.TLSConfig != nil && config.TLSConfig.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	return &http.Client{
		Timeout:   config.Timeout,
		Transport: transport,
	}
}

// ExtractFileInfo 从 URL path 中提取文件名，处理编码和默认值
func ExtractFileInfo(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}

	path := u.Path
	if path == "" || path == "/" {
		return "unknown"
	}

	parts := strings.Split(path, "/")
	fileName := parts[len(parts)-1]

	decoded, err := url.QueryUnescape(fileName)
	if err != nil {
		return fileName
	}

	if decoded == "" {
		return "unknown"
	}

	return decoded
}
