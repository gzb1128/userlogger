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
	lastKv []any // 最近一次 Info 携带的 klog 结构化字段（用于断言"无泄露"）
}

func (s *testSink) Init(info logr.RuntimeInfo) {}
func (s *testSink) Enabled(level int) bool     { return true }
func (s *testSink) Info(level int, msg string, kv ...interface{}) {
	s.infos = append(s.infos, msg)
	s.lastKv = append([]any{}, kv...)
}
func (s *testSink) Error(err error, msg string, kv ...interface{}) {
	s.errors = append(s.errors, msg)
}
func (s *testSink) WithValues(kv ...interface{}) logr.LogSink {
	return &testSinkVals{parent: s, vals: kv}
}
func (s *testSink) WithName(name string) logr.LogSink { return s }

// testSinkVals 是 WithValues 返回的子 sink：把携带的 kv 记回 parent，
// 使测试能观察到"klog 侧确实持有运维上下文"。
type testSinkVals struct {
	parent *testSink
	vals   []any
}

func (v *testSinkVals) Init(logr.RuntimeInfo) {}
func (v *testSinkVals) Enabled(int) bool      { return true }
func (v *testSinkVals) Info(_ int, msg string, kv ...any) {
	v.parent.infos = append(v.parent.infos, msg)
	v.parent.lastKv = append(append([]any{}, v.vals...), kv...)
}
func (v *testSinkVals) Error(_ error, msg string, kv ...any) {
	v.parent.errors = append(v.parent.errors, msg)
}
func (v *testSinkVals) WithValues(kv ...any) logr.LogSink {
	return &testSinkVals{parent: v.parent, vals: append(append([]any{}, v.vals...), kv...)}
}
func (v *testSinkVals) WithName(string) logr.LogSink { return v }

func klogCtx(t *testing.T) (context.Context, *testSink) {
	t.Helper()
	s := &testSink{}
	return klog.NewContext(context.Background(), logr.New(s)), s
}

// kvHas 报告 klog 的扁平 kv 切片是否包含指定的 key/value 对。
func kvHas(kv []any, k, v string) bool {
	for i := 0; i+1 < len(kv); i += 2 {
		if ks, ok := kv[i].(string); ok && ks == k {
			if vs, ok := kv[i+1].(string); ok && vs == v {
				return true
			}
		}
	}
	return false
}

// capture 是一个 ulog.UserLogger，记录所有输出用于断言。
type capture struct {
	infos  []string
	errors []string
	logs   []string
}

func (c *capture) Log(m string)             { c.logs = append(c.logs, m) }
func (c *capture) Logf(f string, a ...any)  { c.logs = append(c.logs, fmt.Sprintf(f, a...)) }
func (c *capture) Info(m string)            { c.infos = append(c.infos, m) }
func (c *capture) Infof(f string, a ...any) { c.infos = append(c.infos, fmt.Sprintf(f, a...)) }
func (c *capture) Error(m string)           { c.errors = append(c.errors, m) }
func (c *capture) Errorf(f string, a ...any) error {
	err := fmt.Errorf(f, a...)
	c.errors = append(c.errors, err.Error())
	return err
}
func (c *capture) Flush() error { return nil }

func TestFromContext_NoKlog_NoMirror(t *testing.T) {
	base := &capture{}
	ul := FromContext(NewContext(context.Background(), base))
	if _, ok := ul.sink.(*klogMirror); ok {
		t.Fatal("should not mirror when klog not in ctx")
	}
	ul.Info("test")
	if len(base.infos) != 1 {
		t.Fatalf("expected 1 info, got %d", len(base.infos))
	}
}

func TestFromContext_WithKlog_Mirrors(t *testing.T) {
	ctx, _ := klogCtx(t)
	ctx = NewContext(ctx, &capture{})
	ul := FromContext(ctx)
	if _, ok := ul.sink.(*klogMirror); !ok {
		t.Fatal("expected klogMirror sink")
	}
}

func TestFromContext_NoUserLogger_KlogStillReceives(t *testing.T) {
	ctx, sink := klogCtx(t)
	FromContext(ctx).Info("no logger")
	if len(sink.infos) != 1 || sink.infos[0] != "no logger" {
		t.Fatalf("klog should still receive output, got %v", sink.infos)
	}
}

func TestFromContext_NoopPath_DoesNotPanic(t *testing.T) {
	ul := FromContext(context.Background())
	ul.Log("a")
	ul.Infof("b %d", 1)
	ul.Error("c")
	if err := ul.Errorf("d: %v", errors.New("x")); err == nil {
		t.Fatal("noop Errorf should still return an error")
	}
	if err := ul.Flush(); err != nil {
		t.Fatalf("noop Flush should be nil, got %v", err)
	}
}

func TestKlogDualWrite_Info(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).Info("deploy ok")
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

// 连续 WithScope 必须把 scope 段拼成 [a/b]，而不是嵌套成 [b] [a]。
func TestLogger_WithScopeJoinsSegments(t *testing.T) {
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

// scope 段含 '%' 时不能被当作 fmt 动词（经由 *Logger.WithScope 覆盖）。
func TestLogger_PercentInScope(t *testing.T) {
	ctx, sink := klogCtx(t)
	base := &capture{}
	ctx = NewContext(ctx, base)
	FromContext(ctx).WithScope("deploy-100%").Logf("progress %d%%", 50)
	if sink.infos[0] != "[deploy-100%] progress 50%" || base.logs[0] != "[deploy-100%] progress 50%" {
		t.Fatalf("scoped logf mismatch, sink=%q base=%q", sink.infos[0], base.logs[0])
	}
}

// 直接证明"无泄露"属性：klog 侧确实持有运维上下文(traceID)，而 base(用户 dst)
// 只收到纯消息字符串。该属性由接口签名结构性地保证（Info 只接受 string）。
func TestKlogMirror_NoOperatorContextLeaksToBase(t *testing.T) {
	sink := &testSink{}
	klogger := logr.New(sink).WithValues("traceID", "secret-123")
	ctx := klog.NewContext(context.Background(), klogger)

	base := &capture{}
	m := &klogMirror{base: base, klogger: klog.FromContext(ctx)}
	m.Info("deploying")
	m.Errorf("failed: %w", errors.New("boom"))

	// klog 侧收到消息，并携带运维上下文
	if len(sink.infos) != 1 || sink.infos[0] != "deploying" {
		t.Fatalf("klog should receive info, got %v", sink.infos)
	}
	if !kvHas(sink.lastKv, "traceID", "secret-123") {
		t.Errorf("klog side should carry traceID, got %v", sink.lastKv)
	}
	// base（用户 dst）只收到纯消息——不含运维上下文
	if len(base.infos) != 1 || base.infos[0] != "deploying" {
		t.Fatalf("base should receive only the message, got %v", base.infos)
	}
	if len(base.errors) != 1 || !strings.Contains(base.errors[0], "failed: boom") {
		t.Fatalf("base error mismatch, got %v", base.errors)
	}
}
