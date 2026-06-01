// Package async provides AsyncLogger, a channel-based non-blocking UserLogger
// that batches log entries and persists them via a caller-supplied LogWriter.
//
// # Usage
//
// Implement LogWriter for your backend (DB, file, MQ, etc.):
//
//	type dbWriter struct { db *sql.DB }
//	func (w *dbWriter) WriteLog(ctx context.Context, content string) error {
//	    _, err := w.db.ExecContext(ctx, "INSERT INTO logs (content) VALUES (?)", content)
//	    return err
//	}
//
// Create and use:
//
//	logger := async.New(&dbWriter{db: db}, async.DefaultOptions())
//	defer logger.Close()
//	logger.Info("hello")
package async

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gaozebin3/userlogger"
	"github.com/gaozebin3/userlogger/scoped"
	"github.com/gaozebin3/userlogger/span"
	"k8s.io/klog/v2"
)

// LogWriter persists log content to a concrete backend.
type LogWriter interface {
	WriteLog(ctx context.Context, content string) error
}

// Options configures an AsyncLogger.
type Options struct {
	ChannelBufferCount int           // channel capacity in entries (default 10000)
	BatchSize          int           // entries per flush (default 50)
	FlushInterval      time.Duration // periodic flush interval (default 3s)
	MaxRetry           int           // write retries (default 3)
	WriteTimeout       time.Duration // per WriteLog timeout (default 5s)
	CloseTimeout       time.Duration // Close drain timeout (default 10s)
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() *Options {
	return &Options{
		ChannelBufferCount: 10000,
		BatchSize:          50,
		FlushInterval:      3 * time.Second,
		MaxRetry:           3,
		WriteTimeout:       5 * time.Second,
		CloseTimeout:       10 * time.Second,
	}
}

// AsyncLogger is a channel-based, non-blocking UserLogger.
// Architecture: fixed-capacity channel → single consumer goroutine → batched
// writes with retry.  When the channel is full, entries are silently dropped
// and an overflow warning is prepended to the next batch.
type AsyncLogger struct {
	writer        LogWriter
	logCh         chan string
	ctx           context.Context
	cancel        context.CancelFunc
	consumerWg    sync.WaitGroup
	closed        bool
	closedMu      sync.Mutex
	closeOnce     sync.Once
	batchSize     int
	flushInterval time.Duration
	maxRetry      int
	writeTimeout  time.Duration
	closeTimeout  time.Duration
	droppedCount  int64
}

// New creates an AsyncLogger and starts its consumer goroutine.
func New(writer LogWriter, opts *Options) *AsyncLogger {
	ctx, cancel := context.WithCancel(context.Background())
	l := &AsyncLogger{
		writer:        writer,
		logCh:         make(chan string, opts.ChannelBufferCount),
		ctx:           ctx,
		cancel:        cancel,
		batchSize:     opts.BatchSize,
		flushInterval: opts.FlushInterval,
		maxRetry:      opts.MaxRetry,
		writeTimeout:  opts.WriteTimeout,
		closeTimeout:  opts.CloseTimeout,
	}
	l.consumerWg.Add(1)
	go l.consumerLoop()
	return l
}

func (l *AsyncLogger) sendLog(message string) {
	l.closedMu.Lock()
	closed := l.closed
	l.closedMu.Unlock()
	if closed {
		return
	}
	select {
	case l.logCh <- message + "\n":
	default:
		atomic.AddInt64(&l.droppedCount, 1)
	}
}

func (l *AsyncLogger) Log(message string)                      { l.sendLog(message) }
func (l *AsyncLogger) Logf(f string, a ...interface{})         { l.Log(fmt.Sprintf(f, a...)) }
func (l *AsyncLogger) Info(message string)                     { l.Infof("%s", message) }
func (l *AsyncLogger) Error(message string)                    { l.Errorf("%s", message) }

func (l *AsyncLogger) Infof(f string, a ...interface{}) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	l.sendLog(fmt.Sprintf("[%s] %s", ts, fmt.Sprintf(f, a...)))
}

func (l *AsyncLogger) Errorf(f string, a ...interface{}) error {
	err := fmt.Errorf(f, a...)
	ts := time.Now().Format("2006-01-02 15:04:05")
	l.sendLog(fmt.Sprintf("[%s] ❌ %s", ts, err.Error()))
	return err
}

func (l *AsyncLogger) WithScope(scope string) userlogger.UserLogger {
	return scoped.New(l, scope)
}

func (l *AsyncLogger) StartSpan(name string) userlogger.Span {
	return span.New(l, name)
}

func (l *AsyncLogger) consumerLoop() {
	defer l.consumerWg.Done()
	batch := make([]string, 0, l.batchSize)
	ticker := time.NewTicker(l.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case e := <-l.logCh:
			batch = append(batch, e)
			if len(batch) >= l.batchSize {
				l.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				l.flushBatch(batch)
				batch = batch[:0]
			}
		case <-l.ctx.Done():
			for {
				select {
				case e := <-l.logCh:
					batch = append(batch, e)
					if len(batch) >= l.batchSize {
						l.flushBatch(batch)
						batch = batch[:0]
					}
				default:
					goto DRAINED
				}
			}
		DRAINED:
			l.flushBatch(batch)
			return
		}
	}
}

func (l *AsyncLogger) flushBatch(batch []string) {
	dropped := atomic.SwapInt64(&l.droppedCount, 0)
	if len(batch) == 0 && dropped == 0 {
		return
	}
	if dropped > 0 {
		batch = append([]string{fmt.Sprintf("[System Warning] Buffer overflow: skipped %d logs due to slow writer.\n", dropped)}, batch...)
	}
	content := strings.Join(batch, "")
	var err error
	for i := 0; i <= l.maxRetry; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i*100) * time.Millisecond)
		}
		err = l.writeContent(content)
		if err == nil {
			return
		}
	}
	klog.Errorf("AsyncLogger flush failed after %d retries: %v", l.maxRetry, err)
}

func (l *AsyncLogger) writeContent(content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), l.writeTimeout)
	defer cancel()
	return l.writer.WriteLog(ctx, content)
}

// Flush waits for the internal channel to drain (best-effort, 10s timeout).
func (l *AsyncLogger) Flush() error {
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	empty := 0
	for {
		select {
		case <-ticker.C:
			if len(l.logCh) == 0 {
				empty++
				if empty >= 4 {
					return nil
				}
			} else {
				empty = 0
			}
		case <-timeout:
			return fmt.Errorf("flush timeout")
		case <-l.ctx.Done():
			return fmt.Errorf("logger closed")
		}
	}
}

// Close stops accepting new entries, drains the channel, and waits for the
// consumer to exit.  Safe to call multiple times.
func (l *AsyncLogger) Close() error {
	var firstErr error
	l.closeOnce.Do(func() {
		l.closedMu.Lock()
		l.closed = true
		l.closedMu.Unlock()
		l.cancel()
		done := make(chan struct{})
		go func() { l.consumerWg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(l.closeTimeout):
			firstErr = fmt.Errorf("close timeout: consumer goroutine did not exit in time")
		}
		if d := atomic.LoadInt64(&l.droppedCount); d > 0 {
			klog.Warningf("AsyncLogger closed with %d logs dropped", d)
		}
	})
	return firstErr
}

var _ userlogger.UserLogger = (*AsyncLogger)(nil)
