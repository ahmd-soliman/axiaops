package retry

import (
	"context"
	"log/slog"
	"math"
	"time"

	"axiaops.io/shared/errors"
)

// Config holds retry configuration
type Config struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultConfig returns sensible defaults for AWS API retries
func DefaultConfig() Config {
	return Config{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    5 * time.Second,
	}
}

// IsRetryable determines if an error should be retried
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Use error categorization for better retry decisions
	catErr := errors.Categorize(err, "")
	return catErr.Category.IsRetryable()
}

// Do executes fn with exponential backoff retry logic
func Do(ctx context.Context, config Config, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(float64(config.BaseDelay) * math.Pow(2, float64(attempt-1)))
			if delay > config.MaxDelay {
				delay = config.MaxDelay
			}

			slog.Debug("retry: backing off", "attempt", attempt, "delay_ms", delay.Milliseconds())
			
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		lastErr = fn()
		if lastErr == nil {
			if attempt > 0 {
				slog.Info("retry: succeeded", "attempts", attempt+1)
			}
			return nil
		}

		if !IsRetryable(lastErr) {
			slog.Debug("retry: non-retryable error", "error", lastErr)
			return lastErr
		}

		slog.Warn("retry: retryable error", "attempt", attempt+1, "error", lastErr)
	}

	slog.Error("retry: max attempts exceeded", "attempts", config.MaxAttempts, "last_error", lastErr)
	return lastErr
}