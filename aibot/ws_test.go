package aibot

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func nopLogger() Logger {
	return NewLoggerFunc(func(string, string, ...interface{}) {})
}

func newTestManager(t *testing.T, wsURL string) *WsConnectionManager {
	t.Helper()
	// maxReconnectAttempts=0 表示无限重连；disconnected_event 场景因 isManualClose 不会触发重连。
	return NewWsConnectionManager(nopLogger(), 30000, 100, 0, wsURL, nil, 500, 5)
}

// startWSMirror 启动一个 WebSocket 测试服务端。
// 每连入一个客户端，握手后把该连接通过 connCh 交给测试方，并阻塞读取直到连接关闭。
// 返回服务端地址、新连接 channel 和清理函数。
func startWSMirror(t *testing.T) (string, chan *websocket.Conn, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	connCh := make(chan *websocket.Conn, 4)

	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connCh <- conn
		// 阻塞读取直到连接被关闭
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				_ = conn.Close()
				return
			}
		}
	})}
	go func() {
		_ = server.Serve(ln)
	}()

	cleanup := func() {
		_ = server.Close()
		_ = ln.Close()
	}
	return "ws://" + ln.Addr().String(), connCh, cleanup
}

func waitConnected(t *testing.T, mgr *WsConnectionManager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.IsConnected() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("manager did not connect to test server")
}

func waitDisconnected(t *testing.T, mgr *WsConnectionManager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !mgr.IsConnected() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("connection still marked as connected after disconnect")
}

// TestDisconnectedEventDoesNotPanic 回归测试 PR #2：
// 连接稳定后服务端推送 disconnected_event 并断开。SDK 读协程不得对已关闭/置 nil 的
// 连接解引用而 panic，且连接状态应被清理（IsConnected 变为 false）。
func TestDisconnectedEventDoesNotPanic(t *testing.T) {
	disconnectBody := []byte(`{"event":{"eventtype":"disconnected_event","to_chat_id":"x","eventtime":1767240000}}`)
	frame, err := json.Marshal(WsFrame{Cmd: WsCmd.EVENT_CALLBACK, Body: disconnectBody})
	if err != nil {
		t.Fatalf("marshal frame failed: %v", err)
	}

	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	mgr := newTestManager(t, url)
	disconnectedCh := make(chan string, 1)
	mgr.OnServerDisconnect = func(reason string) { disconnectedCh <- reason }
	mgr.Connect()
	waitConnected(t, mgr)

	// 用服务端侧连接推送 disconnected_event 后主动断开，模拟多实例同 BotID 被踢下线。
	serverConn := <-connCh
	if err := serverConn.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatalf("server write failed: %v", err)
	}
	_ = serverConn.Close()

	// 若读协程 panic，测试进程会直接崩溃。OnServerDisconnect 被调用说明确实走到了
	// disconnected_event 处理分支（而非把连接关闭误判为网络错误走重连）。
	select {
	case <-disconnectedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("OnServerDisconnect was not called after disconnected_event")
	}

	// 连接状态应被清理，且 isManualClose 语义下不应触发自动重连。
	waitDisconnected(t, mgr)
	time.Sleep(300 * time.Millisecond) // 超过重连基延迟（100ms），确认未重连
	if mgr.IsConnected() {
		t.Fatal("manager unexpectedly reconnected after disconnected_event")
	}
}

// TestSendAfterDisconnect 验证：Disconnect 后 IsConnected 为 false，
// sendFrame 静默返回而不触碰已关闭连接，Send 返回错误而非 panic。
func TestSendAfterDisconnect(t *testing.T) {
	url, _, cleanup := startWSMirror(t)
	defer cleanup()

	mgr := newTestManager(t, url)
	mgr.Connect()
	waitConnected(t, mgr)

	mgr.Disconnect()
	if mgr.IsConnected() {
		t.Fatal("IsConnected() = true after Disconnect, want false")
	}

	mgr.sendFrame(WsFrame{Cmd: WsCmd.HEARTBEAT, Headers: WsFrameHeaders{ReqID: "ping-1"}})
	if err := mgr.Send(WsFrame{Cmd: WsCmd.HEARTBEAT, Headers: WsFrameHeaders{ReqID: "ping-2"}}); err == nil {
		t.Fatal("Send() after Disconnect succeeded, want error")
	}
}
