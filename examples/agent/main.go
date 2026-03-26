package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/go-sphere/wecom-aibot-go-sdk/aibot"
)

func main() {
	configPath := "config.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Println(err)
		fmt.Println("请创建 config.json，参考 config.example.json")
		return
	}

	security, err := NewSecurityPolicy(cfg.WorkingDir, cfg.BashPatterns)
	if err != nil {
		fmt.Println("安全策略初始化失败:", err)
		return
	}

	fmt.Println("企业微信 AI Agent")
	fmt.Println("Bot ID:", cfg.BotID)
	fmt.Println("Model:", cfg.Model)
	fmt.Println("Working Dir:", cfg.WorkingDir)

	// Anthropic client
	aiOpts := []option.RequestOption{
		option.WithAPIKey(cfg.AnthropicAPIKey),
	}
	if cfg.AnthropicBaseURL != "" {
		aiOpts = append(aiOpts, option.WithBaseURL(cfg.AnthropicBaseURL))
	}
	ai := anthropic.NewClient(aiOpts...)

	// WeChat WebSocket client
	client := aibot.NewWSClient(aibot.WSClientOptions{
		BotID:  cfg.BotID,
		Secret: cfg.BotSecret,
	})

	// Connection callbacks
	client.OnConnected(func() {
		fmt.Println("连接已建立")
	})
	client.OnAuthenticated(func() {
		fmt.Println("认证成功")
	})
	client.OnDisconnected(func(reason string) {
		fmt.Println("连接断开:", reason)
	})
	client.OnReconnecting(func(attempt int) {
		fmt.Printf("正在重连 (第 %d 次)...\n", attempt)
	})
	client.OnError(func(err error) {
		fmt.Println("错误:", err.Error())
	})

	// Text message → agent loop
	client.OnMessageText(func(frame *aibot.WsFrame) {
		var msg aibot.TextMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			fmt.Println("解析消息失败:", err.Error())
			return
		}
		fmt.Printf("收到文本: %s\n", msg.Text.Content)

		go RunAgent(
			context.Background(),
			&ai,
			client,
			frame,
			cfg,
			security,
			msg.Text.Content,
		)
	})

	// Enter chat → welcome message
	client.OnEventEnterChat(func(frame *aibot.WsFrame) {
		var eventMsg aibot.EventMessage
		if err := aibot.ParseMessageBody(frame, &eventMsg); err != nil {
			fmt.Println("解析事件失败:", err.Error())
			return
		}
		fmt.Printf("用户进入会话: %s\n", eventMsg.From.UserID)
		welcomeBody := aibot.CreateTextReplyBody("你好！我是 AI 助手，可以帮你读写文件和执行命令。")
		_, _ = client.ReplyWelcome(frame, welcomeBody)
	})

	// Connect and wait for exit
	client.Connect()

	fmt.Println("\n按 Ctrl+C 退出")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n正在断开连接...")
	client.Disconnect()
	fmt.Println("已退出")
}
