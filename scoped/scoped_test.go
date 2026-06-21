package scoped_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gaozebin3/userlogger/scoped"
)

type mock struct {
	mu   sync.Mutex
	logs []string
}

func (m *mock) Log(s string)             { m.mu.Lock(); m.logs = append(m.logs, s); m.mu.Unlock() }
func (m *mock) Logf(f string, a ...any)  { m.Log(fmt.Sprintf(f, a...)) }
func (m *mock) Info(s string)            { m.mu.Lock(); m.logs = append(m.logs, "[INFO] "+s); m.mu.Unlock() }
func (m *mock) Infof(f string, a ...any) { m.Info(fmt.Sprintf(f, a...)) }
func (m *mock) Error(s string)           { m.mu.Lock(); m.logs = append(m.logs, "[ERROR] "+s); m.mu.Unlock() }
func (m *mock) Errorf(f string, a ...any) error {
	m.mu.Lock()
	e := fmt.Errorf(f, a...)
	m.logs = append(m.logs, "[ERROR] "+e.Error())
	m.mu.Unlock()
	return e
}
func (m *mock) Flush() error { return nil }
func (m *mock) get() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.logs...)
}

func TestScopePrefix(t *testing.T) {
	m := &mock{}
	scoped.New(m, "metadata").Info("test")
	if !strings.Contains(m.get()[0], "[metadata] test") {
		t.Errorf("got %q", m.get()[0])
	}
}

func TestAppendJoinsScope(t *testing.T) {
	m := &mock{}
	scoped.New(m, "a").Append("b").Info("x")
	if !strings.Contains(m.get()[0], "[a/b] x") {
		t.Errorf("got %q", m.get()[0])
	}
}

func TestErrorfPreservesWrap(t *testing.T) {
	m := &mock{}
	root := errors.New("root")
	err := scoped.New(m, "app/100%").Errorf("fail: %w", root)
	if !errors.Is(err, root) {
		t.Fatal("should wrap")
	}
	if strings.Contains(m.get()[0], "%!w") {
		t.Fatalf("artifact in %q", m.get()[0])
	}
}

func TestPercentInScope(t *testing.T) {
	m := &mock{}
	// scope 段里的 '%' 不能被当作 fmt 动词重新解析
	scoped.New(m, "load-100%").Logf("progress %d%%", 50)
	if !strings.Contains(m.get()[0], "[load-100%] progress 50%") {
		t.Errorf("got %q", m.get()[0])
	}
}

func TestConcurrentSafety(t *testing.T) {
	m := &mock{}
	base := scoped.New(m, "root")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			base.Append(fmt.Sprintf("w-%d", id)).Info("msg")
		}(i)
	}
	wg.Wait()
	if len(m.get()) != 100 {
		t.Errorf("expected 100 logs, got %d", len(m.get()))
	}
}

func TestImmutability(t *testing.T) {
	m := &mock{}
	l1 := scoped.New(m, "a")
	l2 := l1.Append("b")
	l3 := l2.Append("c")
	l1.Info("1")
	l2.Info("2")
	l3.Info("3")
	logs := m.get()
	if !strings.Contains(logs[0], "[a] 1") {
		t.Errorf("l1: %q", logs[0])
	}
	if !strings.Contains(logs[1], "[a/b] 2") {
		t.Errorf("l2: %q", logs[1])
	}
	if !strings.Contains(logs[2], "[a/b/c] 3") {
		t.Errorf("l3: %q", logs[2])
	}
}

func TestAllMethods(t *testing.T) {
	m := &mock{}
	l := scoped.New(m, "t")
	l.Log("a")
	l.Logf("b %d", 1)
	l.Info("c")
	l.Infof("d %d", 2)
	l.Error("e")
	l.Errorf("f: %v", fmt.Errorf("x"))
	if len(m.get()) != 6 {
		t.Errorf("expected 6, got %d", len(m.get()))
	}
}
