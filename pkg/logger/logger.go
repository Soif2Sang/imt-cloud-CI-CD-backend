package logger

import (
	"log/slog"
	"os"
)

// Init initializes the global logger.
// Currently it defaults to a JSON handler on stdout.
func Init() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
}

// Info logs at Info level.
func Info(msg string, args ...any) {
	slog.Info(msg, args...)
}

// Error logs at Error level.
func Error(msg string, args ...any) {
	slog.Error(msg, args...)
}

// Warn logs at Warn level.
func Warn(msg string, args ...any) {
	slog.Warn(msg, args...)
}

// Debug logs at Debug level.
func Debug(msg string, args ...any) {
	slog.Debug(msg, args...)
}

// With returns a logger with the given attributes.
func With(args ...any) *slog.Logger {
	return slog.With(args...)
}
