package iostreams

import (
	"bytes"
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

// TestErrColorSchemeTracksStderr exercises the split-redirection matrix:
// Out color follows stdout, ErrOut color follows stderr, independently.
func TestErrColorSchemeTracksStderr(t *testing.T) {
	cases := []struct {
		name      string
		noColor   string // "" => unset
		stdoutTTY bool
		stderrTTY bool
		wantOutCS bool
		wantErrCS bool
	}{
		{"both-tty", "", true, true, true, true},
		{"stdout-tty-stderr-file", "", true, false, true, false},
		{"stdout-file-stderr-tty", "", false, true, false, true},
		{"neither-tty", "", false, false, false, false},
		{"both-tty-no-color", "1", true, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tc.noColor)
			ios, _ := Test()
			ios.SetStdoutTTY(tc.stdoutTTY)
			ios.SetStderrTTY(tc.stderrTTY)
			if got := ios.ColorScheme().Enabled(); got != tc.wantOutCS {
				t.Errorf("ColorScheme().Enabled() = %v, want %v", got, tc.wantOutCS)
			}
			if got := ios.ErrColorScheme().Enabled(); got != tc.wantErrCS {
				t.Errorf("ErrColorScheme().Enabled() = %v, want %v", got, tc.wantErrCS)
			}
		})
	}
}

func TestNewIOStreamsDefaultsColorOff(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	var in, out, errOut bytes.Buffer
	ios := NewIOStreams(&in, &out, &errOut)
	if ios.In != &in || ios.Out != &out || ios.ErrOut != &errOut {
		t.Errorf("NewIOStreams did not wire the supplied streams")
	}
	if ios.IsStdinTTY() || ios.IsStdoutTTY() || ios.IsStderrTTY() {
		t.Errorf("NewIOStreams TTY flags = (%v,%v,%v); want all false",
			ios.IsStdinTTY(), ios.IsStdoutTTY(), ios.IsStderrTTY())
	}
	if ios.ColorScheme().Enabled() || ios.ErrColorScheme().Enabled() {
		t.Errorf("NewIOStreams color = (out=%v, err=%v); want both off",
			ios.ColorScheme().Enabled(), ios.ErrColorScheme().Enabled())
	}
}
