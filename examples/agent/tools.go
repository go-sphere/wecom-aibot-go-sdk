package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// ToolExecutor dispatches and executes tool calls with security validation.
type ToolExecutor struct {
	security *SecurityPolicy
}

func NewToolExecutor(security *SecurityPolicy) *ToolExecutor {
	return &ToolExecutor{security: security}
}

// Execute runs a tool by name and returns the result text and whether it's an error.
func (te *ToolExecutor) Execute(ctx context.Context, name string, input json.RawMessage) (string, bool) {
	switch name {
	case "read_file":
		return te.readFile(ctx, input)
	case "write_file":
		return te.writeFile(ctx, input)
	case "update_file":
		return te.updateFile(ctx, input)
	case "bash":
		return te.bash(ctx, input)
	default:
		return fmt.Sprintf("未知工具: %s", name), true
	}
}

// ToolDefinitions returns the Anthropic tool parameter definitions.
func ToolDefinitions() []anthropic.ToolUnionParam {
	return []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "read_file",
			Description: anthropic.String("Read the contents of a file at the given path. The path is relative to the working directory."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path (relative to working directory or absolute)",
					},
				},
				Required: []string{"path"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "write_file",
			Description: anthropic.String("Write content to a file. Creates the file and parent directories if they don't exist, overwrites if the file exists."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path (relative to working directory or absolute)",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Content to write to the file",
					},
				},
				Required: []string{"path", "content"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "update_file",
			Description: anthropic.String("Update a file by replacing the first occurrence of old_str with new_str. Use this for targeted edits."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path (relative to working directory or absolute)",
					},
					"old_str": map[string]any{
						"type":        "string",
						"description": "The exact text to find in the file",
					},
					"new_str": map[string]any{
						"type":        "string",
						"description": "The replacement text",
					},
				},
				Required: []string{"path", "old_str", "new_str"},
			},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "bash",
			Description: anthropic.String("Execute a bash command in the working directory. Only commands matching allowed patterns can be executed."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to execute",
					},
				},
				Required: []string{"command"},
			},
		}},
	}
}

// --- Tool implementations ---

const maxFileReadSize = 100 * 1024 // 100KB

type readFileInput struct {
	Path string `json:"path"`
}

func (te *ToolExecutor) readFile(_ context.Context, raw json.RawMessage) (string, bool) {
	var input readFileInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Sprintf("参数解析失败: %v", err), true
	}
	absPath, err := te.security.ValidatePath(input.Path)
	if err != nil {
		return err.Error(), true
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Sprintf("读取文件失败: %v", err), true
	}
	if len(data) > maxFileReadSize {
		return fmt.Sprintf("[文件截断，仅显示前 %d 字节]\n%s", maxFileReadSize, string(data[:maxFileReadSize])), false
	}
	return string(data), false
}

type writeFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (te *ToolExecutor) writeFile(_ context.Context, raw json.RawMessage) (string, bool) {
	var input writeFileInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Sprintf("参数解析失败: %v", err), true
	}
	absPath, err := te.security.ValidatePath(input.Path)
	if err != nil {
		return err.Error(), true
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return fmt.Sprintf("创建目录失败: %v", err), true
	}
	if err := os.WriteFile(absPath, []byte(input.Content), 0644); err != nil {
		return fmt.Sprintf("写入文件失败: %v", err), true
	}
	return fmt.Sprintf("已写入 %d 字节到 %s", len(input.Content), input.Path), false
}

type updateFileInput struct {
	Path   string `json:"path"`
	OldStr string `json:"old_str"`
	NewStr string `json:"new_str"`
}

func (te *ToolExecutor) updateFile(_ context.Context, raw json.RawMessage) (string, bool) {
	var input updateFileInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Sprintf("参数解析失败: %v", err), true
	}
	absPath, err := te.security.ValidatePath(input.Path)
	if err != nil {
		return err.Error(), true
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Sprintf("读取文件失败: %v", err), true
	}
	content := string(data)
	if !strings.Contains(content, input.OldStr) {
		return "未找到要替换的文本", true
	}
	newContent := strings.Replace(content, input.OldStr, input.NewStr, 1)
	if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
		return fmt.Sprintf("写入文件失败: %v", err), true
	}
	return fmt.Sprintf("已更新文件 %s", input.Path), false
}

type bashInput struct {
	Command string `json:"command"`
}

const bashTimeout = 30 * time.Second

func (te *ToolExecutor) bash(ctx context.Context, raw json.RawMessage) (string, bool) {
	var input bashInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Sprintf("参数解析失败: %v", err), true
	}
	if err := te.security.ValidateBashCommand(input.Command); err != nil {
		return err.Error(), true
	}
	ctx, cancel := context.WithTimeout(ctx, bashTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)
	cmd.Dir = te.security.workingDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var result strings.Builder
	if stdout.Len() > 0 {
		result.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("[stderr]\n")
		result.WriteString(stderr.String())
	}
	if err != nil {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("[exit error] %v", err))
		return result.String(), true
	}
	if result.Len() == 0 {
		return "(无输出)", false
	}
	output := result.String()
	if len(output) > maxFileReadSize {
		return fmt.Sprintf("[输出截断，仅显示前 %d 字节]\n%s", maxFileReadSize, output[:maxFileReadSize]), false
	}
	return output, false
}
