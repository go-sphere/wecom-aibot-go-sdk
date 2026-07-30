package aibot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// dialTestConn spins up a WebSocket echo-less server that just drains frames,
// and returns a client connection to it.
func dialTestConn(t *testing.T) *websocket.Conn {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
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

// TestConcurrentSendDoesNotPanic covers the write serialization in Send and
// sendFrame. gorilla/websocket permits only one concurrent writer and panics
// with "concurrent write to websocket connection" otherwise. The manager has
// several independent writers in practice — one processReplyQueue goroutine per
// reqID plus the heartbeat timer — so both write paths must hold writeMu.
//
// Without that lock this test panics and takes the process down; the panic
// happens on a spawned goroutine, so it cannot be recovered by the caller.
func TestConcurrentSendDoesNotPanic(t *testing.T) {
	m := NewWsConnectionManager(
		NewLoggerFunc(func(string, string, ...interface{}) {}),
		0, 0, 0, "", nil, 0, 0,
	)
	m.ws = dialTestConn(t)

	const (
		writers = 8
		frames  = 50
	)
	var wg sync.WaitGroup
	// Exercise both write paths concurrently: Send (reply queue) and sendFrame
	// (heartbeat), which are exactly the two that collide in production.
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < frames; j++ {
				frame := WsFrame{Cmd: WsCmd.RESPONSE, Headers: WsFrameHeaders{ReqID: "req"}}
				if i%2 == 0 {
					_ = m.Send(frame)
				} else {
					m.sendFrame(frame)
				}
			}
		}(i)
	}
	wg.Wait()
}
