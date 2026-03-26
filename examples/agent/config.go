package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	BotID            string   `json:"bot_id"`
	BotSecret        string   `json:"bot_secret"`
	AnthropicAPIKey  string   `json:"anthropic_api_key"`
	AnthropicBaseURL string   `json:"anthropic_base_url,omitempty"`
	Model            string   `json:"model,omitempty"`
	SystemPrompt     string   `json:"system_prompt,omitempty"`
	MaxTokens        int64    `json:"max_tokens,omitempty"`
	WorkingDir       string   `json:"working_dir"`
	BashPatterns     []string `json:"bash_patterns"`
	MaxTurns         int      `json:"max_turns,omitempty"`
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
	if cfg.WorkingDir == "" {
		return nil, fmt.Errorf("working_dir 不能为空")
	}
	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-6"
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 20
	}
	return &cfg, nil
}
