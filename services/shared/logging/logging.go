package logging

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	sentry "github.com/getsentry/sentry-go"
)

// Init configures the global slog logger.
// JSON output when LOG_OUTPUT=json or DEV_MODE is unset; text otherwise.
// Log level controlled by LOG_LEVEL (debug|info|warn|error), default info.
func Init() {
	level := slog.LevelInfo
	if s := os.Getenv("LOG_LEVEL"); s != "" {
		switch strings.ToLower(s) {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}

	var handler slog.Handler
	if os.Getenv("LOG_OUTPUT") == "text" || os.Getenv("DEV_MODE") == "true" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}
	slog.SetDefault(slog.New(handler))
}

// InitSentry initialises the Sentry SDK from environment variables.
// Returns a flush function that must be called before the process exits.
//
// Env vars:
//
//	SENTRY_DSN                — required; disables Sentry when empty
//	APP_ENV                   — e.g. "production", "staging"
//	APP_VERSION               — release tag, e.g. "1.2.3"
//	SENTRY_TRACES_SAMPLE_RATE — float 0–1, default 0.1
func InitSentry(service string) (flush func()) {
	flush = func() {}
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		slog.Warn("sentry: SENTRY_DSN not set, error tracking disabled")
		return
	}

	tracesRate := parseSampleRate("SENTRY_TRACES_SAMPLE_RATE", 0.1)

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      os.Getenv("APP_ENV"),
		Release:          os.Getenv("APP_VERSION"),
		ServerName:       service,
		EnableTracing:    tracesRate > 0,
		TracesSampleRate: tracesRate,
	}); err != nil {
		slog.Error("sentry: initialization failed", "error", err)
		return
	}

	slog.Info("sentry: initialized", "service", service, "traces_rate", tracesRate)
	return func() { sentry.Flush(2 * time.Second) }
}

func parseSampleRate(env string, fallback float64) float64 {
	s := os.Getenv(env)
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 1 {
		slog.Warn("sentry: invalid sample rate, using default", "env", env, "value", s, "default", fallback)
		return fallback
	}
	return v
}
