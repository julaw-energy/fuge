package fuge

import "log/slog"

// Logger is the subset of *slog.Logger the router needs.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

func defaultLogger() Logger { return slog.Default() }
