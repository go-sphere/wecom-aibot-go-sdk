package aibot

import (
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// FileResult 文件下载结果
type FileResult struct {
	Buffer   []byte
	Filename string
}

// WeComApiClient 企业微信 API 客户端
// 仅负责文件下载等 HTTP 辅助功能，消息收发均走 WebSocket 通道
type WeComApiClient struct {
	httpClient *http.Client
	logger     Logger
}

// NewWeComApiClient 创建 API 客户端
func NewWeComApiClient(logger Logger, timeout int) *WeComApiClient {
	if timeout <= 0 {
		timeout = 10000
	}

	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Millisecond,
	}

	return &WeComApiClient{
		httpClient: client,
		logger:     logger,
	}
}

// DownloadFileRaw 下载文件（返回原始数据及文件名）
func (c *WeComApiClient) DownloadFileRaw(fileURL string) (*FileResult, error) {
	c.logger.Info("Downloading file...")

	resp, err := c.httpClient.Get(fileURL)
	if err != nil {
		c.logger.Error("File download failed: " + err.Error())
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("File download failed, status: " + resp.Status)
		return nil, err
	}

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body: " + err.Error())
		return nil, err
	}

	// 从 Content-Disposition 头中解析文件名
	filename := parseFilename(resp.Header.Get("Content-Disposition"))

	c.logger.Info("File downloaded successfully")

	return &FileResult{
		Buffer:   body,
		Filename: filename,
	}, nil
}

// parseFilename 从 Content-Disposition 头解析文件名
func parseFilename(contentDisposition string) string {
	if contentDisposition == "" {
		return ""
	}

	// 优先匹配 filename*=UTF-8''xxx 格式 (RFC 5987)
	if strings.Contains(contentDisposition, "filename*=") {
		// 尝试解析 filename*=UTF-8''
		parts := strings.Split(contentDisposition, "filename*=")
		if len(parts) > 1 {
			value := strings.TrimSpace(parts[1])
			// 可能是 UTF-8''xxx 格式
			if idx := strings.Index(value, "''"); idx != -1 {
				encodedName := value[idx+2:]
				decoded, err := url.QueryUnescape(encodedName)
				if err == nil {
					return decoded
				}
			}
		}
	}

	// 匹配 filename="xxx" 或 filename=xxx 格式
	// 先去掉可能存在的多个 filename= 的情况，取最后一个
	parts := strings.Split(contentDisposition, "filename=")
	if len(parts) > 1 {
		lastPart := parts[len(parts)-1]
		// 去掉引号和分号
		lastPart = strings.Trim(lastPart, "\"; ")
		// 处理可能还有引号的情况
		lastPart = strings.Trim(lastPart, "\"")
		decoded, err := url.QueryUnescape(lastPart)
		if err == nil {
			return decoded
		}
		return lastPart
	}

	return ""
}

// GetFilenameFromURL 从 URL 解析文件名
func GetFilenameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	// 尝试从路径获取
	filename := filepath.Base(parsed.Path)
	if filename != "" && filename != "/" {
		// 去除查询参数
		if idx := strings.Index(filename, "?"); idx != -1 {
			filename = filename[:idx]
		}
		// 尝试解码
		decoded, err := url.QueryUnescape(filename)
		if err == nil {
			filename = decoded
		}
		return filename
	}

	// 尝试从 Content-Type 获取扩展名
	ext := filepath.Ext(filename)
	if ext != "" {
		return filename
	}

	// 尝试从查询参数获取
	query := parsed.Query()
	if filename := query.Get("filename"); filename != "" {
		decoded, _ := url.QueryUnescape(filename)
		return decoded
	}

	return ""
}

// GetMimeType 获取文件的 MIME 类型
func GetMimeType(filename string) string {
	ext := filepath.Ext(filename)
	return mime.TypeByExtension(ext)
}
