# wecom-aibot-go-sdk

企业微信智能机器人 Go SDK —— 基于 WebSocket 长连接通道，提供消息收发、流式回复、模板卡片、事件回调、文件下载解密等核心能力。

> **关于本项目**: 此项目是官方 [wecomteam/aibot-node-sdk](https://github.com/WecomTeam/aibot-node-sdk) 的 Go 语言复刻版本，会保持与官方 Node.js SDK 同步更新。

## 特性

- **WebSocket 长连接** — 基于 `wss://openws.work.weixin.qq.com` 内置默认地址，开箱即用
- **自动认证** — 连接建立后自动发送认证帧（botId + secret）
- **心跳保活** — 自动维护心跳，连续未收到 ack 时自动判定连接异常
- **断线重连** — 指数退避重连策略（1s → 2s → 4s → ... → 30s 上限），支持自定义最大重连次数
- **消息分发** — 自动解析消息类型并触发对应事件（text / image / mixed / voice / file）
- **流式回复** — 内置流式回复方法，支持 Markdown 和图文混排
- **模板卡片** — 支持回复模板卡片消息、流式+卡片组合回复、更新卡片
- **主动推送** — 支持向指定会话主动发送 Markdown 或模板卡片消息，无需依赖回调帧
- **事件回调** — 支持进入会话、模板卡片按钮点击、用户反馈等事件
- **串行回复队列** — 同一 req_id 的回复消息串行发送，自动等待回执
- **文件下载解密** — 内置 AES-256-CBC 文件解密，每个图片/文件消息自带独立的 aeskey
- **可插拔日志** — 支持自定义 Logger，内置带时间戳的 DefaultLogger

## 安装

```bash
go get github.com/go-sphere/wecom-aibot-go-sdk
```

## 快速开始

```go
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	aibot "github.com/go-sphere/wecom-aibot-go-sdk/aibot"
)

func main() {
	// 从环境变量获取配置
	botID := os.Getenv("WECOM_BOT_ID")
	botSecret := os.Getenv("WECOM_BOT_SECRET")

	if botID == "" || botSecret == "" {
		fmt.Println("请设置环境变量 WECOM_BOT_ID 和 WECOM_BOT_SECRET")
		return
	}

	// 创建客户端
	client := aibot.NewWSClient(aibot.WSClientOptions{
		BotID:  botID,
		Secret: botSecret,
	})

	// 连接客户端
	client.Connect()

	// 设置事件处理
	client.OnConnected(func() {
		fmt.Println("连接已建立")
	})

	client.OnAuthenticated(func() {
		fmt.Println("认证成功")
	})

	client.OnDisconnected(func(reason string) {
		fmt.Println("连接断开:", reason)
	})

	// 监听文本消息并进行流式回复
	client.OnMessageText(func(frame *aibot.WsFrame) {
		// 解析消息体
		var msg aibot.TextMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			fmt.Println("解析消息失败:", err.Error())
			return
		}

		fmt.Printf("收到文本: %s\n", msg.Text.Content)

		streamID := fmt.Sprintf("stream_%d", frame.Headers.ReqID)
		reply := "你好！你说的是: " + msg.Text.Content

		// 发送流式回复
		client.ReplyStream(frame, streamID, reply, true, nil, nil)
	})

	// 监听进入会话事件（发送欢迎语）
	client.OnEventEnterChat(func(frame *aibot.WsFrame) {
		welcomeBody := aibot.CreateTextReplyBody("您好！我是智能助手，有什么可以帮您的吗？")
		client.ReplyWelcome(frame, welcomeBody)
	})

	// 优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	client.Disconnect()
}
```

## 配置选项

`WSClientOptions` 完整配置：

| 参数 | 类型 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `BotID` | `string` | ✅ | — | 机器人 ID（企业微信后台获取） |
| `Secret` | `string` | ✅ | — | 机器人 Secret（企业微信后台获取） |
| `ReconnectInterval` | `int` | — | `1000` | 重连基础延迟（毫秒），实际延迟按指数退避递增（1s → 2s → 4s → ... → 30s 上限） |
| `MaxReconnectAttempts` | `int` | — | `10` | 最大重连次数（`-1` 表示无限重连） |
| `HeartbeatInterval` | `int` | — | `30000` | 心跳间隔（毫秒） |
| `RequestTimeout` | `int` | — | `10000` | HTTP 请求超时时间（毫秒） |
| `WSURL` | `string` | — | `wss://openws.work.weixin.qq.com` | 自定义 WebSocket 连接地址 |
| `WsDialer` | `*websocket.Dialer` | — | `nil` | 自定义 WebSocket Dialer（如 TLS 配置等） |
| `Logger` | `Logger` | — | `DefaultLogger` | 自定义日志实例 |

## API 文档

### WSClient

核心客户端类，提供连接管理、消息收发等功能。

#### 构造函数

```go
client := aibot.NewWSClient(options aibot.WSClientOptions)
```

#### 方法

| 方法 | 说明 | 返回值 |
| --- | --- | --- |
| `Connect()` | 建立 WebSocket 连接，连接后自动认证 | `*WSClient` |
| `Disconnect()` | 主动断开连接 | `void` |
| `IsConnected()` | 获取当前 WebSocket 连接状态 | `bool` |
| `Reply(frame, body, cmd)` | 通过 WebSocket 通道发送回复消息（通用方法） | `(*WsFrame, error)` |
| `ReplyStream(frame, streamID, content, finish, msgItem, feedback)` | 发送流式文本回复（便捷方法，支持 Markdown） | `(*WsFrame, error)` |
| `ReplyWelcome(frame, body)` | 发送欢迎语回复（支持文本或模板卡片），需在收到事件 5s 内调用 | `(*WsFrame, error)` |
| `ReplyTemplateCard(frame, templateCard, feedback)` | 回复模板卡片消息 | `(*WsFrame, error)` |
| `ReplyStreamWithCard(frame, streamID, content, finish, options)` | 发送流式消息 + 模板卡片组合回复 | `(*WsFrame, error)` |
| `ReplyStreamNonBlocking(frame, streamID, content, finish, msgItem, feedback)` | 非阻塞流式回复：若上一条同 reqId 未 ack 则跳过 | `(*WsFrame, error)` |
| `HasPendingReplyAck(frame)` | 检查指定帧是否有未完成的 ack | `bool` |
| `UpdateTemplateCard(frame, templateCard, userIDs)` | 更新模板卡片（响应 template_card_event），需在收到事件 5s 内调用 | `(*WsFrame, error)` |
| `SendMessage(chatID, body)` | 主动发送消息（支持 Markdown 或模板卡片），无需依赖回调帧 | `(*WsFrame, error)` |
| `SendMarkdown(chatID, content)` | 主动发送 Markdown 消息 | `(*WsFrame, error)` |
| `SendTemplateCard(chatID, templateCard)` | 主动发送模板卡片消息 | `(*WsFrame, error)` |
| `DownloadFile(fileURL, aesKey)` | 下载文件并使用 AES 密钥解密，返回文件内容和文件名 | `([]byte, string, error)` |

### ReplyStream 详细说明

```go
client.ReplyStream(
	frame *aibot.WsFrame,      // 收到的原始 WebSocket 帧
	streamID string,            // 流式消息 ID（使用 aibot.GenerateReqId("stream") 生成）
	content string,             // 回复内容（支持 Markdown）
	finish bool,                // 是否结束流式消息，默认 false
	msgItem []aibot.ReplyMsgItem, // 图文混排项（仅 finish=true 时有效）
	feedback *aibot.ReplyFeedback, // 反馈信息（仅首次回复时设置）
)
```

### ReplyWelcome 详细说明

发送欢迎语回复，需在收到 `enter_chat` 事件 5 秒内调用，超时将无法发送。

```go
// 文本欢迎语
welcomeBody := aibot.CreateTextReplyBody("欢迎！")
client.ReplyWelcome(frame, welcomeBody)

// 模板卡片欢迎语
// 使用 aibot.TemplateCard 构建模板卡片
```

### SendMessage 详细说明

主动向指定会话推送消息，无需依赖收到的回调帧。

```go
// 发送 Markdown 消息
client.SendMarkdown("userid_or_chatid", "这是一条**主动推送**的消息")

// 发送模板卡片消息
templateCard := aibot.TemplateCard{
	CardType: "text_notice",
	MainTitle: &aibot.TemplateCardMainTitle{
		Title: "通知",
	},
}
client.SendTemplateCard("userid_or_chatid", templateCard)
```

### DownloadFile 使用示例

```go
// aesKey 取自消息体中的 Image.AesKey 或 File.AesKey
client.OnMessageImage(func(frame *aibot.WsFrame) {
	var msg aibot.ImageMessage
	aibot.ParseMessageBody(frame, &msg)

	data, filename, err := client.DownloadFile(msg.Image.URL, msg.Image.AesKey)
	if err != nil {
		fmt.Println("下载失败:", err)
		return
	}
	fmt.Printf("文件名: %s, 大小: %d bytes\n", filename, len(data))
})
```

## 事件列表

所有事件均通过 `client.OnXxx(handler)` 设置：

| 方法 | 回调参数 | 说明 |
| --- | --- | --- |
| `OnConnected` | — | WebSocket 连接建立 |
| `OnAuthenticated` | — | 认证成功 |
| `OnDisconnected` | `reason: string` | 连接断开 |
| `OnReconnecting` | `attempt: int` | 正在重连（第 N 次） |
| `OnError` | `err: error` | 发生错误 |
| `OnMessage` | `frame: *WsFrame` | 收到消息（所有类型） |
| `OnMessageText` | `frame: *WsFrame` | 收到文本消息 |
| `OnMessageImage` | `frame: *WsFrame` | 收到图片消息 |
| `OnMessageMixed` | `frame: *WsFrame` | 收到图文混排消息 |
| `OnMessageVoice` | `frame: *WsFrame` | 收到语音消息 |
| `OnMessageFile` | `frame: *WsFrame` | 收到文件消息 |
| `OnMessageVideo` | `frame: *WsFrame` | 收到视频消息 |
| `OnEvent` | `frame: *WsFrame` | 收到事件回调（所有事件类型） |
| `OnEventEnterChat` | `frame: *WsFrame` | 收到进入会话事件（用户当天首次进入单聊会话） |
| `OnEventTemplateCardEvent` | `frame: *WsFrame` | 收到模板卡片事件（用户点击卡片按钮） |
| `OnEventFeedbackEvent` | `frame: *WsFrame` | 收到用户反馈事件 |
| `OnEventDisconnected` | `frame: *WsFrame` | 服务端因新连接建立而断开当前连接 |

消息和事件回调在当前连接生命周期的独立协程中按到达顺序执行，可直接调用 `Reply*` 等待回执。`OnAuthenticated` 仍在读协程上执行，不能在其中同步等待回执。

待分发回调使用可增长的内存队列，积压达到 256 条时记录警告，不因队列满而丢弃消息。耗时回调应自行启动协程处理，避免持续积压导致内存增长。`Disconnect` 或重试耗尽会清空未开始的回调，正在执行的回调会继续完成；下次 `Connect` 使用独立队列。

收到 `disconnected_event` 时立即关闭连接，按顺序处理已排队的回调、`OnEventDisconnected` 和 `OnDisconnected`，随后退出分发协程。

## 消息类型

SDK 支持以下消息类型（`MessageType` 常量）：

| 类型 | 值 | 说明 |
| --- | --- | --- |
| `MessageTypeText` | `"text"` | 文本消息 |
| `MessageTypeImage` | `"image"` | 图片消息（URL 已加密，使用消息中的 `Image.AesKey` 解密） |
| `MessageTypeMixed` | `"mixed"` | 图文混排消息（包含 text / image 子项） |
| `MessageTypeVoice` | `"voice"` | 语音消息（已转文本） |
| `MessageTypeFile` | `"file"` | 文件消息（URL 已加密，使用消息中的 `File.AesKey` 解密） |

SDK 支持以下事件类型（`EventType` 常量）：

| 类型 | 值 | 说明 |
| --- | --- | --- |
| `EventTypeEnterChat` | `"enter_chat"` | 进入会话事件：用户当天首次进入机器人单聊会话 |
| `EventTypeTemplateCardEvent` | `"template_card_event"` | 模板卡片事件：用户点击模板卡片按钮 |
| `EventTypeFeedbackEvent` | `"feedback_event"` | 用户反馈事件：用户对机器人回复进行反馈 |

### 消息帧结构（`WsFrame`）

```go
type WsFrame struct {
	Cmd     string          // 命令类型
	Headers WsFrameHeaders // 请求头信息
	Body    json.RawMessage // 消息体
	ErrCode int            // 响应错误码
	ErrMsg  string         // 响应错误信息
}

type WsFrameHeaders struct {
	ReqID string // 请求 ID（回复时需透传）
}
```

## 自定义日志

实现 `Logger` 接口即可自定义日志输出：

```go
type Logger interface {
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
}
```

使用示例：

```go
type CustomLogger struct{}

func (l *CustomLogger) Debug(format string, v ...interface{}) { /* ... */ }
func (l *CustomLogger) Info(format string, v ...interface{})  { /* ... */ }
func (l *CustomLogger) Warn(format string, v ...interface{})  { /* ... */ }
func (l *CustomLogger) Error(format string, v ...interface{}) { /* ... */ }

client := aibot.NewWSClient(aibot.WSClientOptions{
	BotID:  "your-bot-id",
	Secret: "your-bot-secret",
	Logger: &CustomLogger{},
})
```

## 工具函数

| 函数 | 说明 |
| --- | --- |
| `GetMsgID(frame)` | 从 frame 中提取 msgid |
| `GetReqID(frame)` | 从 frame 中提取 req_id |
| `GetMsgType(frame)` | 从 frame 中提取 msgtype |
| `GetEventType(frame)` | 从 frame 中提取 eventtype |
| `ParseMessageBody(frame, v)` | 解析消息体为指定类型 |
| `GenerateReqId(cmd)` | 生成请求 ID |
| `CreateTextReplyBody(content)` | 创建文本回复消息体 |
| `CreateMarkdownReplyBody(content)` | 创建 Markdown 回复消息体 |
| `CreateWelcomeReplyBody(content)` | 创建欢迎语回复消息体 |
| `CreateStreamReplyBody(streamID, content, finish, msgItem, feedback)` | 创建流式回复消息体 |

### 非阻塞流式回复

在流式生成场景中，如果逐 token 调用 `ReplyStream`，可能因上一条消息还没收到 ack 而排队积压。使用 `ReplyStreamNonBlocking` 可在 ack 未返回时直接跳过中间帧，避免延迟：

```go
client.OnMessageText(func(frame *aibot.WsFrame) {
	streamID := aibot.GenerateReqId("stream")

	// 模拟流式生成，逐段推送
	for _, chunk := range []string{"你好", "，", "世界", "！"} {
		_, err := client.ReplyStreamNonBlocking(frame, streamID, chunk, false, nil, nil)
		if errors.Is(err, aibot.ErrReplySkipped) {
			// 上一条还没 ack，跳过本次中间帧
			continue
		}
		if err != nil {
			fmt.Println("发送失败:", err)
			return
		}
	}

	// 最终帧必须发送（finish=true 不受限制）
	_, _ = client.ReplyStream(frame, streamID, "你好，世界！", true, nil, nil)
})
```

### WecomCrypto 加解密模块

SDK 内置了独立的企业微信消息加解密模块，支持 AES-256-CBC 加密/解密和 SHA1 签名：

```go
import (
	"errors"
	aibot "github.com/go-sphere/wecom-aibot-go-sdk/aibot"
)

// 创建实例
crypto, err := aibot.NewWecomCrypto("<token>", "<encodingAESKey>", "<corpId/botId>")
if err != nil {
	panic(err)
}

// 加密
encrypt, signature, err := crypto.Encrypt(`{"hello":"world"}`, "1234567890", "nonce123")
if err != nil {
	panic(err)
}

// 验证签名
if !crypto.VerifySignature(signature, "1234567890", "nonce123", encrypt) {
	panic("签名验证失败")
}

// 解密
decrypted, err := crypto.Decrypt(encrypt)
if err != nil {
	panic(err)
}
fmt.Println(decrypted) // {"hello":"world"}
```

### 工具函数

| 函数 | 说明 |
| --- | --- |
| `DecodeEncodingAESKey(encodingAESKey)` | 解码企业微信 EncodingAESKey |
| `PKCS7Pad(data, blockSize)` | PKCS#7 填充 |
| `PKCS7Unpad(data, blockSize)` | PKCS#7 解除填充 |

## 项目结构

```
wecom-aibot-go-sdk/
├── aibot/
│   ├── client.go           # WSClient 核心客户端
│   ├── ws.go               # WebSocket 长连接管理器
│   ├── message_handler.go  # 消息解析与事件分发
│   ├── api.go              # HTTP API 客户端（文件下载）
│   ├── crypto.go           # AES-256-CBC 文件解密
│   ├── logger.go           # 日志接口与默认实现
│   ├── utils.go            # 工具方法
│   ├── types.go            # 类型定义
│   └── crypto.go           # AES 文件解密
├── examples/
│   └── basic/main.go       # 基础使用示例
├── go.mod
└── README.md
```

## 运行示例

```bash
# 克隆项目
git clone https://github.com/go-sphere/wecom-aibot-go-sdk.git
cd wecom-aibot-go-sdk

# 运行示例
WECOM_BOT_ID=your-bot-id WECOM_BOT_SECRET=your-bot-secret go run examples/basic/main.go
```

## License

MIT
