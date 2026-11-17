package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type SecurityPolicy struct {
	workingDir   string
	bashPatterns []*regexp.Regexp
}

func NewSecurityPolicy(workingDir string, bashPatterns []string) (*SecurityPolicy, error) {
	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("解析工作目录失败: %w", err)
	}
	compiled := make([]*regexp.Regexp, 0, len(bashPatterns))
	for _, p := range bashPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("编译正则表达式 %q 失败: %w", p, err)
		}
		compiled = append(compiled, re)
	}
	return &SecurityPolicy{
		workingDir:   absDir,
		bashPatterns: compiled,
	}, nil
}

const shellMetacharacters = ";|&`$()><\n\r"

// ValidatePath resolves a path relative to workingDir and ensures it stays within bounds.
func (s *SecurityPolicy) ValidatePath(path string) (string, error) {
	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath = filepath.Clean(filepath.Join(s.workingDir, path))
	}
	// Ensure the resolved path is within the working directory (handles root "/" safely).
	prefix := s.workingDir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if absPath != s.workingDir && !strings.HasPrefix(absPath, prefix) {
		return "", fmt.Errorf("路径 %q 超出工作目录 %q", path, s.workingDir)
	}
	return absPath, nil
}

// ValidateBashCommand checks whether a command is free of shell metacharacters
// and matches any allowed pattern.
func (s *SecurityPolicy) ValidateBashCommand(cmd string) error {
	if strings.ContainsAny(cmd, shellMetacharacters) {
		return fmt.Errorf("命令 %q 包含不允许的 shell 元字符", cmd)
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("命令不能为空")
	}
	for _, re := range s.bashPatterns {
		if re.MatchString(cmd) {
			return nil
		}
	}
	return fmt.Errorf("命令 %q 不在允许的命令列表中", cmd)
}
