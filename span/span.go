// Package span provides a simple timed Span implementation that logs
// success/failure status with elapsed duration.
//
// # Usage
//
//	s := span.New(logger, "deploy application")
//	defer func() { if err != nil { s.End(err) } else { s.Done() } }()
package span

import (
	"fmt"
	"time"

	"github.com/gaozebin3/userlogger"
)

type impl struct {
	logger    userlogger.UserLogger
	name      string
	startTime time.Time
}

// New creates a Span that starts timing immediately.
func New(logger userlogger.UserLogger, name string) userlogger.Span {
	return &impl{logger: logger, name: name, startTime: time.Now()}
}

// End completes the span:
//   - success: "✓ <name> done (<duration>)"
//   - failure: "✗ <name> failed (<duration>): <err>"
func (s *impl) End(err error) {
	d := time.Since(s.startTime)
	if err != nil {
		s.logger.Errorf("✗ %s failed (%s): %v", s.name, formatDuration(d), err)
	} else {
		s.logger.Infof("✓ %s done (%s)", s.name, formatDuration(d))
	}
}

func (s *impl) Done() { s.End(nil) }

// formatDuration renders a human-friendly duration:
// <1s → "500ms", <1m → "1.2s", ≥1m → "2.5m".
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	} else if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}
