package aibot

import "sync"

// dispatchQueueWarningSize is a backlog warning threshold, not a capacity limit.
const dispatchQueueWarningSize = 256

type dispatchItem struct {
	frame  *WsFrame
	notify func()
}

// callbackDispatcher belongs to one Connect/Disconnect lifecycle. Socket readers
// retain its identity so an old reader cannot enqueue into a later lifecycle.
type callbackDispatcher struct {
	mu      sync.Mutex
	queue   []dispatchItem
	wake    chan struct{}
	done    chan struct{}
	closed  bool
	stopped bool
}

func newCallbackDispatcher() *callbackDispatcher {
	return &callbackDispatcher{
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
}

func (d *callbackDispatcher) signal() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *callbackDispatcher) enqueue(item dispatchItem) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0
	}
	d.queue = append(d.queue, item)
	d.signal()
	return len(d.queue)
}

// finish seals the queue after its final notifications. The worker drains the
// accepted callbacks and exits without requiring a subsequent Disconnect call.
func (d *callbackDispatcher) finish(items ...dispatchItem) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.queue = append(d.queue, items...)
	d.closed = true
	d.signal()
}

// stop discards callbacks that have not started. It never waits for a callback,
// which may itself be calling Disconnect.
func (d *callbackDispatcher) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	d.stopped = true
	d.queue = nil
	d.signal()
}

func (d *callbackDispatcher) next() (dispatchItem, bool) {
	for {
		d.mu.Lock()
		if d.stopped {
			d.mu.Unlock()
			return dispatchItem{}, false
		}
		if len(d.queue) > 0 {
			item := d.queue[0]
			d.queue[0] = dispatchItem{}
			d.queue = d.queue[1:]
			if len(d.queue) == 0 {
				d.queue = nil
			}
			d.mu.Unlock()
			return item, true
		}
		closed := d.closed
		d.mu.Unlock()
		if closed {
			return dispatchItem{}, false
		}
		<-d.wake
	}
}

func (m *WsConnectionManager) startDispatch() *callbackDispatcher {
	m.dispatchMu.Lock()
	defer m.dispatchMu.Unlock()
	if m.dispatcher == nil {
		m.dispatcher = newCallbackDispatcher()
		go m.dispatchLoop(m.dispatcher)
	}
	return m.dispatcher
}

func (m *WsConnectionManager) stopDispatch() {
	m.dispatchMu.Lock()
	defer m.dispatchMu.Unlock()
	if m.dispatcher != nil {
		m.dispatcher.stop()
		m.dispatcher = nil
	}
}

func (m *WsConnectionManager) dispatchLoop(d *callbackDispatcher) {
	defer close(d.done)
	defer func() {
		m.dispatchMu.Lock()
		if m.dispatcher == d {
			m.dispatcher = nil
		}
		m.dispatchMu.Unlock()
	}()
	for {
		item, ok := d.next()
		if !ok {
			return
		}
		m.dispatch(item)
	}
}

func (m *WsConnectionManager) dispatch(item dispatchItem) {
	defer func() {
		if r := recover(); r != nil {
			if item.frame != nil {
				m.logger.Error("Panic in OnMessage handler: cmd=%s, reqId=%s, panic=%v", item.frame.Cmd, item.frame.Headers.ReqID, r)
			} else {
				m.logger.Error("Panic in disconnect handler: %v", r)
			}
		}
	}()
	if item.frame != nil && m.OnMessage != nil {
		m.OnMessage(item.frame)
	}
	if item.notify != nil {
		item.notify()
	}
}

func (m *WsConnectionManager) enqueueDispatch(d *callbackDispatcher, frame *WsFrame) {
	if pending := d.enqueue(dispatchItem{frame: frame}); pending == dispatchQueueWarningSize {
		m.logger.Warn("Callback queue has %d pending frames; offload slow handlers to avoid memory growth", pending)
	}
}
