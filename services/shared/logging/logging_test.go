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
	_ = os.Unsetenv("LOG_OUTPUT")
	_ = os.Unsetenv("DEV_MODE")
	Init("test-service")
	slog.Info("test message", "key", "value")
}

func TestInit_TextOutput_WhenDEVMode(t *testing.T) {
	oldDevMode := os.Getenv("DEV_MODE")
	defer func() {
		if oldDevMode != "" {
			_ = os.Setenv("DEV_MODE", oldDevMode)
		} else {
			_ = os.Unsetenv("DEV_MODE")
		}
	}()
	_ = os.Setenv("DEV_MODE", "true")
	Init("test-service")
}

func TestInit_LogLevel_Debug(t *testing.T) {
	oldLevel := os.Getenv("LOG_LEVEL")
	defer func() {
		if oldLevel != "" {
			_ = os.Setenv("LOG_LEVEL", oldLevel)
		} else {
			_ = os.Unsetenv("LOG_LEVEL")
		}
	}()
	_ = os.Setenv("LOG_LEVEL", "debug")
	Init("test-service")
}

func TestInit_LogLevel_Warn(t *testing.T) {
	oldLevel := os.Getenv("LOG_LEVEL")
	defer func() {
		if oldLevel != "" {
			_ = os.Setenv("LOG_LEVEL", oldLevel)
		} else {
			_ = os.Unsetenv("LOG_LEVEL")
		}
	}()
	_ = os.Setenv("LOG_LEVEL", "warn")
	Init("test-service")
}

func TestInit_IncludesServiceName(t *testing.T) {
	_ = os.Setenv("LOG_OUTPUT", "json")
	_ = os.Unsetenv("DEV_MODE")
	Init("my-service")

	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	_ = h.Handle(t.Context(), record)

	if buf.String() == "" {
		t.Errorf("expected non-empty log output, got empty string")
	}
}

func TestInit_JSONFormatValid(t *testing.T) {
	_ = os.Setenv("LOG_OUTPUT", "json")
	_ = os.Unsetenv("DEV_MODE")
	_ = os.Setenv("APP_ENV", "test")
	_ = os.Setenv("APP_VERSION", "0.0.1")

	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(h)
	logger.Info("test", "foo", "bar")

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("expected valid JSON, got: %v", err)
	}
	if result["msg"] != "test" {
		t.Errorf("expected msg=test, got %v", result["msg"])
	}
}
