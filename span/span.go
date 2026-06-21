// Package span 提供计时 Span，输出带耗时的成功/失败状态。
//
// 用法（通常经由 *userlogger.Logger.StartSpan 间接使用）：
//
//	s := span.New(logger, "部署应用")
//	defer func() { if err != nil { s.End(err) } else { s.Done() } }()
package span

import (
	"fmt"
	"time"

	"github.com/gaozebin3/userlogger/internal/ulog"
)

type impl struct {
	logger    ulog.UserLogger
	name      string
	startTime time.Time
}

// New 创建立即开始计时的 Span。
func New(logger ulog.UserLogger, name string) ulog.Span {
	return &impl{logger: logger, name: name, startTime: time.Now()}
}

// End 结束 span：
//   - 成功："✓ <name> done (<duration>)"
//   - 失败："✗ <name> failed (<duration>): <err>"
func (s *impl) End(err error) {
	d := time.Since(s.startTime)
	if err != nil {
		s.logger.Errorf("✗ %s failed (%s): %v", s.name, formatDuration(d), err)
	} else {
		s.logger.Infof("✓ %s done (%s)", s.name, formatDuration(d))
	}
}

func (s *impl) Done() { s.End(nil) }

// formatDuration 友好时长：<1s → "500ms"，<1m → "1.2s"，≥1m → "2.5m"。
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	} else if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}
