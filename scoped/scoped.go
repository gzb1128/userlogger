// Package scoped provides ScopedUserLogger, an immutable UserLogger decorator
// that prepends [scope1/scope2/...] to every message.
//
// # Usage
//
// Wrap any userlogger.UserLogger with one or more scope segments:
//
//	base := userlogger.FromContext(ctx)
//	logger := scoped.New(base, "service-deploy", "order-service")
//	logger.Info("starting")
//	// Output: [2025-01-15 10:30:45] [service-deploy/order-service] starting
//
// WithScope returns a new instance without mutating the receiver, so the
// original logger is unaffected.  Scope depth should be kept to 2-3 levels.
package scoped

import (
	"fmt"
	"strings"

	"github.com/gaozebin3/userlogger"
)

// ScopedUserLogger prepends [scope1/scope2/...] to every message.
// It is immutable — WithScope returns a fresh instance.
type ScopedUserLogger struct {
	base  userlogger.UserLogger
	scope []string
}

// New wraps base with the given scope segments.
func New(base userlogger.UserLogger, scope ...string) *ScopedUserLogger {
	s := make([]string, len(scope))
	copy(s, scope)
	return &ScopedUserLogger{base: base, scope: s}
}

func (l *ScopedUserLogger) Log(message string)                       { l.base.Log(l.prefix(message)) }
func (l *ScopedUserLogger) Logf(format string, args ...interface{})   { l.base.Logf(l.prefixFmt(format), args...) }
func (l *ScopedUserLogger) Info(message string)                      { l.base.Info(l.prefix(message)) }
func (l *ScopedUserLogger) Infof(format string, args ...interface{})  { l.base.Infof(l.prefixFmt(format), args...) }
func (l *ScopedUserLogger) Error(message string)                     { l.base.Error(l.prefix(message)) }
func (l *ScopedUserLogger) Flush() error                             { return l.base.Flush() }

// Errorf prepends the scope and wraps the formatted error via %w so that
// errors.Is/As continue to work.
func (l *ScopedUserLogger) Errorf(format string, args ...interface{}) error {
	p := l.formatScope()
	if p == "" {
		return l.base.Errorf(format, args...)
	}
	return l.base.Errorf("%s %w", p, fmt.Errorf(format, args...))
}

// WithScope appends a scope segment and returns a new ScopedUserLogger.
func (l *ScopedUserLogger) WithScope(scope string) userlogger.UserLogger {
	s := make([]string, len(l.scope)+1)
	copy(s, l.scope)
	s[len(l.scope)] = scope
	return &ScopedUserLogger{base: l.base, scope: s}
}

// StartSpan creates a timed span via the span sub-package.
func (l *ScopedUserLogger) StartSpan(name string) userlogger.Span {
	return userlogger.NewSpan(l, name)
}

func (l *ScopedUserLogger) prefix(msg string) string {
	if p := l.formatScope(); p != "" {
		return p + " " + msg
	}
	return msg
}

func (l *ScopedUserLogger) prefixFmt(format string) string {
	if p := l.formatScope(); p != "" {
		return p + " " + format
	}
	return format
}

// formatScope returns [s1/s2/...].
func (l *ScopedUserLogger) formatScope() string {
	if len(l.scope) == 0 {
		return ""
	}
	return fmt.Sprintf("[%s]", strings.Join(l.scope, "/"))
}

var _ userlogger.UserLogger = (*ScopedUserLogger)(nil)
