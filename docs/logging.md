# Logging

AxiaOps uses Go's standard `log/slog` package for structured logging.

## Setup

Both services call `logging.Init()` at startup, before any other initialization:

```go
logging.Init("api") // or "ingestion"
```

This lives in `services/shared/logging/logging.go`.

## Output format

| Condition | Format |
|---|---|
| `DEV_MODE=true` | Human-readable text (`key=value`) |
| `LOG_OUTPUT=text` | Human-readable text (`key=value`) |
| Default (production) | JSON, one object per line |

JSON output is structured for log aggregators (CloudWatch Logs, Datadog, etc.).

## Log level

Controlled by the `LOG_LEVEL` environment variable. Accepted values (case-insensitive):

| Value | Level |
|---|---|
| `debug` | Debug and above |
| `info` | Info and above (default) |
| `warn` | Warn and above |
| `error` | Errors only |

## Writing logs

Use the global `slog` functions directly — `Init()` sets the default logger so there is no package-level logger to import:

```go
slog.Info("server started", "addr", addr)
slog.Error("db connection failed", "error", err)
slog.Debug("incoming request", "method", r.Method, "path", r.URL.Path)
```

Prefer key-value pairs over format strings. Never log raw secret values (`secret_key`, `SecretEncrypted`, etc.).


## Environment variable summary

| Variable | Service | Default | Notes |
|---|---|---|---|
| `DEV_MODE` | both | `false` | `true` → text log output |
| `LOG_OUTPUT` | both | `json` | Set to `text` for human-readable output without enabling full dev mode |
| `LOG_LEVEL` | both | `info` | `debug` \| `info` \| `warn` \| `error` |
| `APP_ENV` | both | _(empty)_ | Attached to every log line as `env` |
| `APP_VERSION` | both | _(empty)_ | Attached to every log line as `version` |
