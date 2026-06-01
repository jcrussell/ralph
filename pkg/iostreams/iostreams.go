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

	cs    *ColorScheme
	errCS *ColorScheme
}

// System returns the IOStreams backed by os.Stdin/Stdout/Stderr with
// TTY detection populated. Out color is enabled when stdout is a TTY and
// NO_COLOR is unset; ErrOut color is gated on stderr independently so the
// two streams color correctly under split redirection (e.g. 2>file).
func System() *IOStreams {
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	stderrTTY := term.IsTerminal(int(os.Stderr.Fd()))
	allow := EnvAllowsColor()
	return &IOStreams{
		In:          os.Stdin,
		Out:         os.Stdout,
		ErrOut:      os.Stderr,
		stdinIsTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		stdoutIsTTY: stdoutTTY,
		stderrIsTTY: stderrTTY,
		cs:          NewColorScheme(stdoutTTY && allow),
		errCS:       NewColorScheme(stderrTTY && allow),
	}
}

// NewIOStreams returns an IOStreams over caller-supplied readers and
// writers. TTY flags are false and both color schemes are disabled — the
// caller opts in (e.g. the run TUI wiring a redirected inner stream).
func NewIOStreams(in io.Reader, out, errOut io.Writer) *IOStreams {
	return &IOStreams{
		In:     in,
		Out:    out,
		ErrOut: errOut,
		cs:     NewColorScheme(false),
		errCS:  NewColorScheme(false),
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
		errCS:  NewColorScheme(false),
	}, bufs
}

// IsStdinTTY reports whether stdin was a TTY at construction time.
func (s *IOStreams) IsStdinTTY() bool { return s.stdinIsTTY }

// IsStdoutTTY reports whether stdout was a TTY at construction time.
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutIsTTY }

// IsStderrTTY reports whether stderr was a TTY at construction time.
func (s *IOStreams) IsStderrTTY() bool { return s.stderrIsTTY }

// SetStdinTTY overrides the stdin TTY flag. Stdin carries no color scheme,
// so unlike the Out/Err setters this only flips the flag. Intended for tests
// that exercise stdin-TTY-gated behavior (e.g. the run TUI activation gate)
// without a real terminal.
func (s *IOStreams) SetStdinTTY(v bool) { s.stdinIsTTY = v }

// SetStdoutTTY overrides the stdout TTY flag and re-derives the Out color
// scheme so callers that branch on either stay consistent. Intended for
// tests that exercise TTY-adaptive renderers without a real terminal.
func (s *IOStreams) SetStdoutTTY(v bool) {
	s.stdoutIsTTY = v
	s.cs = NewColorScheme(v && EnvAllowsColor())
}

// SetStderrTTY overrides the stderr TTY flag and re-derives the ErrOut
// color scheme. Symmetric with SetStdoutTTY for tests that exercise the
// ErrOut color path without a real terminal.
func (s *IOStreams) SetStderrTTY(v bool) {
	s.stderrIsTTY = v
	s.errCS = NewColorScheme(v && EnvAllowsColor())
}

// ColorScheme returns the Out color renderer, gated on stdout. Data
// renderers (e.g. tableprinter) that write to Out use this.
func (s *IOStreams) ColorScheme() *ColorScheme { return s.cs }

// ErrColorScheme returns the ErrOut color renderer, gated on stderr.
// Chatter and progress written to ErrOut use this so color tracks the
// stream it lands on, independent of stdout.
func (s *IOStreams) ErrColorScheme() *ColorScheme { return s.errCS }
