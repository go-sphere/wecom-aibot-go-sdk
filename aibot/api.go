package aibot

import (
	"fmt"
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
	Buffer   []byte // 响应体原始数据（未解密）
	Filename string // 从 Content-Disposition 解析出的文件名，可能为空
}

// WeComApiClient 企业微信 API 客户端
// 仅负责文件下载等 HTTP 辅助功能，消息收发均走 WebSocket 通道
// 可并发使用。通常通过 WSClient.GetAPI 获取。
type WeComApiClient struct {
	httpClient *http.Client
	logger     Logger
}

// NewWeComApiClient 创建 API 客户端
// timeout 为 HTTP 请求超时（毫秒），<= 0 时使用 10000；logger 不能为 nil。
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
//
// 对 fileURL 发起 HTTP GET，不解密。请求失败、响应状态不是 200 或读取响应体失败时返回错误；成功时返回完整读入内存的响应体。加密文件可再用 DecryptFile 解密，或直接使用 WSClient.DownloadFile。
func (c *WeComApiClient) DownloadFileRaw(fileURL string) (*FileResult, error) {
	c.logger.Info("Downloading file...")

	resp, err := c.httpClient.Get(fileURL)
	if err != nil {
		c.logger.Error("File download failed: %s", err.Error())
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.logger.Error("Failed to close response body: %s", err.Error())
		}
	}()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("File download failed, status: %s", resp.Status)
		// 排空响应体以便复用底层连接，并返回明确错误而非 (nil, nil)
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("file download failed with status %s", resp.Status)
	}

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body: %s", err.Error())
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
// 优先取路径的最后一段并做 URL 解码；URL 无法解析时返回空字符串。
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
// 按扩展名通过 mime.TypeByExtension 查询，未知扩展名返回空字符串。
func GetMimeType(filename string) string {
	ext := filepath.Ext(filename)
	return mime.TypeByExtension(ext)
}
