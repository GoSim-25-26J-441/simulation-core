package logger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name  string
		level string
	}{
		{"Debug level", "debug"},
		{"Info level", "info"},
		{"Warn level", "warn"},
		{"Error level", "error"},
		{"Default level", "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(tt.level, &buf)
			if logger == nil {
				t.Error("Expected logger to be created")
			}
		})
	}
}

func TestNewText(t *testing.T) {
	var buf bytes.Buffer
	logger := NewText("info", &buf)
	if logger == nil {
		t.Error("Expected text logger to be created")
	}

	logger.Info("test message")
	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected log output to contain 'test message', got: %s", output)
	}
}

func TestLogLevels(t *testing.T) {
	tests := []struct {
		name     string
		logLevel string
		logFunc  func(string, ...any)
		logMsg   string
		expected bool
	}{
		{"Debug when debug level", "debug", Debug, "debug message", true},
		{"Info when debug level", "debug", Info, "info message", true},
		{"Debug when info level", "info", Debug, "debug message", false},
		{"Info when info level", "info", Info, "info message", true},
		{"Warn when info level", "info", Warn, "warn message", true},
		{"Error when info level", "info", Error, "error message", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(tt.logLevel, &buf)
			SetDefault(logger)

			tt.logFunc(tt.logMsg)
			output := buf.String()

			if tt.expected && !strings.Contains(output, tt.logMsg) {
				t.Errorf("Expected log output to contain '%s', got: %s", tt.logMsg, output)
			}
			if !tt.expected && strings.Contains(output, tt.logMsg) {
				t.Errorf("Expected log output NOT to contain '%s', but it did: %s", tt.logMsg, output)
			}
		})
	}
}

func TestJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := New("info", &buf)
	SetDefault(logger)

	Info("test message", "key", "value", "number", 42)
	output := buf.String()

	// Parse JSON to validate structure
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON log output: %v", err)
	}

	if logEntry["msg"] != "test message" {
		t.Errorf("Expected msg 'test message', got '%v'", logEntry["msg"])
	}
	if logEntry["key"] != "value" {
		t.Errorf("Expected key 'value', got '%v'", logEntry["key"])
	}
	if logEntry["number"] != float64(42) {
		t.Errorf("Expected number 42, got '%v'", logEntry["number"])
	}
}

func TestWith(t *testing.T) {
	var buf bytes.Buffer
	logger := New("info", &buf)
	SetDefault(logger)

	contextLogger := With("request_id", "123", "user", "test")
	contextLogger.Info("request received")

	output := buf.String()
	if !strings.Contains(output, "request_id") {
		t.Error("Expected log output to contain 'request_id'")
	}
	if !strings.Contains(output, "123") {
		t.Error("Expected log output to contain '123'")
	}
	if !strings.Contains(output, "user") {
		t.Error("Expected log output to contain 'user'")
	}
}

func TestSetDefault(t *testing.T) {
	var buf bytes.Buffer
	logger := New("debug", &buf)
	SetDefault(logger)

	Debug("test debug message")
	output := buf.String()

	if !strings.Contains(output, "test debug message") {
		t.Error("Expected debug message to be logged after SetDefault")
	}
}
