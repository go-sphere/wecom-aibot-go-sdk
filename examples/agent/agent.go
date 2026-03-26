package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/go-sphere/wecom-aibot-go-sdk/aibot"
)

// agentStreamState holds the mutable state shared between the agent loop
// and the ticker goroutine that pushes content to WeChat.
// Only the current status OR the streaming text is shown — no history.
type agentStreamState struct {
	mu          sync.Mutex
	statusText  string // current tool status (replaced, not accumulated)
	currentText string // streaming text from final turn
}

func (s *agentStreamState) fullContent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentText != "" {
		return s.currentText
	}
	return s.statusText
}

func (s *agentStreamState) setStatus(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusText = text
}

func (s *agentStreamState) appendText(delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentText += delta
}

func (s *agentStreamState) clearCurrentText() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentText = ""
}

func (s *agentStreamState) setErrorText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentText = text
}

// RunAgent executes the agent loop: call Claude with tools, execute tool calls,
// stream results to WeChat as a single evolving message.
func RunAgent(
	ctx context.Context,
	ai *anthropic.Client,
	wsClient *aibot.WSClient,
	frame *aibot.WsFrame,
	cfg *Config,
	security *SecurityPolicy,
	userMessage string,
) {
	streamID := fmt.Sprintf("stream_%s", frame.Headers.ReqID)
	state := &agentStreamState{}
	executor := NewToolExecutor(security)
	tools := ToolDefinitions()

	done := make(chan struct{})
	finishDone := make(chan struct{})

	// Ticker goroutine: periodically push content to WeChat.
	// This is the ONLY goroutine that calls ReplyStream.
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		var lastSent string

		for {
			select {
			case <-ticker.C:
				current := state.fullContent()
				if current != "" && current != lastSent {
					_, _ = wsClient.ReplyStream(frame, streamID, current, false, nil, nil)
					lastSent = current
				}
			case <-done:
				finalContent := state.fullContent()
				if finalContent == "" {
					finalContent = "处理完成"
				}
				_, _ = wsClient.ReplyStream(frame, streamID, finalContent, true, nil, nil)
				close(finishDone)
				return
			}
		}
	}()

	// Build initial messages
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(userMessage)),
	}

	// Build system prompt
	var system []anthropic.TextBlockParam
	if cfg.SystemPrompt != "" {
		system = []anthropic.TextBlockParam{{Text: cfg.SystemPrompt}}
	}

	// Agent loop
	for turn := 0; turn < cfg.MaxTurns; turn++ {
		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(cfg.Model),
			MaxTokens: cfg.MaxTokens,
			Messages:  messages,
			Tools:     tools,
		}
		if len(system) > 0 {
			params.System = system
		}

		stream := ai.Messages.NewStreaming(ctx, params)

		var accMessage anthropic.Message
		turnHasToolUse := false

		for stream.Next() {
			event := stream.Current()
			_ = accMessage.Accumulate(event)

			switch evt := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				if evt.ContentBlock.Type == "tool_use" {
					turnHasToolUse = true
				}
			case anthropic.ContentBlockDeltaEvent:
				if delta, ok := evt.Delta.AsAny().(anthropic.TextDelta); ok {
					if !turnHasToolUse {
						state.appendText(delta.Text)
					}
				}
			}
		}

		if err := stream.Err(); err != nil {
			state.setErrorText(fmt.Sprintf("AI 服务错误: %v", err))
			break
		}

		// Check stop reason
		if accMessage.StopReason == anthropic.StopReasonToolUse {
			// Discard any reasoning text that leaked before tool_use was detected
			state.clearCurrentText()

			// Append assistant message to conversation
			messages = append(messages, accMessage.ToParam())

			// Execute each tool call
			var toolResults []anthropic.ContentBlockParamUnion
			for _, block := range accMessage.Content {
				toolUse, ok := block.AsAny().(anthropic.ToolUseBlock)
				if !ok {
					continue
				}

				state.setStatus(fmt.Sprintf("🔧 正在执行 %s...", toolUse.Name))

				result, isError := executor.Execute(ctx, toolUse.Name, toolUse.Input)

				toolResults = append(toolResults,
					anthropic.NewToolResultBlock(toolUse.ID, result, isError))
			}

			// Append tool results as user message
			messages = append(messages, anthropic.NewUserMessage(toolResults...))
			continue
		}

		// end_turn, max_tokens, or other — text is already in currentText
		break
	}

	close(done)
	<-finishDone
}
