package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Init configures the global slog logger.
// JSON output by default; text when LOG_OUTPUT=text. Log level controlled
// by LOG_LEVEL (debug|info|warn|error), default info.
//
// We deliberately do NOT consult DEV_MODE here. Cross-package DEV_MODE
// reads bypass the build-tag-gated `devModeEnabled()` seam in
// services/{api,ingestion}/cmd/devmode_*.go (B1.7 layer 3 — plan §4.10.2),
// re-introducing the runtime-bypass attack the convention is meant to
// close. Local dev that wants text output sets LOG_OUTPUT=text directly
// (scripts/start.sh does this when starting host-mode services).
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
	if os.Getenv("LOG_OUTPUT") == "text" {
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
