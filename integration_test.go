package userlogger

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gaozebin3/userlogger/async"
	"k8s.io/klog/v2"
)

// dbWriter 模拟生产中的 append-mode 日志库（如 UpdateTaskActionLog）。
type dbWriter struct {
	mu      sync.Mutex
	content string
}

func (w *dbWriter) WriteLog(ctx context.Context, content string) error {
	w.mu.Lock()
	w.content += content
	w.mu.Unlock()
	return nil
}
func (w *dbWriter) get() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.content
}

// 验证生产集成形态与核心"无泄露"属性：
//   - 用户日志落库（带 scope/时间戳/span 标记）
//   - 运维侧 klog 收到用户活动（镜像）
//   - 运维上下文（traceID 等）绝不进入用户库
func TestIntegration_ProductionShape_NoLeak(t *testing.T) {
	ctx, sink := klogCtx(t)

	// 运维侧把 traceID 挂到 klog（用户不应看到）
	klogger := klog.FromContext(ctx).WithValues("traceID", "secret-123")
	ctx = klog.NewContext(ctx, klogger)

	db := &dbWriter{}
	aw := async.New(db, async.DefaultOptions())
	ctx = NewContext(ctx, aw)

	ul := FromContext(ctx)
	ul.WithScope("deploy/order").Info("starting")
	ul.StartSpan("deploy app").Done()
	ul.Logf("stdout: %s", "hello")

	if err := aw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := aw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	content := db.get()
	for _, want := range []string{"[deploy/order] starting", "✓ deploy app done", "stdout: hello"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q in DB content:\n%s", want, content)
		}
	}

	// 核心断言：运维上下文不得泄露到用户库
	if strings.Contains(content, "secret-123") || strings.Contains(content, "traceID") {
		t.Errorf("operator context leaked into user DB:\n%s", content)
	}

	// 反向佐证：klog 侧确实持有运维上下文（从而上面的"库中无 traceID"才是有意义的）
	if !kvHas(sink.lastKv, "traceID", "secret-123") {
		t.Errorf("klog side should carry traceID for the no-leak assertion to be meaningful, got %v", sink.lastKv)
	}

	// 镜像：klog 仍收到用户活动
	if len(sink.infos) == 0 {
		t.Errorf("klog mirror should receive user activity, got %v", sink.infos)
	}
}
