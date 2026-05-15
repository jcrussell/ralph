package iostreams

import (
	"io"
	"testing"
)

func TestTestBuffersAreIsolated(t *testing.T) {
	ios, bufs := Test()
	if _, err := io.WriteString(ios.Out, "out-data"); err != nil {
		t.Fatalf("write Out: %v", err)
	}
	if _, err := io.WriteString(ios.ErrOut, "err-data"); err != nil {
		t.Fatalf("write ErrOut: %v", err)
	}
	if got := bufs.Out.String(); got != "out-data" {
		t.Errorf("Out = %q, want %q", got, "out-data")
	}
	if got := bufs.ErrOut.String(); got != "err-data" {
		t.Errorf("ErrOut = %q, want %q", got, "err-data")
	}
}

func TestTestStreamsDefaultToNonTTY(t *testing.T) {
	ios, _ := Test()
	if ios.IsStdinTTY() || ios.IsStdoutTTY() || ios.IsStderrTTY() {
		t.Errorf("Test() TTY flags = (%v,%v,%v); want all false",
			ios.IsStdinTTY(), ios.IsStdoutTTY(), ios.IsStderrTTY())
	}
}

func TestSetStdoutTTYRederivesColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	ios, _ := Test()
	if ios.IsStdoutTTY() || ios.ColorScheme().Enabled() {
		t.Fatalf("Test() defaults = (tty=%v, color=%v); want both false",
			ios.IsStdoutTTY(), ios.ColorScheme().Enabled())
	}
	ios.SetStdoutTTY(true)
	if !ios.IsStdoutTTY() {
		t.Errorf("IsStdoutTTY() = false after SetStdoutTTY(true)")
	}
	if !ios.ColorScheme().Enabled() {
		t.Errorf("ColorScheme().Enabled() = false after SetStdoutTTY(true)")
	}
	ios.SetStdoutTTY(false)
	if ios.IsStdoutTTY() || ios.ColorScheme().Enabled() {
		t.Errorf("after SetStdoutTTY(false): tty=%v color=%v; want both false",
			ios.IsStdoutTTY(), ios.ColorScheme().Enabled())
	}
}

func TestSetStdoutTTYHonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	ios, _ := Test()
	ios.SetStdoutTTY(true)
	if !ios.IsStdoutTTY() {
		t.Errorf("IsStdoutTTY() = false")
	}
	if ios.ColorScheme().Enabled() {
		t.Errorf("ColorScheme().Enabled() = true with NO_COLOR=1; want false")
	}
}

func TestSystemPopulatesAllStreams(t *testing.T) {
	ios := System()
	if ios.In == nil || ios.Out == nil || ios.ErrOut == nil {
		t.Fatalf("System() streams = (%v,%v,%v); want all non-nil",
			ios.In, ios.Out, ios.ErrOut)
	}
}
