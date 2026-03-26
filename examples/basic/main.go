package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/go-sphere/wecom-aibot-go-sdk/aibot"
)

type Config struct {
	BotID            string `json:"bot_id"`
	BotSecret        string `json:"bot_secret"`
	AnthropicAPIKey  string `json:"anthropic_api_key"`
	AnthropicBaseURL string `json:"anthropic_base_url,omitempty"`
	Model            string `json:"model,omitempty"`
	SystemPrompt     string `json:"system_prompt,omitempty"`
	MaxTokens        int64  `json:"max_tokens,omitempty"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if cfg.BotID == "" || cfg.BotSecret == "" {
		return nil, fmt.Errorf("bot_id 和 bot_secret 不能为空")
	}
	if cfg.AnthropicAPIKey == "" {
		return nil, fmt.Errorf("anthropic_api_key 不能为空")
	}
	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-6"
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}
	return &cfg, nil
}

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

	fmt.Println("企业微信 AI Bot SDK Go 版本")
	fmt.Println("Bot ID:", cfg.BotID)
	fmt.Println("Model:", cfg.Model)

	// 创建 Anthropic 客户端
	aiOpts := []option.RequestOption{
		option.WithAPIKey(cfg.AnthropicAPIKey),
	}
	if cfg.AnthropicBaseURL != "" {
		aiOpts = append(aiOpts, option.WithBaseURL(cfg.AnthropicBaseURL))
	}
	ai := anthropic.NewClient(aiOpts...)

	// 创建企业微信客户端
	client := aibot.NewWSClient(aibot.WSClientOptions{
		BotID:  cfg.BotID,
		Secret: cfg.BotSecret,
	})

	// 连接状态回调
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

	// 通用消息回调
	client.OnMessage(func(frame *aibot.WsFrame) {
		msgType := aibot.GetMsgType(frame)
		fmt.Printf("收到消息: %s\n", msgType)
	})

	// 文本消息 - Anthropic 流式对话（使用节流发送，避免 ack 超时）
	client.OnMessageText(func(frame *aibot.WsFrame) {
		var msg aibot.TextMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			fmt.Println("解析消息失败:", err.Error())
			return
		}
		fmt.Printf("收到文本: %s\n", msg.Text.Content)

		streamID := fmt.Sprintf("stream_%s", frame.Headers.ReqID)

		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(cfg.Model),
			MaxTokens: cfg.MaxTokens,
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Text.Content)),
			},
		}
		if cfg.SystemPrompt != "" {
			params.System = []anthropic.TextBlockParam{
				{Text: cfg.SystemPrompt},
			}
		}

		stream := ai.Messages.NewStreaming(context.Background(), params)

		// 使用 goroutine + ticker 解耦 token 累积与 WS 发送
		// ReplyStream 是阻塞的（等待 ACK 最多 5 秒），不能在每个 token 上调用
		var (
			mu          sync.Mutex
			fullContent string
			lastSent    string
			streamErr   error
			done        = make(chan struct{})
			finishDone  = make(chan struct{})
		)

		// 发送协程：每 300ms 检查是否有新内容需要推送
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					mu.Lock()
					current := fullContent
					mu.Unlock()
					if current != "" && current != lastSent {
						_, _ = client.ReplyStream(frame, streamID, current, false, nil, nil)
						lastSent = current
					}
				case <-done:
					mu.Lock()
					finalContent := fullContent
					finalErr := streamErr
					mu.Unlock()

					if finalErr != nil {
						fmt.Println("Anthropic API 错误:", finalErr.Error())
						if finalContent == "" {
							finalContent = "AI 服务暂时不可用"
						}
					}
					_, _ = client.ReplyStream(frame, streamID, finalContent, true, nil, nil)
					close(finishDone)
					return
				}
			}
		}()

		// 主循环：仅累积 token，不阻塞
		for stream.Next() {
			event := stream.Current()
			switch evt := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				if delta, ok := evt.Delta.AsAny().(anthropic.TextDelta); ok {
					mu.Lock()
					fullContent += delta.Text
					mu.Unlock()
				}
			}
		}

		if err := stream.Err(); err != nil {
			mu.Lock()
			streamErr = err
			mu.Unlock()
		}

		close(done)
		<-finishDone
	})

	// 图片消息
	client.OnMessageImage(func(frame *aibot.WsFrame) {
		var msg aibot.ImageMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			fmt.Println("解析消息失败:", err.Error())
			return
		}
		fmt.Printf("收到图片: %s\n", msg.Image.URL)

		data, filename, err := client.DownloadFile(msg.Image.URL, msg.Image.AesKey)
		if err != nil {
			fmt.Println("下载图片失败:", err.Error())
			return
		}

		if filename == "" {
			filename = "image.jpg"
		}
		tmpDir, _ := os.MkdirTemp("", "wecom-")
		tmpPath := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(tmpPath, data, 0644); err != nil {
			fmt.Println("保存文件失败:", err.Error())
		} else {
			fmt.Printf("图片已保存: %s (%d bytes)\n", tmpPath, len(data))
		}
	})

	// 文件消息
	client.OnMessageFile(func(frame *aibot.WsFrame) {
		var msg aibot.FileMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			fmt.Println("解析消息失败:", err.Error())
			return
		}
		fmt.Printf("收到文件: %s\n", msg.File.URL)

		data, filename, err := client.DownloadFile(msg.File.URL, msg.File.AesKey)
		if err != nil {
			fmt.Println("下载文件失败:", err.Error())
			return
		}

		if filename == "" {
			filename = "file.dat"
		}
		tmpDir, _ := os.MkdirTemp("", "wecom-")
		tmpPath := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(tmpPath, data, 0644); err != nil {
			fmt.Println("保存文件失败:", err.Error())
		} else {
			fmt.Printf("文件已保存: %s (%d bytes)\n", tmpPath, len(data))
		}
	})

	// 事件处理
	client.OnEvent(func(frame *aibot.WsFrame) {
		eventType := aibot.GetEventType(frame)
		fmt.Printf("收到事件: %s\n", eventType)
	})

	client.OnEventEnterChat(func(frame *aibot.WsFrame) {
		var eventMsg aibot.EventMessage
		if err := aibot.ParseMessageBody(frame, &eventMsg); err != nil {
			fmt.Println("解析事件失败:", err.Error())
			return
		}
		fmt.Printf("用户进入会话: %s\n", eventMsg.From.UserID)
		welcomeBody := aibot.CreateTextReplyBody("你好！有什么可以帮助你的吗？")
		_, _ = client.ReplyWelcome(frame, welcomeBody)
	})

	client.OnEventTemplateCardEvent(func(frame *aibot.WsFrame) {
		var eventMsg aibot.EventMessage
		if err := aibot.ParseMessageBody(frame, &eventMsg); err != nil {
			fmt.Println("解析事件失败:", err.Error())
			return
		}
		var eventData aibot.TemplateCardEventData
		if err := json.Unmarshal(eventMsg.Event, &eventData); err != nil {
			fmt.Println("解析事件数据失败:", err.Error())
			return
		}
		fmt.Printf("模板卡片事件: key=%s task=%s\n", eventData.EventKey, eventData.TaskID)
	})

	// 连接并等待退出信号
	client.Connect()

	fmt.Println("\n按 Ctrl+C 退出")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n正在断开连接...")
	client.Disconnect()
	fmt.Println("已退出")
}
