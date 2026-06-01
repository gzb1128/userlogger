# userlogger

A Go logging library for user-facing operation logs with scope grouping, span timing, klog dual-write, and async batched writes.

## Features

- `UserLogger` core interface with plain, formatted, and error log methods plus `Flush`
- `async.AsyncLogger` — non-blocking, channel-based batched writes
- `scoped` — scope prefix decorator for grouping logs by business context
- `span` — operation duration tracking
- Automatic `klog` dual-write when a `klog.Logger` is present in context

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

`writer` must implement:

```go
type LogWriter interface {
    WriteLog(ctx context.Context, content string) error
}
```

## Packages

- `userlogger` — core interfaces, context helpers, klog dual-write
- `async` — async batched writer implementation
- `scoped` — scope prefix decorator
- `span` — timed span implementation

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `ChannelBufferCount` | `10000` | channel capacity |
| `BatchSize` | `50` | entries per batch |
| `FlushInterval` | `3s` | periodic flush interval |
| `MaxRetry` | `3` | write retries, `0` disables |
| `WriteTimeout` | `5s` | per-write timeout |
| `CloseTimeout` | `10s` | close drain timeout |

## License

MIT
