package aibot

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// Logger 日志接口
type Logger interface {
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
}

// DefaultLogger 默认日志实现
type DefaultLogger struct {
	logger *log.Logger
	mu     sync.Mutex
}

// NewDefaultLogger 创建默认日志实例
func NewDefaultLogger() *DefaultLogger {
	return &DefaultLogger{
		logger: log.New(os.Stdout, "[aibot] ", log.LstdFlags|log.Lshortfile),
	}
}

// Debug 调试级别日志
func (l *DefaultLogger) Debug(format string, v ...interface{}) {
	l.log("DEBUG", format, v...)
}

// Info 信息级别日志
func (l *DefaultLogger) Info(format string, v ...interface{}) {
	l.log("INFO", format, v...)
}

// Warn 警告级别日志
func (l *DefaultLogger) Warn(format string, v ...interface{}) {
	l.log("WARN", format, v...)
}

// Error 错误级别日志
func (l *DefaultLogger) Error(format string, v ...interface{}) {
	l.log("ERROR", format, v...)
}

func (l *DefaultLogger) log(level, format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, v...)
	l.logger.Printf("[%s] [%s] %s", timestamp, level, msg)
}

// ============================================================
// Logger 适配器 - 用于兼容不同日志框架
// ============================================================

// LoggerFunc 函数式日志适配器
type LoggerFunc func(level string, format string, v ...interface{})

// Debug 调试级别日志
func (f LoggerFunc) Debug(format string, v ...interface{}) {
	f("DEBUG", format, v...)
}

// Info 信息级别日志
func (f LoggerFunc) Info(format string, v ...interface{}) {
	f("INFO", format, v...)
}

// Warn 警告级别日志
func (f LoggerFunc) Warn(format string, v ...interface{}) {
	f("WARN", format, v...)
}

// Error 错误级别日志
func (f LoggerFunc) Error(format string, v ...interface{}) {
	f("ERROR", format, v...)
}

// NewLoggerFunc 创建函数式日志适配器
func NewLoggerFunc(fn func(level, format string, v ...interface{})) Logger {
	return LoggerFunc(fn)
}
