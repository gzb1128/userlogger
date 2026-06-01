package userlogger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
)

type testSink struct {
	infos  []string
	errors []string
}

func (s *testSink) Init(info logr.RuntimeInfo)                     {}
func (s *testSink) Enabled(level int) bool                         { return true }
func (s *testSink) Info(level int, msg string, kv ...interface{})  { s.infos = append(s.infos, msg) }
func (s *testSink) Error(err error, msg string, kv ...interface{}) { s.errors = append(s.errors, msg) }
func (s *testSink) WithValues(kv ...interface{}) logr.LogSink      { return s }
func (s *testSink) WithName(name string) logr.LogSink              { return s }

func klogCtx(t *testing.T) (context.Context, *testSink) {
	t.Helper()
	s := &testSink{}
	return klog.NewContext(context.Background(), logr.New(s)), s
}

type capture struct {
	infos  []string
	errors []string
	logs   []string
}

func (c *capture) Log(m string)                     { c.logs = append(c.logs, m) }
func (c *capture) Logf(f string, a ...interface{})  { c.logs = append(c.logs, fmt.Sprintf(f, a...)) }
func (c *capture) Info(m string)                    { c.infos = append(c.infos, m) }
func (c *capture) Infof(f string, a ...interface{}) { c.infos = append(c.infos, fmt.Sprintf(f, a...)) }
func (c *capture) Error(m string)                   { c.errors = append(c.errors, m) }
func (c *capture) Errorf(f string, a ...interface{}) error {
	err := fmt.Errorf(f, a...)
	c.errors = append(c.errors, err.Error())
	return err
}
func (c *capture) Flush() error                { return nil }
func (c *capture) WithScope(string) UserLogger { return c }
func (c *capture) StartSpan(string) Span       { return &noopSpan{} }

func TestFromContext_NoKlog(t *testing.T) {
	base := &capture{}
	ul := FromContext(NewContext(context.Background(), base))
	if _, ok := ul.(*klogUserLogger); ok {
		t.Fatal("should not wrap when klog not in ctx")
	}
	ul.Info("test")
	if len(base.infos) != 1 {
		t.Fatalf("expected 1 info, got %d", len(base.infos))
	}
}

func TestFromContext_WithKlog(t *testing.T) {
	ctx, _ := klogCtx(t)
	ctx = NewContext(ctx, &capture{})
	ul := FromContext(ctx)
	if _, ok := ul.(*klogUserLogger); !ok {
		t.Fatal("expected klogUserLogger wrapper")
	}
}

func TestFromContext_NoUserLogger(t *testing.T) {
	ctx, sink := klogCtx(t)
	FromContext(ctx).Info("no logger")
	if len(sink.infos) != 1 || sink.infos[0] != "no logger" {
		t.Fatalf("klog should still receive output, got %v", sink.infos)
	}
}

func TestKlogDualWrite_Info(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	ul := FromContext(ctx)
	ul.Info("deploy ok")
	if sink.infos[0] != "deploy ok" || base.infos[0] != "deploy ok" {
		t.Fatal("both sinks should receive info")
	}
}

func TestKlogDualWrite_Error(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).Error("timeout")
	if len(sink.errors) != 1 || len(base.errors) != 1 {
		t.Fatal("both sinks should receive error")
	}
}

func TestKlogDualWrite_Errorf(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	err := FromContext(ctx).Errorf("fail: %v", errors.New("boom"))
	if err == nil || !strings.Contains(sink.errors[0], "fail") || !strings.Contains(base.errors[0], "fail") {
		t.Fatalf("expected wrapped error, got sink=%v base=%v", sink.errors, base.errors)
	}
}

func TestKlogScoped_ScopePrefix(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).WithScope("TPaaS/kafka").Info("created")
	expected := "[TPaaS/kafka] created"
	if sink.infos[0] != expected || base.infos[0] != expected {
		t.Fatalf("expected %q, got sink=%q base=%q", expected, sink.infos[0], base.infos[0])
	}
}

func TestKlogScoped_Nested(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).WithScope("deploy").WithScope("middleware").Info("starting")
	expected := "[deploy/middleware] starting"
	if sink.infos[0] != expected || base.infos[0] != expected {
		t.Fatalf("expected %q, got sink=%q base=%q", expected, sink.infos[0], base.infos[0])
	}
}

func TestKlogScoped_ErrorfPreservesWrap(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	root := errors.New("root cause")
	err := FromContext(ctx).WithScope("app-deploy/100%").Errorf("exec failed: %w", root)
	if !errors.Is(err, root) {
		t.Fatal("should preserve wrapped error")
	}
	if strings.Contains(sink.errors[0], "%!w") || strings.Contains(base.errors[0], "%!w") {
		t.Fatal("should not contain fmt %w artifact")
	}
}

func TestKlogSpan_Done(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).StartSpan("init").Done()
	if !strings.Contains(sink.infos[0], "✓") || !strings.Contains(sink.infos[0], "init") {
		t.Fatalf("expected success span, got %q", sink.infos[0])
	}
}

func TestKlogSpan_EndErr(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).StartSpan("deploy").End(errors.New("pull failed"))
	if !strings.Contains(sink.errors[0], "✗") || !strings.Contains(sink.errors[0], "deploy") {
		t.Fatalf("expected failure span, got %q", sink.errors[0])
	}
}

func TestKlogDualWrite_Logf(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).Logf("deployed %d instances", 3)
	if sink.infos[0] != "deployed 3 instances" || base.logs[0] != "deployed 3 instances" {
		t.Fatalf("dual-write logf mismatch, sink=%q base=%q", sink.infos[0], base.logs[0])
	}
}

func TestKlogScoped_Logf(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).WithScope("deploy").Logf("progress %d%%", 50)
	if sink.infos[0] != "[deploy] progress 50%" || base.logs[0] != "[deploy] progress 50%" {
		t.Fatalf("scoped logf mismatch, sink=%q base=%q", sink.infos[0], base.logs[0])
	}
}
