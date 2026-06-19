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
//	// Output: [<timestamp>] [service-deploy/order-service] starting
//
// WithScope returns a new instance without mutating the receiver, so the
// original logger is unaffected.  Scope depth should be kept to 2-3 levels.
package scoped

import (
	"fmt"
	"strings"

	"github.com/gaozebin3/userlogger"
	"github.com/gaozebin3/userlogger/span"
)

// ScopedUserLogger prepends [scope1/scope2/...] to every message.
// It is immutable — WithScope returns a fresh instance.
type ScopedUserLogger struct {
	base   userlogger.UserLogger
	scope  []string
	prefix string // cached "[s1/s2/...]" computed once at construction; "" if no scope
}

// New wraps base with the given scope segments.
func New(base userlogger.UserLogger, scope ...string) *ScopedUserLogger {
	s := make([]string, len(scope))
	copy(s, scope)
	return &ScopedUserLogger{base: base, scope: s, prefix: joinScope(s)}
}

func (l *ScopedUserLogger) Log(message string) { l.base.Log(l.applyPrefix(message)) }
func (l *ScopedUserLogger) Logf(format string, args ...interface{}) {
	l.base.Log(l.applyPrefix(fmt.Sprintf(format, args...)))
}
func (l *ScopedUserLogger) Info(message string) { l.base.Info(l.applyPrefix(message)) }
func (l *ScopedUserLogger) Infof(format string, args ...interface{}) {
	l.base.Info(l.applyPrefix(fmt.Sprintf(format, args...)))
}
func (l *ScopedUserLogger) Error(message string) { l.base.Error(l.applyPrefix(message)) }
func (l *ScopedUserLogger) Flush() error         { return l.base.Flush() }

// Errorf prepends the scope and wraps the formatted error via %w so that
// errors.Is/As continue to work.
func (l *ScopedUserLogger) Errorf(format string, args ...interface{}) error {
	if l.prefix == "" {
		return l.base.Errorf(format, args...)
	}
	return l.base.Errorf("%s %w", l.prefix, fmt.Errorf(format, args...))
}

// WithScope appends a scope segment and returns a new ScopedUserLogger.
func (l *ScopedUserLogger) WithScope(scope string) userlogger.UserLogger {
	s := make([]string, len(l.scope)+1)
	copy(s, l.scope)
	s[len(l.scope)] = scope
	return &ScopedUserLogger{base: l.base, scope: s, prefix: joinScope(s)}
}

// StartSpan creates a timed span via the span sub-package.
func (l *ScopedUserLogger) StartSpan(name string) userlogger.Span {
	return span.New(l, name)
}

func (l *ScopedUserLogger) applyPrefix(msg string) string {
	if l.prefix != "" {
		return l.prefix + " " + msg
	}
	return msg
}

// joinScope returns "[s1/s2/...]" for a non-empty slice, "" otherwise.
func joinScope(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return "[" + strings.Join(s, "/") + "]"
}

var _ userlogger.UserLogger = (*ScopedUserLogger)(nil)
