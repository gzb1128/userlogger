// Package ulog 定义 userlogger 的核心接口契约，作为叶子包不依赖任何其它内部包，
// 用以打破根包与 scoped/span/async 之间的导入循环。
//
// UserLogger 面向终端用户输出（类 shell 终端文本流）；Span 跟踪操作耗时与成败。
// WithScope/StartSpan 不在接口上：它们由具体类型 *userlogger.Logger 提供，
// 后端（async/noop/自定义 writer）只需实现下列输出方法。
package ulog

// UserLogger 是面向终端用户的日志接口。
//
// 两类输出：
//   - 非结构化（Log/Logf）：纯文本，不带时间戳。用于中间过程、命令输出等。
//   - 结构化（Info/Infof/Error/Errorf）：带时间戳。用于关键步骤与错误。
//
// Errorf 同时返回 error，便于 `return ul.Errorf(...)` 写法。
type UserLogger interface {
	Log(message string)
	Logf(format string, args ...any)
	Info(message string)
	Infof(format string, args ...any)
	Error(message string)
	Errorf(format string, args ...any) error
	Flush() error
}

// Span 跟踪一次操作的耗时与成败。
// End(nil) 或 Done() 表示成功；End(err) 表示失败。
type Span interface {
	End(err error)
	Done()
}
