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

func TestSystemPopulatesAllStreams(t *testing.T) {
	ios := System()
	if ios.In == nil || ios.Out == nil || ios.ErrOut == nil {
		t.Fatalf("System() streams = (%v,%v,%v); want all non-nil",
			ios.In, ios.Out, ios.ErrOut)
	}
}
