# userlogger

Go 业务日志库，用于记录面向用户的操作过程日志。

## Features

- `UserLogger` 核心接口，支持普通日志、格式化日志、错误日志和 `Flush`
- `async.AsyncLogger` 提供非阻塞异步批量写入
- `scoped` 支持业务 scope 前缀，便于区分并发任务日志
- `span` 支持操作耗时追踪
- 自动兼容 `klog` context，可同时写用户日志和系统日志

## Install

```bash
go get github.com/gaozebin3/userlogger
```

## Usage

```go
logger := async.New(writer, async.DefaultOptions())
defer logger.Close()

ctx = userlogger.NewContext(ctx, logger)
ul := userlogger.FromContext(ctx)

ul.WithScope("deploy/order-service").Info("starting")

sp := ul.StartSpan("deploy application")
defer sp.Done()
```

`writer` 需要实现：

```go
type LogWriter interface {
    WriteLog(ctx context.Context, content string) error
}
```

## Packages

- `userlogger`: 核心接口、context helper、klog 双写
- `async`: 异步批量写入实现
- `scoped`: scope 前缀装饰器
- `span`: 耗时追踪实现

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `ChannelBufferCount` | `10000` | channel 容量 |
| `BatchSize` | `50` | 批量写入条数 |
| `FlushInterval` | `3s` | 定时刷盘间隔 |
| `MaxRetry` | `3` | 写入重试次数，`0` 表示不重试 |
| `WriteTimeout` | `5s` | 单次写入超时 |
| `CloseTimeout` | `10s` | 关闭等待超时 |

## License

MIT
