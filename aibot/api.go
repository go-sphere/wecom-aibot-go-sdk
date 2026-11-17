package aibot

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
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
// timeout 为 HTTP 请求超时（毫秒），<= 0 时使用 10000；logger 为 nil 时使用 NewDefaultLogger。
func NewWeComApiClient(logger Logger, timeout int) *WeComApiClient {
	if timeout <= 0 {
		timeout = 10000
	}
	if logger == nil {
		logger = NewDefaultLogger()
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
	if resp == nil {
		return nil, fmt.Errorf("file download failed: empty response")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.logger.Error("Failed to close response body: %s", err.Error())
		}
	}()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("File download failed, status: %s", resp.Status)
		// 有限排空响应体以便复用小错误响应的底层连接，同时避免大响应体无限阻塞
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
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

	if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
		if filename := params["filename"]; filename != "" {
			return filename
		}
	}

	// 兜底解析非标准 Content-Disposition（如缺少 disposition type 或未加引号的空格）
	if idx := strings.Index(contentDisposition, "filename*="); idx != -1 {
		value := strings.TrimSpace(contentDisposition[idx+len("filename*="):])
		if semi := strings.Index(value, ";"); semi != -1 {
			value = value[:semi]
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if quoteIdx := strings.Index(value, "''"); quoteIdx != -1 {
			encodedName := value[quoteIdx+2:]
			if decoded, err := url.PathUnescape(encodedName); err == nil {
				return decoded
			}
			return encodedName
		}
	}

	// 匹配 filename="xxx" 或 filename=xxx 格式（取最后一个）
	parts := strings.Split(contentDisposition, "filename=")
	if len(parts) > 1 {
		lastPart := parts[len(parts)-1]
		if semi := strings.Index(lastPart, ";"); semi != -1 {
			lastPart = lastPart[:semi]
		}
		lastPart = strings.Trim(strings.TrimSpace(lastPart), `"`)
		if decoded, err := url.PathUnescape(lastPart); err == nil {
			return decoded
		}
		return lastPart
	}

	return ""
}

// GetFilenameFromURL 从 URL 解析文件名
// 优先取路径的最后一段并做 URL 路径解码；路径无文件名时回退到 filename 查询参数；URL 无法解析时返回空字符串。
func GetFilenameFromURL(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	// 尝试从路径获取
	filename := path.Base(parsedURL.Path)
	if filename != "" && filename != "." && filename != "/" {
		if decoded, err := url.PathUnescape(filename); err == nil {
			filename = decoded
		}
		if filename != "" && filename != "." && filename != "/" {
			return filename
		}
	}

	// 尝试从查询参数获取
	if filename := parsedURL.Query().Get("filename"); filename != "" {
		return filename
	}

	return ""
}

// GetMimeType 获取文件的 MIME 类型
// 按扩展名通过 mime.TypeByExtension 查询，未知扩展名返回空字符串。
func GetMimeType(filename string) string {
	ext := filepath.Ext(filename)
	return mime.TypeByExtension(ext)
}
