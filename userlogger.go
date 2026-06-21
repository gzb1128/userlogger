// Package userlogger 提供面向终端用户的操作日志：类 shell 终端的简洁文本流，
// 落库后供最终用户查看；同时把每条用户日志选择性地镜像写入 klog（运维侧）。
//
// 两类受众、两种格式：
//   - 用户（本包）：简单文本，不带内部上下文（pin/traceID 等）。
//   - 运维（klog）：结构化、带完整上下文；klog 自身的直接调用不会进入用户 dst。
//
// # 快速开始
//
// 假设 ctx 已携带 UserLogger（见下文 Context）：
//
//	ul := userlogger.FromContext(ctx)
//	ul.Info("starting deployment")         // 带时间戳
//	ul.Logf("deployed %d instances", 10)    // 不带时间戳
//	ul.Log("intermediate output")           // 纯文本
//	ul.Error("deployment failed")           // 带时间戳错误
//
// # Scope —— 按业务上下文分组
//
//	deploy := ul.WithScope("service-deploy/order-service")
//	env := deploy.WithScope("env-setup")    // 第三层，建议保持 2-3 层
//
// 用业务语义命名（动作/对象），而非内部代码路径。
//
// # Span —— 跟踪操作耗时
//
//	span := ul.StartSpan("deploy application")
//	defer func() { if err != nil { span.End(err) } else { span.Done() } }()
//
// # Context
//
//	// 注入：base 通常为 async.AsyncLogger
//	ctx = userlogger.NewContext(ctx, logger)
//
//	// 取出：若无 UserLogger 返回 no-op；若 ctx 同时含 klog.Logger，则返回
//	// 会镜像写入 klog 的 *Logger。
//	ul := userlogger.FromContext(ctx)
//
// # 子包
//
//   - scoped: scope 前缀装饰器
//   - span:   计时 span
//   - async:  基于 channel 的非阻塞批量 writer（LogWriter 接口）
package userlogger

import (
	"context"
	"fmt"

	"github.com/gaozebin3/userlogger/internal/ulog"
	"github.com/gaozebin3/userlogger/scoped"
	"github.com/gaozebin3/userlogger/span"
	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
)

// UserLogger 是面向终端用户的日志接口（ulog.UserLogger 的别名，方法集定义在 internal/ulog）。
// WithScope/StartSpan 不在接口上：它们由 FromContext 返回的 *Logger 提供，
// 因此后端只需实现 UserLogger 的输出方法（见 internal/ulog/ulog.go）。
type UserLogger = ulog.UserLogger

// Span 跟踪一次操作的耗时与成败。
type Span = ulog.Span

type ctxKey struct{}

var contextKey = ctxKey{}

// Logger 是面向调用方的门面类型，由 FromContext 构造。它在 sink 之上提供
// WithScope/StartSpan 链式调用；sink 可能是 async 后端，或包了一层 klog 镜像。
type Logger struct {
	sink ulog.UserLogger
}

func (l *Logger) Log(message string)                      { l.sink.Log(message) }
func (l *Logger) Logf(format string, args ...any)         { l.sink.Logf(format, args...) }
func (l *Logger) Info(message string)                     { l.sink.Info(message) }
func (l *Logger) Infof(format string, args ...any)        { l.sink.Infof(format, args...) }
func (l *Logger) Error(message string)                    { l.sink.Error(message) }
func (l *Logger) Errorf(format string, args ...any) error { return l.sink.Errorf(format, args...) }
func (l *Logger) Flush() error                            { return l.sink.Flush() }

// WithScope 返回一个带 scope 前缀的新 *Logger。连续调用会把 scope 段拼接成
// [a/b/c]（而非嵌套的多个括号）。原 Logger 不受影响（不可变）。
func (l *Logger) WithScope(scope string) *Logger {
	if sc, ok := l.sink.(*scoped.ScopedUserLogger); ok {
		return &Logger{sink: sc.Append(scope)}
	}
	return &Logger{sink: scoped.New(l.sink, scope)}
}

// StartSpan 创建计时 span；输出经由当前 sink（含 scope 与 klog 镜像）。
func (l *Logger) StartSpan(name string) Span {
	return span.New(l.sink, name)
}

// FromContext 从 ctx 取出 UserLogger 并返回门面 *Logger。
// 若 ctx 无 UserLogger，使用 no-op；若 ctx 同时含 klog.Logger，sink 会额外镜像写入 klog。
func FromContext(ctx context.Context) *Logger {
	base, _ := ctx.Value(contextKey).(ulog.UserLogger)
	if base == nil {
		base = noopUserLogger{}
	}
	sink := base
	if _, err := logr.FromContext(ctx); err == nil {
		sink = &klogMirror{base: base, klogger: klog.FromContext(ctx)}
	}
	return &Logger{sink: sink}
}

// NewContext 返回携带给定 UserLogger 的 ctx。
func NewContext(ctx context.Context, ul UserLogger) context.Context {
	return context.WithValue(ctx, contextKey, ul)
}

// --- no-op ---

type noopUserLogger struct{}

func (noopUserLogger) Log(string)                      {}
func (noopUserLogger) Logf(string, ...any)             {}
func (noopUserLogger) Info(string)                     {}
func (noopUserLogger) Infof(string, ...any)            {}
func (noopUserLogger) Error(string)                    {}
func (noopUserLogger) Errorf(f string, a ...any) error { return fmt.Errorf(f, a...) }
func (noopUserLogger) Flush() error                    { return nil }

var _ ulog.UserLogger = noopUserLogger{}

// --- klog 镜像（单向：用户日志 → klog；klog 直接调用不进用户 dst）---

// klogMirror 把每条输出同时写入 base（用户 dst）与 klog（运维）。
// 它只实现输出方法：scope/span 由 *Logger 在其外层套用，故此处无重复实现。
type klogMirror struct {
	base    ulog.UserLogger
	klogger klog.Logger
}

func (k *klogMirror) Log(m string) { k.klogger.Info(m); k.base.Log(m) }
func (k *klogMirror) Logf(f string, a ...any) {
	k.klogger.Info(fmt.Sprintf(f, a...))
	k.base.Logf(f, a...)
}
func (k *klogMirror) Info(m string) { k.klogger.Info(m); k.base.Info(m) }
func (k *klogMirror) Infof(f string, a ...any) {
	k.klogger.Info(fmt.Sprintf(f, a...))
	k.base.Infof(f, a...)
}
func (k *klogMirror) Error(m string) { k.klogger.Error(nil, m); k.base.Error(m) }
func (k *klogMirror) Errorf(f string, a ...any) error {
	err := fmt.Errorf(f, a...)
	k.klogger.Error(nil, err.Error())
	return k.base.Errorf(f, a...)
}
func (k *klogMirror) Flush() error { return k.base.Flush() }

var _ ulog.UserLogger = (*klogMirror)(nil)
