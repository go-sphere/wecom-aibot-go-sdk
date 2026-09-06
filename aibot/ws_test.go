package aibot

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
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

// wsMirrorConn 是镜像服务端上的一条连接：服务端侧读循环把收到的帧推入 frames，
// 测试方从 frames 读取（唯一的读端），向 conn 写入（唯一的写端）。
// gorilla 连接只允许单一读者与单一写者，这种设计避免测试与镜像读循环并发读同一连接。
type wsMirrorConn struct {
	conn   *websocket.Conn
	frames chan WsFrame
}

// write 向客户端写入一个帧（JSON）。
func (mc *wsMirrorConn) write(t *testing.T, frame WsFrame) {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal frame failed: %v", err)
	}
	if err := mc.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("mirror write failed: %v", err)
	}
}

// startWSMirror 启动一个 WebSocket 测试服务端。
// 每连入一个客户端，握手后由服务端侧读循环持续消费该连接的帧并推入 frames channel，
// 连接对象通过 connCh 交给测试方。返回服务端地址、新连接 channel 和清理函数。
func startWSMirror(t *testing.T) (string, chan *wsMirrorConn, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	connCh := make(chan *wsMirrorConn, 8)

	upgrader := websocket.Upgrader{}
	var mu sync.Mutex
	var conns []*websocket.Conn
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		conns = append(conns, conn)
		mu.Unlock()

		mc := &wsMirrorConn{
			conn:   conn,
			frames: make(chan WsFrame, 64),
		}
		connCh <- mc
		defer close(mc.frames) // 连接关闭（读循环退出）时通知测试方
		// 单一读循环：持续消费客户端帧，防止客户端写缓冲填满而阻塞。
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				_ = conn.Close()
				return
			}
			var frame WsFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				continue // 忽略无法解析的帧
			}
			select {
			case mc.frames <- frame:
			default: // 帧积压时丢弃最旧的后续帧，避免阻塞读循环
			}
		}
	})}
	go func() {
		_ = server.Serve(ln)
	}()

	cleanup := func() {
		_ = server.Close()
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	}
	return "ws://" + ln.Addr().String(), connCh, cleanup
}

// readServerFrame 从镜像连接读取下一个帧（带超时）。
func readServerFrame(t *testing.T, mc *wsMirrorConn) WsFrame {
	t.Helper()
	select {
	case frame, ok := <-mc.frames:
		if !ok {
			t.Fatal("mirror connection closed while waiting for frame")
		}
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server frame")
		return WsFrame{}
	}
}

// authOK 读取客户端发来的认证帧并回复认证成功。
func authOK(t *testing.T, mc *wsMirrorConn) {
	t.Helper()
	frame := readServerFrame(t, mc)
	if frame.Cmd != WsCmd.SUBSCRIBE {
		t.Fatalf("first frame cmd = %q, want %q", frame.Cmd, WsCmd.SUBSCRIBE)
	}
	mc.write(t, WsFrame{Headers: WsFrameHeaders{ReqID: frame.Headers.ReqID}})
}

// waitConnected 轮询等待连接建立。
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
	mc := <-connCh
	if err := mc.conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatalf("server write failed: %v", err)
	}
	_ = mc.conn.Close()

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

// TestHeartbeatPeriodic 验证认证成功后心跳周期性发送：
// 认证帧后能连续收到多个心跳帧，证明心跳会持续续发而非只发一次（回归 RF-002）。
func TestHeartbeatPeriodic(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	mgr := newTestManager(t, url)
	mgr.heartbeatInterval = 60 // ms
	mgr.Connect()
	waitConnected(t, mgr)

	mc := <-connCh
	authOK(t, mc)

	// 认证成功后应周期性收到心跳帧
	seen := 0
	var last time.Time
	deadline := time.Now().Add(3 * time.Second)
	for seen < 2 && time.Now().Before(deadline) {
		frame := readServerFrame(t, mc)
		if frame.Cmd != WsCmd.HEARTBEAT {
			continue // 忽略其他帧（如重复认证），只统计心跳
		}
		if !last.IsZero() {
			interval := time.Since(last)
			if interval < time.Duration(mgr.heartbeatInterval)*time.Millisecond/3 {
				t.Fatalf("heartbeat interval too short: %v", interval)
			}
		}
		last = time.Now()
		seen++
	}
	if seen < 2 {
		t.Fatalf("expected periodic heartbeats, got %d", seen)
	}
}

// TestHeartbeatMissingPongTriggersReconnect 验证：服务端不响应心跳时，
// 连续 maxMissedPong 次无 pong 后连接被判定死亡并触发重连（回归 RF-002）。
func TestHeartbeatMissingPongTriggersReconnect(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	mgr := newTestManager(t, url)
	mgr.heartbeatInterval = 40 // ms
	reconnected := make(chan int, 8)
	mgr.OnReconnecting = func(attempt int) { reconnected <- attempt }
	mgr.Connect()
	waitConnected(t, mgr)

	mc := <-connCh
	authOK(t, mc) // 认证成功但不回任何 pong

	select {
	case <-reconnected:
		// 连接已被判定死亡并进入重连流程
	case <-time.After(5 * time.Second):
		t.Fatal("connection was not declared dead after missing pongs")
	}
}

// TestDisconnectConnectReconnectable 验证 Disconnect 后仍可再次 Connect 建立全新连接：
// 生命周期可重启（回归 RF-003：旧实现 Disconnect 永久 cancel ctx）。
func TestDisconnectConnectReconnectable(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	mgr := newTestManager(t, url)
	mgr.Connect()
	waitConnected(t, mgr)
	<-connCh // 第一条连接

	mgr.Disconnect()
	if mgr.IsConnected() {
		t.Fatal("IsConnected() = true after Disconnect, want false")
	}

	// 再次 Connect：应能建立第二条连接且读协程存活（服务端能收到认证帧）
	mgr.Connect()
	waitConnected(t, mgr)

	mc2 := <-connCh
	authOK(t, mc2)
}

// TestAuthFailureExhaustionStopsReconnect 验证：认证失败达到上限后不再自动重连，
// 且用户仍可显式 Connect() 发起新连接（回归 RF-011）。
func TestAuthFailureExhaustionStopsReconnect(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	// maxAuthFailureAttempts = 2，重连基延迟 1ms 便于快速耗尽
	mgr := NewWsConnectionManager(nopLogger(), 30000, 1, 10, url, nil, 500, 2)
	exhausted := make(chan error, 1)
	mgr.OnError = func(err error) {
		if _, ok := err.(*WSAuthFailureError); ok {
			select {
			case exhausted <- err:
			default:
			}
		}
	}

	// 认证失败路径：manager 会拨入 maxAuthFailureAttempts+1 条连接
	// （初始 1 次 + maxAuthFailureAttempts 次重连），并在最后一次失败时触发耗尽错误。
	// 独立 goroutine 逐条回一个带匹配 req_id 的认证失败帧。
	go func() {
		for i := 0; i < 3; i++ { // maxAuthFailureAttempts(2) + 初始 1 次 = 3 次认证
			mc := <-connCh
			frame := readServerFrame(t, mc) // 读取 SUBSCRIBE 帧以复用其 req_id
			resp := WsFrame{
				ErrCode: 401,
				ErrMsg:  "bad secret",
				Headers: WsFrameHeaders{ReqID: frame.Headers.ReqID},
			}
			mc.write(t, resp)
		}
	}()

	mgr.Connect()

	select {
	case <-exhausted:
		// 认证失败达到上限，停止自动重连
	case <-time.After(5 * time.Second):
		t.Fatal("auth failure exhaustion error not fired")
	}

	// 耗尽后短暂观察：应无新连接自动拨入
	select {
	case mc := <-connCh:
		t.Fatalf("manager unexpectedly reconnected after auth exhaustion: %p", mc)
	case <-time.After(300 * time.Millisecond):
	}

	// 显式 Connect() 应能发起新连接
	mgr.Connect()
	waitConnected(t, mgr)
}

// TestReplyAckRoundTrip 验证回复→回执完整链路：SendReply 等待回执并返回 ack 帧，
// 错误码回执透传为错误。
func TestReplyAckRoundTrip(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	mgr := newTestManager(t, url)
	mgr.Connect()
	waitConnected(t, mgr)
	mc := <-connCh
	authOK(t, mc)

	type result struct {
		frame *WsFrame
		err   error
	}
	done := make(chan result, 2)

	go func() {
		frame, err := mgr.SendReply("req-1", map[string]interface{}{"msgtype": "text"}, WsCmd.RESPONSE)
		done <- result{frame, err}
	}()

	// 服务端应收到回复帧
	sent := readServerFrame(t, mc)
	if sent.Cmd != WsCmd.RESPONSE || sent.Headers.ReqID != "req-1" {
		t.Fatalf("unexpected sent frame: cmd=%q reqID=%q", sent.Cmd, sent.Headers.ReqID)
	}

	// 回正常回执
	mc.write(t, WsFrame{Headers: WsFrameHeaders{ReqID: "req-1"}})

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("SendReply returned error: %v", r.err)
		}
		if r.frame == nil {
			t.Fatal("SendReply returned nil ack frame")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendReply did not return after ack")
	}

	// 回执错误码应透传为错误
	go func() {
		_, err := mgr.SendReply("req-2", map[string]interface{}{"msgtype": "text"}, WsCmd.RESPONSE)
		done <- result{nil, err}
	}()
	sent2 := readServerFrame(t, mc)
	if sent2.Headers.ReqID != "req-2" {
		t.Fatalf("unexpected reqID %q", sent2.Headers.ReqID)
	}
	mc.write(t, WsFrame{ErrCode: 90001, ErrMsg: "boom", Headers: WsFrameHeaders{ReqID: "req-2"}})

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("SendReply with errcode ack returned nil error")
		}
		if !strings.Contains(r.err.Error(), "90001") {
			t.Fatalf("error does not contain errcode: %v", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendReply did not return after error ack")
	}
}

// TestSendReplyTimeout 验证回执超时：SendReply 在 replyAckTimeout 后返回错误，
// 且超时后同 reqID 的下一条消息仍可正常发送。
func TestSendReplyTimeout(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	mgr := newTestManager(t, url)
	mgr.Connect()
	waitConnected(t, mgr)
	mc := <-connCh
	authOK(t, mc)

	origTimeout := replyAckTimeout.Load()
	replyAckTimeout.Store(100) // ms
	defer replyAckTimeout.Store(origTimeout)

	start := time.Now()
	_, err := mgr.SendReply("req-timeout", map[string]interface{}{"msgtype": "text"}, WsCmd.RESPONSE)
	if err == nil {
		t.Fatal("SendReply succeeded despite no ack, want timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}

	// 超时后同 reqID 仍可再次发送（清理路径正确）
	go func() {
		_, _ = mgr.SendReply("req-timeout", map[string]interface{}{"msgtype": "text"}, WsCmd.RESPONSE)
	}()
	sent2 := readServerFrame(t, mc)
	if sent2.Headers.ReqID != "req-timeout" {
		t.Fatalf("unexpected reqID %q after timeout re-send", sent2.Headers.ReqID)
	}
}

// TestEarlyDisconnectKeepsDisconnected 验证：连接建立后立刻 Disconnect，
// 竞态窗口内（认证/读循环启动前）不 panic、不残留连接。
func TestEarlyDisconnectKeepsDisconnected(t *testing.T) {
	url, connCh, cleanup := startWSMirror(t)
	defer cleanup()

	mgr := newTestManager(t, url)
	mgr.Connect()
	waitConnected(t, mgr)
	mc := <-connCh

	// 立即断开：此时认证与读循环可能仍在启动过程中
	mgr.Disconnect()
	if mgr.IsConnected() {
		t.Fatal("IsConnected() = true after Disconnect")
	}

	// 服务端侧应观察到连接被客户端关闭（镜像读循环会在客户端关闭时退出并关闭连接）
	select {
	case <-mc.frames: // 可能有已缓冲的帧；继续等待连接关闭
	default:
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := <-mc.frames; !ok {
			return // channel 关闭说明读循环已退出、连接已关闭
		}
	}
	t.Fatal("server connection still alive after client Disconnect")
}
