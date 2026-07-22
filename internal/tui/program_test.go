package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// runDone runs pg.Run in a goroutine and returns a channel that delivers its
// error, so tests can assert Run unblocks (the program quit) within a timeout
// rather than hanging the suite.
func runDone(pg *Program) <-chan error {
	errc := make(chan error, 1)
	go func() { errc <- pg.Run() }()
	return errc
}

// waitQuit blocks until Run returns or the deadline elapses, failing the test
// on timeout so a program that never quits surfaces as a clear failure.
func waitQuit(t *testing.T, errc <-chan error) {
	t.Helper()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after quit signal")
	}
}

// TestObserverSendsMetricsMsg checks the metrics bridge: the loop.Observer the
// program hands to loop.Run forwards each Snapshot as a metricsMsg over Send.
func TestObserverSendsMetricsMsg(t *testing.T) {
	rec := &fakeSender{}
	obs := observer{send: rec}

	snap := fullSnapshot()
	obs.Observe(snap)

	if len(rec.msgs) != 1 {
		t.Fatalf("Observe should Send exactly one message, got %d", len(rec.msgs))
	}
	mm, ok := rec.msgs[0].(metricsMsg)
	if !ok {
		t.Fatalf("Observe should Send a metricsMsg, got %T", rec.msgs[0])
	}
	if mm.s.Iter != snap.Iter {
		t.Errorf("metricsMsg carried Iter=%d, want %d", mm.s.Iter, snap.Iter)
	}
}

// TestProgramObserverImplementsLoopObserver pins the seam: the value the
// program exposes for loop.Options.Observer satisfies loop.Observer.
func TestProgramObserverImplementsLoopObserver(t *testing.T) {
	ios, _ := iostreams.Test()
	pg := New(ios, loop.Snapshot{}, Header{}, DefaultLogCaps(), tea.WithoutRenderer(), tea.WithInput(&bytes.Buffer{}))
	// Observer()'s return type is loop.Observer by its signature; assert it is
	// non-nil so loop.Run never receives a nil sink.
	if pg.Observer() == nil {
		t.Fatal("Observer must return a non-nil loop.Observer")
	}
	if pg.LoopIO() == nil {
		t.Fatal("LoopIO must return the inner redirected streams")
	}
	if pg.LoopIO() == ios {
		t.Fatal("LoopIO must be a distinct inner IOStreams, not the render target")
	}
}

// TestDoneThenQuitUnblocksRun verifies the loop-done path: Finish marks the run
// finished but does NOT quit (the run stays on screen for review); Run unblocks
// only on a subsequent user quit keypress.
func TestDoneThenQuitUnblocksRun(t *testing.T) {
	ios, _ := iostreams.Test()
	pg := New(ios, loop.Snapshot{}, Header{}, DefaultLogCaps(), tea.WithoutRenderer(), tea.WithInput(&bytes.Buffer{}))

	errc := runDone(pg)
	pg.p.Send(tea.WindowSizeMsg{Width: 100, Height: 24})
	pg.Finish(nil, true)

	// Finish alone must not unblock Run — the program keeps rendering.
	select {
	case <-errc:
		t.Fatal("Run returned after Finish; it must stay alive until the user quits")
	case <-time.After(100 * time.Millisecond):
	}

	// Quit now confirms: the first ctrl+c arms the prompt, the second forces it.
	pg.p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	pg.p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	waitQuit(t, errc)
}

// TestProgramRendersMetricsToErrOut is the integration assertion: with a real
// renderer pointed at the injected ErrOut, a Snapshot pushed through the
// Observer bridge renders into the panel, and nothing lands on Out (stdout's
// stand-in). Quit is driven by a Ctrl-C keypress (a clean Finish no longer quits).
func TestProgramRendersMetricsToErrOut(t *testing.T) {
	ios, bufs := iostreams.Test()
	pg := New(ios, loop.Snapshot{}, Header{}, DefaultLogCaps(), tea.WithInput(&bytes.Buffer{}))

	errc := runDone(pg)
	pg.p.Send(tea.WindowSizeMsg{Width: 120, Height: 24})
	pg.Observer().Observe(fullSnapshot())
	// Let the renderer flush at least one frame containing the metrics before
	// quitting; the final flush on quit captures the latest View regardless.
	time.Sleep(50 * time.Millisecond)
	// Quit now confirms: first ctrl+c arms the prompt, second forces it.
	pg.p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	pg.p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	waitQuit(t, errc)

	if got := bufs.ErrOut.String(); !strings.Contains(got, "iter 5/20") {
		t.Errorf("ErrOut should contain the rendered metrics panel, got:\n%q", got)
	}
	if got := bufs.Out.String(); got != "" {
		t.Errorf("the TUI must never write to Out (stdout), got:\n%q", got)
	}
}
