package observability

import (
	"context"
	"fmt"
	"log/slog"
)

// LogError logs an error with structured context using slog.
// Use this for important error conditions that should be visible in logs.
//
// Example:
//
//	observability.LogError(ctx, err, "operation", "fetch_costs", "account_id", accountID)
func LogError(ctx context.Context, err error, tags ...any) {
	if err == nil {
		return
	}
	slog.ErrorContext(ctx, "error",
		append([]any{"error", err}, tags...)...,
	)
}

// LogWarn logs a warning with structured context.
func LogWarn(ctx context.Context, message string, tags ...any) {
	slog.WarnContext(ctx, message, tags...)
}

// LogInfo logs an informational message with structured context.
func LogInfo(ctx context.Context, message string, tags ...any) {
	slog.InfoContext(ctx, message, tags...)
}
