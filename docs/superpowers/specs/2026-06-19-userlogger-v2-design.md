# userlogger v2 — 终端接口 + 选择性 klog 双写

## 背景

userlogger 面向**终端用户**输出操作日志（类 shell 终端文本流），落库后供用户查看（如任务执行日志）。
klog 面向**运维**，结构化、带 traceID/taskID 等内部上下文。两类受众、两种格式、两种传输，
因此 userlogger 是独立于 klog 的专用接口，而非 klog 的全局拦截层。

核心需求（已在生产 dms-api-server 中验证）：

1. 用户 dst = 按任务追加的纯文本日志（append-mode），用户像看终端一样阅读。
2. 内部上下文（pin/traceID 等）**不得**泄露到用户文本。
3. 用户可见的每条日志**同时**镜像写入 klog（运维可见），并自动继承 klog ctx 中的结构化字段。
4. klog 自身的直接调用**只**进 klog，永不进用户 dst。

## v1 的问题

- 根包重复实现了 `span`/`scoped`（`spanImpl`/`fmtDur`/`klogScoped`/`klogSpan`），根因是
  `scoped`/`span` 导入根接口 → 根无法反向导入它们 → 只能复制。
- `WithScope`/`StartSpan` 放在接口上，迫使每个后端（DB/noop/klog 包装器）都实现它们，
  正是 klog 包装器重复 scope/span 的原因。

## v2 设计

### 包结构（用叶子接口包打破循环）

```
userlogger/
  userlogger.go            // 门面：FromContext、*Logger、NewContext、noop、
                           //   未导出 klogMirror；re-export UserLogger/Span
  internal/ulog/ulog.go    // 叶子：UserLogger + Span 接口（仅依赖标准库）
  scoped/scoped.go         // scope 前缀装饰器（导入 ulog）
  span/span.go             // 计时 span 装饰器（导入 ulog）
  async/async.go           // AsyncLogger + LogWriter（导入 ulog）
```

- `internal/ulog` 无依赖 → 打破循环。
- `scoped`/`span`/`async` 导入 `ulog`，不导入根。
- 根导入 `ulog` + `scoped` + `span`；通过类型别名暴露 `userlogger.UserLogger`。

### 接口（瘦身）

```go
type UserLogger interface {
    Log(message string)
    Logf(format string, args ...any)
    Info(message string)
    Infof(format string, args ...any)
    Error(message string)
    Errorf(format string, args ...any) error
    Flush() error
}
type Span interface { End(err error); Done() }
```

`WithScope`/`StartSpan` **移出接口**，仅存在于具体类型 `*Logger` 上。
后端（async/noop/dbWriter）只实现 8 个输出方法。

### `*Logger` 门面 + `FromContext`

- `FromContext(ctx) *Logger`：取 base UserLogger（来自 ctx），若 ctx 也有 klog，
  则用未导出的 `klogMirror{base, klogger}` 包一层；返回 `&Logger{sink}`。
- `*Logger` 的输出方法委托给 `sink`；`WithScope` 返回新的 `*Logger`，
  其 sink = `scoped.New(l.sink, s)`；`StartSpan` = `span.New(l.sink, name)`。
- `klogMirror` 只实现 8 个输出方法（`klog.Info(m); base.Info(m)`），
  **不再有 klogScoped/klogSpan** —— scope/span 在 mirror 外层由 `*Logger` 套用。

### 数据流（选择性双写）

请求入口：`aw := async.New(dbWriter, opts); ctx = userlogger.NewContext(ctx, aw)`，
klog ctx 已由运维侧注入 traceID/taskID。

- `ul.Info("deploying")` → mirror → `klog.Info("deploying")`（+结构化字段）+ `aw.Info` → `[ts] deploying` 入库。
- `ul.WithScope("中间件/Redis").Info("starting")` → scoped 加前缀 → mirror 双写 →
  库得到 `[ts] [中间件/Redis] starting`；klog 得到带 scope 与结构化字段的消息。
- 直接 `klog.FromContext(ctx).Info(...)`（系统日志）→ **只**进 klog，不进库。✓ 无泄露。

### 删除 / 保留

- **删除**：根 `spanImpl`/`fmtDur`、`klogScoped`/`klogScopedUserLogger`、`klogSpan`、
  `formatScope`/`prefixFmt`、`async.WithScope`/`async.StartSpan`。
- **保留**：`async.AsyncLogger` 核心（channel/batch/retry/close + `LogWriter`）、
  时间戳/scope/span 文本格式（与生产输出一致）、现有强化测试。
- **生产迁移**：`DatabaseUserLogger` 收缩为 `dbWriter` 适配器，实现
  `WriteLog(ctx, content)` 调 `UpdateTaskActionLog`，再 `async.New(dbWriter, opts)`。

### 非目标（留扩展点）

- 用户 dst 的结构化输出（"配置项，默认关闭"）：`LogWriter.WriteLog(ctx, content string)`
  保持默认；未来可由 `async` 探测 `StructuredWriter` 接口实现。当前不建。
- logr 桥接：本期不做（瘦接口已是扩展点）。
