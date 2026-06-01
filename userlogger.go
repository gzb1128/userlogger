// Package userlogger provides high-performance, extensible user-facing logging
// built on top of klog.
//
// # Quick Start
//
//	ul := userlogger.FromContext(ctx)
//	ul.Info("starting deployment")        // structured, with timestamp
//	ul.Logf("deployed %d instances", 10)   // formatted, no timestamp
//	ul.Log("intermediate output")          // plain text, no timestamp
//	ul.Error("deployment failed")          // error, with timestamp
//
// # Scope — Group Logs by Business Context
//
// Use WithScope to add a [scope] prefix so logs from different modules or
// concurrent tasks are distinguishable.  Scope depth is capped at 3 levels.
//
//	deployLogger := ul.WithScope("service-deploy/order-service")
//	envLogger := deployLogger.WithScope("env-setup")  // 3rd level — the limit
//
// Name scopes with business semantics (action/object), not internal code paths:
//
//	Good:  "service-deploy/payment-api", "Helm/order-service", "middleware/MySQL"
//	Bad:   "cvessel/chart/order-api", "handler/step-1"
//
// # Span — Track Operation Duration
//
// Use StartSpan for business-meaningful operations that take >1 s.
// Call Done() on success, End(err) on failure.
//
//	span := ul.StartSpan("deploy application")
//	defer func() { if err != nil { span.End(err) } else { span.Done() } }()
//
// # Context
//
//	// Inject
//	logger := async.New(writer, async.DefaultOptions())
//	ctx = userlogger.NewContext(ctx, logger)
//
//	// Retrieve — returns no-op if absent, dual-writes to klog if present
//	ul := userlogger.FromContext(ctx)
//
// # Sub-packages
//
//   - scoped:  ScopedUserLogger — scope prefix decorator
//   - span:    timed Span implementation
//   - async:   AsyncLogger — channel-based non-blocking writer with LogWriter interface
package userlogger

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
)

// UserLogger is the core interface for user-facing business logs.
//
// Two output categories:
//   - Unstructured (Log/Logf): plain text, no timestamp.  For intermediate output.
//   - Structured (Info/Infof/Error/Errorf): with timestamp.  For key steps and errors.
type UserLogger interface {
	Log(message string)
	Logf(format string, args ...interface{})
	Info(message string)
	Infof(format string, args ...interface{})
	Error(message string)
	Errorf(format string, args ...interface{}) error
	Flush() error
	WithScope(scope string) UserLogger
	StartSpan(name string) Span
}

// Span tracks the duration and outcome of an operation.
type Span interface {
	End(err error)
	Done()
}

type ctxKey struct{}

var contextKey = ctxKey{}

// FromContext retrieves a UserLogger from ctx.
// Returns no-op if absent.  If ctx also contains a klog.Logger, returns a
// dual-write wrapper.
func FromContext(ctx context.Context) UserLogger {
	ul, _ := ctx.Value(contextKey).(UserLogger)
	if ul == nil {
		ul = &noopUserLogger{}
	}
	if _, err := logr.FromContext(ctx); err == nil {
		return &klogUserLogger{base: ul, klogger: klog.FromContext(ctx)}
	}
	return ul
}

// NewContext returns a ctx carrying the given UserLogger.
func NewContext(ctx context.Context, ul UserLogger) context.Context {
	return context.WithValue(ctx, contextKey, ul)
}

// NewScopedLogger is a convenience alias for scoped.New.
// Import the scoped sub-package directly for full functionality.
func NewScopedLogger(base UserLogger, scopeSegments ...string) UserLogger {
	return newScoped(base, scopeSegments...)
}

// NewSpan is a convenience alias for creating a basic timed span.
func NewSpan(logger UserLogger, name string) Span {
	return &spanImpl{logger: logger, name: name, start: time.Now()}
}

// --- no-op implementations ---

type noopUserLogger struct{}

func (n *noopUserLogger) Log(string)                                     {}
func (n *noopUserLogger) Logf(string, ...interface{})                    {}
func (n *noopUserLogger) Info(string)                                    {}
func (n *noopUserLogger) Infof(string, ...interface{})                   {}
func (n *noopUserLogger) Error(string)                                   {}
func (n *noopUserLogger) Errorf(f string, a ...interface{}) error        { return fmt.Errorf(f, a...) }
func (n *noopUserLogger) Flush() error                                   { return nil }
func (n *noopUserLogger) WithScope(string) UserLogger                    { return n }
func (n *noopUserLogger) StartSpan(string) Span                          { return &noopSpan{} }

type noopSpan struct{}

func (n *noopSpan) End(error) {}
func (n *noopSpan) Done()     {}

// --- klog dual-write wrapper ---

type klogUserLogger struct {
	base    UserLogger
	klogger klog.Logger
}

func (k *klogUserLogger) Log(m string)                    { k.klogger.Info(m); k.base.Log(m) }
func (k *klogUserLogger) Logf(f string, a ...interface{})  { k.klogger.Info(fmt.Sprintf(f, a...)); k.base.Logf(f, a...) }
func (k *klogUserLogger) Info(m string)                   { k.klogger.Info(m); k.base.Info(m) }
func (k *klogUserLogger) Infof(f string, a ...interface{}) { k.klogger.Info(fmt.Sprintf(f, a...)); k.base.Infof(f, a...) }
func (k *klogUserLogger) Error(m string)                  { k.klogger.Error(nil, m); k.base.Error(m) }
func (k *klogUserLogger) Errorf(f string, a ...interface{}) error {
	err := fmt.Errorf(f, a...)
	k.klogger.Error(nil, err.Error())
	return k.base.Errorf(f, a...)
}
func (k *klogUserLogger) Flush() error              { return k.base.Flush() }
func (k *klogUserLogger) WithScope(s string) UserLogger { return &klogScoped{base: k, scopes: []string{s}} }
func (k *klogUserLogger) StartSpan(n string) Span    { return NewSpan(k, n) }

var _ UserLogger = (*klogUserLogger)(nil)

// --- minimal scoped / span for root package self-containment ---

type scopedImpl struct {
	base  UserLogger
	scope []string
}

func newScoped(base UserLogger, segs ...string) *scopedImpl {
	s := make([]string, len(segs))
	copy(s, segs)
	return &scopedImpl{base: base, scope: s}
}

func (l *scopedImpl) Log(m string)                      { l.base.Log(l.pfx(m)) }
func (l *scopedImpl) Logf(f string, a ...interface{})    { l.base.Logf(l.pfx(f), a...) }
func (l *scopedImpl) Info(m string)                     { l.base.Info(l.pfx(m)) }
func (l *scopedImpl) Infof(f string, a ...interface{})   { l.base.Infof(l.pfx(f), a...) }
func (l *scopedImpl) Error(m string)                    { l.base.Error(l.pfx(m)) }
func (l *scopedImpl) Errorf(f string, a ...interface{}) error {
	if p := l.scopeStr(); p != "" {
		return l.base.Errorf("%s %w", p, fmt.Errorf(f, a...))
	}
	return l.base.Errorf(f, a...)
}
func (l *scopedImpl) Flush() error                      { return l.base.Flush() }
func (l *scopedImpl) WithScope(s string) UserLogger {
	ns := make([]string, len(l.scope)+1)
	copy(ns, l.scope)
	ns[len(l.scope)] = s
	return &scopedImpl{base: l.base, scope: ns}
}
func (l *scopedImpl) StartSpan(n string) Span { return NewSpan(l, n) }

func (l *scopedImpl) pfx(m string) string {
	if p := l.scopeStr(); p != "" {
		return p + " " + m
	}
	return m
}

func (l *scopedImpl) scopeStr() string {
	if len(l.scope) == 0 {
		return ""
	}
	return "[" + strings.Join(l.scope, "/") + "]"
}

var _ UserLogger = (*scopedImpl)(nil)

type spanImpl struct {
	logger UserLogger
	name   string
	start  time.Time
}

func (s *spanImpl) End(err error) {
	d := time.Since(s.start)
	if err != nil {
		s.logger.Errorf("✗ %s failed (%s): %v", s.name, fmtDur(d), err)
	} else {
		s.logger.Infof("✓ %s done (%s)", s.name, fmtDur(d))
	}
}
func (s *spanImpl) Done() { s.End(nil) }

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	} else if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}

var _ Span = (*spanImpl)(nil)

// klogScoped carries scope prefixes under the klog dual-write mode.
type klogScoped struct {
	base   *klogUserLogger
	scopes []string
}

func (s *klogScoped) Log(m string)                    { s.base.Log(s.pfx(m)) }
func (s *klogScoped) Logf(f string, a ...interface{})  { s.base.Log(s.pfx(fmt.Sprintf(f, a...))) }
func (s *klogScoped) Info(m string)                   { s.base.Info(s.pfx(m)) }
func (s *klogScoped) Infof(f string, a ...interface{}) { s.base.Info(s.pfx(fmt.Sprintf(f, a...))) }
func (s *klogScoped) Error(m string)                  { s.base.Error(s.pfx(m)) }
func (s *klogScoped) Errorf(f string, a ...interface{}) error {
	p := s.scope()
	if p == "" {
		return s.base.Errorf(f, a...)
	}
	return s.base.Errorf("%s %w", p, fmt.Errorf(f, a...))
}
func (s *klogScoped) Flush() error                { return s.base.Flush() }
func (s *klogScoped) WithScope(scope string) UserLogger {
	ns := make([]string, len(s.scopes)+1)
	copy(ns, s.scopes)
	ns[len(s.scopes)] = scope
	return &klogScoped{base: s.base, scopes: ns}
}
func (s *klogScoped) StartSpan(n string) Span { return NewSpan(s, n) }

func (s *klogScoped) pfx(m string) string {
	if p := s.scope(); p != "" {
		return p + " " + m
	}
	return m
}

func (s *klogScoped) scope() string {
	if len(s.scopes) == 0 {
		return ""
	}
	p := "["
	for i, sc := range s.scopes {
		if i > 0 {
			p += "/"
		}
		p += sc
	}
	return p + "]"
}

var _ UserLogger = (*klogScoped)(nil)
