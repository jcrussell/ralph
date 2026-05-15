package log_test

import (
	"log/slog"
	"testing"

	ralphlog "github.com/jcrussell/ralph/internal/log"
)

func TestParseLevelValid(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"info", slog.LevelInfo},
		{"  Info  ", slog.LevelInfo},
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
	}
	for _, c := range cases {
		got, err := ralphlog.ParseLevel(c.in)
		if err != nil {
			t.Errorf("ParseLevel(%q) err = %v; want nil", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLevel(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestParseLevelInvalid(t *testing.T) {
	for _, in := range []string{"", "error", "trace", "verbose", "1", "foo"} {
		if _, err := ralphlog.ParseLevel(in); err == nil {
			t.Errorf("ParseLevel(%q) err = nil; want error", in)
		}
	}
}
