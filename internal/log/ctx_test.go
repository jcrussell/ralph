package log_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	ralphlog "github.com/jcrussell/ralph/internal/log"
)

func TestWithLoggerRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	ctx := ralphlog.WithLogger(context.Background(), l)

	if got := ralphlog.From(ctx); got != l {
		t.Errorf("From(ctx) = %p; want round-trip equality with %p", got, l)
	}
}

func TestFromFallsBackToDefault(t *testing.T) {
	if got := ralphlog.From(context.Background()); got != slog.Default() {
		t.Errorf("From(empty ctx) = %p; want slog.Default() = %p", got, slog.Default())
	}
}

func TestWithLoggerPreservesAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewTextHandler(&buf, nil))
	decorated := base.With("cmd", "ralph run")
	ctx := ralphlog.WithLogger(context.Background(), decorated)

	ralphlog.From(ctx).InfoContext(ctx, "hi")

	if got := buf.String(); !strings.Contains(got, `cmd="ralph run"`) {
		t.Errorf("log output missing cmd attr; got %q", got)
	}
}
