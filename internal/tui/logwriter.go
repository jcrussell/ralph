package tui

// logwriter.go is the bridge from the loop's existing stdout/stderr tee into
// the live-run log pane (epic ralph-g3s, bead ralph-g3s.6). loop.Run writes
// subprocess output and narrative chatter through an IOStreams whose Out and
// ErrOut tee to the operator terminal (internal/loop/iteration.go,
// openGateStreams). The TUI swaps in an inner, redirected IOStreams whose Out
// and ErrOut both wrap a single logWriter, so that interleaved byte stream
// flows into the pane in order while the on-disk artifact files — the durable
// sink — stay untouched.
//
// The loop emits bytes, but the model consumes whole lines (logLineMsg, fed to
// the bounded logRing). logWriter is the splitter: it buffers across Write
// boundaries and emits one logLineMsg per '\n'-terminated line through a narrow
// sender (the single tea.Program.Send transport, per ralph-g3s.1). Write never
// errors and never reports a short write — losing a log line must never stall
// or fail the loop — and once the program has quit it no-ops, so a still-
// draining loop cannot Send into a torn-down program. The files survive
// regardless of any of this.

import (
	"bytes"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

// sender is the narrow seam logWriter needs from the live program: the single
// Send transport that folds a message into the model (ralph-g3s.1).
// *tea.Program satisfies it; tests pass a fake recorder.
type sender interface {
	Send(tea.Msg)
}

// logWriter is an io.Writer that splits its input on '\n' and emits each
// completed line as a logLineMsg through send. Out and ErrOut of the inner
// IOStreams share one instance, so writes from either stream interleave in the
// pane in arrival order; that also means concurrent Writes are possible (the
// loop tees subprocess stdout and stderr), so it guards its buffer with a
// mutex. A partial trailing line stays buffered until its newline arrives.
type logWriter struct {
	send sender

	mu   sync.Mutex
	buf  []byte
	done bool
}

// Write splits p into lines, emitting a logLineMsg for each completed line,
// and buffers any trailing partial line for the next call. It always reports
// (len(p), nil): the log pane is best-effort chatter, never a failure surface
// for the loop, and the artifact files capture the bytes regardless. After
// stop it is a no-op (still (len(p), nil)) so a draining loop cannot Send into
// a quit program.
func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.send.Send(logLineMsg{line: string(w.buf[:i])})
		// Compact the remainder to the front, reusing the backing array so
		// cap tracks the longest line rather than growing with each split.
		w.buf = append(w.buf[:0], w.buf[i+1:]...)
	}
	return len(p), nil
}

// stop marks the writer done so subsequent Writes no-op. It is called when the
// program quits, before the loop is cancelled, to cut off Sends into a torn-
// down program; the still-running loop's writes then fall through harmlessly
// while its file tee keeps capturing. Any buffered partial line is dropped —
// it has no newline, the pane shows whole lines only, and the files hold it.
// Idempotent.
func (w *logWriter) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.done = true
	w.buf = nil
}

// newLogIOStreams builds the inner, redirected IOStreams handed to loop.Run:
// a single logWriter backs both Out and ErrOut so the loop's interleaved tee
// flows into one ordered log pane. It returns the writer too, so the program
// orchestration (bead ralph-g3s.7) can stop it on quit. In is nil — the loop
// has no input surface (ralph has zero interactive prompting in the run path).
func newLogIOStreams(send sender) (*iostreams.IOStreams, *logWriter) {
	w := &logWriter{send: send}
	return iostreams.NewIOStreams(nil, w, w), w
}
