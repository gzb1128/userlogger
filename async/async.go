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
// For every field except MaxRetry, a zero or negative value falls back to the
// default shown below. MaxRetry is special: 0 means "no retry" (a failing
// batch is reported once and dropped); pass a negative value (or use
// DefaultOptions) to get the default of 3 retries.
type Options struct {
	ChannelBufferCount int
	BatchSize          int
	FlushInterval      time.Duration
	MaxRetry           int
	WriteTimeout       time.Duration
	CloseTimeout       time.Duration
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

func normalizeOptions(opts *Options) *Options {
	if opts == nil {
		return DefaultOptions()
	}
	o := *opts
	if o.ChannelBufferCount <= 0 {
		o.ChannelBufferCount = 10000
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 50
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = 3 * time.Second
	}
	if o.MaxRetry < 0 {
		o.MaxRetry = 3
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 5 * time.Second
	}
	if o.CloseTimeout <= 0 {
		o.CloseTimeout = 10 * time.Second
	}
	return &o
}

// AsyncLogger is a channel-based, non-blocking UserLogger.
// Architecture: fixed-capacity channel → single consumer goroutine → batched
// writes with retry.  When the channel is full, entries are silently dropped
// and an overflow warning is prepended to the next batch.
type AsyncLogger struct {
	writer        LogWriter
	logCh         chan string
	flushCh       chan chan error
	ctx           context.Context
	cancel        context.CancelFunc
	consumerWg    sync.WaitGroup
	closedMu      sync.Mutex
	closed        bool
	closeOnce     sync.Once
	closeErr      error
	consumerErr   error
	batchSize     int
	flushInterval time.Duration
	maxRetry      int
	writeTimeout  time.Duration
	closeTimeout  time.Duration
	droppedCount  int64
}

// New creates an AsyncLogger and starts its consumer goroutine.
// Nil opts uses defaults; zero-valued fields are normalized.
func New(writer LogWriter, opts *Options) *AsyncLogger {
	o := normalizeOptions(opts)
	ctx, cancel := context.WithCancel(context.Background())
	l := &AsyncLogger{
		writer:        writer,
		logCh:         make(chan string, o.ChannelBufferCount),
		flushCh:       make(chan chan error),
		ctx:           ctx,
		cancel:        cancel,
		batchSize:     o.BatchSize,
		flushInterval: o.FlushInterval,
		maxRetry:      o.MaxRetry,
		writeTimeout:  o.WriteTimeout,
		closeTimeout:  o.CloseTimeout,
	}
	l.consumerWg.Add(1)
	go l.consumerLoop()
	return l
}

func (l *AsyncLogger) sendLog(message string) {
	l.closedMu.Lock()
	if l.closed {
		l.closedMu.Unlock()
		return
	}
	select {
	case l.logCh <- message + "\n":
		l.closedMu.Unlock()
	default:
		l.closedMu.Unlock()
		atomic.AddInt64(&l.droppedCount, 1)
	}
}

func (l *AsyncLogger) Log(message string)              { l.sendLog(message) }
func (l *AsyncLogger) Logf(f string, a ...interface{}) { l.Log(fmt.Sprintf(f, a...)) }
func (l *AsyncLogger) Info(message string) {
	l.sendLog(timestampPrefix() + message)
}
func (l *AsyncLogger) Error(message string) {
	l.sendLog(timestampPrefix() + "❌ " + message)
}

func (l *AsyncLogger) Infof(f string, a ...interface{}) {
	l.sendLog(timestampPrefix() + fmt.Sprintf(f, a...))
}

func (l *AsyncLogger) Errorf(f string, a ...interface{}) error {
	err := fmt.Errorf(f, a...)
	l.sendLog(timestampPrefix() + "❌ " + err.Error())
	return err
}

// timestampPrefix returns "[YYYY-MM-DD HH:MM:SS] " using a single concat
// instead of two fmt.Sprintf calls.
func timestampPrefix() string {
	return "[" + time.Now().Format("2006-01-02 15:04:05") + "] "
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
		case ack := <-l.flushCh:
			batch = l.drainAndFlush(batch, ack)
		case <-ticker.C:
			if len(batch) > 0 {
				l.flushBatch(batch)
				batch = batch[:0]
			}
		case <-l.ctx.Done():
			var lastErr error
			for {
				select {
				case e := <-l.logCh:
					batch = append(batch, e)
					if len(batch) >= l.batchSize {
						if err := l.flushBatch(batch); err != nil {
							lastErr = err
						}
						batch = batch[:0]
					}
				default:
					if err := l.flushBatch(batch); err != nil {
						lastErr = err
					}
					l.consumerErr = lastErr
					return
				}
			}
		}
	}
}

func (l *AsyncLogger) drainAndFlush(batch []string, ack chan<- error) []string {
	var lastErr error
	for {
		select {
		case e := <-l.logCh:
			batch = append(batch, e)
			if len(batch) >= l.batchSize {
				if err := l.flushBatch(batch); err != nil {
					lastErr = err
				}
				batch = batch[:0]
			}
		default:
			if err := l.flushBatch(batch); err != nil {
				lastErr = err
			}
			ack <- lastErr
			return batch[:0]
		}
	}
}

func (l *AsyncLogger) flushBatch(batch []string) error {
	dropped := atomic.LoadInt64(&l.droppedCount)
	if len(batch) == 0 && dropped == 0 {
		return nil
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
			if dropped > 0 {
				atomic.AddInt64(&l.droppedCount, -dropped)
			}
			return nil
		}
	}
	klog.Errorf("AsyncLogger flush failed after %d retries: %v", l.maxRetry, err)
	return err
}

func (l *AsyncLogger) writeContent(content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), l.writeTimeout)
	defer cancel()
	return l.writer.WriteLog(ctx, content)
}

// Flush drains pending entries and waits for them to be persisted.
// It blocks until the consumer finishes the flush, the logger is closed, or
// a 10s hard deadline elapses (independent of WriteTimeout/MaxRetry). The
// returned error is the persistence error from the drained batch, or
// "logger closed" / "flush timeout" for those termination cases.
func (l *AsyncLogger) Flush() error {
	ack := make(chan error, 1)
	select {
	case l.flushCh <- ack:
	case <-l.ctx.Done():
		return fmt.Errorf("logger closed")
	}
	select {
	case err := <-ack:
		return err
	case <-time.After(10 * time.Second):
		return fmt.Errorf("flush timeout")
	case <-l.ctx.Done():
		return fmt.Errorf("logger closed")
	}
}

// Close stops accepting new entries, drains the channel, and waits for the
// consumer to exit. Safe to call multiple times. Returns the last persistence
// error seen during the final drain, or a close-timeout error if the consumer
// did not exit within CloseTimeout.
func (l *AsyncLogger) Close() error {
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
			l.closeErr = fmt.Errorf("close timeout: consumer goroutine did not exit in time")
		}
		if l.closeErr == nil {
			l.closeErr = l.consumerErr
		}
		if d := atomic.LoadInt64(&l.droppedCount); d > 0 {
			klog.Warningf("AsyncLogger closed with %d logs dropped", d)
		}
	})
	return l.closeErr
}

var _ userlogger.UserLogger = (*AsyncLogger)(nil)
