# Logging

AxiaOps uses Go's standard `log/slog` package for structured logging, supplemented by Sentry for error tracking in production.

## Setup

Both services call two functions at startup, before any other initialization:

```go
logging.Init("api") // or "ingestion"
flush := logging.InitSentry("api")
defer flush()
```

Both live in `services/shared/logging/logging.go`.

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

## Sentry

Sentry captures errors and (optionally) traces in non-dev environments.

### Environment variables

| Variable | Required | Description |
|---|---|---|
| `SENTRY_DSN` | Yes (to enable) | Project DSN from the Sentry dashboard. Leave empty to disable. |
| `APP_ENV` | No | Environment tag, e.g. `production`, `staging` |
| `APP_VERSION` | No | Release tag shown in Sentry, e.g. `1.2.3` |
| `SENTRY_TRACES_SAMPLE_RATE` | No | Float `0`–`1`. Default `0.1` (10 % of transactions). |

When `SENTRY_DSN` is empty, `InitSentry` logs a warning and returns a no-op flush function — the service still starts normally.

### Flushing

Sentry buffers events and sends them in the background. The flush function returned by `InitSentry` drains the buffer (waits up to 2 seconds) before the process exits. Always `defer` it:

```go
flush := logging.InitSentry("api")
defer flush()
```

Without this, events captured near shutdown may be dropped.

## Environment variable summary

| Variable | Service | Default | Notes |
|---|---|---|---|
| `DEV_MODE` | both | `false` | `true` → text log output |
| `LOG_OUTPUT` | both | `json` | Set to `text` for human-readable output without enabling full dev mode |
| `LOG_LEVEL` | both | `info` | `debug` \| `info` \| `warn` \| `error` |
| `APP_ENV` | both | _(empty)_ | Attached to every log line as `env` and passed to Sentry |
| `APP_VERSION` | both | _(empty)_ | Attached to every log line as `version` and passed to Sentry |
| `SENTRY_DSN` | both | _(empty)_ | Leave empty to disable Sentry |
| `SENTRY_TRACES_SAMPLE_RATE` | both | `0.1` | Set to `0` to disable tracing |
