package aibot

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSendMessageInvalidBody 验证非法 body 返回错误而非 panic（回归 RF-005）
func TestSendMessageInvalidBody(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	client := NewWSClient(WSClientOptions{
		WSURL:  url,
		Logger: nopLogger(),
	})
	defer client.Disconnect()
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
	sendErr := make(chan error, 1)
	go func() {
		_, err := client.SendMessage("chat-1", CreateTextReplyBody("hi"))
		sendErr <- err
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
	if err := <-sendErr; err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
}

// TestReplyAckTimeoutOption 验证 WSClientOptions.ReplyAckTimeout 作用于实际等待回执的发送。
func TestReplyAckTimeoutOption(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	client := NewWSClient(WSClientOptions{
		WSURL:           url,
		ReplyAckTimeout: 100,
		Logger:          nopLogger(),
	})
	defer client.Disconnect()
	client.Connect()
	waitConnected(t, client.wsManager)
	mc := <-connCh
	authOK(t, mc)

	start := time.Now()
	_, err := client.SendMessage("chat-1", CreateTextReplyBody("hi"))
	if err == nil {
		t.Fatal("SendMessage succeeded despite no ack, want timeout error")
	}
	if !strings.Contains(err.Error(), "timeout (100ms)") {
		t.Fatalf("error does not include configured timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

// TestReplyAckTimeoutDefaultsToFiveSeconds 验证省略配置时保留原有 5 秒默认值。
func TestReplyAckTimeoutDefaultsToFiveSeconds(t *testing.T) {
	client := NewWSClient(WSClientOptions{Logger: nopLogger()})
	if client.wsManager.replyAckTimeout != 5000 {
		t.Fatalf("reply ack timeout = %d, want 5000", client.wsManager.replyAckTimeout)
	}
	if client.options.ReplyAckTimeout != 5000 {
		t.Fatalf("options.ReplyAckTimeout = %d, want 5000", client.options.ReplyAckTimeout)
	}
}

// TestHandlerRegistrationDataRace 验证 Connect 后注册/更换 handler 与消息分发并发安全
// （回归 RF-009；在 -race 下运行可检出数据竞争）。
func TestHandlerRegistrationDataRace(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	client := NewWSClient(WSClientOptions{WSURL: url, Logger: nopLogger()})
	defer client.Disconnect()
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

func TestClientNilGuardsAndEmptyUpload(t *testing.T) {
	client := NewWSClient(WSClientOptions{Logger: nopLogger()})

	if _, err := client.Reply(nil, CreateTextReplyBody("hi"), ""); err == nil {
		t.Fatal("Reply(nil) succeeded, want error")
	}
	if client.HasPendingReplyAck(nil) {
		t.Fatal("HasPendingReplyAck(nil) = true, want false")
	}
	if _, err := client.ReplyStreamNonBlocking(nil, "stream-1", "hi", false, nil, nil); err == nil {
		t.Fatal("ReplyStreamNonBlocking(nil) succeeded, want error")
	}
	if _, err := client.UploadMedia(nil, UploadMediaOptions{Type: WeComMediaTypeFile, Filename: "a.txt"}); err == nil {
		t.Fatal("UploadMedia(nil) succeeded, want error")
	}
	if _, err := client.UploadMedia([]byte{}, UploadMediaOptions{Type: WeComMediaTypeFile, Filename: "a.txt"}); err == nil {
		t.Fatal("UploadMedia(empty) succeeded, want error")
	}
}

func TestGetEventTypeAndStringEncodedEvent(t *testing.T) {
	objFrame := &WsFrame{
		Cmd:  WsCmd.EVENT_CALLBACK,
		Body: json.RawMessage(`{"msgtype":"event","event":{"eventtype":"enter_chat"}}`),
	}
	if got := GetEventType(objFrame); got != "enter_chat" {
		t.Fatalf("GetEventType(object) = %q, want enter_chat", got)
	}

	strFrame := &WsFrame{
		Cmd:  WsCmd.EVENT_CALLBACK,
		Body: json.RawMessage(`{"msgtype":"event","event":"{\"eventtype\":\"disconnected_event\"}"}`),
	}
	if got := GetEventType(strFrame); got != "disconnected_event" {
		t.Fatalf("GetEventType(string) = %q, want disconnected_event", got)
	}

	mgr := silentManager()
	defer mgr.Disconnect()
	if !mgr.isDisconnectedEvent(strFrame) {
		t.Fatal("isDisconnectedEvent(string) = false, want true")
	}

	var gotEvent string
	client := NewWSClient(WSClientOptions{Logger: nopLogger()})
	client.OnEventEnterChat(func(f *WsFrame) {
		gotEvent = GetEventType(f)
	})
	client.messageHandler.HandleFrame(objFrame, client)
	if gotEvent != "enter_chat" {
		t.Fatalf("OnEventEnterChat did not fire for object event, got %q", gotEvent)
	}
}

func TestGenerateRandomStringLengths(t *testing.T) {
	for _, length := range []int{0, 1, 2, 7, 8, 32} {
		s := GenerateRandomString(length)
		if len(s) != length {
			t.Fatalf("GenerateRandomString(%d) returned length %d (%q)", length, len(s), s)
		}
	}
}
