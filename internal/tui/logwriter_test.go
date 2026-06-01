package tui

import (
	"io"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeSender records every message Send receives, in order, so tests can
// assert on the exact logLineMsg sequence a writer emits. It is safe for
// concurrent Send (the writer may be fed from multiple goroutines).
type fakeSender struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (f *fakeSender) Send(msg tea.Msg) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, msg)
}

// lines returns the recorded messages as the log lines they carried, failing
// if any message is not a logLineMsg.
func (f *fakeSender) lines(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.msgs))
	for i, msg := range f.msgs {
		ll, ok := msg.(logLineMsg)
		if !ok {
			t.Fatalf("msg %d: got %T, want logLineMsg", i, msg)
		}
		out = append(out, ll.line)
	}
	return out
}

func eqLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// writeChunks feeds each chunk to w in turn and asserts every Write reports a
// full, error-free write — the writer must never error or report a short
// write regardless of how the bytes are sliced.
func writeChunks(t *testing.T, w io.Writer, chunks ...string) {
	t.Helper()
	for _, c := range chunks {
		n, err := w.Write([]byte(c))
		if err != nil {
			t.Fatalf("Write(%q) err = %v, want nil", c, err)
		}
		if n != len(c) {
			t.Fatalf("Write(%q) n = %d, want %d", c, n, len(c))
		}
	}
}

func TestLogWriter_SplitsOnNewline(t *testing.T) {
	fs := &fakeSender{}
	w := &logWriter{send: fs}

	writeChunks(t, w, "foo\nbar\nbaz\n")

	eqLines(t, fs.lines(t), []string{"foo", "bar", "baz"})
}

func TestLogWriter_LineSpanningChunks(t *testing.T) {
	fs := &fakeSender{}
	w := &logWriter{send: fs}

	// A line split across Write boundaries must yield exactly one logLineMsg
	// once its newline arrives, and no partial line before that.
	writeChunks(t, w, "foo\nba", "r", "\nbaz")
	eqLines(t, fs.lines(t), []string{"foo", "bar"}) // "baz" still buffered

	writeChunks(t, w, "\n")
	eqLines(t, fs.lines(t), []string{"foo", "bar", "baz"})
}

func TestLogWriter_NoNewlineBuffers(t *testing.T) {
	fs := &fakeSender{}
	w := &logWriter{send: fs}

	// Bytes without a newline emit nothing — they are held for the eventual
	// line terminator.
	writeChunks(t, w, "partial")
	eqLines(t, fs.lines(t), nil)
}

func TestLogWriter_EmptyAndBlankLines(t *testing.T) {
	fs := &fakeSender{}
	w := &logWriter{send: fs}

	// Consecutive newlines produce empty lines, preserved verbatim; an empty
	// Write is a clean no-op.
	writeChunks(t, w, "", "a\n\n\nb\n")
	eqLines(t, fs.lines(t), []string{"a", "", "", "b"})
}

func TestLogWriter_PreservesANSI(t *testing.T) {
	fs := &fakeSender{}
	w := &logWriter{send: fs}

	// Escapes are stored verbatim; the ring/viewport handle width, not the
	// writer (see logring.go).
	writeChunks(t, w, "\x1b[31mred\x1b[0m\n")
	eqLines(t, fs.lines(t), []string{"\x1b[31mred\x1b[0m"})
}

func TestLogWriter_NoOpAfterStop(t *testing.T) {
	fs := &fakeSender{}
	w := &logWriter{send: fs}

	writeChunks(t, w, "before\n")
	w.stop()

	// Post-stop writes still report a full, error-free write but emit nothing.
	n, err := w.Write([]byte("after\n"))
	if err != nil || n != len("after\n") {
		t.Fatalf("post-stop Write = (%d, %v), want (%d, nil)", n, err, len("after\n"))
	}
	eqLines(t, fs.lines(t), []string{"before"})

	// stop is idempotent.
	w.stop()
}

func TestNewLogIOStreams_OutAndErrOutFeedSamePane(t *testing.T) {
	fs := &fakeSender{}
	ios, w := newLogIOStreams(fs)

	if ios.Out == nil || ios.ErrOut == nil {
		t.Fatal("Out/ErrOut must be non-nil")
	}
	if ios.Out != ios.ErrOut {
		t.Error("Out and ErrOut must share one writer so the loop's tee interleaves in order")
	}

	// Both streams flow through the one writer into the one ordered sequence.
	writeChunks(t, ios.Out, "out\n")
	writeChunks(t, ios.ErrOut, "err\n")
	eqLines(t, fs.lines(t), []string{"out", "err"})

	// The returned handle is that writer.
	w.stop()
	writeChunks(t, ios.Out, "dropped\n")
	eqLines(t, fs.lines(t), []string{"out", "err"})
}

// the writer is an io.Writer the inner IOStreams can wrap, and the
// production *tea.Program satisfies the narrow sender seam.
var (
	_ io.Writer = (*logWriter)(nil)
	_ sender    = (*tea.Program)(nil)
)
