package span_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gaozebin3/userlogger/span"
)

type mock struct{ logs []string }

func (m *mock) Log(s string)             { m.logs = append(m.logs, s) }
func (m *mock) Logf(f string, a ...any)  { m.logs = append(m.logs, fmt.Sprintf(f, a...)) }
func (m *mock) Info(s string)            { m.logs = append(m.logs, "[INFO] "+s) }
func (m *mock) Infof(f string, a ...any) { m.Info(fmt.Sprintf(f, a...)) }
func (m *mock) Error(s string)           { m.logs = append(m.logs, "[ERROR] "+s) }
func (m *mock) Errorf(f string, a ...any) error {
	e := fmt.Errorf(f, a...)
	m.logs = append(m.logs, "[ERROR] "+e.Error())
	return e
}
func (m *mock) Flush() error { return nil }

func TestSpan_Done(t *testing.T) {
	m := &mock{}
	span.New(m, "deploy").Done()
	if !strings.Contains(m.logs[0], "✓ deploy done") {
		t.Errorf("got %q", m.logs[0])
	}
}

func TestSpan_EndErr(t *testing.T) {
	m := &mock{}
	span.New(m, "deploy").End(fmt.Errorf("boom"))
	if !strings.Contains(m.logs[0], "✗ deploy failed") || !strings.Contains(m.logs[0], "boom") {
		t.Errorf("got %q", m.logs[0])
	}
}

func TestSpan_Duration(t *testing.T) {
	m := &mock{}
	s := span.New(m, "op")
	time.Sleep(15 * time.Millisecond)
	s.Done()
	if !strings.Contains(m.logs[0], "ms") && !strings.Contains(m.logs[0], "s") {
		t.Errorf("missing duration in %q", m.logs[0])
	}
}
