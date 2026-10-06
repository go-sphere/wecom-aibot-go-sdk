package aibot

import (
	"encoding/json"
)

// ============================================================================
// 事件处理器接口
// ============================================================================

// MessageHandler 消息处理器
// 负责解析 WebSocket 帧并分发为具体的消息事件和事件回调
// WSClient 内部使用；只有自行管理 WsConnectionManager 时才需要直接使用。无内部状态，可并发使用。
type MessageHandler struct {
	logger Logger
}

// NewMessageHandler 创建消息处理器，logger 不能为 nil。
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
//
// frame.Cmd 为 WsCmd.EVENT_CALLBACK 时按事件分发（先 EmitEvent，再按 eventtype 调用对应方法），否则按消息分发（先 EmitMessage，再按 msgtype 调用对应方法）。无效帧、缺少 msgtype 或 eventtype 的帧只记录日志。emitter 中的 panic 会被恢复并记录日志。
func (h *MessageHandler) HandleFrame(frame *WsFrame, emitter FrameEmitter) {
	defer func() {
		if r := recover(); r != nil {
			h.logger.Error("Panic in HandleFrame: %s", toString(r))
		}
	}()

	if frame == nil || frame.Body == nil {
		h.logger.Warn("Received invalid message format: frame is nil or body is empty")
		return
	}

	// 解析 body 获取 msgtype
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		h.logger.Warn("Failed to parse message body: %s", err.Error())
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
		h.logger.Error("Failed to parse message body: %s", err.Error())
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
	case string(MessageTypeVideo):
		emitter.EmitMessageVideo(frame)
	default:
		h.logger.Debug("Received unhandled message type: %s", msgtype)
	}
}

// handleEventCallback 处理事件推送回调
func (h *MessageHandler) handleEventCallback(frame *WsFrame, emitter FrameEmitter) {
	// 解析 body
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(frame.Body, &bodyMap); err != nil {
		h.logger.Error("Failed to parse event body: %s", err.Error())
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
			h.logger.Error("Failed to parse event JSON: %s", err.Error())
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
	case string(EventTypeDisconnected):
		emitter.EmitEventDisconnected(frame)
	default:
		h.logger.Debug("Received unhandled event type: %s", eventType)
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
// MessageHandler.HandleFrame 通过它分发帧；WSClient 实现了该接口，将帧转发给已注册的回调。实现应能处理在读协程上被同步调用。
type FrameEmitter interface {
	// 通用消息事件
	EmitMessage(frame *WsFrame)
	// 特定类型消息事件
	EmitMessageText(frame *WsFrame)
	EmitMessageImage(frame *WsFrame)
	EmitMessageMixed(frame *WsFrame)
	EmitMessageVoice(frame *WsFrame)
	EmitMessageFile(frame *WsFrame)
	EmitMessageVideo(frame *WsFrame)
	// 通用事件
	EmitEvent(frame *WsFrame)
	// 特定类型事件
	EmitEventEnterChat(frame *WsFrame)
	EmitEventTemplateCardEvent(frame *WsFrame)
	EmitEventFeedbackEvent(frame *WsFrame)
	EmitEventDisconnected(frame *WsFrame)
}

// ============================================================================
// 空实现
// ============================================================================

// NoOpEmitter 空事件发射器（用于不需要事件分发的场景）
// 所有方法都不做任何事，零值可用。
type NoOpEmitter struct{}

// EmitMessage 空操作。
func (e *NoOpEmitter) EmitMessage(frame *WsFrame) {}

// EmitMessageText 空操作。
func (e *NoOpEmitter) EmitMessageText(frame *WsFrame) {}

// EmitMessageImage 空操作。
func (e *NoOpEmitter) EmitMessageImage(frame *WsFrame) {}

// EmitMessageMixed 空操作。
func (e *NoOpEmitter) EmitMessageMixed(frame *WsFrame) {}

// EmitMessageVoice 空操作。
func (e *NoOpEmitter) EmitMessageVoice(frame *WsFrame) {}

// EmitMessageFile 空操作。
func (e *NoOpEmitter) EmitMessageFile(frame *WsFrame) {}

// EmitMessageVideo 空操作。
func (e *NoOpEmitter) EmitMessageVideo(frame *WsFrame) {}

// EmitEvent 空操作。
func (e *NoOpEmitter) EmitEvent(frame *WsFrame) {}

// EmitEventEnterChat 空操作。
func (e *NoOpEmitter) EmitEventEnterChat(frame *WsFrame) {}

// EmitEventTemplateCardEvent 空操作。
func (e *NoOpEmitter) EmitEventTemplateCardEvent(frame *WsFrame) {}

// EmitEventFeedbackEvent 空操作。
func (e *NoOpEmitter) EmitEventFeedbackEvent(frame *WsFrame) {}

// EmitEventDisconnected 空操作。
func (e *NoOpEmitter) EmitEventDisconnected(frame *WsFrame) {}
