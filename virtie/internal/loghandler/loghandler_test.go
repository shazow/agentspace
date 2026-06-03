package loghandler

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLoggerUsesTextHandlerOptions(t *testing.T) {
	var logs bytes.Buffer
	logger := NewLogger(&logs, &Options{Level: slog.LevelInfo, NoColor: true})

	logger.Debug("debug message")
	if got := logs.String(); got != "" {
		t.Fatalf("expected debug log to be suppressed, got %q", got)
	}

	logger.Info("info message")
	if got := logs.String(); !strings.Contains(got, `INF`) || !strings.Contains(got, `info message`) {
		t.Fatalf("expected info log, got %q", got)
	}
}

func TestNewHandlerReturnsSlogHandler(t *testing.T) {
	var logs bytes.Buffer
	handler := NewHandler(&logs, &Options{NoColor: true})

	if _, ok := handler.(slog.Handler); !ok {
		t.Fatalf("expected slog.Handler, got %T", handler)
	}
}

func TestNewLoggerCanColorizeOutput(t *testing.T) {
	var logs bytes.Buffer
	logger := NewLogger(&logs, &Options{NoColor: false})
	logger.Info("info message")

	if got := logs.String(); !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected colorized output, got %q", got)
	}
}
