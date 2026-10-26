package aibot

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ErrReplySkipped 非阻塞流式回复被跳过（上一条同 reqId 消息仍在等待 ack）。
// 由 WSClient.ReplyStreamNonBlocking 原样返回，可用 errors.Is 判断；此时没有发送任何数据。
var ErrReplySkipped = errors.New("reply skipped: previous message still pending ack")

// ============================================================================
// 事件回调类型定义
// ============================================================================

// MessageHandlerFunc 消息处理函数。
// frame 为收到的回调帧，可传给 ParseMessageBody 解析，或作为 Reply 系列方法的参数。
// 函数在分发协程上按到达顺序串行执行，可直接调用 Reply 系列方法等待回执；
// 耗时操作会推迟后续帧的分发。
type MessageHandlerFunc func(frame *WsFrame)

// EventHandlerFunc 事件处理函数。
// 执行约束与 MessageHandlerFunc 相同；事件详情在 EventMessage.Event 中。
type EventHandlerFunc func(frame *WsFrame)

// ConnectionHandlerFunc 连接状态处理函数，用于 OnConnected 与 OnAuthenticated。
type ConnectionHandlerFunc func()

// ReconnectHandlerFunc 重连处理函数。attempt 为当前重连序号，从 1 开始；认证失败重试与断线重连分别计数。
type ReconnectHandlerFunc func(attempt int)

// ErrorHandlerFunc 错误处理函数。
// 可能收到拨号错误、认证失败错误，以及重试耗尽时的 *WSAuthFailureError 或
// *WSReconnectExhaustedError（可用 errors.As 判断，此时 SDK 已停止自动重连）。
type ErrorHandlerFunc func(err error)

// DisconnectHandlerFunc 断开连接处理函数。reason 为人类可读的断开原因，不是稳定的错误码。
type DisconnectHandlerFunc func(reason string)

// ============================================================================
// WSClient 企业微信智能机器人客户端
// ============================================================================

// WSClient 企业微信智能机器人客户端
// 使用 WebSocket 长连接通道与企业微信通信
//
// 通过 NewWSClient 创建，零值不可用。回调注册、Connect、Disconnect 与各发送方法可从多个协程并发调用。连接由 WSClient 持有，调用方负责在退出时调用 Disconnect。
type WSClient struct {
	options   RequiredWSClientOptions
	apiClient *WeComApiClient
	wsManager *WsConnectionManager
	logger    Logger

	// 事件处理函数
	onMessage      MessageHandlerFunc
	onMessageText  MessageHandlerFunc
	onMessageImage MessageHandlerFunc
	onMessageMixed MessageHandlerFunc
	onMessageVoice MessageHandlerFunc
	onMessageFile  MessageHandlerFunc
	onMessageVideo MessageHandlerFunc

	onEvent                  EventHandlerFunc
	onEventEnterChat         EventHandlerFunc
	onEventTemplateCardEvent EventHandlerFunc
	onEventFeedbackEvent     EventHandlerFunc
	onEventDisconnected      EventHandlerFunc

	onConnected     ConnectionHandlerFunc
	onAuthenticated ConnectionHandlerFunc
	onDisconnected  DisconnectHandlerFunc
	onReconnecting  ReconnectHandlerFunc
	onError         ErrorHandlerFunc

	// 消息处理器
	messageHandler *MessageHandler

	// 状态
	started bool
	mu      sync.RWMutex
}

// NewWSClient 创建 WSClient 实例
//
// 只构造客户端，不发起网络连接，也不校验凭证；凭证错误在 Connect 后通过 OnError 报告。
// options 中为零值的字段使用 DefaultWSClientOptions 中的默认值；Logger 为 nil 时使用
// NewDefaultLogger（输出到标准输出）。
func NewWSClient(options WSClientOptions) *WSClient {
	// 设置默认值
	opts := DefaultWSClientOptions

	if options.ReconnectInterval > 0 {
		opts.ReconnectInterval = options.ReconnectInterval
	}
	if options.MaxReconnectAttempts != 0 {
		opts.MaxReconnectAttempts = options.MaxReconnectAttempts
	}
	if options.MaxAuthFailureAttempts != 0 {
		opts.MaxAuthFailureAttempts = options.MaxAuthFailureAttempts
	}
	if options.HeartbeatInterval > 0 {
		opts.HeartbeatInterval = options.HeartbeatInterval
	}
	if options.RequestTimeout > 0 {
		opts.RequestTimeout = options.RequestTimeout
	}
	if options.MaxReplyQueueSize > 0 {
		opts.MaxReplyQueueSize = options.MaxReplyQueueSize
	}
	if options.WSURL != "" {
		opts.WSURL = options.WSURL
	}

	logger := options.Logger
	if logger == nil {
		logger = NewDefaultLogger()
	}

	client := &WSClient{
		options:        opts,
		logger:         logger,
		messageHandler: NewMessageHandler(logger),
	}

	// 初始化 API 客户端
	client.apiClient = NewWeComApiClient(logger, opts.RequestTimeout)

	// 初始化 WebSocket 管理器
	client.wsManager = NewWsConnectionManager(
		logger,
		opts.HeartbeatInterval,
		opts.ReconnectInterval,
		opts.MaxReconnectAttempts,
		opts.WSURL,
		options.WsDialer,
		opts.MaxReplyQueueSize,
		opts.MaxAuthFailureAttempts,
	)

	// 设置认证凭证
	extraAuthParams := make(map[string]interface{})
	if options.Scene != nil {
		extraAuthParams["scene"] = *options.Scene
	}
	if options.PlugVersion != "" {
		extraAuthParams["plug_version"] = options.PlugVersion
	}
	client.wsManager.SetCredentials(options.BotID, options.Secret, extraAuthParams)

	// 绑定 WebSocket 事件
	client.setupWsEvents()

	return client
}

// setupWsEvents 设置 WebSocket 事件处理
func (c *WSClient) setupWsEvents() {
	c.wsManager.OnConnected = func() {
		if handler := c.connectedHandler(); handler != nil {
			handler()
		}
	}

	c.wsManager.OnAuthenticated = func() {
		c.logger.Info("Authenticated")
		if handler := c.authenticatedHandler(); handler != nil {
			handler()
		}
	}

	c.wsManager.OnDisconnected = func(reason string) {
		if handler := c.disconnectedHandler(); handler != nil {
			handler(reason)
		}
	}

	// 服务端因新连接建立而主动断开旧连接
	c.wsManager.OnServerDisconnect = func(reason string) {
		c.logger.Warn("Server disconnected this connection: %s", reason)
		c.mu.Lock()
		c.started = false
		c.mu.Unlock()
		if handler := c.disconnectedHandler(); handler != nil {
			handler(reason)
		}
	}

	c.wsManager.OnReconnecting = func(attempt int) {
		if handler := c.reconnectingHandler(); handler != nil {
			handler(attempt)
		}
	}

	c.wsManager.OnError = func(err error) {
		// 重连/认证重试耗尽：SDK 已停止自动重连，复位 started 以允许用户再次 Connect()
		var authErr *WSAuthFailureError
		var reconnectErr *WSReconnectExhaustedError
		if errors.As(err, &authErr) || errors.As(err, &reconnectErr) {
			c.mu.Lock()
			c.started = false
			c.mu.Unlock()
		}
		if handler := c.errorHandler(); handler != nil {
			handler(err)
		}
	}

	c.wsManager.OnMessage = func(frame *WsFrame) {
		c.messageHandler.HandleFrame(frame, c)
	}
}

// ============================================================================
// 连接管理
// ============================================================================

// Connect 建立 WebSocket 长连接
// SDK 默认使用内置地址（可用 WSClientOptions.WSURL 覆盖）建立连接，连接成功后自动发送认证帧（botId + secret）。
// 连接断开或认证失败重试耗尽后（started 已被复位），可再次调用 Connect() 重新发起连接。
//
// Connect 不等待建连完成：拨号在后台协程中进行，结果通过 OnConnected、OnAuthenticated 与 OnError 回调通知。客户端已启动时再次调用只记录警告并返回。
// 网络断开或心跳超时后会按指数退避自动重连；服务端因新连接推送 disconnected_event
// 时不会自动重连。返回值为 c 本身，便于链式调用。
func (c *WSClient) Connect() *WSClient {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		c.logger.Warn("Client already connected")
		return c
	}

	c.logger.Info("Establishing WebSocket connection...")
	c.started = true

	c.wsManager.Connect()

	return c
}

// Disconnect 断开 WebSocket 连接
//
// 关闭当前连接、停止心跳并取消挂起的自动重连；正在等待回执或排队中的回复立即以错误返回。客户端未启动时只记录警告。Disconnect 不等待后台协程退出；之后可再次调用 Connect 重新连接。
func (c *WSClient) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		c.logger.Warn("Client not connected")
		return
	}

	c.logger.Info("Disconnecting...")
	c.started = false
	c.wsManager.Disconnect()
	c.logger.Info("Disconnected")
}

// IsConnected 获取当前连接状态
//
// 返回 true 表示 WebSocket 已建立，不代表认证已成功；认证成功请以 OnAuthenticated 为准。
func (c *WSClient) IsConnected() bool {
	return c.wsManager.IsConnected()
}

// ============================================================================
// 事件绑定
// ============================================================================

// OnMessage 收到消息（所有类型）
//
// 每条消息回调先触发 OnMessage，再触发对应类型的 OnMessageText 等回调。
// 再次调用会替换之前的回调，传 nil 取消。回调执行约束见 MessageHandlerFunc。
func (c *WSClient) OnMessage(handler MessageHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMessage = handler
}

// OnMessageText 收到文本消息
func (c *WSClient) OnMessageText(handler MessageHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMessageText = handler
}

// OnMessageImage 收到图片消息
func (c *WSClient) OnMessageImage(handler MessageHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMessageImage = handler
}

// OnMessageMixed 收到图文混排消息
func (c *WSClient) OnMessageMixed(handler MessageHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMessageMixed = handler
}

// OnMessageVoice 收到语音消息
func (c *WSClient) OnMessageVoice(handler MessageHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMessageVoice = handler
}

// OnMessageFile 收到文件消息
func (c *WSClient) OnMessageFile(handler MessageHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMessageFile = handler
}

// OnMessageVideo 收到视频消息
func (c *WSClient) OnMessageVideo(handler MessageHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMessageVideo = handler
}

// OnEvent 收到事件回调（所有事件类型）
//
// 每个事件先触发 OnEvent，再触发对应类型的 OnEventEnterChat 等回调。
// 再次调用会替换之前的回调，传 nil 取消。
func (c *WSClient) OnEvent(handler EventHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEvent = handler
}

// OnEventEnterChat 收到进入会话事件
func (c *WSClient) OnEventEnterChat(handler EventHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEventEnterChat = handler
}

// OnEventTemplateCardEvent 收到模板卡片事件
func (c *WSClient) OnEventTemplateCardEvent(handler EventHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEventTemplateCardEvent = handler
}

// OnEventFeedbackEvent 收到用户反馈事件
func (c *WSClient) OnEventFeedbackEvent(handler EventHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEventFeedbackEvent = handler
}

// OnEventDisconnected 收到连接断开事件（有新连接建立，服务端主动断开当前旧连接）
//
// 收到该事件后 SDK 关闭旧连接、不再自动重连，并随后触发 OnDisconnected。
func (c *WSClient) OnEventDisconnected(handler EventHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEventDisconnected = handler
}

// OnConnected 连接建立
//
// 在每次（含自动重连）WebSocket 建立、认证帧发出后调用，此时认证尚未完成。
func (c *WSClient) OnConnected(handler ConnectionHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onConnected = handler
}

// OnAuthenticated 认证成功
//
// 每次（含自动重连）认证成功后调用，此后才能收到消息回调。
func (c *WSClient) OnAuthenticated(handler ConnectionHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onAuthenticated = handler
}

// OnDisconnected 连接断开
//
// 连接因读错误、心跳超时、认证失败或服务端踢下线而断开时调用；调用 Disconnect
// 主动断开时不调用。
func (c *WSClient) OnDisconnected(handler DisconnectHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onDisconnected = handler
}

// OnReconnecting 重连中
//
// 在安排下一次重连时调用，attempt 为本轮重试次数（从 1 开始）。
func (c *WSClient) OnReconnecting(handler ReconnectHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onReconnecting = handler
}

// OnError 发生错误
//
// 错误类型见 ErrorHandlerFunc。重连或认证重试耗尽后可在回调内再次调用 Connect 重新连接。
func (c *WSClient) OnError(handler ErrorHandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onError = handler
}

// ============================================================================
// Handler 快照读取（分发线程在锁外调用，读取时加锁取当前值）
// ============================================================================

func (c *WSClient) messageHandler_() MessageHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onMessage
}

func (c *WSClient) messageTextHandler() MessageHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onMessageText
}

func (c *WSClient) messageImageHandler() MessageHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onMessageImage
}

func (c *WSClient) messageMixedHandler() MessageHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onMessageMixed
}

func (c *WSClient) messageVoiceHandler() MessageHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onMessageVoice
}

func (c *WSClient) messageFileHandler() MessageHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onMessageFile
}

func (c *WSClient) messageVideoHandler() MessageHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onMessageVideo
}

func (c *WSClient) eventHandler() EventHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onEvent
}

func (c *WSClient) eventEnterChatHandler() EventHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onEventEnterChat
}

func (c *WSClient) eventTemplateCardEventHandler() EventHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onEventTemplateCardEvent
}

func (c *WSClient) eventFeedbackEventHandler() EventHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onEventFeedbackEvent
}

func (c *WSClient) eventDisconnectedHandler() EventHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onEventDisconnected
}

func (c *WSClient) connectedHandler() ConnectionHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onConnected
}

func (c *WSClient) authenticatedHandler() ConnectionHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onAuthenticated
}

func (c *WSClient) disconnectedHandler() DisconnectHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onDisconnected
}

func (c *WSClient) reconnectingHandler() ReconnectHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onReconnecting
}

func (c *WSClient) errorHandler() ErrorHandlerFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.onError
}

// ============================================================================
// FrameEmitter 实现
// ============================================================================

// EmitMessage 实现 FrameEmitter：调用 OnMessage 注册的回调（未注册时忽略）。
// Emit 系列方法供 MessageHandler 分发帧使用，应用代码通常无需直接调用。
func (c *WSClient) EmitMessage(frame *WsFrame) {
	if h := c.messageHandler_(); h != nil {
		h(frame)
	}
}

// EmitMessageText 实现 FrameEmitter：调用 OnMessageText 注册的回调。
func (c *WSClient) EmitMessageText(frame *WsFrame) {
	if h := c.messageTextHandler(); h != nil {
		h(frame)
	}
}

// EmitMessageImage 实现 FrameEmitter：调用 OnMessageImage 注册的回调。
func (c *WSClient) EmitMessageImage(frame *WsFrame) {
	if h := c.messageImageHandler(); h != nil {
		h(frame)
	}
}

// EmitMessageMixed 实现 FrameEmitter：调用 OnMessageMixed 注册的回调。
func (c *WSClient) EmitMessageMixed(frame *WsFrame) {
	if h := c.messageMixedHandler(); h != nil {
		h(frame)
	}
}

// EmitMessageVoice 实现 FrameEmitter：调用 OnMessageVoice 注册的回调。
func (c *WSClient) EmitMessageVoice(frame *WsFrame) {
	if h := c.messageVoiceHandler(); h != nil {
		h(frame)
	}
}

// EmitMessageFile 实现 FrameEmitter：调用 OnMessageFile 注册的回调。
func (c *WSClient) EmitMessageFile(frame *WsFrame) {
	if h := c.messageFileHandler(); h != nil {
		h(frame)
	}
}

// EmitMessageVideo 实现 FrameEmitter：调用 OnMessageVideo 注册的回调。
func (c *WSClient) EmitMessageVideo(frame *WsFrame) {
	if h := c.messageVideoHandler(); h != nil {
		h(frame)
	}
}

// EmitEvent 实现 FrameEmitter：调用 OnEvent 注册的回调。
func (c *WSClient) EmitEvent(frame *WsFrame) {
	if h := c.eventHandler(); h != nil {
		h(frame)
	}
}

// EmitEventEnterChat 实现 FrameEmitter：调用 OnEventEnterChat 注册的回调。
func (c *WSClient) EmitEventEnterChat(frame *WsFrame) {
	if h := c.eventEnterChatHandler(); h != nil {
		h(frame)
	}
}

// EmitEventTemplateCardEvent 实现 FrameEmitter：调用 OnEventTemplateCardEvent 注册的回调。
func (c *WSClient) EmitEventTemplateCardEvent(frame *WsFrame) {
	if h := c.eventTemplateCardEventHandler(); h != nil {
		h(frame)
	}
}

// EmitEventFeedbackEvent 实现 FrameEmitter：调用 OnEventFeedbackEvent 注册的回调。
func (c *WSClient) EmitEventFeedbackEvent(frame *WsFrame) {
	if h := c.eventFeedbackEventHandler(); h != nil {
		h(frame)
	}
}

// EmitEventDisconnected 实现 FrameEmitter：调用 OnEventDisconnected 注册的回调。
func (c *WSClient) EmitEventDisconnected(frame *WsFrame) {
	if h := c.eventDisconnectedHandler(); h != nil {
		h(frame)
	}
}

// ============================================================================
// 消息回复方法
// ============================================================================

// Reply 通过 WebSocket 通道发送回复消息（通用方法）
//
// frame 必须是收到的回调帧（不能为 nil），回复使用其 req_id；body 序列化为 JSON 作为帧的 body；cmd 为空时使用 WsCmd.RESPONSE。同一 req_id 的回复按调用顺序串行发送，Reply 阻塞直到服务端回执、5 秒回执超时、连接断开或调用 Disconnect，排队等待的时间不计入超时。成功时返回回执帧。
//
// 以下情况返回错误：req_id 为空、body 无法序列化、连接未建立、该 req_id 排队数超过
// MaxReplyQueueSize、回执超时、回执 errcode 非 0（错误信息包含 errcode），以及连接断开导致回复被取消。不要在消息回调中同步调用，见 MessageHandlerFunc。
func (c *WSClient) Reply(frame *WsFrame, body interface{}, cmd string) (*WsFrame, error) {
	reqID := frame.Headers.ReqID
	if reqID == "" {
		return nil, fmt.Errorf("req_id is empty")
	}

	return c.wsManager.SendReply(reqID, body, cmd)
}

// HasPendingReplyAck 检查指定消息帧是否有未完成的 ack
//
// 只反映已发出、正在等待回执的回复，不包括仍在排队的回复。frame 不能为 nil。
//
// 用于流式场景：调用方可据此决定是否跳过当前中间帧，避免排队积压。
func (c *WSClient) HasPendingReplyAck(frame *WsFrame) bool {
	reqID := frame.Headers.ReqID
	return c.wsManager.hasPendingAck(reqID)
}

// ReplyStreamNonBlocking 非阻塞流式文本回复
//
// 如果上一条同 reqId 的消息尚未收到 ack，则跳过本次发送（返回 ErrReplySkipped），
// 避免流式中间帧排队积压导致延迟。
//
// 注意：finish=true 的最终帧不受此限制，始终保证发送（走正常队列）。
func (c *WSClient) ReplyStreamNonBlocking(frame *WsFrame, streamID, content string, finish bool, msgItem []ReplyMsgItem, feedback *ReplyFeedback) (*WsFrame, error) {
	if !finish && c.HasPendingReplyAck(frame) {
		return nil, ErrReplySkipped
	}
	return c.ReplyStream(frame, streamID, content, finish, msgItem, feedback)
}

// ReplyStream 发送流式文本回复（便捷方法）
//
// 同一条流式消息的每次调用都使用相同的 streamID，content 为截至当前的完整内容
// （支持 Markdown），最后一次传 finish=true 结束。msgItem 与 feedback 可为 nil。
// 阻塞与错误语义同 Reply。
func (c *WSClient) ReplyStream(frame *WsFrame, streamID, content string, finish bool, msgItem []ReplyMsgItem, feedback *ReplyFeedback) (*WsFrame, error) {
	stream := StreamReplyBody{
		Stream: struct {
			ID       string         `json:"id"`
			Finish   bool           `json:"finish,omitempty"`
			Content  string         `json:"content,omitempty"`
			MsgItem  []ReplyMsgItem `json:"msg_item,omitempty"`
			Feedback *ReplyFeedback `json:"feedback,omitempty"`
		}{
			ID:       streamID,
			Finish:   finish,
			Content:  content,
			MsgItem:  msgItem,
			Feedback: feedback,
		},
	}

	body := map[string]interface{}{
		"msgtype": "stream",
		"stream":  stream.Stream,
	}

	return c.Reply(frame, body, "")
}

// ReplyWelcome 发送欢迎语回复
// body 可用 CreateWelcomeReplyBody、WelcomeTextReplyBody 或 WelcomeTemplateCardReplyBody 构造。
// 注意：此方法需要使用对应事件（如 enter_chat）的 req_id 才能调用
// 收到事件回调后需在 5 秒内发送回复
func (c *WSClient) ReplyWelcome(frame *WsFrame, body interface{}) (*WsFrame, error) {
	return c.Reply(frame, body, WsCmd.RESPONSE_WELCOME)
}

// ReplyTemplateCard 回复模板卡片消息
// feedback 非 nil 时覆盖 templateCard.Feedback。阻塞与错误语义同 Reply。
func (c *WSClient) ReplyTemplateCard(frame *WsFrame, templateCard TemplateCard, feedback *ReplyFeedback) (*WsFrame, error) {
	card := templateCard
	if feedback != nil {
		card.Feedback = feedback
	}

	body := map[string]interface{}{
		"msgtype":       "template_card",
		"template_card": card,
	}

	return c.Reply(frame, body, "")
}

// ReplyStreamWithCard 发送流式消息 + 模板卡片组合回复
// options 中各字段均可为零值；TemplateCard 为 nil 时只发送流式部分，CardFeedback
// 非 nil 时覆盖卡片的 Feedback。阻塞与错误语义同 Reply。
func (c *WSClient) ReplyStreamWithCard(
	frame *WsFrame,
	streamID, content string,
	finish bool,
	options struct {
		MsgItem        []ReplyMsgItem
		StreamFeedback *ReplyFeedback
		TemplateCard   *TemplateCard
		CardFeedback   *ReplyFeedback
	},
) (*WsFrame, error) {
	stream := struct {
		ID       string         `json:"id"`
		Finish   bool           `json:"finish,omitempty"`
		Content  string         `json:"content,omitempty"`
		MsgItem  []ReplyMsgItem `json:"msg_item,omitempty"`
		Feedback *ReplyFeedback `json:"feedback,omitempty"`
	}{
		ID:       streamID,
		Finish:   finish,
		Content:  content,
		MsgItem:  options.MsgItem,
		Feedback: options.StreamFeedback,
	}

	body := map[string]interface{}{
		"msgtype": "stream_with_template_card",
		"stream":  stream,
	}

	if options.TemplateCard != nil {
		card := *options.TemplateCard
		if options.CardFeedback != nil {
			card.Feedback = options.CardFeedback
		}
		body["template_card"] = card
	}

	return c.Reply(frame, body, "")
}

// UpdateTemplateCard 更新模板卡片
// userIDs 为空时不携带 userids 字段。阻塞与错误语义同 Reply。
// 注意：此方法需要使用对应事件（template_card_event）的 req_id 才能调用
// 收到事件回调后需在 5 秒内发送回复
func (c *WSClient) UpdateTemplateCard(frame *WsFrame, templateCard TemplateCard, userIDs []string) (*WsFrame, error) {
	body := map[string]interface{}{
		"response_type": "update_template_card",
		"template_card": templateCard,
	}

	if len(userIDs) > 0 {
		body["userids"] = userIDs
	}

	return c.Reply(frame, body, WsCmd.RESPONSE_UPDATE)
}

// ============================================================================
// 主动发送消息
// ============================================================================

// SendMessage 主动发送消息
// 向指定会话（单聊或群聊）主动推送消息，无需依赖收到的回调帧
//
// body 必须能序列化为 JSON 对象（map、struct 或其指针），否则返回错误且不发送；chatID 会写入 body 的 chatid 字段。每次调用使用新的 req_id，阻塞直到回执、5 秒回执超时或连接断开，错误语义同 Reply。
func (c *WSClient) SendMessage(chatID string, body interface{}) (*WsFrame, error) {
	reqID := GenerateReqId(WsCmd.SEND_MSG)

	// 将 body 合入信封并附加 chatid。
	// 先序列化 body 为 JSON 对象再追加字段，避免对 nil/非对象输入 panic。
	bodyMap, err := objectMap(body)
	if err != nil {
		return nil, err
	}

	bodyMap["chatid"] = chatID

	return c.wsManager.SendReply(reqID, bodyMap, WsCmd.SEND_MSG)
}

// objectMap 将输入转换为 map[string]interface{}。
// 输入必须是可序列化为 JSON 对象的类型（map、struct、指向它们的指针），
// 否则返回错误；nil、null、数组、标量等非法输入不会 panic。
func objectMap(v interface{}) (map[string]interface{}, error) {
	if v == nil {
		return nil, errors.New("SendMessage: body must not be nil")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("SendMessage: failed to serialize body: %w", err)
	}
	m := make(map[string]interface{})
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("SendMessage: body must be a JSON object (map or struct), got %T", v)
	}
	if m == nil { // JSON null 会成功反序列化为 nil map
		return nil, fmt.Errorf("SendMessage: body must be a JSON object (map or struct), got %T", v)
	}
	return m, nil
}

// SendMarkdown 发送 Markdown 消息
// 是 SendMessage 的便捷封装，阻塞与错误语义相同。
func (c *WSClient) SendMarkdown(chatID, content string) (*WsFrame, error) {
	body := SendMarkdownMsgBody{
		ChatID: chatID,
		Markdown: struct {
			Content string `json:"content"`
		}{
			Content: content,
		},
	}
	body.MsgType = "markdown"

	return c.SendMessage(chatID, body)
}

// SendTemplateCard 发送模板卡片消息
// 是 SendMessage 的便捷封装，阻塞与错误语义相同。
func (c *WSClient) SendTemplateCard(chatID string, templateCard TemplateCard) (*WsFrame, error) {
	body := SendTemplateCardMsgBody{
		ChatID:       chatID,
		TemplateCard: templateCard,
	}
	body.MsgType = "template_card"

	return c.SendMessage(chatID, body)
}

// ============================================================================
// 媒体上传与发送
// ============================================================================

// UploadMedia 上传临时素材（三步分片上传）
//
// 通过 WebSocket 长连接执行分片上传：init → chunk × N → finish
// 单个分片不超过 512KB（Base64 编码前），最多 100 个分片。
//
// fileBuffer 超过 100 个分片时直接返回错误。各步骤串行执行、逐条等待回执，耗时随分片数增长；任一步失败即返回包装后的错误（%w），已上传的分片不会被清理。成功时返回的 MediaID 可传给 ReplyMedia 或 SendMediaMessage。需要已建立的连接，不要在消息回调中同步调用。
func (c *WSClient) UploadMedia(fileBuffer []byte, options UploadMediaOptions) (*UploadMediaFinishResult, error) {
	totalSize := len(fileBuffer)

	// 分片大小：512KB（Base64 编码前）
	const chunkSize = 512 * 1024
	totalChunks := (totalSize + chunkSize - 1) / chunkSize

	if totalChunks > 100 {
		return nil, fmt.Errorf("file too large: %d chunks exceeds maximum of 100 chunks (max ~50MB)", totalChunks)
	}

	// 计算文件 MD5
	md5Hash := md5Sum(fileBuffer)

	c.logger.Info("Uploading media: type=%s, filename=%s, size=%d, chunks=%d", options.Type, options.Filename, totalSize, totalChunks)

	// Step 1: 初始化上传
	initReqID := GenerateReqId(WsCmd.UPLOAD_MEDIA_INIT)
	initResult, err := c.wsManager.SendReply(initReqID, UploadMediaInitBody{
		Type:        options.Type,
		Filename:    options.Filename,
		TotalSize:   totalSize,
		TotalChunks: totalChunks,
		MD5:         md5Hash,
	}, WsCmd.UPLOAD_MEDIA_INIT)
	if err != nil {
		return nil, fmt.Errorf("upload init failed: %w", err)
	}

	var initResp UploadMediaInitResult
	if err := json.Unmarshal(initResult.Body, &initResp); err != nil {
		return nil, fmt.Errorf("upload init response parse failed: %w", err)
	}
	if initResp.UploadID == "" {
		return nil, fmt.Errorf("upload init failed: no upload_id returned")
	}

	c.logger.Info("Upload init success: upload_id=%s", initResp.UploadID)

	// Step 2: 分片上传（串行，避免并发问题）
	for i := range totalChunks {
		start := i * chunkSize
		end := start + chunkSize
		if end > totalSize {
			end = totalSize
		}
		chunk := fileBuffer[start:end]
		base64Data := base64Encode(chunk)

		chunkReqID := GenerateReqId(WsCmd.UPLOAD_MEDIA_CHUNK)
		_, err := c.wsManager.SendReply(chunkReqID, UploadMediaChunkBody{
			UploadID:   initResp.UploadID,
			ChunkIndex: i,
			Base64Data: base64Data,
		}, WsCmd.UPLOAD_MEDIA_CHUNK)
		if err != nil {
			return nil, fmt.Errorf("chunk %d upload failed: %w", i, err)
		}

		c.logger.Debug("Uploaded chunk %d/%d (%d bytes)", i+1, totalChunks, len(chunk))
	}

	c.logger.Info("All %d chunks uploaded, finishing...", totalChunks)

	// Step 3: 完成上传
	finishReqID := GenerateReqId(WsCmd.UPLOAD_MEDIA_FINISH)
	finishResult, err := c.wsManager.SendReply(finishReqID, UploadMediaFinishBody(initResp), WsCmd.UPLOAD_MEDIA_FINISH)
	if err != nil {
		return nil, fmt.Errorf("upload finish failed: %w", err)
	}

	var finishResp UploadMediaFinishResult
	if err := json.Unmarshal(finishResult.Body, &finishResp); err != nil {
		return nil, fmt.Errorf("upload finish response parse failed: %w", err)
	}
	if finishResp.MediaID == "" {
		return nil, fmt.Errorf("upload finish failed: no media_id returned")
	}

	c.logger.Info("Upload complete: media_id=%s, type=%s", finishResp.MediaID, finishResp.Type)

	return &finishResp, nil
}

// ReplyMedia 被动回复媒体消息（便捷方法）
//
// 通过 aibot_respond_msg 被动回复通道发送媒体消息（file/image/voice/video）
//
// mediaID 来自 UploadMedia；videoOptions 仅在 mediaType 为 WeComMediaTypeVideo 时使用，可为 nil。阻塞与错误语义同 Reply。
func (c *WSClient) ReplyMedia(frame *WsFrame, mediaType WeComMediaType, mediaID string, videoOptions *VideoMediaContent) (*WsFrame, error) {
	body := buildMediaMsgBody(mediaType, mediaID, videoOptions)
	return c.Reply(frame, body, "")
}

// SendMediaMessage 主动发送媒体消息（便捷方法）
//
// 通过 aibot_send_msg 主动推送通道发送媒体消息
//
// 参数含义同 ReplyMedia，阻塞与错误语义同 SendMessage。
func (c *WSClient) SendMediaMessage(chatID string, mediaType WeComMediaType, mediaID string, videoOptions *VideoMediaContent) (*WsFrame, error) {
	body := buildMediaMsgBody(mediaType, mediaID, videoOptions)
	return c.SendMessage(chatID, body)
}

// buildMediaMsgBody 构建媒体消息体
func buildMediaMsgBody(mediaType WeComMediaType, mediaID string, videoOptions *VideoMediaContent) SendMediaMsgBody {
	body := SendMediaMsgBody{
		MsgType: mediaType,
	}
	switch mediaType {
	case WeComMediaTypeFile:
		body.File = &MediaContent{MediaID: mediaID}
	case WeComMediaTypeImage:
		body.Image = &MediaContent{MediaID: mediaID}
	case WeComMediaTypeVoice:
		body.Voice = &MediaContent{MediaID: mediaID}
	case WeComMediaTypeVideo:
		vc := &VideoMediaContent{MediaID: mediaID}
		if videoOptions != nil {
			vc.Title = videoOptions.Title
			vc.Description = videoOptions.Description
		}
		body.Video = vc
	}
	return body
}

// ============================================================================
// 文件操作
// ============================================================================

// DownloadFile 下载文件并使用 AES 密钥解密
//
// fileURL 与 aesKey 来自图片、文件、视频等消息体（如 ImageContent.URL 与
// ImageContent.AesKey）。通过 HTTP GET 下载，超时为 WSClientOptions.RequestTimeout，不使用 WebSocket 连接。aesKey 为空时返回未解密的原始数据。返回值依次为文件内容、从 Content-Disposition 解析出的文件名（可能为空）与错误；非 200 响应、下载失败或解密失败时返回错误。
func (c *WSClient) DownloadFile(fileURL, aesKey string) ([]byte, string, error) {
	c.logger.Info("Downloading and decrypting file...")

	// 下载加密的文件数据
	result, err := c.apiClient.DownloadFileRaw(fileURL)
	if err != nil {
		c.logger.Error("File download failed: %s", err.Error())
		return nil, "", err
	}

	c.logger.Debug("Downloaded %d bytes", len(result.Buffer))

	// 如果没有提供 aesKey，直接返回原始数据
	if aesKey == "" {
		c.logger.Warn("No aesKey provided, returning raw file data")
		return result.Buffer, result.Filename, nil
	}

	// 使用 AES-256-CBC 解密
	decrypted, err := DecryptFile(result.Buffer, aesKey)
	if err != nil {
		c.logger.Error("File decryption failed: %s", err.Error())
		return nil, "", err
	}

	c.logger.Info("File downloaded and decrypted successfully")
	return decrypted, result.Filename, nil
}

// ============================================================================
// 工具方法
// ============================================================================

// GetAPI 获取 API 客户端实例（供高级用途使用，如文件下载）
// 返回的实例由 WSClient 持有，可并发使用。
func (c *WSClient) GetAPI() *WeComApiClient {
	return c.apiClient
}

// ============================================================================
// 便捷函数
// ============================================================================

// GetMsgID 从 frame 中提取 msgid
// frame 为 nil、body 为空、无法解析或缺少该字段时返回空字符串。
func GetMsgID(frame *WsFrame) string {
	if frame == nil || frame.Body == nil {
		return ""
	}

	var bodyMap map[string]interface{}
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		return ""
	}

	if msgid, ok := bodyMap["msgid"].(string); ok {
		return msgid
	}
	return ""
}

// GetReqID 从 frame 中提取 req_id，frame 为 nil 时返回空字符串。
func GetReqID(frame *WsFrame) string {
	if frame == nil {
		return ""
	}
	return frame.Headers.ReqID
}

// GetMsgType 从 frame 中提取 msgtype（如 "text"、"image"、"event"）
// frame 为 nil、body 为空、无法解析或缺少该字段时返回空字符串。
func GetMsgType(frame *WsFrame) string {
	if frame == nil || frame.Body == nil {
		return ""
	}

	var bodyMap map[string]interface{}
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		return ""
	}

	if msgtype, ok := bodyMap["msgtype"].(string); ok {
		return msgtype
	}
	return ""
}

// GetEventType 从 frame 中提取 event.eventtype（如 "enter_chat"）
// 非事件帧或无法解析时返回空字符串。
func GetEventType(frame *WsFrame) string {
	if frame == nil || frame.Body == nil {
		return ""
	}

	var bodyMap map[string]interface{}
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		return ""
	}

	eventRaw, ok := bodyMap["event"]
	if !ok {
		return ""
	}

	eventMap, ok := eventRaw.(map[string]interface{})
	if !ok {
		return ""
	}

	if eventType, ok := eventMap["eventtype"].(string); ok {
		return eventType
	}
	return ""
}

// ParseMessageBody 解析消息体为指定类型
//
// v 为指向目标结构的指针，如 *TextMessage、*ImageMessage 或 *EventMessage。
// frame 或 body 为 nil 时返回错误，JSON 解码错误原样返回。
func ParseMessageBody(frame *WsFrame, v interface{}) error {
	if frame == nil || frame.Body == nil {
		return fmt.Errorf("frame or body is nil")
	}
	return json.Unmarshal(frame.Body, v)
}

// ============================================================================
// 包级别便捷函数
// ============================================================================

// CreateTextReplyBody 创建文本回复消息体（msgtype 为 text），可用于 Reply、ReplyWelcome 或 SendMessage。
func CreateTextReplyBody(content string) map[string]interface{} {
	return map[string]interface{}{
		"msgtype": "text",
		"text": map[string]interface{}{
			"content": content,
		},
	}
}

// CreateMarkdownReplyBody 创建 Markdown 回复消息体（msgtype 为 markdown），可用于 Reply 或 SendMessage。
func CreateMarkdownReplyBody(content string) map[string]interface{} {
	return map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]interface{}{
			"content": content,
		},
	}
}

// CreateWelcomeReplyBody 创建欢迎语回复消息体（文本类型），用于 ReplyWelcome。
func CreateWelcomeReplyBody(content string) map[string]interface{} {
	return map[string]interface{}{
		"msgtype": "text",
		"text": map[string]interface{}{
			"content": content,
		},
	}
}

// CreateStreamReplyBody 创建流式回复消息体，可传给 Reply；效果与 ReplyStream 相同。
// msgItem 为空、feedback 为 nil 时省略对应字段。
func CreateStreamReplyBody(streamID, content string, finish bool, msgItem []ReplyMsgItem, feedback *ReplyFeedback) map[string]interface{} {
	stream := map[string]interface{}{
		"id":      streamID,
		"finish":  finish,
		"content": content,
	}

	if len(msgItem) > 0 {
		stream["msg_item"] = msgItem
	}

	if feedback != nil {
		stream["feedback"] = feedback
	}

	return map[string]interface{}{
		"msgtype": "stream",
		"stream":  stream,
	}
}
