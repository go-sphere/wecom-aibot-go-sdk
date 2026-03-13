package aibot

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// ============================================================================
// 事件回调类型定义
// ============================================================================

// MessageHandlerFunc 消息处理函数
type MessageHandlerFunc func(frame *WsFrame)

// EventHandlerFunc 事件处理函数
type EventHandlerFunc func(frame *WsFrame)

// ConnectionHandlerFunc 连接状态处理函数
type ConnectionHandlerFunc func()

// ReconnectHandlerFunc 重连处理函数
type ReconnectHandlerFunc func(attempt int)

// ErrorHandlerFunc 错误处理函数
type ErrorHandlerFunc func(err error)

// DisconnectHandlerFunc 断开连接处理函数
type DisconnectHandlerFunc func(reason string)

// ============================================================================
// WSClient 企业微信智能机器人客户端
// ============================================================================

// WSClient 企业微信智能机器人客户端
// 使用 WebSocket 长连接通道与企业微信通信
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

	onEvent                  EventHandlerFunc
	onEventEnterChat         EventHandlerFunc
	onEventTemplateCardEvent EventHandlerFunc
	onEventFeedbackEvent     EventHandlerFunc

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
func NewWSClient(options WSClientOptions) *WSClient {
	// 设置默认值
	opts := RequiredWSClientOptions{
		ReconnectInterval:    1000,
		MaxReconnectAttempts: 10,
		HeartbeatInterval:    30000,
		RequestTimeout:       10000,
		WSURL:                "wss://openws.work.weixin.qq.com",
	}

	if options.ReconnectInterval > 0 {
		opts.ReconnectInterval = options.ReconnectInterval
	}
	if options.MaxReconnectAttempts != 0 {
		opts.MaxReconnectAttempts = options.MaxReconnectAttempts
	}
	if options.HeartbeatInterval > 0 {
		opts.HeartbeatInterval = options.HeartbeatInterval
	}
	if options.RequestTimeout > 0 {
		opts.RequestTimeout = options.RequestTimeout
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
	)

	// 设置认证凭证
	client.wsManager.SetCredentials(options.BotID, options.Secret)

	// 绑定 WebSocket 事件
	client.setupWsEvents()

	return client
}

// setupWsEvents 设置 WebSocket 事件处理
func (c *WSClient) setupWsEvents() {
	c.wsManager.OnConnected = func() {
		if c.onConnected != nil {
			c.onConnected()
		}
	}

	c.wsManager.OnAuthenticated = func() {
		c.logger.Info("Authenticated")
		if c.onAuthenticated != nil {
			c.onAuthenticated()
		}
	}

	c.wsManager.OnDisconnected = func(reason string) {
		if c.onDisconnected != nil {
			c.onDisconnected(reason)
		}
	}

	c.wsManager.OnReconnecting = func(attempt int) {
		if c.onReconnecting != nil {
			c.onReconnecting(attempt)
		}
	}

	c.wsManager.OnError = func(err error) {
		if c.onError != nil {
			c.onError(err)
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
// SDK 使用内置默认地址建立连接，连接成功后自动发送认证帧（botId + secret）
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
func (c *WSClient) IsConnected() bool {
	return c.wsManager.IsConnected()
}

// ============================================================================
// 事件绑定
// ============================================================================

// OnMessage 收到消息（所有类型）
func (c *WSClient) OnMessage(handler MessageHandlerFunc) {
	c.onMessage = handler
}

// OnMessageText 收到文本消息
func (c *WSClient) OnMessageText(handler MessageHandlerFunc) {
	c.onMessageText = handler
}

// OnMessageImage 收到图片消息
func (c *WSClient) OnMessageImage(handler MessageHandlerFunc) {
	c.onMessageImage = handler
}

// OnMessageMixed 收到图文混排消息
func (c *WSClient) OnMessageMixed(handler MessageHandlerFunc) {
	c.onMessageMixed = handler
}

// OnMessageVoice 收到语音消息
func (c *WSClient) OnMessageVoice(handler MessageHandlerFunc) {
	c.onMessageVoice = handler
}

// OnMessageFile 收到文件消息
func (c *WSClient) OnMessageFile(handler MessageHandlerFunc) {
	c.onMessageFile = handler
}

// OnEvent 收到事件回调（所有事件类型）
func (c *WSClient) OnEvent(handler EventHandlerFunc) {
	c.onEvent = handler
}

// OnEventEnterChat 收到进入会话事件
func (c *WSClient) OnEventEnterChat(handler EventHandlerFunc) {
	c.onEventEnterChat = handler
}

// OnEventTemplateCardEvent 收到模板卡片事件
func (c *WSClient) OnEventTemplateCardEvent(handler EventHandlerFunc) {
	c.onEventTemplateCardEvent = handler
}

// OnEventFeedbackEvent 收到用户反馈事件
func (c *WSClient) OnEventFeedbackEvent(handler EventHandlerFunc) {
	c.onEventFeedbackEvent = handler
}

// OnConnected 连接建立
func (c *WSClient) OnConnected(handler ConnectionHandlerFunc) {
	c.onConnected = handler
}

// OnAuthenticated 认证成功
func (c *WSClient) OnAuthenticated(handler ConnectionHandlerFunc) {
	c.onAuthenticated = handler
}

// OnDisconnected 连接断开
func (c *WSClient) OnDisconnected(handler DisconnectHandlerFunc) {
	c.onDisconnected = handler
}

// OnReconnecting 重连中
func (c *WSClient) OnReconnecting(handler ReconnectHandlerFunc) {
	c.onReconnecting = handler
}

// OnError 发生错误
func (c *WSClient) OnError(handler ErrorHandlerFunc) {
	c.onError = handler
}

// ============================================================================
// FrameEmitter 实现
// ============================================================================

func (c *WSClient) EmitMessage(frame *WsFrame) {
	if c.onMessage != nil {
		c.onMessage(frame)
	}
}

func (c *WSClient) EmitMessageText(frame *WsFrame) {
	if c.onMessageText != nil {
		c.onMessageText(frame)
	}
}

func (c *WSClient) EmitMessageImage(frame *WsFrame) {
	if c.onMessageImage != nil {
		c.onMessageImage(frame)
	}
}

func (c *WSClient) EmitMessageMixed(frame *WsFrame) {
	if c.onMessageMixed != nil {
		c.onMessageMixed(frame)
	}
}

func (c *WSClient) EmitMessageVoice(frame *WsFrame) {
	if c.onMessageVoice != nil {
		c.onMessageVoice(frame)
	}
}

func (c *WSClient) EmitMessageFile(frame *WsFrame) {
	if c.onMessageFile != nil {
		c.onMessageFile(frame)
	}
}

func (c *WSClient) EmitEvent(frame *WsFrame) {
	if c.onEvent != nil {
		c.onEvent(frame)
	}
}

func (c *WSClient) EmitEventEnterChat(frame *WsFrame) {
	if c.onEventEnterChat != nil {
		c.onEventEnterChat(frame)
	}
}

func (c *WSClient) EmitEventTemplateCardEvent(frame *WsFrame) {
	if c.onEventTemplateCardEvent != nil {
		c.onEventTemplateCardEvent(frame)
	}
}

func (c *WSClient) EmitEventFeedbackEvent(frame *WsFrame) {
	if c.onEventFeedbackEvent != nil {
		c.onEventFeedbackEvent(frame)
	}
}

// ============================================================================
// 消息回复方法
// ============================================================================

// Reply 通过 WebSocket 通道发送回复消息（通用方法）
func (c *WSClient) Reply(frame *WsFrame, body interface{}, cmd string) (*WsFrame, error) {
	reqID := frame.Headers.ReqID
	if reqID == "" {
		return nil, fmt.Errorf("req_id is empty")
	}

	return c.wsManager.SendReply(reqID, body, cmd)
}

// ReplyStream 发送流式文本回复（便捷方法）
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
// 注意：此方法需要使用对应事件（如 enter_chat）的 req_id 才能调用
// 收到事件回调后需在 5 秒内发送回复
func (c *WSClient) ReplyWelcome(frame *WsFrame, body interface{}) (*WsFrame, error) {
	return c.Reply(frame, body, WsCmd.RESPONSE_WELCOME)
}

// ReplyTemplateCard 回复模板卡片消息
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
func (c *WSClient) SendMessage(chatID string, body interface{}) (*WsFrame, error) {
	reqID := GenerateReqId(WsCmd.SEND_MSG)

	var bodyMap map[string]interface{}
	bodyBytes, _ := json.Marshal(body)
	json.Unmarshal(bodyBytes, &bodyMap)

	bodyMap["chatid"] = chatID

	return c.wsManager.SendReply(reqID, bodyMap, WsCmd.SEND_MSG)
}

// SendMarkdown 发送 Markdown 消息
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
func (c *WSClient) SendTemplateCard(chatID string, templateCard TemplateCard) (*WsFrame, error) {
	body := SendTemplateCardMsgBody{
		ChatID:       chatID,
		TemplateCard: templateCard,
	}
	body.MsgType = "template_card"

	return c.SendMessage(chatID, body)
}

// ============================================================================
// 文件操作
// ============================================================================

// DownloadFile 下载文件并使用 AES 密钥解密
func (c *WSClient) DownloadFile(fileURL, aesKey string) ([]byte, string, error) {
	c.logger.Info("Downloading and decrypting file...")

	// 下载加密的文件数据
	result, err := c.apiClient.DownloadFileRaw(fileURL)
	if err != nil {
		c.logger.Error("File download failed: " + err.Error())
		return nil, "", err
	}

	c.logger.Debug(fmt.Sprintf("Downloaded %d bytes, aesKey: %s", len(result.Buffer), aesKey))

	// 如果没有提供 aesKey，直接返回原始数据
	if aesKey == "" {
		c.logger.Warn("No aesKey provided, returning raw file data")
		return result.Buffer, result.Filename, nil
	}

	// 使用 AES-256-CBC 解密
	decrypted, err := DecryptFile(result.Buffer, aesKey)
	if err != nil {
		c.logger.Error("File decryption failed: " + err.Error())
		return nil, "", err
	}

	c.logger.Info("File downloaded and decrypted successfully")
	return decrypted, result.Filename, nil
}

// ============================================================================
// 工具方法
// ============================================================================

// GetAPI 获取 API 客户端实例（供高级用途使用，如文件下载）
func (c *WSClient) GetAPI() *WeComApiClient {
	return c.apiClient
}

// ============================================================================
// 便捷函数
// ============================================================================

// GetMsgID 从 frame 中提取 msgid
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

// GetReqID 从 frame 中提取 req_id
func GetReqID(frame *WsFrame) string {
	if frame == nil {
		return ""
	}
	return frame.Headers.ReqID
}

// GetMsgType 从 frame 中提取 msgtype
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

// GetEventType 从 frame 中提取 eventtype
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
func ParseMessageBody(frame *WsFrame, v interface{}) error {
	if frame == nil || frame.Body == nil {
		return fmt.Errorf("frame or body is nil")
	}
	return json.Unmarshal(frame.Body, v)
}

// ============================================================================
// 包级别便捷函数
// ============================================================================

// CreateTextReplyBody 创建文本回复消息体
func CreateTextReplyBody(content string) map[string]interface{} {
	return map[string]interface{}{
		"msgtype": "text",
		"text": map[string]interface{}{
			"content": content,
		},
	}
}

// CreateMarkdownReplyBody 创建 Markdown 回复消息体
func CreateMarkdownReplyBody(content string) map[string]interface{} {
	return map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]interface{}{
			"content": content,
		},
	}
}

// CreateWelcomeReplyBody 创建欢迎语回复消息体
func CreateWelcomeReplyBody(content string) map[string]interface{} {
	return map[string]interface{}{
		"msgtype": "text",
		"text": map[string]interface{}{
			"content": content,
		},
	}
}

// CreateStreamReplyBody 创建流式回复消息体
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

// ============================================================================
// SDK 初始化（用于 init 函数）
// ============================================================================

// 确保 RequiredWSClientOptions 实现了默认值的接口
var _ = func() error {
	opts := WSClientOptions{
		BotID:  "test",
		Secret: "test",
	}
	_ = opts
	return nil
}()

// 为了兼容性，提供一个字符串数组的支持
func parseUserIDs(userIDs string) []string {
	if userIDs == "" {
		return nil
	}
	return strings.Split(userIDs, ",")
}
