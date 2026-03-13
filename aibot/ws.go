package aibot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ============================================================================
// 常量定义
// ============================================================================

const (
	// 默认 WebSocket 连接地址
	defaultWSURL = "wss://openws.work.weixin.qq.com"

	// 重连参数
	defaultReconnectBaseDelay = 1000  // 基础延迟 (ms)
	reconnectMaxDelay         = 30000 // 最大延迟 (ms)
	maxMissedPong             = 2     // 最大丢失 pong 次数

	// 队列参数
	replyAckTimeout   = 5000 // 回执超时 (ms)
	maxReplyQueueSize = 100  // 单个 req_id 的最大队列长度
)

// ============================================================================
// 回调函数类型
// ============================================================================

// OnConnectedFunc 连接建立回调
type OnConnectedFunc func()

// OnAuthenticatedFunc 认证成功回调
type OnAuthenticatedFunc func()

// OnDisconnectedFunc 连接断开回调
type OnDisconnectedFunc func(reason string)

// OnMessageFunc 收到消息回调
type OnMessageFunc func(frame *WsFrame)

// OnReconnectingFunc 重连回调
type OnReconnectingFunc func(attempt int)

// OnErrorFunc 错误回调
type OnErrorFunc func(err error)

// ============================================================================
// 回复队列项
// ============================================================================

type replyQueueItem struct {
	frame   WsFrame
	resolve func(*WsFrame)
	reject  func(error)
}

// ============================================================================
// WsConnectionManager WebSocket 长连接管理器
// ============================================================================

// WsConnectionManager 负责维护与企业微信的 WebSocket 长连接
type WsConnectionManager struct {
	logger Logger
	wsURL  string

	heartbeatInterval    int
	reconnectBaseDelay   int
	maxReconnectAttempts int

	ws            *websocket.Conn
	isManualClose bool

	// 认证凭证
	botID     string
	botSecret string

	// 重连状态
	reconnectAttempts int
	missedPongCount   int

	// 心跳定时器
	heartbeatTimer *time.Timer

	// 回复队列
	replyQueues   map[string][]replyQueueItem
	replyQueuesMu sync.Mutex
	pendingAcks   map[string]*pendingAck
	pendingAcksMu sync.Mutex

	// 回调函数
	OnConnected     OnConnectedFunc
	OnAuthenticated OnAuthenticatedFunc
	OnDisconnected  OnDisconnectedFunc
	OnMessage       OnMessageFunc
	OnReconnecting  OnReconnectingFunc
	OnError         OnErrorFunc

	// 上下文和取消
	ctx    context.Context
	cancel context.CancelFunc
}

// pendingAck 待回执项
type pendingAck struct {
	resolve func(*WsFrame)
	reject  func(error)
	timer   *time.Timer
}

// NewWsConnectionManager 创建 WebSocket 连接管理器
func NewWsConnectionManager(
	logger Logger,
	heartbeatInterval int,
	reconnectBaseDelay int,
	maxReconnectAttempts int,
	wsURL string,
) *WsConnectionManager {
	if heartbeatInterval <= 0 {
		heartbeatInterval = 30000
	}
	if reconnectBaseDelay <= 0 {
		reconnectBaseDelay = defaultReconnectBaseDelay
	}
	if maxReconnectAttempts == 0 {
		maxReconnectAttempts = 10
	}
	if wsURL == "" {
		wsURL = defaultWSURL
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &WsConnectionManager{
		logger:               logger,
		wsURL:                wsURL,
		heartbeatInterval:    heartbeatInterval,
		reconnectBaseDelay:   reconnectBaseDelay,
		maxReconnectAttempts: maxReconnectAttempts,

		replyQueues: make(map[string][]replyQueueItem),
		pendingAcks: make(map[string]*pendingAck),

		ctx:    ctx,
		cancel: cancel,
	}
}

// SetCredentials 设置认证凭证
func (m *WsConnectionManager) SetCredentials(botID, botSecret string) {
	m.botID = botID
	m.botSecret = botSecret
}

// Connect 建立 WebSocket 连接
func (m *WsConnectionManager) Connect() {
	m.isManualClose = false

	// 清理可能未完全关闭的旧连接
	if m.ws != nil {
		m.ws.Close()
		m.ws = nil
	}

	m.logger.Info("Connecting to WebSocket: " + m.wsURL + "...")

	go m.connect()
}

func (m *WsConnectionManager) connect() {
	dialer := websocket.Dialer{}

	ws, _, err := dialer.Dial(m.wsURL, nil)
	if err != nil {
		m.logger.Error("Failed to create WebSocket connection: " + err.Error())
		m.OnError(err)
		m.scheduleReconnect()
		return
	}

	m.ws = ws
	m.setupEventHandlers()

	// 连接建立后立即发送认证帧
	m.sendAuth()
}

// setupEventHandlers 设置事件处理器
func (m *WsConnectionManager) setupEventHandlers() {
	if m.ws == nil {
		return
	}

	// 读取消息
	go func() {
		for {
			select {
			case <-m.ctx.Done():
				return
			default:
			}

			_, data, err := m.ws.ReadMessage()
			if err != nil {
				if m.isManualClose {
					return
				}

				m.logger.Error("WebSocket read error: " + err.Error())
				m.handleClose(err.Error())
				return
			}

			m.handleMessage(data)
		}
	}()
}

// handleMessage 处理收到的消息
func (m *WsConnectionManager) handleMessage(data []byte) {
	var frame WsFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		m.logger.Error("Failed to parse WebSocket message: " + err.Error())
		return
	}

	// 消息推送回调
	if frame.Cmd == WsCmd.CALLBACK || frame.Cmd == WsCmd.EVENT_CALLBACK {
		m.logger.Debug("Received push message: " + string(data))
		m.OnMessage(&frame)
		return
	}

	// 通过 req_id 前缀区分响应类型
	reqID := frame.Headers.ReqID

	// 检查是否是回复消息的回执
	if m.hasPendingAck(reqID) {
		m.handleReplyAck(reqID, &frame)
		return
	}

	// 认证响应
	if strings.HasPrefix(reqID, WsCmd.SUBSCRIBE) {
		m.handleAuthResponse(&frame)
		return
	}

	// 心跳响应
	if strings.HasPrefix(reqID, WsCmd.HEARTBEAT) {
		m.handleHeartbeatResponse(&frame)
		return
	}

	// 未知帧类型
	m.logger.Warn("Received unknown frame: " + string(data))
	m.OnMessage(&frame)
}

// handleAuthResponse 处理认证响应
func (m *WsConnectionManager) handleAuthResponse(frame *WsFrame) {
	if frame.ErrCode != 0 {
		m.logger.Error(fmt.Sprintf("Authentication failed: errcode=%d, errmsg=%s", frame.ErrCode, frame.ErrMsg))
		m.OnError(errors.New(fmt.Sprintf("Authentication failed: %s (code: %d)", frame.ErrMsg, frame.ErrCode)))
		return
	}

	m.logger.Info("Authentication successful")
	m.startHeartbeat()
	m.OnAuthenticated()
}

// handleHeartbeatResponse 处理心跳响应
func (m *WsConnectionManager) handleHeartbeatResponse(frame *WsFrame) {
	if frame.ErrCode != 0 {
		m.logger.Warn(fmt.Sprintf("Heartbeat ack error: errcode=%d, errmsg=%s", frame.ErrCode, frame.ErrMsg))
		return
	}

	m.missedPongCount = 0
	m.logger.Debug("Received heartbeat ack")
}

// handleClose 处理连接关闭
func (m *WsConnectionManager) handleClose(reason string) {
	m.stopHeartbeat()
	m.clearPendingMessages("WebSocket connection closed (" + reason + ")")

	m.OnDisconnected(reason)

	if !m.isManualClose {
		m.scheduleReconnect()
	}
}

// sendAuth 发送认证帧
func (m *WsConnectionManager) sendAuth() {
	if m.ws == nil {
		return
	}

	m.reconnectAttempts = 0
	m.missedPongCount = 0

	frame := WsFrame{
		Cmd: WsCmd.SUBSCRIBE,
		Headers: WsFrameHeaders{
			ReqID: generateReqId(WsCmd.SUBSCRIBE),
		},
		Body: json.RawMessage(fmt.Sprintf(`{"bot_id":"%s","secret":"%s"}`, m.botID, m.botSecret)),
	}

	m.sendFrame(frame)
	m.logger.Info("Auth frame sent")
}

// sendHeartbeat 发送心跳
func (m *WsConnectionManager) sendHeartbeat() {
	// 检查丢失 pong 次数
	if m.missedPongCount >= maxMissedPong {
		m.logger.Warn(fmt.Sprintf("No heartbeat ack received for %d consecutive pings, connection considered dead", m.missedPongCount))
		m.stopHeartbeat()
		if m.ws != nil {
			m.ws.Close()
		}
		return
	}

	m.missedPongCount++

	frame := WsFrame{
		Cmd: WsCmd.HEARTBEAT,
		Headers: WsFrameHeaders{
			ReqID: generateReqId(WsCmd.HEARTBEAT),
		},
	}

	m.sendFrame(frame)

	pingMsg := ""
	if m.missedPongCount > 1 {
		pingMsg = fmt.Sprintf(" (awaiting %d pong)", m.missedPongCount)
	}
	m.logger.Debug("Heartbeat sent" + pingMsg)
}

// startHeartbeat 启动心跳
func (m *WsConnectionManager) startHeartbeat() {
	m.stopHeartbeat()

	m.heartbeatTimer = time.AfterFunc(
		time.Duration(m.heartbeatInterval)*time.Millisecond,
		m.sendHeartbeat,
	)

	m.logger.Debug(fmt.Sprintf("Heartbeat timer started, interval: %dms", m.heartbeatInterval))
}

// stopHeartbeat 停止心跳
func (m *WsConnectionManager) stopHeartbeat() {
	if m.heartbeatTimer != nil {
		m.heartbeatTimer.Stop()
		m.heartbeatTimer = nil
		m.logger.Debug("Heartbeat timer stopped")
	}
}

// scheduleReconnect 安排重连
func (m *WsConnectionManager) scheduleReconnect() {
	if m.maxReconnectAttempts > 0 && m.reconnectAttempts >= m.maxReconnectAttempts {
		m.logger.Error(fmt.Sprintf("Max reconnect attempts reached (%d), giving up", m.maxReconnectAttempts))
		m.OnError(errors.New("max reconnect attempts exceeded"))
		return
	}

	m.reconnectAttempts++

	// 指数退避: 1s, 2s, 4s, 8s ...
	delay := m.reconnectBaseDelay
	if m.reconnectAttempts > 1 {
		delay = m.reconnectBaseDelay * (1 << (m.reconnectAttempts - 1))
	}
	if delay > reconnectMaxDelay {
		delay = reconnectMaxDelay
	}

	m.logger.Info(fmt.Sprintf("Reconnecting in %dms (attempt %d)...", delay, m.reconnectAttempts))
	m.OnReconnecting(m.reconnectAttempts)

	time.AfterFunc(
		time.Duration(delay)*time.Millisecond,
		func() {
			if m.isManualClose {
				return
			}
			m.Connect()
		},
	)
}

// sendFrame 发送帧
func (m *WsConnectionManager) sendFrame(frame WsFrame) {
	if m.ws == nil {
		return
	}

	data, err := json.Marshal(frame)
	if err != nil {
		m.logger.Error("Failed to marshal frame: " + err.Error())
		return
	}

	if err := m.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		m.logger.Error("Failed to send frame: " + err.Error())
	}
}

// Send 发送数据帧
func (m *WsConnectionManager) Send(frame WsFrame) error {
	if m.ws == nil {
		return errors.New("WebSocket not connected, unable to send data")
	}

	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}

	return m.ws.WriteMessage(websocket.TextMessage, data)
}

// SendReply 通过 WebSocket 通道发送回复消息
func (m *WsConnectionManager) SendReply(reqID string, body interface{}, cmd string) (*WsFrame, error) {
	if cmd == "" {
		cmd = WsCmd.RESPONSE
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	frame := WsFrame{
		Cmd: cmd,
		Headers: WsFrameHeaders{
			ReqID: reqID,
		},
		Body: bodyJSON,
	}

	// 放入队列
	item := replyQueueItem{
		frame: frame,
	}

	// 使用 channel 来实现异步等待
	resultChan := make(chan *WsFrame, 1)
	errChan := make(chan error, 1)

	item.resolve = func(ack *WsFrame) {
		resultChan <- ack
	}
	item.reject = func(err error) {
		errChan <- err
	}

	m.enqueueReply(reqID, item)

	// 等待结果
	select {
	case ack := <-resultChan:
		return ack, nil
	case err := <-errChan:
		return nil, err
	}
}

// enqueueReply 将回复放入队列
func (m *WsConnectionManager) enqueueReply(reqID string, item replyQueueItem) {
	m.replyQueuesMu.Lock()
	defer m.replyQueuesMu.Unlock()

	if _, ok := m.replyQueues[reqID]; !ok {
		m.replyQueues[reqID] = []replyQueueItem{}
	}

	queue := m.replyQueues[reqID]

	// 防止队列无限增长
	if len(queue) >= maxReplyQueueSize {
		m.logger.Warn(fmt.Sprintf("Reply queue for reqId %s exceeds max size (%d), rejecting new message", reqID, maxReplyQueueSize))
		item.reject(errors.New(fmt.Sprintf("Reply queue for reqId %s exceeds max size", reqID)))
		return
	}

	queue = append(queue, item)
	m.replyQueues[reqID] = queue

	// 如果队列中只有这一条，立即处理
	if len(queue) == 1 {
		go m.processReplyQueue(reqID)
	}
}

// processReplyQueue 处理回复队列
func (m *WsConnectionManager) processReplyQueue(reqID string) {
	m.replyQueuesMu.Lock()
	queue, ok := m.replyQueues[reqID]
	if !ok || len(queue) == 0 {
		m.replyQueuesMu.Unlock()
		return
	}

	item := queue[0]
	m.replyQueuesMu.Unlock()

	// 发送帧
	if err := m.Send(item.frame); err != nil {
		m.logger.Error(fmt.Sprintf("Failed to send reply for reqId %s: %s", reqID, err.Error()))

		m.replyQueuesMu.Lock()
		if q, exists := m.replyQueues[reqID]; exists && len(q) > 0 {
			m.replyQueues[reqID] = q[1:]
		}
		m.replyQueuesMu.Unlock()

		item.reject(err)
		m.processReplyQueue(reqID)
		return
	}

	m.logger.Debug(fmt.Sprintf("Reply message sent via WebSocket, reqId: %s", reqID))

	// 设置回执超时
	m.addPendingAck(reqID, item.resolve, item.reject)
}

// addPendingAck 添加待回执项
func (m *WsConnectionManager) addPendingAck(reqID string, resolve func(*WsFrame), reject func(error)) {
	m.pendingAcksMu.Lock()
	defer m.pendingAcksMu.Unlock()

	// 如果已存在，先清理
	if existing, ok := m.pendingAcks[reqID]; ok {
		existing.timer.Stop()
	}

	timer := time.AfterFunc(
		time.Duration(replyAckTimeout)*time.Millisecond,
		func() {
			m.handleReplyAckTimeout(reqID)
		},
	)

	m.pendingAcks[reqID] = &pendingAck{
		resolve: resolve,
		reject:  reject,
		timer:   timer,
	}
}

// hasPendingAck 检查是否有待回执项
func (m *WsConnectionManager) hasPendingAck(reqID string) bool {
	m.pendingAcksMu.Lock()
	defer m.pendingAcksMu.Unlock()
	_, ok := m.pendingAcks[reqID]
	return ok
}

// handleReplyAck 处理回复回执
func (m *WsConnectionManager) handleReplyAck(reqID string, frame *WsFrame) {
	m.pendingAcksMu.Lock()
	pending, ok := m.pendingAcks[reqID]
	if !ok {
		m.pendingAcksMu.Unlock()
		return
	}

	// 清除超时定时器
	pending.timer.Stop()
	delete(m.pendingAcks, reqID)
	m.pendingAcksMu.Unlock()

	// 从队列中移除
	m.replyQueuesMu.Lock()
	if q, exists := m.replyQueues[reqID]; exists && len(q) > 0 {
		m.replyQueues[reqID] = q[1:]
		if len(m.replyQueues[reqID]) == 0 {
			delete(m.replyQueues, reqID)
		}
	}
	m.replyQueuesMu.Unlock()

	if frame.ErrCode != 0 {
		m.logger.Warn(fmt.Sprintf("Reply ack error: reqId=%s, errcode=%d, errmsg=%s", reqID, frame.ErrCode, frame.ErrMsg))
		pending.reject(fmt.Errorf("reply ack error: %s (code: %d)", frame.ErrMsg, frame.ErrCode))
	} else {
		m.logger.Debug(fmt.Sprintf("Reply ack received for reqId: %s", reqID))
		pending.resolve(frame)
	}

	// 继续处理队列中的下一条
	m.processReplyQueue(reqID)
}

// handleReplyAckTimeout 处理回复超时
func (m *WsConnectionManager) handleReplyAckTimeout(reqID string) {
	m.pendingAcksMu.Lock()
	pending, ok := m.pendingAcks[reqID]
	if !ok {
		m.pendingAcksMu.Unlock()
		return
	}

	delete(m.pendingAcks, reqID)
	m.pendingAcksMu.Unlock()

	m.logger.Warn(fmt.Sprintf("Reply ack timeout (%dms) for reqId: %s", replyAckTimeout, reqID))

	// 从队列中移除
	m.replyQueuesMu.Lock()
	if q, exists := m.replyQueues[reqID]; exists && len(q) > 0 {
		m.replyQueues[reqID] = q[1:]
		if len(m.replyQueues[reqID]) == 0 {
			delete(m.replyQueues, reqID)
		}
	}
	m.replyQueuesMu.Unlock()

	pending.reject(fmt.Errorf("reply ack timeout (%dms) for reqId: %s", replyAckTimeout, reqID))

	// 继续处理队列中的下一条
	m.processReplyQueue(reqID)
}

// clearPendingMessages 清理所有待处理的消息
func (m *WsConnectionManager) clearPendingMessages(reason string) {
	m.pendingAcksMu.Lock()
	for _, pending := range m.pendingAcks {
		pending.timer.Stop()
	}
	m.pendingAcks = make(map[string]*pendingAck)
	m.pendingAcksMu.Unlock()

	m.replyQueuesMu.Lock()
	for reqID, queue := range m.replyQueues {
		for _, item := range queue {
			item.reject(errors.New(reason + ", reply for reqId: " + reqID + " cancelled"))
		}
	}
	m.replyQueues = make(map[string][]replyQueueItem)
	m.replyQueuesMu.Unlock()
}

// Disconnect 断开连接
func (m *WsConnectionManager) Disconnect() {
	m.isManualClose = true
	m.cancel()

	m.stopHeartbeat()
	m.clearPendingMessages("Connection manually closed")

	if m.ws != nil {
		m.ws.Close()
		m.ws = nil
	}

	m.logger.Info("WebSocket connection manually closed")
}

// IsConnected 获取当前连接状态
func (m *WsConnectionManager) IsConnected() bool {
	return m.ws != nil
}
