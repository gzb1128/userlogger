# userlogger

Go 业务日志库，专为面向用户的操作日志设计。提供 scope 分组、span 耗时追踪、klog 双写和异步批量写入。

## 特性

- **Scope 分组** — `[service-deploy/payment-api]` 前缀，区分不同业务模块日志
- **Span 耗时追踪** — `✓ deploy done (1.2s)` / `✗ deploy failed (3.5s): timeout`
- **异步批量写入** — 非阻塞 channel 架构，生产者零等待；可配置批量大小、刷盘间隔、重试次数
- **klog 双写** — 自动检测 `klog.Logger` context，同时写用户日志和系统日志
- **context 传播** — `userlogger.NewContext` / `userlogger.FromContext`，缺失时自动降级为 no-op
- **可插拔后端** — 实现 `async.LogWriter` 接口即可对接数据库、文件、MQ 等

## 安装

```bash
go get github.com/gaozebin3/userlogger
```

## 快速开始

### 基本用法

```go
ul := userlogger.FromContext(ctx)
ul.Info("starting deployment")
ul.Logf("deployed %d instances", 10)
ul.Error("deployment failed")
```

### Scope — 业务分组

```go
deployLogger := ul.WithScope("service-deploy/order-service")
deployLogger.Info("created")
// [service-deploy/order-service] created

envLogger := deployLogger.WithScope("env-setup")
envLogger.Info("done")
// [service-deploy/order-service/env-setup] done
```

### Span — 耗时追踪

```go
sp := ul.StartSpan("deploy application")
defer func() {
    if err != nil {
        sp.End(err)
    } else {
        sp.Done()
    }
}()
```

### 异步写入

实现 `LogWriter` 接口对接后端：

```go
type dbWriter struct{ db *sql.DB }

func (w *dbWriter) WriteLog(ctx context.Context, content string) error {
    _, err := w.db.ExecContext(ctx, "INSERT INTO logs (content) VALUES (?)", content)
    return err
}
```

创建并注入 context：

```go
logger := async.New(&dbWriter{db: db}, async.DefaultOptions())
defer logger.Close()
ctx = userlogger.NewContext(ctx, logger)

ul := userlogger.FromContext(ctx)
ul.Info("hello")
```

## 包结构

```
userlogger/
├── userlogger.go      # 核心接口 UserLogger、Span，context helper，klog 双写
├── async/
│   └── async.go       # AsyncLogger — channel 异步批量写入
├── scoped/
│   └── scoped.go      # ScopedUserLogger — 不可变 scope 装饰器
└── span/
    └── span.go        # 计时 Span — ✓ done / ✗ failed + 耗时
```

## 配置项

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `ChannelBufferCount` | 10000 | channel 容量 |
| `BatchSize` | 50 | 每批写入条数 |
| `FlushInterval` | 3s | 定时刷盘间隔 |
| `MaxRetry` | 3 | 写入重试次数（0 = 不重试） |
| `WriteTimeout` | 5s | 单次 WriteLog 超时 |
| `CloseTimeout` | 10s | Close 等待消费者退出超时 |

## 依赖

- [go-logr/logr](https://github.com/go-logr/logr)
- [k8s.io/klog/v2](https://github.com/kubernetes/klog)

## License

MIT
