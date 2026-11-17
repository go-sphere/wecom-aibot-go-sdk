package aibot

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDownloadFileRaw_Non200ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer server.Close()

	client := NewWeComApiClient(nopLogger(), 10000)

	// 非 200 时不得返回 (nil, nil)：调用方据此访问 result.Buffer 会 panic（回归 RF-001）
	result, err := client.DownloadFileRaw(server.URL)
	if err == nil {
		t.Fatal("DownloadFileRaw with 403 returned nil error, want error")
	}
	if result != nil {
		t.Fatalf("DownloadFileRaw with 403 returned result %v, want nil", result)
	}
}

func TestDownloadFileRaw_Success(t *testing.T) {
	body := []byte("hello file content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="test.txt"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := NewWeComApiClient(nopLogger(), 10000)
	result, err := client.DownloadFileRaw(server.URL)
	if err != nil {
		t.Fatalf("DownloadFileRaw failed: %v", err)
	}
	if result == nil {
		t.Fatal("DownloadFileRaw returned nil result")
	}
	if string(result.Buffer) != string(body) {
		t.Fatalf("DownloadFileRaw body mismatch: got %q, want %q", result.Buffer, body)
	}
	if result.Filename != "test.txt" {
		t.Fatalf("DownloadFileRaw filename = %q, want test.txt", result.Filename)
	}
}

func TestParseFilename(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{header: `attachment; filename="a.txt"`, want: "a.txt"},
		{header: `attachment; filename=a.txt`, want: "a.txt"},
		{header: `attachment; filename*=UTF-8''%E6%B5%8B%E8%AF%95.txt`, want: "测试.txt"},
		{header: `attachment; filename="fallback.txt"; filename*=UTF-8''%E6%B5%8B%E8%AF%95.txt`, want: "测试.txt"},
		{header: `attachment; filename*=UTF-8''%E6%B5%8B%E8%AF%95.txt; size=42`, want: "测试.txt"},
		{header: `attachment; filename="c++.txt"`, want: "c++.txt"},
		{header: `attachment; filename*=UTF-8''a%2Bb.txt`, want: "a+b.txt"},
		{header: `attachment; filename*=UTF-8''a+b.txt`, want: "a+b.txt"},
		{header: `attachment; filename=my file+1.txt`, want: "my file+1.txt"},
		{header: `attachment; filename=my%20file+1.txt; invalid=`, want: "my file+1.txt"},
		{header: "", want: ""},
	}
	for _, tt := range tests {
		if got := parseFilename(tt.header); got != tt.want {
			t.Errorf("parseFilename(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestGetFilenameFromURL(t *testing.T) {
	tests := []struct {
		rawURL string
		want   string
	}{
		{rawURL: "https://example.com/files/report.pdf?token=abc", want: "report.pdf"},
		{rawURL: "https://example.com/files/%E6%B5%8B%E8%AF%95%20a+b.txt", want: "测试 a+b.txt"},
		{rawURL: "https://example.com/?filename=doc.pdf", want: "doc.pdf"},
		{rawURL: "https://example.com?filename=doc.pdf", want: "doc.pdf"},
		{rawURL: "https://example.com/?filename=%E6%B5%8B%E8%AF%95.pdf", want: "测试.pdf"},
		{rawURL: "https://example.com/", want: ""},
		{rawURL: "https://example.com", want: ""},
		{rawURL: "://bad-url", want: ""},
	}
	for _, tt := range tests {
		if got := GetFilenameFromURL(tt.rawURL); got != tt.want {
			t.Errorf("GetFilenameFromURL(%q) = %q, want %q", tt.rawURL, got, tt.want)
		}
	}
}

func TestNewWeComApiClient_NilLoggerDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewWeComApiClient(nil, 0)
	if client.logger == nil {
		t.Fatal("NewWeComApiClient(nil, 0) left logger nil")
	}
	res, err := client.DownloadFileRaw(server.URL)
	if err != nil {
		t.Fatalf("DownloadFileRaw failed: %v", err)
	}
	if string(res.Buffer) != "ok" {
		t.Fatalf("DownloadFileRaw body = %q, want ok", res.Buffer)
	}
}
