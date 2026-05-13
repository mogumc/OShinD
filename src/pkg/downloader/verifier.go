package downloader

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/mogumc/oshind/types"
)

// Verifier 文件校验器
type Verifier struct {
}

// NewVerifier 创建新的校验器
func NewVerifier() *Verifier {
	return &Verifier{}
}

// Verify 校验文件完整性
func (v *Verifier) Verify(filePath string, checksumType string, expectedChecksum string) error {
	// 如果没有指定校验类型，跳过校验
	if checksumType == "" || expectedChecksum == "" {
		return nil
	}

	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// 根据校验类型计算哈希
	var actualChecksum string
	switch strings.ToLower(checksumType) {
	case "md5":
		actualChecksum, err = v.calculateMD5(file)
	case "sha256":
		actualChecksum, err = v.calculateSHA256(file)
	default:
		return fmt.Errorf("unsupported checksum type: %s", checksumType)
	}

	if err != nil {
		return fmt.Errorf("checksum calculation failed: %w", err)
	}

	// 比较校验和
	if !strings.EqualFold(actualChecksum, expectedChecksum) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

// calculateMD5 计算文件 MD5
func (v *Verifier) calculateMD5(file *os.File) (string, error) {
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// calculateSHA256 计算文件 SHA256
func (v *Verifier) calculateSHA256(file *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CalculateChecksum 计算文件校验和
func (v *Verifier) CalculateChecksum(filePath string, checksumType string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	switch strings.ToLower(checksumType) {
	case "md5":
		return v.calculateMD5(file)
	case "sha256":
		return v.calculateSHA256(file)
	default:
		return "", fmt.Errorf("unsupported checksum type: %s", checksumType)
	}
}

// CalculatePartialMD5 计算文件前 N 字节的 MD5
// 用于续传校验时的快速回退：比较临时文件前 chunk_size 字节与首 chunk 的预期内容
func CalculatePartialMD5(filePath string, size int64) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := md5.New()
	limitReader := io.LimitReader(file, size)
	if _, err := io.Copy(hash, limitReader); err != nil {
		return "", fmt.Errorf("failed to read partial data: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// AutoDetectChecksum 自动探测服务器提供的校验和
func AutoDetectChecksum(rawURL string, config *types.DownloadConfig) (checksumType string, checksumValue string, err error) {
	if config == nil {
		config = types.DefaultConfig()
	}

	// 发送 HEAD 请求
	client := &http.Client{
		Timeout: config.Timeout,
	}

	req, err := http.NewRequest("HEAD", rawURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}

	// 设置自定义头
	for key, value := range config.Headers {
		req.Header.Set(key, value)
	}

	req.Header.Set("User-Agent", "OShinD/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查 Content-MD5 头
	contentMD5 := resp.Header.Get("Content-MD5")
	if contentMD5 != "" {
		return "md5", contentMD5, nil
	}

	// 检查其他自定义校验头
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		if len(values) > 0 {
			if strings.Contains(lowerKey, "md5") {
				return "md5", values[0], nil
			}
			if strings.Contains(lowerKey, "sha256") || strings.Contains(lowerKey, "sha-256") {
				return "sha256", values[0], nil
			}
		}
	}

	return "", "", nil
}

// CalculateHash 计算文件哈希并返回值（不比较）
func (v *Verifier) CalculateHash(filePath string, checksumType string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	switch strings.ToLower(checksumType) {
	case "md5":
		return v.calculateMD5(file)
	case "sha256":
		return v.calculateSHA256(file)
	default:
		return "", fmt.Errorf("unsupported checksum type: %s", checksumType)
	}
}

// VerifyTask 校验下载任务的文件
// 校验优先级：1. 显式配置 > 2. Probe 阶段已获取的 Content-MD5 > 3. ETag（若为 32 位十六进制则视为 MD5）
// 优化：只读文件一次算哈希，避免 Verify() + CalculateChecksum() 重复读取
func VerifyTask(task *types.DownloadTask, filePath string) *types.VerifyResult {
	verifier := NewVerifier()

	// 确定校验类型和期望值
	var checksumType, checksumValue string

	// 优先级 1：显式配置
	if task.Config.ChecksumType != "" && task.Config.ChecksumValue != "" {
		checksumType = task.Config.ChecksumType
		checksumValue = task.Config.ChecksumValue
	} else if task.Config.AutoChecksum {
		// 优先级 2：Probe 阶段已获取的 Content-MD5（避免重复发 HEAD 请求）
		if task.Metadata != nil && task.Metadata.Checksum != "" {
			checksumType = "md5"
			checksumValue = task.Metadata.Checksum
		} else if task.Metadata != nil && isMD5Hex(task.Metadata.ETag) {
			// 优先级 3：ETag 为 32 位十六进制视为 MD5
			checksumType = "md5"
			checksumValue = task.Metadata.ETag
		}
	}

	// 无校验源，跳过
	if checksumType == "" || checksumValue == "" {
		return &types.VerifyResult{Skipped: true}
	}

	result := &types.VerifyResult{
		Method:   checksumType,
		Expected: checksumValue,
	}

	// 只读文件一次计算哈希，手动比较（避免 Verify + CalculateChecksum 两次读取）
	actual, err := verifier.CalculateHash(filePath, checksumType)
	if err != nil {
		result.Actual = ""
		result.Passed = false
		return result
	}
	result.Actual = actual
	result.Passed = strings.EqualFold(actual, checksumValue)
	return result
}

// isMD5Hex 判断字符串是否为 32 位十六进制（即 MD5 哈希值）
// 部分 CDN 服务器（如 biligame.com）将 MD5 作为 ETag 返回
var md5HexRegex = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

func isMD5Hex(s string) bool {
	// HTTP ETag 值可能带引号，去掉后再匹配
	s = strings.Trim(s, "\"")
	return md5HexRegex.MatchString(s)
}
