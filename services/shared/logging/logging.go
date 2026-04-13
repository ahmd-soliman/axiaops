package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Init configures the global slog logger.
// JSON output when LOG_OUTPUT=json or DEV_MODE is unset; text otherwise.
// Log level controlled by LOG_LEVEL (debug|info|warn|error), default info.
func Init(service string) {
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
	attrs := []any{"service", service}
	if env := os.Getenv("APP_ENV"); env != "" {
		attrs = append(attrs, "env", env)
	}
	if version := os.Getenv("APP_VERSION"); version != "" {
		attrs = append(attrs, "version", version)
	}
	slog.SetDefault(slog.New(handler).With(attrs...))
}
