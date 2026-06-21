// Package scoped 提供装饰器 ScopedUserLogger，为每条消息前缀 [scope1/scope2/...]。
//
// 用法：包装任意 ulog.UserLogger（通常经由 *userlogger.Logger.WithScope 间接使用）：
//
//	logger := scoped.New(base, "service-deploy", "order-service")
//	logger.Info("starting")
//	// 输出: [<timestamp>] [service-deploy/order-service] starting
//
// WithScope 不在此处：它由 *userlogger.Logger 提供，会把本装饰器套到 sink 上。
package scoped

import (
	"fmt"
	"strings"

	"github.com/gaozebin3/userlogger/internal/ulog"
)

// ScopedUserLogger 为每条消息前缀 [scope1/scope2/...]。不可变。
type ScopedUserLogger struct {
	base   ulog.UserLogger
	scope  []string
	prefix string // 构造时一次性算好的 "[s1/s2/...]"；无 scope 时为 ""
}

// New 用给定 scope 段包装 base。
func New(base ulog.UserLogger, scope ...string) *ScopedUserLogger {
	s := make([]string, len(scope))
	copy(s, scope)
	return &ScopedUserLogger{base: base, scope: s, prefix: joinScope(s)}
}

func (l *ScopedUserLogger) Log(message string) { l.base.Log(l.applyPrefix(message)) }
func (l *ScopedUserLogger) Logf(format string, args ...any) {
	l.base.Log(l.applyPrefix(fmt.Sprintf(format, args...)))
}
func (l *ScopedUserLogger) Info(message string) { l.base.Info(l.applyPrefix(message)) }
func (l *ScopedUserLogger) Infof(format string, args ...any) {
	l.base.Info(l.applyPrefix(fmt.Sprintf(format, args...)))
}
func (l *ScopedUserLogger) Error(message string) { l.base.Error(l.applyPrefix(message)) }
func (l *ScopedUserLogger) Flush() error         { return l.base.Flush() }

// Errorf 前缀 scope，并用 %w 包裹格式化错误，保持 errors.Is/As 可用。
func (l *ScopedUserLogger) Errorf(format string, args ...any) error {
	if l.prefix == "" {
		return l.base.Errorf(format, args...)
	}
	return l.base.Errorf("%s %w", l.prefix, fmt.Errorf(format, args...))
}

// Append 返回追加了一段 scope 的新 ScopedUserLogger（供 *userlogger.Logger.WithScope 使用）。
func (l *ScopedUserLogger) Append(scope string) *ScopedUserLogger {
	s := make([]string, len(l.scope)+1)
	copy(s, l.scope)
	s[len(l.scope)] = scope
	return &ScopedUserLogger{base: l.base, scope: s, prefix: joinScope(s)}
}

func (l *ScopedUserLogger) applyPrefix(msg string) string {
	if l.prefix != "" {
		return l.prefix + " " + msg
	}
	return msg
}

// joinScope 非空切片返回 "[s1/s2/...]"，空切片返回 ""。
func joinScope(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return "[" + strings.Join(s, "/") + "]"
}

var _ ulog.UserLogger = (*ScopedUserLogger)(nil)
