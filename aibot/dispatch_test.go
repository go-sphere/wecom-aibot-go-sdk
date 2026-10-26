package aibot

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func awaitDispatch[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for callback dispatch")
		var zero T
		return zero
	}
}

func TestDispatchLifecycleIsolation(t *testing.T) {
	m := silentManager()
	t.Cleanup(m.Disconnect)
	oldEntered := make(chan struct{})
	oldRelease := make(chan struct{})
	releaseOld := sync.OnceFunc(func() { close(oldRelease) })
	defer releaseOld()
	newEntered := make(chan struct{})
	newRelease := make(chan struct{})
	releaseNew := sync.OnceFunc(func() { close(newRelease) })
	defer releaseNew()
	delivered := make(chan string, 4)
	m.OnMessage = func(frame *WsFrame) {
		switch frame.Headers.ReqID {
		case "old":
			close(oldEntered)
			<-oldRelease
		case "new-held":
			close(newEntered)
			<-newRelease
		default:
			delivered <- frame.Headers.ReqID
		}
	}
	old := m.startDispatch()
	m.enqueueDispatch(old, &WsFrame{Headers: WsFrameHeaders{ReqID: "old"}})
	awaitDispatch(t, oldEntered)
	m.enqueueDispatch(old, &WsFrame{Headers: WsFrameHeaders{ReqID: "old-queued"}})
	m.Disconnect()

	current := m.startDispatch()
	if current == old {
		t.Fatal("new lifecycle reused the stopped dispatcher")
	}
	m.enqueueDispatch(current, &WsFrame{Headers: WsFrameHeaders{ReqID: "new-held"}})
	awaitDispatch(t, newEntered)
	m.enqueueDispatch(current, &WsFrame{Headers: WsFrameHeaders{ReqID: "new-probe"}})
	// An old socket reader may have decoded its last frame just before shutdown.
	m.handleMessage([]byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"old-late"}}`), old)
	releaseOld()
	awaitDispatch(t, old.done)
	select {
	case reqID := <-delivered:
		t.Fatalf("stopped dispatcher delivered %q while the new handler was blocked", reqID)
	default:
	}
	releaseNew()
	if reqID := awaitDispatch(t, delivered); reqID != "new-probe" {
		t.Fatalf("delivered %q, want new-probe", reqID)
	}
	m.Disconnect()
	awaitDispatch(t, current.done)
	select {
	case reqID := <-delivered:
		t.Fatalf("stale frame %q survived lifecycle shutdown", reqID)
	default:
	}
}

func TestExplicitConnectRetiresPreviousDispatcher(t *testing.T) {
	url, _, cleanup := startWSMirror(t)
	t.Cleanup(cleanup)
	m := newTestManager(t, url)
	t.Cleanup(m.Disconnect)
	old := m.startDispatch()
	m.Connect()
	awaitDispatch(t, old.done)
	waitConnected(t, m)
}

func TestDispatchBurstPreservesFramesAndReplyAcks(t *testing.T) {
	m := silentManager()
	t.Cleanup(m.Disconnect)
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	const total = 1024
	delivered := make(chan string, total)
	m.OnMessage = func(frame *WsFrame) {
		if frame.Headers.ReqID == "r0" {
			close(entered)
			<-release
		}
		delivered <- frame.Headers.ReqID
	}
	d := m.startDispatch()
	m.enqueueDispatch(d, &WsFrame{Headers: WsFrameHeaders{ReqID: "r0"}})
	awaitDispatch(t, entered)
	// Run this like the socket reader: it must reach the ack despite the backlog.
	acked := make(chan struct{}, 1)
	m.addPendingAck("reply-probe", func(*WsFrame) { acked <- struct{}{} }, func(err error) {
		t.Errorf("reply failed while callbacks were queued: %v", err)
	})
	go func() {
		for i := 1; i < total; i++ {
			data := fmt.Appendf(nil, `{"cmd":"aibot_msg_callback","headers":{"req_id":"r%d"}}`, i)
			m.handleMessage(data, d)
		}
		m.handleMessage([]byte(`{"headers":{"req_id":"reply-probe"},"errcode":0}`), d)
	}()
	awaitDispatch(t, acked)
	releaseOnce()
	for i := range total {
		if reqID := awaitDispatch(t, delivered); reqID != fmt.Sprintf("r%d", i) {
			t.Fatalf("frame %d = %q, want r%d", i, reqID, i)
		}
	}
}

func TestServerDisconnectDispatchOrderAndExit(t *testing.T) {
	for _, panicInEvent := range []bool{false, true} {
		t.Run(fmt.Sprintf("event_panic=%t", panicInEvent), func(t *testing.T) {
			url, connCh, cleanup := startWSMirror(t)
			t.Cleanup(cleanup)
			c := NewWSClient(WSClientOptions{WSURL: url, Logger: nopLogger()})
			t.Cleanup(c.wsManager.Disconnect)
			entered := make(chan struct{})
			release := make(chan struct{})
			releaseOnce := sync.OnceFunc(func() { close(release) })
			defer releaseOnce()
			order := make(chan string, 3)
			c.OnMessageText(func(*WsFrame) {
				close(entered)
				<-release
				order <- "message"
			})
			c.OnEventDisconnected(func(*WsFrame) {
				order <- "event"
				if panicInEvent {
					panic("terminal event panic")
				}
			})
			c.OnDisconnected(func(string) { order <- "disconnected" })
			c.Connect()
			mc := awaitDispatch(t, connCh)
			authOK(t, mc)
			mc.write(t, WsFrame{Cmd: WsCmd.CALLBACK, Headers: WsFrameHeaders{ReqID: "busy"}, Body: []byte(`{"msgtype":"text"}`)})
			awaitDispatch(t, entered)
			c.wsManager.dispatchMu.Lock()
			d := c.wsManager.dispatcher
			c.wsManager.dispatchMu.Unlock()
			mc.write(t, WsFrame{Cmd: WsCmd.EVENT_CALLBACK, Body: []byte(`{"msgtype":"event","event":{"eventtype":"disconnected_event"}}`)})
			// Socket shutdown must not wait for the busy application handler.
			waitDisconnected(t, c.wsManager)
			select {
			case value := <-order:
				t.Fatalf("premature notification %q", value)
			default:
			}
			releaseOnce()
			for _, want := range []string{"message", "event", "disconnected"} {
				if got := awaitDispatch(t, order); got != want {
					t.Fatalf("notification = %q, want %q", got, want)
				}
			}
			awaitDispatch(t, d.done)
			c.mu.RLock()
			started := c.started
			c.mu.RUnlock()
			if started {
				t.Fatal("client still marked as started after terminal notification")
			}
		})
	}
}

func TestDispatchStopsWhenRetriesExhausted(t *testing.T) {
	for _, authFailure := range []bool{false, true} {
		for _, hasErrorHandler := range []bool{false, true} {
			t.Run(fmt.Sprintf("auth=%t/handler=%t", authFailure, hasErrorHandler), func(t *testing.T) {
				m := silentManager()
				t.Cleanup(m.Disconnect)
				m.maxReconnectAttempts, m.reconnectAttempts = 1, 1
				m.maxAuthFailureAttempts, m.authFailureAttempts = 1, 1
				m.lastCloseWasAuthFailure = authFailure
				if hasErrorHandler {
					m.OnError = func(error) {}
				}
				d := m.startDispatch()
				m.scheduleReconnect()
				awaitDispatch(t, d.done)
			})
		}
	}
}

func TestReconnectFromServerDisconnectCallbacks(t *testing.T) {
	for _, reconnectInEvent := range []bool{false, true} {
		t.Run(fmt.Sprintf("reconnect_in_event=%t", reconnectInEvent), func(t *testing.T) {
			url, connCh, cleanup := startWSMirror(t)
			t.Cleanup(cleanup)
			c := NewWSClient(WSClientOptions{WSURL: url, Logger: nopLogger()})
			t.Cleanup(c.wsManager.Disconnect)
			authenticated := make(chan struct{}, 2)
			c.OnAuthenticated(func() { authenticated <- struct{}{} })
			if reconnectInEvent {
				c.OnEventDisconnected(func(*WsFrame) {
					c.Disconnect()
					c.Connect()
				})
				c.OnDisconnected(func(string) { t.Error("cancelled lifecycle delivered a disconnect notification") })
			} else {
				c.OnDisconnected(func(string) { c.Connect() })
			}
			delivered := make(chan string, 1)
			c.OnMessageText(func(frame *WsFrame) { delivered <- frame.Headers.ReqID })
			c.Connect()
			mc := awaitDispatch(t, connCh)
			authOK(t, mc)
			awaitDispatch(t, authenticated)
			c.wsManager.dispatchMu.Lock()
			old := c.wsManager.dispatcher
			c.wsManager.dispatchMu.Unlock()
			mc.write(t, WsFrame{Cmd: WsCmd.EVENT_CALLBACK, Body: []byte(`{"msgtype":"event","event":{"eventtype":"disconnected_event"}}`)})
			awaitDispatch(t, old.done)
			mc = awaitDispatch(t, connCh)
			authOK(t, mc)
			awaitDispatch(t, authenticated)
			mc.write(t, WsFrame{Cmd: WsCmd.CALLBACK, Headers: WsFrameHeaders{ReqID: "new-lifecycle"}, Body: []byte(`{"msgtype":"text"}`)})
			if got := awaitDispatch(t, delivered); got != "new-lifecycle" {
				t.Fatalf("received callback %q, want new-lifecycle", got)
			}
			c.mu.RLock()
			started := c.started
			c.mu.RUnlock()
			if !started {
				t.Fatal("old disconnect notification reset the new lifecycle")
			}
		})
	}
}

func TestHandlerCanDisconnectWithoutWaitingForItself(t *testing.T) {
	c := NewWSClient(WSClientOptions{Logger: nopLogger()})
	c.started = true
	t.Cleanup(c.wsManager.Disconnect)
	returned := make(chan struct{})
	c.OnMessageText(func(*WsFrame) {
		c.Disconnect()
		close(returned)
	})
	d := c.wsManager.startDispatch()
	c.wsManager.handleMessage([]byte(`{"cmd":"aibot_msg_callback","body":{"msgtype":"text"}}`), d)
	awaitDispatch(t, returned)
	awaitDispatch(t, d.done)
}
