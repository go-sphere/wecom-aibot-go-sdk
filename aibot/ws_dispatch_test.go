package aibot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialAckingConn spins up a WebSocket server that pushes the given frames and
// then acks every reply it receives, echoing the reply's req_id back the way
// 企业微信 does. It returns the client connection.
//
// Acking is what makes the deadlock observable: the ack can only reach the
// client through the same socket the read loop is reading.
func dialAckingConn(t *testing.T, frames ...string) *websocket.Conn {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for _, frame := range frames {
			if err := c.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				return
			}
		}
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var reply WsFrame
			if err := json.Unmarshal(data, &reply); err != nil {
				continue
			}
			ack := fmt.Sprintf(`{"headers":{"req_id":%q},"errcode":0,"errmsg":"ok"}`, reply.Headers.ReqID)
			if err := c.WriteMessage(websocket.TextMessage, []byte(ack)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func silentManager() *WsConnectionManager {
	return NewWsConnectionManager(
		NewLoggerFunc(func(string, string, ...interface{}) {}),
		0, 0, 0, "", nil, 0, 0,
	)
}

// TestHandlerCanAwaitReplyAck is the regression guard for a self-deadlock.
//
// Dispatching frames on the read loop makes a handler that sends a reply wait
// for something only the blocked read loop can supply: SendReply waits for the
// ack, and the ack is delivered by handleMessage. The reply reaches 企业微信 and
// is accepted, but the caller sees a 5s "reply ack timeout" — a success reported
// as a failure.
//
// This is the shape the README documents (reply inline from an event handler),
// mirroring the upstream Node SDK where `await` yields the event loop and the
// socket keeps being read. Go has no such yield, so dispatch must be moved off
// the read loop for that shape to hold.
func TestHandlerCanAwaitReplyAck(t *testing.T) {
	m := silentManager()
	t.Cleanup(m.Disconnect)

	done := make(chan error, 1)
	m.OnMessage = func(frame *WsFrame) {
		// Reply inline, exactly as replyWelcome/updateTemplateCard callers do.
		_, err := m.SendReply(frame.Headers.ReqID, map[string]any{"msgtype": "text"}, WsCmd.RESPONSE_WELCOME)
		done <- err
	}

	conn := dialAckingConn(t, `{"cmd":"aibot_event_callback","headers":{"req_id":"r1"},"body":{"msgtype":"event","event":{"eventtype":"enter_chat"}}}`)
	m.setConn(conn)
	m.setupEventHandlers()

	// Comfortably under replyAckTimeout (5000ms): the point is that the ack
	// arrives on its own, not that it beats the deadline by a hair.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("inline reply from a handler failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler never returned; the ack could not be delivered while it was waiting")
	}
}

// TestDispatchPreservesFrameOrder pins the single-consumer design. Callers
// correlate the messages a platform splits out of one user action by arrival
// order, so frames must be handed over in the order they were read; dispatching
// each on its own goroutine would break that.
func TestDispatchPreservesFrameOrder(t *testing.T) {
	m := silentManager()
	t.Cleanup(m.Disconnect)

	const total = 12
	var mu sync.Mutex
	got := make([]string, 0, total)
	all := make(chan struct{})

	m.OnMessage = func(frame *WsFrame) {
		// Uneven handler cost: with concurrent dispatch, later cheap frames would
		// overtake earlier expensive ones.
		if frame.Headers.ReqID == "r0" {
			time.Sleep(150 * time.Millisecond)
		}
		mu.Lock()
		got = append(got, frame.Headers.ReqID)
		if len(got) == total {
			close(all)
		}
		mu.Unlock()
	}

	frames := make([]string, 0, total)
	want := make([]string, 0, total)
	for i := range total {
		reqID := fmt.Sprintf("r%d", i)
		want = append(want, reqID)
		frames = append(frames, fmt.Sprintf(`{"cmd":"aibot_msg_callback","headers":{"req_id":%q},"body":{"msgtype":"text"}}`, reqID))
	}

	conn := dialAckingConn(t, frames...)
	m.setConn(conn)
	m.setupEventHandlers()

	select {
	case <-all:
	case <-time.After(5 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("only %d/%d frames dispatched: %v", len(got), total, got)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, reqID := range want {
		if got[i] != reqID {
			t.Fatalf("frame order = %v, want %v", got, want)
		}
	}
}

// TestDispatchSurvivesHandlerPanic covers a panicking callback. The read loop
// used to run handlers inside HandleFrame's recover; off that path a panic would
// kill the dispatch goroutine and silently stop delivering every later frame.
func TestDispatchSurvivesHandlerPanic(t *testing.T) {
	m := silentManager()
	t.Cleanup(m.Disconnect)

	second := make(chan string, 1)
	m.OnMessage = func(frame *WsFrame) {
		if frame.Headers.ReqID == "r1" {
			panic("handler blew up")
		}
		second <- frame.Headers.ReqID
	}

	conn := dialAckingConn(t,
		`{"cmd":"aibot_msg_callback","headers":{"req_id":"r1"},"body":{"msgtype":"text"}}`,
		`{"cmd":"aibot_msg_callback","headers":{"req_id":"r2"},"body":{"msgtype":"text"}}`,
	)
	m.setConn(conn)
	m.setupEventHandlers()

	select {
	case reqID := <-second:
		if reqID != "r2" {
			t.Errorf("dispatched req_id = %q, want %q", reqID, "r2")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("frame after a panicking handler was never dispatched")
	}
}

// TestReplyAckStillHandledOnReadLoop guards the other half of the split: acks,
// auth and heartbeat responses must keep being handled inline. Routing them
// through the dispatch queue would put them behind the very handler waiting for
// them and reintroduce the deadlock.
func TestReplyAckStillHandledOnReadLoop(t *testing.T) {
	m := silentManager()
	t.Cleanup(m.Disconnect)

	dispatched := make(chan string, 4)
	m.OnMessage = func(frame *WsFrame) { dispatched <- frame.Headers.ReqID }

	conn := dialAckingConn(t)
	m.setConn(conn)
	m.setupEventHandlers()

	if _, err := m.SendReply("r9", map[string]any{"msgtype": "text"}, WsCmd.RESPONSE); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	// The ack resolved the reply; it must not also surface as an inbound frame.
	select {
	case reqID := <-dispatched:
		t.Errorf("ack frame was dispatched to OnMessage (req_id=%q); it belongs to the reply, not the application", reqID)
	case <-time.After(300 * time.Millisecond):
	}
}
