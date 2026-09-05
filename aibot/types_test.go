package aibot

import (
	"encoding/json"
	"testing"
)

func TestWeComTimestamp_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want WeComTimestamp
	}{
		{name: "numeric timestamp", json: `1767240000`, want: 1767240000},
		{name: "string timestamp", json: `"1767240000"`, want: 1767240000},
		{name: "zero numeric", json: `0`, want: 0},
		{name: "null", json: `null`, want: 0},
		{name: "empty string", json: `""`, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got WeComTimestamp
			if err := json.Unmarshal([]byte(tt.json), &got); err != nil {
				t.Fatalf("json.Unmarshal(%s) failed: %v", tt.json, err)
			}
			if got != tt.want {
				t.Fatalf("json.Unmarshal(%s) = %d, want %d", tt.json, int64(got), int64(tt.want))
			}
		})
	}
}

func TestWeComTimestamp_UnmarshalJSON_Invalid(t *testing.T) {
	invalid := []string{`"abc"`, `{}`, `true`, `12.5`}
	for _, in := range invalid {
		var got WeComTimestamp
		if err := json.Unmarshal([]byte(in), &got); err == nil {
			t.Fatalf("json.Unmarshal(%s) succeeded (%d), want error", in, int64(got))
		}
	}
}

func TestUploadMediaFinishResult_UnmarshalJSON(t *testing.T) {
	// 企微返回 created_at 为数字时间戳（见 PR #1：原 string 字段导致解析失败）
	raw := []byte(`{"type":"image","media_id":"MEDIA_ID_123","created_at":1767240000}`)
	var got UploadMediaFinishResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if got.MediaID != "MEDIA_ID_123" {
		t.Errorf("MediaID = %q, want MEDIA_ID_123", got.MediaID)
	}
	if int64(got.CreatedAt) != 1767240000 {
		t.Errorf("CreatedAt = %d, want 1767240000", int64(got.CreatedAt))
	}
}
