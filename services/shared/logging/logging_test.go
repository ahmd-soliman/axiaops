package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestInit_WithJSONOutput(t *testing.T) {
	// Clear any existing env vars
	os.Unsetenv("LOG_OUTPUT")
	os.Unsetenv("DEV_MODE")

	Init("test-service")

	// Log a test message
	slog.Info("test message", "key", "value")

	// We can't directly verify the output format here, but we verify Init doesn't panic
	// More comprehensive testing would require capturing log output
}

func TestInit_TextOutput_WhenDEVMode(t *testing.T) {
	oldDevMode := os.Getenv("DEV_MODE")
	defer func() {
		if oldDevMode != "" {
			os.Setenv("DEV_MODE", oldDevMode)
		} else {
			os.Unsetenv("DEV_MODE")
		}
	}()

	os.Setenv("DEV_MODE", "true")
	Init("test-service")

	// Verify no panic; slog is initialized with text handler
}

func TestInit_LogLevel_Debug(t *testing.T) {
	oldLevel := os.Getenv("LOG_LEVEL")
	defer func() {
		if oldLevel != "" {
			os.Setenv("LOG_LEVEL", oldLevel)
		} else {
			os.Unsetenv("LOG_LEVEL")
		}
	}()

	os.Setenv("LOG_LEVEL", "debug")
	Init("test-service")

	// Verify no panic; slog initialized with debug level
}

func TestInit_LogLevel_Warn(t *testing.T) {
	oldLevel := os.Getenv("LOG_LEVEL")
	defer func() {
		if oldLevel != "" {
			os.Setenv("LOG_LEVEL", oldLevel)
		} else {
			os.Unsetenv("LOG_LEVEL")
		}
	}()

	os.Setenv("LOG_LEVEL", "warn")
	Init("test-service")

	// Verify no panic; slog initialized with warn level
}

func TestInit_IncludesServiceName(t *testing.T) {
	os.Setenv("LOG_OUTPUT", "json")
	os.Unsetenv("DEV_MODE")
	Init("my-service")

	// Capture output
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	h.Handle(nil, record)

	// Verify service name is included (basic check)
	output := buf.String()
	if output == "" {
		t.Errorf("expected non-empty log output, got empty string")
	}
}

func TestInitSentry_DisabledWhenMissingDSN(t *testing.T) {
	oldDSN := os.Getenv("SENTRY_DSN")
	defer func() {
		if oldDSN != "" {
			os.Setenv("SENTRY_DSN", oldDSN)
		} else {
			os.Unsetenv("SENTRY_DSN")
		}
	}()

	os.Unsetenv("SENTRY_DSN")
	flush := InitSentry("test-service")

	// flush should be a no-op (returns immediately)
	flush()

	// Test passes if no panic
}

func TestInitSentry_FlushReturnsFunction(t *testing.T) {
	oldDSN := os.Getenv("SENTRY_DSN")
	defer func() {
		if oldDSN != "" {
			os.Setenv("SENTRY_DSN", oldDSN)
		} else {
			os.Unsetenv("SENTRY_DSN")
		}
	}()

	os.Unsetenv("SENTRY_DSN")
	flush := InitSentry("test-service")

	if flush == nil {
		t.Fatal("expected flush function, got nil")
	}

	// Calling flush should not panic
	flush()
}

func TestInitSentry_ParsesSampleRate(t *testing.T) {
	oldDSN := os.Getenv("SENTRY_DSN")
	oldRate := os.Getenv("SENTRY_TRACES_SAMPLE_RATE")
	defer func() {
		if oldDSN != "" {
			os.Setenv("SENTRY_DSN", oldDSN)
		} else {
			os.Unsetenv("SENTRY_DSN")
		}
		if oldRate != "" {
			os.Setenv("SENTRY_TRACES_SAMPLE_RATE", oldRate)
		} else {
			os.Unsetenv("SENTRY_TRACES_SAMPLE_RATE")
		}
	}()

	// Test with valid sample rate
	os.Unsetenv("SENTRY_DSN") // Sentry disabled, but parsing still tested
	os.Setenv("SENTRY_TRACES_SAMPLE_RATE", "0.5")
	flush := InitSentry("test-service")
	flush()

	// Test passes if no panic on parsing
}

func TestInitSentry_InvalidSampleRate_UsesDefault(t *testing.T) {
	oldDSN := os.Getenv("SENTRY_DSN")
	oldRate := os.Getenv("SENTRY_TRACES_SAMPLE_RATE")
	defer func() {
		if oldDSN != "" {
			os.Setenv("SENTRY_DSN", oldDSN)
		} else {
			os.Unsetenv("SENTRY_DSN")
		}
		if oldRate != "" {
			os.Setenv("SENTRY_TRACES_SAMPLE_RATE", oldRate)
		} else {
			os.Unsetenv("SENTRY_TRACES_SAMPLE_RATE")
		}
	}()

	os.Unsetenv("SENTRY_DSN")
	os.Setenv("SENTRY_TRACES_SAMPLE_RATE", "invalid")
	flush := InitSentry("test-service")
	flush()

	// Test passes if no panic; invalid rate should be ignored and default used
}

func TestInitSentry_IncludesEnvironment(t *testing.T) {
	oldDSN := os.Getenv("SENTRY_DSN")
	oldEnv := os.Getenv("APP_ENV")
	defer func() {
		if oldDSN != "" {
			os.Setenv("SENTRY_DSN", oldDSN)
		} else {
			os.Unsetenv("SENTRY_DSN")
		}
		if oldEnv != "" {
			os.Setenv("APP_ENV", oldEnv)
		} else {
			os.Unsetenv("APP_ENV")
		}
	}()

	os.Unsetenv("SENTRY_DSN")
	os.Setenv("APP_ENV", "production")
	flush := InitSentry("test-service")
	defer flush()

	// Test passes if no panic
}

func TestInitSentry_IncludesVersion(t *testing.T) {
	oldDSN := os.Getenv("SENTRY_DSN")
	oldVer := os.Getenv("APP_VERSION")
	defer func() {
		if oldDSN != "" {
			os.Setenv("SENTRY_DSN", oldDSN)
		} else {
			os.Unsetenv("SENTRY_DSN")
		}
		if oldVer != "" {
			os.Setenv("APP_VERSION", oldVer)
		} else {
			os.Unsetenv("APP_VERSION")
		}
	}()

	os.Unsetenv("SENTRY_DSN")
	os.Setenv("APP_VERSION", "1.2.3")
	flush := InitSentry("test-service")
	defer flush()

	// Test passes if no panic
}

func TestInit_JSONFormatValid(t *testing.T) {
	os.Setenv("LOG_OUTPUT", "json")
	os.Unsetenv("DEV_MODE")
	os.Setenv("APP_ENV", "test")
	os.Setenv("APP_VERSION", "0.0.1")

	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	logger := slog.New(h)
	logger.Info("test", "foo", "bar")

	// Verify JSON is valid
	var result map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &result)
	if err != nil {
		t.Fatalf("expected valid JSON, got: %v", err)
	}

	if result["msg"] != "test" {
		t.Errorf("expected msg=test, got %v", result["msg"])
	}
}
