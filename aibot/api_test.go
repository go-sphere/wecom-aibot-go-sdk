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
		{header: "", want: ""},
	}
	for _, tt := range tests {
		if got := parseFilename(tt.header); got != tt.want {
			t.Errorf("parseFilename(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}
