package iostreams

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IOStreams is the stdio bundle commands write through. Construct via
// System() in production or Test() in tests; callers should treat the
// concrete type as opaque (per byob-iostreams.1, no direct os.Std* use).
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	stdinIsTTY  bool
	stdoutIsTTY bool
	stderrIsTTY bool

	cs *ColorScheme
}

// System returns the IOStreams backed by os.Stdin/Stdout/Stderr with
// TTY detection populated. Color is enabled when stdout is a TTY and
// NO_COLOR is unset.
func System() *IOStreams {
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	return &IOStreams{
		In:          os.Stdin,
		Out:         os.Stdout,
		ErrOut:      os.Stderr,
		stdinIsTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		stdoutIsTTY: stdoutTTY,
		stderrIsTTY: term.IsTerminal(int(os.Stderr.Fd())),
		cs:          NewColorScheme(stdoutTTY && EnvAllowsColor()),
	}
}

// Test returns an IOStreams whose In/Out/ErrOut are the bytes.Buffers
// in the returned TestBuffers, for assertion-friendly tests. Color is
// always disabled.
func Test() (*IOStreams, *TestBuffers) {
	bufs := &TestBuffers{}
	return &IOStreams{
		In:     &bufs.In,
		Out:    &bufs.Out,
		ErrOut: &bufs.ErrOut,
		cs:     NewColorScheme(false),
	}, bufs
}

// IsStdinTTY reports whether stdin was a TTY at construction time.
func (s *IOStreams) IsStdinTTY() bool { return s.stdinIsTTY }

// IsStdoutTTY reports whether stdout was a TTY at construction time.
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutIsTTY }

// IsStderrTTY reports whether stderr was a TTY at construction time.
func (s *IOStreams) IsStderrTTY() bool { return s.stderrIsTTY }

// SetStdoutTTY overrides the stdout TTY flag and re-derives the color
// scheme so callers that branch on either stay consistent. Intended for
// tests that exercise TTY-adaptive renderers without a real terminal.
func (s *IOStreams) SetStdoutTTY(v bool) {
	s.stdoutIsTTY = v
	s.cs = NewColorScheme(v && EnvAllowsColor())
}

// ColorScheme returns the color renderer attached to these streams.
func (s *IOStreams) ColorScheme() *ColorScheme { return s.cs }
