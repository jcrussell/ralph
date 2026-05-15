package log

import (
	"context"
	"log/slog"
)

// ctxKey is the unexported context-key type. A struct type prevents
// accidental collisions with other packages' context values.
type ctxKey struct{}

// WithLogger returns ctx with l attached for later retrieval by From.
// The root command's PersistentPreRunE uses this to thread a logger
// pre-decorated with per-invocation attributes through cmd.Context().
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From returns the logger attached via WithLogger, or slog.Default()
// when none is set. Command code that already holds a context.Context
// should reach for this helper instead of accepting a *slog.Logger
// parameter.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
