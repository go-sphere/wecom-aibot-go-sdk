package aibot

import (
	"encoding/json"
)

// ============================================================================
// 事件处理器接口
// ============================================================================

// MessageHandler 消息处理器
// 负责解析 WebSocket 帧并分发为具体的消息事件和事件回调
type MessageHandler struct {
	logger Logger
}

// NewMessageHandler 创建消息处理器
func NewMessageHandler(logger Logger) *MessageHandler {
	return &MessageHandler{
		logger: logger,
	}
}

// HandleFrame 处理收到的 WebSocket 帧
//
//	WebSocket 推送帧结构：
//	- 消息推送：{ cmd: "aibot_msg_callback", headers: { req_id: "xxx" }, body: { msgid, msgtype, ... } }
//	- 事件推送：{ cmd: "aibot_event_callback", headers: { req_id: "xxx" }, body: { msgid, msgtype: "event", event: { ... } }
func (h *MessageHandler) HandleFrame(frame *WsFrame, emitter FrameEmitter) {
	defer func() {
		if r := recover(); r != nil {
			h.logger.Error("Panic in HandleFrame: " + toString(r))
		}
	}()

	if frame == nil || frame.Body == nil {
		h.logger.Warn("Received invalid message format: frame is nil or body is empty")
		return
	}

	// 解析 body 获取 msgtype
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		h.logger.Warn("Failed to parse message body: " + err.Error())
		return
	}

	msgtype, _ := bodyMap["msgtype"].(string)
	if msgtype == "" {
		h.logger.Warn("Received message without msgtype")
		return
	}

	// 事件推送回调处理
	if frame.Cmd == WsCmd.EVENT_CALLBACK {
		h.handleEventCallback(frame, emitter)
		return
	}

	// 消息推送回调处理
	h.handleMessageCallback(frame, emitter)
}

// handleMessageCallback 处理消息推送回调
func (h *MessageHandler) handleMessageCallback(frame *WsFrame, emitter FrameEmitter) {
	// 解析 body 获取具体消息类型
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		h.logger.Error("Failed to parse message body: " + err.Error())
		return
	}

	msgtype, _ := bodyMap["msgtype"].(string)

	// 触发通用消息事件
	emitter.EmitMessage(frame)

	// 根据消息类型触发特定事件
	switch msgtype {
	case string(MessageTypeText):
		emitter.EmitMessageText(frame)
	case string(MessageTypeImage):
		emitter.EmitMessageImage(frame)
	case string(MessageTypeMixed):
		emitter.EmitMessageMixed(frame)
	case string(MessageTypeVoice):
		emitter.EmitMessageVoice(frame)
	case string(MessageTypeFile):
		emitter.EmitMessageFile(frame)
	default:
		h.logger.Debug("Received unhandled message type: " + msgtype)
	}
}

// handleEventCallback 处理事件推送回调
func (h *MessageHandler) handleEventCallback(frame *WsFrame, emitter FrameEmitter) {
	// 解析 body
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		h.logger.Error("Failed to parse event body: " + err.Error())
		return
	}

	// 获取 event 字段
	eventRaw, ok := bodyMap["event"]
	if !ok {
		h.logger.Debug("Received event callback without event field")
		return
	}

	// event 可能是 map[string]interface{} 或 json.RawMessage
	var eventMap map[string]interface{}
	switch v := eventRaw.(type) {
	case map[string]interface{}:
		eventMap = v
	case string:
		if err := json.Unmarshal([]byte(v), &eventMap); err != nil {
			h.logger.Error("Failed to parse event JSON: " + err.Error())
			return
		}
	default:
		h.logger.Debug("Received event callback with invalid event format")
		return
	}

	// 获取事件类型
	eventTypeRaw, ok := eventMap["eventtype"]
	if !ok {
		h.logger.Debug("Received event callback without eventtype")
		return
	}

	eventType, _ := eventTypeRaw.(string)
	if eventType == "" {
		h.logger.Debug("Received event callback with empty eventtype")
		return
	}

	// 触发通用事件
	emitter.EmitEvent(frame)

	// 根据事件类型触发特定事件
	switch eventType {
	case string(EventTypeEnterChat):
		emitter.EmitEventEnterChat(frame)
	case string(EventTypeTemplateCardEvent):
		emitter.EmitEventTemplateCardEvent(frame)
	case string(EventTypeFeedbackEvent):
		emitter.EmitEventFeedbackEvent(frame)
	default:
		h.logger.Debug("Received unhandled event type: " + eventType)
	}
}

// toString 转换为字符串
func toString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case error:
		return val.Error()
	default:
		return ""
	}
}

// ============================================================================
// 事件发射器接口
// ============================================================================

// FrameEmitter 帧事件发射器接口
type FrameEmitter interface {
	// 通用消息事件
	EmitMessage(frame *WsFrame)
	// 特定类型消息事件
	EmitMessageText(frame *WsFrame)
	EmitMessageImage(frame *WsFrame)
	EmitMessageMixed(frame *WsFrame)
	EmitMessageVoice(frame *WsFrame)
	EmitMessageFile(frame *WsFrame)
	// 通用事件
	EmitEvent(frame *WsFrame)
	// 特定类型事件
	EmitEventEnterChat(frame *WsFrame)
	EmitEventTemplateCardEvent(frame *WsFrame)
	EmitEventFeedbackEvent(frame *WsFrame)
}

// ============================================================================
// 空实现
// ============================================================================

// NoOpEmitter 空事件发射器（用于不需要事件分发的场景）
type NoOpEmitter struct{}

func (e *NoOpEmitter) EmitMessage(frame *WsFrame)                {}
func (e *NoOpEmitter) EmitMessageText(frame *WsFrame)            {}
func (e *NoOpEmitter) EmitMessageImage(frame *WsFrame)           {}
func (e *NoOpEmitter) EmitMessageMixed(frame *WsFrame)           {}
func (e *NoOpEmitter) EmitMessageVoice(frame *WsFrame)           {}
func (e *NoOpEmitter) EmitMessageFile(frame *WsFrame)            {}
func (e *NoOpEmitter) EmitEvent(frame *WsFrame)                  {}
func (e *NoOpEmitter) EmitEventEnterChat(frame *WsFrame)         {}
func (e *NoOpEmitter) EmitEventTemplateCardEvent(frame *WsFrame) {}
func (e *NoOpEmitter) EmitEventFeedbackEvent(frame *WsFrame)     {}
