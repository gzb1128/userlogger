package async_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gaozebin3/userlogger"
	"github.com/gaozebin3/userlogger/async"
)

type mockWriter struct {
	mu        sync.Mutex
	content   string
	count     int32
	delay     time.Duration
	failUntil int32
}

func (m *mockWriter) WriteLog(ctx context.Context, c string) error {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if v := atomic.LoadInt32(&m.failUntil); v > 0 {
		atomic.AddInt32(&m.failUntil, -1)
		return errors.New("mock error")
	}
	m.mu.Lock()
	atomic.AddInt32(&m.count, 1)
	m.content += c
	m.mu.Unlock()
	return nil
}

func (m *mockWriter) get() string { m.mu.Lock(); defer m.mu.Unlock(); return m.content }
func (m *mockWriter) cnt() int32  { return atomic.LoadInt32(&m.count) }
func has(s, sub string) bool      { return strings.Contains(s, sub) }

func opts() *async.Options { return async.DefaultOptions() }

func TestInterface(t *testing.T) {
	var _ userlogger.UserLogger = (*async.AsyncLogger)(nil)
}

func TestErrorfWrap(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, opts())
	root := errors.New("root")
	err := l.Errorf("fail: %w", root)
	if !errors.Is(err, root) {
		t.Fatal("should wrap")
	}
	l.Close()
	if !has(w.get(), "fail: root") {
		t.Fatalf("got %q", w.get())
	}
}

func TestNonBlocking(t *testing.T) {
	w := &mockWriter{delay: 500 * time.Millisecond}
	l := async.New(w, opts())
	defer l.Close()
	start := time.Now()
	for i := 0; i < 100; i++ {
		l.Infof("log %d", i)
	}
	if time.Since(start) > 10*time.Millisecond {
		t.Errorf("writes should be non-blocking, took %v", time.Since(start))
	}
}

func TestBatchWrite(t *testing.T) {
	w := &mockWriter{}
	o := opts()
	o.BatchSize = 10
	l := async.New(w, o)
	defer l.Close()
	for i := 0; i < 25; i++ {
		l.Infof("log %d", i)
	}
	time.Sleep(200 * time.Millisecond)
	l.Flush()
	if w.get() == "" {
		t.Error("expected content")
	}
}

func TestCloseFlushes(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, opts())
	for i := 0; i < 5; i++ {
		l.Infof("log %d", i)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if !has(w.get(), fmt.Sprintf("log %d", i)) {
			t.Errorf("missing log %d", i)
		}
	}
}

func TestGracefulShutdown(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, opts())
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			l.Infof("log %d", i)
		}
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	if w.get() == "" {
		t.Error("expected some logs")
	}
}

func TestOverflowWarning(t *testing.T) {
	w := &mockWriter{delay: 200 * time.Millisecond}
	o := opts()
	o.ChannelBufferCount = 100
	o.BatchSize = 50
	l := async.New(w, o)
	defer l.Close()
	for i := 0; i < 1000; i++ {
		l.Infof("log %d", i)
	}
	time.Sleep(500 * time.Millisecond)
	l.Flush()
	time.Sleep(200 * time.Millisecond)
	c := w.get()
	if c == "" {
		t.Error("expected logs")
	}
	if has(c, "[System Warning] Buffer overflow") {
		t.Log("overflow warning present")
	}
}

func TestConcurrentWrites(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, opts())
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				l.Infof("g%d log%d", id, j)
			}
		}(i)
	}
	wg.Wait()
	l.Flush()
	time.Sleep(200 * time.Millisecond)
	l.Close()
	c := w.get()
	if !has(c, "g0") || !has(c, "g9") {
		t.Error("expected logs from all goroutines")
	}
}

func TestScopedConcurrent(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, opts())
	ctx := userlogger.NewContext(context.Background(), l)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ul := userlogger.FromContext(ctx).WithScope("test").WithScope(fmt.Sprintf("w%d", id))
			ul.Info("start")
			ul.StartSpan("proc").Done()
			ul.Info("end")
		}(i)
	}
	wg.Wait()
	time.Sleep(300 * time.Millisecond)
	l.Close()
	c := w.get()
	if !has(c, "[test/w") {
		t.Errorf("missing scope prefix in %q", c)
	}
}

func TestRetry(t *testing.T) {
	w := &mockWriter{failUntil: 2}
	o := opts()
	o.MaxRetry = 3
	o.FlushInterval = 100 * time.Millisecond
	l := async.New(w, o)
	l.Info("retry test")
	time.Sleep(800 * time.Millisecond)
	l.Flush()
	time.Sleep(200 * time.Millisecond)
	l.Close()
	if w.get() == "" {
		t.Error("expected log after retry")
	}
}

func TestCloseNoPanic(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, opts())
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			l.Infof("log %d", i)
			time.Sleep(time.Microsecond)
		}
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("goroutine hung")
	}
}

func TestNewNilOptions(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, nil)
	l.Info("nil opts test")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if w.get() == "" {
		t.Error("expected log output with nil opts")
	}
}

func TestNewZeroOptions(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, &async.Options{})
	l.Info("zero opts test")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if w.get() == "" {
		t.Error("expected log output with zero opts")
	}
}

func TestFlushGuarantee(t *testing.T) {
	w := &mockWriter{delay: 20 * time.Millisecond}
	o := opts()
	o.FlushInterval = 10 * time.Minute
	l := async.New(w, o)
	for i := 0; i < 5; i++ {
		l.Infof("msg %d", i)
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	c := w.get()
	for i := 0; i < 5; i++ {
		if !has(c, fmt.Sprintf("msg %d", i)) {
			t.Errorf("msg %d missing after Flush", i)
		}
	}
	l.Close()
}

func TestFlushAfterClose(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, opts())
	l.Close()
	if err := l.Flush(); err == nil {
		t.Error("expected error flushing after close")
	}
}

func TestCloseIdempotent(t *testing.T) {
	w := &mockWriter{}
	l := async.New(w, opts())
	err1 := l.Close()
	err2 := l.Close()
	err3 := l.Close()
	if err1 != nil || err2 != nil || err3 != nil {
		t.Errorf("all Close calls should succeed: %v %v %v", err1, err2, err3)
	}
}

func TestMaxRetryZero(t *testing.T) {
	w := &mockWriter{failUntil: 1}
	o := opts()
	o.MaxRetry = 0
	o.FlushInterval = 10 * time.Minute
	l := async.New(w, o)
	l.Info("no-retry test")
	if err := l.Flush(); err == nil {
		t.Error("Flush should return error when writer fails with MaxRetry=0")
	}
	l.Close()
}

func TestCloseReportsFlushFailure(t *testing.T) {
	w := &mockWriter{failUntil: 10}
	o := opts()
	o.MaxRetry = 0
	o.FlushInterval = 10 * time.Minute
	l := async.New(w, o)
	l.Info("will-fail")
	if err := l.Close(); err == nil {
		t.Error("Close should return error when final drain persistence fails")
	}
}

func TestFlushDoesNotReportRecoveredBackgroundFailure(t *testing.T) {
	w := &mockWriter{failUntil: 10}
	o := opts()
	o.BatchSize = 1
	o.MaxRetry = 0
	o.FlushInterval = 10 * time.Minute
	l := async.New(w, o)
	l.Info("trigger-batch-fail")
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&w.failUntil) == 10 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for background flush failure")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	atomic.StoreInt32(&w.failUntil, 0)
	l.Info("ok-after-fail")
	deadline = time.After(2 * time.Second)
	for !has(w.get(), "ok-after-fail") {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for later background flush success")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := l.Flush(); err != nil {
		t.Fatalf("Flush should not report recovered background flush error: %v", err)
	}
	l.Close()
}

func TestCloseDoesNotReportRecoveredBackgroundFailure(t *testing.T) {
	w := &mockWriter{failUntil: 10}
	o := opts()
	o.BatchSize = 1
	o.MaxRetry = 0
	o.FlushInterval = 10 * time.Minute
	l := async.New(w, o)
	l.Info("trigger-batch-fail")
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&w.failUntil) == 10 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for background flush failure")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	atomic.StoreInt32(&w.failUntil, 0)
	l.Info("ok-before-close")
	deadline = time.After(2 * time.Second)
	for !has(w.get(), "ok-before-close") {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for later background flush success")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close should not report recovered background flush error: %v", err)
	}
}
