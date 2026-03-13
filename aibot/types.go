package aibot

import "encoding/json"

// ============================================================================
// 常量定义
// ============================================================================

// WsCmd WebSocket 命令类型常量
var WsCmd = struct {
	// 开发者 → 企业微信
	SUBSCRIBE        string // 认证订阅
	HEARTBEAT        string // 心跳
	RESPONSE         string // 回复消息
	RESPONSE_WELCOME string // 回复欢迎语
	RESPONSE_UPDATE  string // 更新模板卡片
	SEND_MSG         string // 主动发送消息

	// 企业微信 → 开发者
	CALLBACK       string // 消息推送回调
	EVENT_CALLBACK string // 事件推送回调
}{
	SUBSCRIBE:        "aibot_subscribe",
	HEARTBEAT:        "ping",
	RESPONSE:         "aibot_respond_msg",
	RESPONSE_WELCOME: "aibot_respond_welcome_msg",
	RESPONSE_UPDATE:  "aibot_respond_update_msg",
	SEND_MSG:         "aibot_send_msg",
	CALLBACK:         "aibot_msg_callback",
	EVENT_CALLBACK:   "aibot_event_callback",
}

// MessageType 消息类型枚举
type MessageType string

const (
	MessageTypeText  MessageType = "text"
	MessageTypeImage MessageType = "image"
	MessageTypeMixed MessageType = "mixed"
	MessageTypeVoice MessageType = "voice"
	MessageTypeFile  MessageType = "file"
)

// TemplateCardType 卡片类型枚举
type TemplateCardType string

const (
	TemplateCardTypeTextNotice          TemplateCardType = "text_notice"
	TemplateCardTypeNewsNotice          TemplateCardType = "news_notice"
	TemplateCardTypeButtonInteraction   TemplateCardType = "button_interaction"
	TemplateCardTypeVoteInteraction     TemplateCardType = "vote_interaction"
	TemplateCardTypeMultipleInteraction TemplateCardType = "multiple_interaction"
)

// EventType 事件类型枚举
type EventType string

const (
	EventTypeEnterChat         EventType = "enter_chat"
	EventTypeTemplateCardEvent EventType = "template_card_event"
	EventTypeFeedbackEvent     EventType = "feedback_event"
)

// ============================================================================
// 通用类型
// ============================================================================

// WsFrame WebSocket 帧结构
type WsFrame struct {
	Cmd     string          `json:"cmd,omitempty"`     // 命令类型
	Headers WsFrameHeaders  `json:"headers"`           // 请求头信息
	Body    json.RawMessage `json:"body,omitempty"`    // 消息体
	ErrCode int             `json:"errcode,omitempty"` // 响应错误码
	ErrMsg  string          `json:"errmsg,omitempty"`  // 响应错误信息
}

// WsFrameHeaders 仅包含 headers 的 WsFrame 子集
type WsFrameHeaders struct {
	ReqID string `json:"req_id"`
}

// ============================================================================
// 消息相关类型
// ============================================================================

// MessageFrom 消息发送者信息
type MessageFrom struct {
	UserID string `json:"userid"`
}

// TextContent 文本结构体
type TextContent struct {
	Content string `json:"content"`
}

// ImageContent 图片结构体
type ImageContent struct {
	URL    string `json:"url"`    // 图片的下载 url
	AesKey string `json:"aeskey"` // 解密密钥
}

// VoiceContent 语音结构体
type VoiceContent struct {
	Content string `json:"content"` // 语音转换成文本的内容
}

// FileContent 文件结构体
type FileContent struct {
	URL    string `json:"url"`    // 文件的下载 url
	AesKey string `json:"aeskey"` // 解密密钥
}

// MixedMsgItem 图文混排子项
type MixedMsgItem struct {
	MsgType string        `json:"msgtype"` // text / image
	Text    *TextContent  `json:"text,omitempty"`
	Image   *ImageContent `json:"image,omitempty"`
}

// MixedContent 图文混排结构体
type MixedContent struct {
	MsgItem []MixedMsgItem `json:"msg_item"`
}

// QuoteContent 引用结构体
type QuoteContent struct {
	MsgType string        `json:"msgtype"` // text / image / mixed / voice / file
	Text    *TextContent  `json:"text,omitempty"`
	Image   *ImageContent `json:"image,omitempty"`
	Mixed   *MixedContent `json:"mixed,omitempty"`
	Voice   *VoiceContent `json:"voice,omitempty"`
	File    *FileContent  `json:"file,omitempty"`
}

// BaseMessage 基础消息结构
type BaseMessage struct {
	MsgID       string        `json:"msgid"`
	AibotID     string        `json:"aibotid"`
	ChatID      string        `json:"chatid,omitempty"`
	ChatType    string        `json:"chattype"` // single / group
	From        MessageFrom   `json:"from"`
	CreateTime  int64         `json:"create_time,omitempty"`
	ResponseURL string        `json:"response_url,omitempty"`
	MsgType     string        `json:"msgtype"`
	Quote       *QuoteContent `json:"quote,omitempty"`
}

// TextMessage 文本消息
type TextMessage struct {
	BaseMessage
	Text TextContent `json:"text"`
}

// ImageMessage 图片消息
type ImageMessage struct {
	BaseMessage
	Image ImageContent `json:"image"`
}

// MixedMessage 图文混排消息
type MixedMessage struct {
	BaseMessage
	Mixed MixedContent `json:"mixed"`
}

// VoiceMessage 语音消息
type VoiceMessage struct {
	BaseMessage
	Voice VoiceContent `json:"voice"`
}

// FileMessage 文件消息
type FileMessage struct {
	BaseMessage
	File FileContent `json:"file"`
}

// ReplyOptions 回复消息选项
type ReplyOptions struct {
	MsgID  string `json:"msgid"`
	ChatID string `json:"chatid"`
}

// SendTextParams 发送文本消息参数
type SendTextParams struct {
	ReplyOptions
	Content string `json:"content"`
}

// SendMarkdownParams 发送 Markdown 消息参数
type SendMarkdownParams struct {
	ReplyOptions
	Content string `json:"content"`
}

// ============================================================================
// 事件相关类型
// ============================================================================

// EventFrom 事件发送者信息
type EventFrom struct {
	UserID string `json:"userid"`
	CorpID string `json:"corpid,omitempty"`
}

// EnterChatEvent 进入会话事件
type EnterChatEvent struct {
	EventType EventType `json:"eventtype"`
}

// TemplateCardEventData 模板卡片事件
type TemplateCardEventData struct {
	EventType EventType `json:"eventtype"`
	EventKey  string    `json:"event_key,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
}

// FeedbackEventData 用户反馈事件
type FeedbackEventData struct {
	EventType EventType `json:"eventtype"`
}

// EventMessage 事件回调消息结构
type EventMessage struct {
	MsgID      string          `json:"msgid"`
	CreateTime int64           `json:"create_time"`
	AibotID    string          `json:"aibotid"`
	ChatID     string          `json:"chatid,omitempty"`
	ChatType   string          `json:"chattype,omitempty"`
	From       EventFrom       `json:"from"`
	MsgType    string          `json:"msgtype"` // 固定为 event
	Event      json.RawMessage `json:"event"`
}

// ============================================================================
// 回复消息相关类型
// ============================================================================

// ReplyMsgItem 图文混排子项
type ReplyMsgItem struct {
	MsgType string `json:"msgtype"` // 固定为 image
	Image   struct {
		Base64 string `json:"base64"` // Base64 编码的图片
		MD5    string `json:"md5"`    // 图片的 MD5 值
	} `json:"image"`
}

// ReplyFeedback 反馈信息
type ReplyFeedback struct {
	ID string `json:"id"` // 反馈 ID
}

// StreamReplyBody 流式回复消息体
type StreamReplyBody struct {
	MsgType string `json:"msgtype"` // 固定为 stream
	Stream  struct {
		ID       string         `json:"id"`                 // 流式消息 ID
		Finish   bool           `json:"finish,omitempty"`   // 是否结束
		Content  string         `json:"content,omitempty"`  // 回复内容
		MsgItem  []ReplyMsgItem `json:"msg_item,omitempty"` // 图文混排
		Feedback *ReplyFeedback `json:"feedback,omitempty"` // 反馈信息
	} `json:"stream"`
}

// WelcomeTextReplyBody 欢迎语回复消息体（文本类型）
type WelcomeTextReplyBody struct {
	MsgType string `json:"msgtype"` // 固定为 text
	Text    struct {
		Content string `json:"content"` // 欢迎语文本内容
	} `json:"text"`
}

// WelcomeTemplateCardReplyBody 欢迎语回复消息体（模板卡片类型）
type WelcomeTemplateCardReplyBody struct {
	MsgType      string       `json:"msgtype"` // 固定为 template_card
	TemplateCard TemplateCard `json:"template_card"`
}

// TemplateCardReplyBody 模板卡片回复消息体
type TemplateCardReplyBody struct {
	MsgType      string       `json:"msgtype"` // 固定为 template_card
	TemplateCard TemplateCard `json:"template_card"`
}

// StreamWithTemplateCardReplyBody 流式消息 + 模板卡片组合回复消息体
type StreamWithTemplateCardReplyBody struct {
	MsgType string `json:"msgtype"` // 固定为 stream_with_template_card
	Stream  struct {
		ID       string         `json:"id"`
		Finish   bool           `json:"finish,omitempty"`
		Content  string         `json:"content,omitempty"`
		MsgItem  []ReplyMsgItem `json:"msg_item,omitempty"`
		Feedback *ReplyFeedback `json:"feedback,omitempty"`
	} `json:"stream"`
	TemplateCard *TemplateCard `json:"template_card,omitempty"`
}

// UpdateTemplateCardBody 更新模板卡片消息体
type UpdateTemplateCardBody struct {
	ResponseType string       `json:"response_type"` // 固定为 update_template_card
	UserIDs      []string     `json:"userids,omitempty"`
	TemplateCard TemplateCard `json:"template_card"`
}

// SendMarkdownMsgBody 主动发送 Markdown 消息体
type SendMarkdownMsgBody struct {
	MsgType  string `json:"msgtype"` // 固定为 markdown
	Markdown struct {
		Content string `json:"content"`
	} `json:"markdown"`
	ChatID string `json:"chatid"`
}

// SendTemplateCardMsgBody 主动发送模板卡片消息体
type SendTemplateCardMsgBody struct {
	MsgType      string       `json:"msgtype"` // 固定为 template_card
	TemplateCard TemplateCard `json:"template_card"`
	ChatID       string       `json:"chatid"`
}

// ============================================================================
// 模板卡片结构体
// ============================================================================

// TemplateCardSource 卡片来源样式信息
type TemplateCardSource struct {
	IconURL   string `json:"icon_url,omitempty"`
	Desc      string `json:"desc,omitempty"`
	DescColor int    `json:"desc_color,omitempty"` // 0-3
}

// TemplateCardActionMenu 卡片右上角更多操作按钮
type TemplateCardActionMenu struct {
	Desc       string `json:"desc"`
	ActionList []struct {
		Text string `json:"text"`
		Key  string `json:"key"`
	} `json:"action_list"`
}

// TemplateCardMainTitle 模板卡片主标题
type TemplateCardMainTitle struct {
	Title string `json:"title,omitempty"`
	Desc  string `json:"desc,omitempty"`
}

// TemplateCardEmphasisContent 关键数据样式
type TemplateCardEmphasisContent struct {
	Title string `json:"title,omitempty"`
	Desc  string `json:"desc,omitempty"`
}

// TemplateCardQuoteArea 引用文献样式
type TemplateCardQuoteArea struct {
	Type      int    `json:"type,omitempty"` // 0-2
	URL       string `json:"url,omitempty"`
	AppID     string `json:"appid,omitempty"`
	PagePath  string `json:"pagepath,omitempty"`
	Title     string `json:"title,omitempty"`
	QuoteText string `json:"quote_text,omitempty"`
}

// TemplateCardHorizontalContent 二级标题+文本列表
type TemplateCardHorizontalContent struct {
	Type    int    `json:"type,omitempty"` // 0, 1, 3
	KeyName string `json:"keyname"`
	Value   string `json:"value,omitempty"`
	URL     string `json:"url,omitempty"`
	UserID  string `json:"userid,omitempty"`
}

// TemplateCardJumpAction 跳转指引样式
type TemplateCardJumpAction struct {
	Type     int    `json:"type,omitempty"` // 0-3
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	AppID    string `json:"appid,omitempty"`
	PagePath string `json:"pagepath,omitempty"`
	Question string `json:"question,omitempty"`
}

// TemplateCardAction 整体卡片的点击跳转事件
type TemplateCardAction struct {
	Type     int    `json:"type"` // 0-2
	URL      string `json:"url,omitempty"`
	AppID    string `json:"appid,omitempty"`
	PagePath string `json:"pagepath,omitempty"`
}

// TemplateCardVerticalContent 卡片二级垂直内容
type TemplateCardVerticalContent struct {
	Title string `json:"title"`
	Desc  string `json:"desc,omitempty"`
}

// TemplateCardImage 图片样式
type TemplateCardImage struct {
	URL         string  `json:"url"`
	AspectRatio float64 `json:"aspect_ratio,omitempty"`
}

// TemplateCardImageTextArea 左图右文样式
type TemplateCardImageTextArea struct {
	Type     int    `json:"type,omitempty"` // 0-2
	URL      string `json:"url,omitempty"`
	AppID    string `json:"appid,omitempty"`
	PagePath string `json:"pagepath,omitempty"`
	Title    string `json:"title,omitempty"`
	Desc     string `json:"desc,omitempty"`
	ImageURL string `json:"image_url"`
}

// TemplateCardSubmitButton 提交按钮样式
type TemplateCardSubmitButton struct {
	Text string `json:"text"`
	Key  string `json:"key"`
}

// TemplateCardSelectionItem 下拉式选择器
type TemplateCardSelectionItem struct {
	QuestionKey string `json:"question_key"`
	Title       string `json:"title,omitempty"`
	Disable     bool   `json:"disable,omitempty"`
	SelectedID  string `json:"selected_id,omitempty"`
	OptionList  []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"option_list"`
}

// TemplateCardButton 模板卡片按钮
type TemplateCardButton struct {
	Text  string `json:"text"`
	Style int    `json:"style,omitempty"` // 1-4
	Key   string `json:"key"`
}

// TemplateCardCheckbox 选择题样式
type TemplateCardCheckbox struct {
	QuestionKey string `json:"question_key"`
	Disable     bool   `json:"disable,omitempty"`
	Mode        int    `json:"mode,omitempty"` // 0-1
	OptionList  []struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		IsChecked bool   `json:"is_checked,omitempty"`
	} `json:"option_list"`
}

// TemplateCard 模板卡片结构
type TemplateCard struct {
	CardType          string                          `json:"card_type"`
	Source            *TemplateCardSource             `json:"source,omitempty"`
	ActionMenu        *TemplateCardActionMenu         `json:"action_menu,omitempty"`
	MainTitle         *TemplateCardMainTitle          `json:"main_title,omitempty"`
	EmphasisContent   *TemplateCardEmphasisContent    `json:"emphasis_content,omitempty"`
	QuoteArea         *TemplateCardQuoteArea          `json:"quote_area,omitempty"`
	SubTitleText      string                          `json:"sub_title_text,omitempty"`
	HorizontalContent []TemplateCardHorizontalContent `json:"horizontal_content_list,omitempty"`
	JumpList          []TemplateCardJumpAction        `json:"jump_list,omitempty"`
	CardAction        *TemplateCardAction             `json:"card_action,omitempty"`
	CardImage         *TemplateCardImage              `json:"card_image,omitempty"`
	ImageTextArea     *TemplateCardImageTextArea      `json:"image_text_area,omitempty"`
	VerticalContent   []TemplateCardVerticalContent   `json:"vertical_content_list,omitempty"`
	ButtonSelection   *TemplateCardSelectionItem      `json:"button_selection,omitempty"`
	ButtonList        []TemplateCardButton            `json:"button_list,omitempty"`
	Checkbox          *TemplateCardCheckbox           `json:"checkbox,omitempty"`
	SelectList        []TemplateCardSelectionItem     `json:"select_list,omitempty"`
	SubmitButton      *TemplateCardSubmitButton       `json:"submit_button,omitempty"`
	TaskID            string                          `json:"task_id,omitempty"`
	Feedback          *ReplyFeedback                  `json:"feedback,omitempty"`
}

// ============================================================================
// 配置相关类型
// ============================================================================

// WSClientOptions WSClient 配置选项
type WSClientOptions struct {
	BotID                string // 机器人 ID
	Secret               string // 机器人 Secret
	ReconnectInterval    int    // 重连基础延迟（毫秒），默认 1000
	MaxReconnectAttempts int    // 最大重连次数，默认 10，-1 表示无限
	HeartbeatInterval    int    // 心跳间隔（毫秒），默认 30000
	RequestTimeout       int    // 请求超时（毫秒），默认 10000
	WSURL                string // 自定义 WebSocket 地址
	Logger               Logger // 自定义日志
}

// RequiredWSClientOptions 带默认值的配置
type RequiredWSClientOptions struct {
	ReconnectInterval    int
	MaxReconnectAttempts int
	HeartbeatInterval    int
	RequestTimeout       int
	WSURL                string
}

// DefaultWSClientOptions 默认配置值
var DefaultWSClientOptions = RequiredWSClientOptions{
	ReconnectInterval:    1000,
	MaxReconnectAttempts: 10,
	HeartbeatInterval:    30000,
	RequestTimeout:       10000,
	WSURL:                "wss://openws.work.weixin.qq.com",
}
