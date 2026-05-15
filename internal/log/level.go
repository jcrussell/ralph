package log

import (
	"fmt"
	"log/slog"
	"strings"
)

// ParseLevel maps a case-insensitive level name (warn|info|debug) to a
// slog.Level. The root command consults this for both --log-level and
// $RALPH_LOG (byob-logging.3). An unrecognized name is reported so the
// caller can decide whether to surface it (flag error) or swallow it
// (env-var fallback).
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	default:
		return 0, fmt.Errorf("unknown log level %q (want warn|info|debug)", s)
	}
}
