package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/go-sphere/wecom-aibot-go-sdk/aibot"
)

func main() {
	// 从环境变量获取配置
	botID := os.Getenv("WECOM_BOT_ID")
	botSecret := os.Getenv("WECOM_BOT_SECRET")

	if botID == "" || botSecret == "" {
		fmt.Println("请设置环境变量 WECOM_BOT_ID 和 WECOM_BOT_SECRET")
		fmt.Println("示例: WECOM_BOT_ID=xxx WECOM_BOT_SECRET=xxx go run examples/main.go")
		return
	}

	fmt.Println("企业微信 AI Bot SDK Go 版本")
	fmt.Println("Bot ID:", botID)

	// 创建客户端
	client := aibot.NewWSClient(aibot.WSClientOptions{
		BotID:  botID,
		Secret: botSecret,
		WSURL:  "",  // 使用默认地址
		Logger: nil, // 使用默认日志
	})

	// 设置事件处理
	client.OnConnected(func() {
		fmt.Println("✓ 连接已建立")
	})

	client.OnAuthenticated(func() {
		fmt.Println("✓ 认证成功")
	})

	client.OnDisconnected(func(reason string) {
		fmt.Println("✗ 连接断开:", reason)
	})

	client.OnReconnecting(func(attempt int) {
		fmt.Printf("⟳ 正在重连 (第 %d 次)...\n", attempt)
	})

	client.OnError(func(err error) {
		fmt.Println("✗ 错误:", err.Error())
	})

	// 消息处理
	client.OnMessage(func(frame *aibot.WsFrame) {
		msgType := aibot.GetMsgType(frame)
		fmt.Printf("📩 收到消息: %s\n", msgType)
	})

	client.OnMessageText(func(frame *aibot.WsFrame) {
		fmt.Println("📝 收到文本消息")

		// 解析消息体
		var msg aibot.TextMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			fmt.Println("解析消息失败:", err.Error())
			return
		}

		fmt.Printf("  内容: %s\n", msg.Text.Content)

		// 回复消息
		streamID := fmt.Sprintf("stream_%s", frame.Headers.ReqID)
		reply := "收到消息: " + msg.Text.Content

		_, _ = client.ReplyStream(frame, streamID, reply, true, nil, nil)
	})

	client.OnMessageImage(func(frame *aibot.WsFrame) {
		fmt.Println("🖼️ 收到图片消息")

		// 解析消息体
		var msg aibot.ImageMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			fmt.Println("解析消息失败:", err.Error())
			return
		}

		// 打印完整消息结构用于调试
		fmt.Printf("  原始 Body: %s\n", string(frame.Body))
		fmt.Printf("  图片 URL: %s\n", msg.Image.URL)
		fmt.Printf("  图片 AesKey: '%s' (len=%d)\n", msg.Image.AesKey, len(msg.Image.AesKey))

		// 下载图片
		data, filename, err := client.DownloadFile(msg.Image.URL, msg.Image.AesKey)
		if err != nil {
			fmt.Println("下载图片失败:", err.Error())
			return
		}

		// 保存到临时文件
		if filename == "" {
			filename = "image.jpg"
		}
		tmpDir, _ := os.MkdirTemp("", "wecom-")
		tmpPath := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(tmpPath, data, 0644); err != nil {
			fmt.Println("保存文件失败:", err.Error())
		} else {
			fmt.Printf("  图片已保存到: %s\n", tmpPath)
			fmt.Printf("  图片大小: %d bytes\n", len(data))
		}
	})

	client.OnMessageFile(func(frame *aibot.WsFrame) {
		fmt.Println("📎 收到文件消息")

		// 解析消息体
		var msg aibot.FileMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			fmt.Println("解析消息失败:", err.Error())
			return
		}

		fmt.Printf("  文件 URL: %s\n", msg.File.URL)

		// 下载文件
		data, filename, err := client.DownloadFile(msg.File.URL, msg.File.AesKey)
		if err != nil {
			fmt.Println("下载文件失败:", err.Error())
			return
		}

		// 保存到临时文件
		if filename == "" {
			filename = "file.dat"
		}
		tmpDir, _ := os.MkdirTemp("", "wecom-")
		tmpPath := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(tmpPath, data, 0644); err != nil {
			fmt.Println("保存文件失败:", err.Error())
		} else {
			fmt.Printf("  文件已保存到: %s\n", tmpPath)
			fmt.Printf("  文件大小: %d bytes\n", len(data))
		}
	})

	// 事件处理
	client.OnEvent(func(frame *aibot.WsFrame) {
		eventType := aibot.GetEventType(frame)
		fmt.Printf("📋 收到事件: %s\n", eventType)
	})

	client.OnEventEnterChat(func(frame *aibot.WsFrame) {
		fmt.Println("👋 用户进入会话")

		// 解析消息体
		var eventMsg aibot.EventMessage
		if err := aibot.ParseMessageBody(frame, &eventMsg); err != nil {
			fmt.Println("解析事件失败:", err.Error())
			return
		}

		fmt.Printf("  用户: %s\n", eventMsg.From.UserID)

		// 发送欢迎语
		welcomeBody := aibot.CreateTextReplyBody("你好！有什么可以帮助你的吗？")
		_, _ = client.ReplyWelcome(frame, welcomeBody)
	})

	client.OnEventTemplateCardEvent(func(frame *aibot.WsFrame) {
		fmt.Println("📋 用户点击模板卡片")

		// 解析消息体
		var eventMsg aibot.EventMessage
		if err := aibot.ParseMessageBody(frame, &eventMsg); err != nil {
			fmt.Println("解析事件失败:", err.Error())
			return
		}

		// 解析事件数据
		var eventData aibot.TemplateCardEventData
		if err := json.Unmarshal(eventMsg.Event, &eventData); err != nil {
			fmt.Println("解析事件数据失败:", err.Error())
			return
		}

		fmt.Printf("  Button Key: %s\n", eventData.EventKey)
		fmt.Printf("  Task ID: %s\n", eventData.TaskID)
	})

	// 连接客户端
	client.Connect()

	// 等待信号
	fmt.Println("\n按 Ctrl+C 退出")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// 断开连接
	fmt.Println("\n正在断开连接...")
	client.Disconnect()
	fmt.Println("已退出")
}
