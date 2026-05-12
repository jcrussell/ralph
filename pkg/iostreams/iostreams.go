package iostreams

import (
	"io"
	"os"

	"golang.org/x/term"
)

type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	stdinIsTTY  bool
	stdoutIsTTY bool
	stderrIsTTY bool
}

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

func Test() (*IOStreams, *TestBuffers) {
	bufs := &TestBuffers{}
	return &IOStreams{In: &bufs.In, Out: &bufs.Out, ErrOut: &bufs.ErrOut}, bufs
}

func (s *IOStreams) IsStdinTTY() bool  { return s.stdinIsTTY }
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutIsTTY }
func (s *IOStreams) IsStderrTTY() bool { return s.stderrIsTTY }
