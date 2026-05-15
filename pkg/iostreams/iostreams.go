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
}

// System returns the IOStreams backed by os.Stdin/Stdout/Stderr with
// TTY detection populated.
func System() *IOStreams {
	return &IOStreams{
		In:          os.Stdin,
		Out:         os.Stdout,
		ErrOut:      os.Stderr,
		stdinIsTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		stdoutIsTTY: term.IsTerminal(int(os.Stdout.Fd())),
		stderrIsTTY: term.IsTerminal(int(os.Stderr.Fd())),
	}
}

// Test returns an IOStreams whose In/Out/ErrOut are the bytes.Buffers
// in the returned TestBuffers, for assertion-friendly tests.
func Test() (*IOStreams, *TestBuffers) {
	bufs := &TestBuffers{}
	return &IOStreams{In: &bufs.In, Out: &bufs.Out, ErrOut: &bufs.ErrOut}, bufs
}

// IsStdinTTY reports whether stdin was a TTY at construction time.
func (s *IOStreams) IsStdinTTY() bool { return s.stdinIsTTY }

// IsStdoutTTY reports whether stdout was a TTY at construction time.
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutIsTTY }

// IsStderrTTY reports whether stderr was a TTY at construction time.
func (s *IOStreams) IsStderrTTY() bool { return s.stderrIsTTY }
