package aibot

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// TestSendMessageInvalidBody 验证非法 body 返回错误而非 panic（回归 RF-005）
func TestSendMessageInvalidBody(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	client := NewWSClient(WSClientOptions{
		WSURL: url,
	})
	client.Connect()
	waitConnected(t, client.wsManager)
	mc := <-connCh
	authOK(t, mc) // 认证成功，使 SendReply 正常走队列

	// nil body：应返回错误而非 panic
	if _, err := client.SendMessage("chat-1", nil); err == nil {
		t.Fatal("SendMessage with nil body succeeded, want error")
	}

	// 非对象 body：应返回错误而非 panic
	if _, err := client.SendMessage("chat-1", "just a string"); err == nil {
		t.Fatal("SendMessage with string body succeeded, want error")
	}
	if _, err := client.SendMessage("chat-1", []int{1, 2, 3}); err == nil {
		t.Fatal("SendMessage with slice body succeeded, want error")
	}

	// 合法对象 body：应正常入队发送（由服务端收到）
	go func() {
		_, _ = client.SendMessage("chat-1", CreateTextReplyBody("hi"))
	}()
	sent := readServerFrame(t, mc)
	if sent.Cmd != WsCmd.SEND_MSG {
		t.Fatalf("sent frame cmd = %q, want %q", sent.Cmd, WsCmd.SEND_MSG)
	}
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(sent.Body, &bodyMap); err != nil {
		t.Fatalf("unmarshal sent body failed: %v", err)
	}
	if bodyMap["chatid"] != "chat-1" {
		t.Fatalf("sent body chatid = %v, want chat-1", bodyMap["chatid"])
	}
	// 回执避免 goroutine 泄漏
	mc.write(t, WsFrame{Headers: WsFrameHeaders{ReqID: sent.Headers.ReqID}})
}

// TestHandlerRegistrationDataRace 验证 Connect 后注册/更换 handler 与消息分发并发安全
// （回归 RF-009；在 -race 下运行可检出数据竞争）。
func TestHandlerRegistrationDataRace(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	client := NewWSClient(WSClientOptions{WSURL: url})
	calls := make(chan string, 128)
	client.OnMessageText(func(frame *WsFrame) {
		calls <- "old"
	})
	client.Connect()
	waitConnected(t, client.wsManager)
	mc := <-connCh
	authOK(t, mc)

	// 并发：一边持续更换 handler，一边推送文本消息触发分发
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			client.OnMessageText(func(frame *WsFrame) { calls <- "new" })
		}
	}()

	body := jsonMarshal(map[string]interface{}{"msgtype": "text", "text": map[string]interface{}{"content": "hi"}})
	for i := 0; i < 50; i++ {
		mc.write(t, WsFrame{Cmd: WsCmd.CALLBACK, Headers: WsFrameHeaders{ReqID: "cb-1"}, Body: body})
	}
	wg.Wait()

	// 等待所有推送被分发（数量级校验，避免 handler 崩溃导致静默丢消息）
	timeout := time.After(3 * time.Second)
	count := 0
	for count < 50 {
		select {
		case <-calls:
			count++
		case <-timeout:
			t.Fatalf("only %d/50 messages dispatched", count)
		}
	}
}

func jsonMarshal(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
