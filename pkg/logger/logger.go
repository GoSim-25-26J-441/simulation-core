package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

var (
	// Default is the default logger instance
	Default *slog.Logger
)

func init() {
	// Initialize with info level by default
	Default = New("info", os.Stdout)
}

// New creates a new structured logger with the specified level and output
func New(level string, output io.Writer) *slog.Logger {
	var logLevel slog.Level

	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: logLevel,
	})

	return slog.New(handler)
}

// NewText creates a new text-formatted logger (useful for development)
func NewText(level string, output io.Writer) *slog.Logger {
	var logLevel slog.Level

	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(output, &slog.HandlerOptions{
		Level: logLevel,
	})

	return slog.New(handler)
}

// SetDefault sets the default logger
func SetDefault(logger *slog.Logger) {
	Default = logger
	slog.SetDefault(logger)
}

// Debug logs a debug message
func Debug(msg string, args ...any) {
	Default.Debug(msg, args...)
}

// Info logs an info message
func Info(msg string, args ...any) {
	Default.Info(msg, args...)
}

// Warn logs a warning message
func Warn(msg string, args ...any) {
	Default.Warn(msg, args...)
}

// Error logs an error message
func Error(msg string, args ...any) {
	Default.Error(msg, args...)
}

// With returns a logger with additional attributes
func With(args ...any) *slog.Logger {
	return Default.With(args...)
}
