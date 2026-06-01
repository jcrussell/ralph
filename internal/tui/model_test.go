package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

// step folds one message into m and returns the concrete model plus any
// command, so tests can assert on both state and the emitted Cmd.
func step(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	tm, cmd := m.Update(msg)
	got, ok := tm.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", tm)
	}
	return got, cmd
}

// runeKey builds a KeyMsg for a single-rune keypress (e.g. "q", "e").
func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// newTestModel builds a model over non-TTY test streams (color off).
func newTestModel(t *testing.T) model {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	ios, _ := iostreams.Test()
	return newModel(ios)
}

// sized returns a model that has received an initial WindowSizeMsg.
func sized(t *testing.T, w, h int) model {
	t.Helper()
	m, _ := step(t, newTestModel(t), tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func TestViewBeforeResize(t *testing.T) {
	if got := newTestModel(t).View(); got != "initializing…" {
		t.Errorf("pre-resize View = %q, want initializing placeholder", got)
	}
}

func TestMetricsMsgPopulatesPanel(t *testing.T) {
	m := sized(t, 100, 24)
	if m.hasSnap {
		t.Fatalf("model should have no snapshot before metricsMsg")
	}
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})

	if !m.hasSnap {
		t.Fatalf("metricsMsg should set hasSnap")
	}
	view := m.View()
	for _, w := range []string{"iter 5/20", "clean (ready)", "gate pass", "cost $1.23/$5.00"} {
		if !strings.Contains(view, w) {
			t.Errorf("View missing %q after metricsMsg\n--- view ---\n%s", w, view)
		}
	}
}

func TestLogLineMsgAppendsAndShows(t *testing.T) {
	m := sized(t, 100, 24)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	m, _ = step(t, m, logLineMsg{line: "hello-from-runner"})
	m, _ = step(t, m, logLineMsg{line: "second-line"})

	if len(m.logs) != 2 {
		t.Fatalf("logs = %d, want 2", len(m.logs))
	}
	if !strings.Contains(m.View(), "hello-from-runner") {
		t.Errorf("View should show appended log line:\n%s", m.View())
	}
}

func TestQuitKeyReturnsQuit(t *testing.T) {
	for _, key := range []tea.KeyMsg{runeKey('q'), {Type: tea.KeyCtrlC}} {
		m, cmd := step(t, sized(t, 80, 20), key)
		if !m.quitting {
			t.Errorf("%v should set quitting", key)
		}
		if cmd == nil {
			t.Fatalf("%v should return a command", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%v should return tea.Quit, got %T", key, cmd())
		}
	}
}

func TestExpandTogglesPanel(t *testing.T) {
	m := sized(t, 100, 24)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	if !strings.Contains(m.View(), "iter 5/20") {
		t.Fatalf("collapsed View should show metrics panel")
	}

	m, _ = step(t, m, runeKey('e'))
	if !m.expanded {
		t.Fatalf("'e' should set expanded")
	}
	if strings.Contains(m.View(), "iter 5/20") {
		t.Errorf("expanded View should hide the metrics panel:\n%s", m.View())
	}

	m, _ = step(t, m, runeKey('e'))
	if m.expanded {
		t.Fatalf("'e' should toggle expanded back off")
	}
	if !strings.Contains(m.View(), "iter 5/20") {
		t.Errorf("re-collapsed View should show the metrics panel again")
	}
}

func TestResizeRelayoutsViewport(t *testing.T) {
	m := sized(t, 80, 30)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	tall := m.vp.Height

	m, _ = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	short := m.vp.Height

	if short >= tall {
		t.Errorf("shrinking the window should reduce viewport height: tall=%d short=%d", tall, short)
	}
	if m.vp.Width != 80 {
		t.Errorf("viewport width should track resize, got %d", m.vp.Width)
	}
}

func TestTinyWindowDegradesGracefully(t *testing.T) {
	m := sized(t, 10, 3) // below both minimums
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})

	view := m.View() // must not panic
	lines := strings.Split(view, "\n")
	if len(lines) > 3 {
		t.Errorf("degraded view should clip to height (3), got %d lines:\n%s", len(lines), view)
	}
	for _, ln := range lines {
		if n := utf8.RuneCountInString(ln); n > 10 {
			t.Errorf("degraded line exceeds width 10 (got %d): %q", n, ln)
		}
	}
}

func TestNoColorYieldsNoANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	ios, _ := iostreams.Test()
	m := newModel(ios)
	if m.colorEnabled {
		t.Fatalf("non-TTY + NO_COLOR should disable color")
	}
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	m, _ = step(t, m, logLineMsg{line: "plain log line"})

	if strings.Contains(m.View(), "\x1b") {
		t.Errorf("View must contain no ANSI escapes when color is off:\n%q", m.View())
	}
}

func TestTickAdvancesElapsed(t *testing.T) {
	m := sized(t, 100, 24)

	// No snapshot yet: ticks must not advance a phantom clock.
	m, cmd := step(t, m, tickMsg{})
	if m.liveElapsed != 0 {
		t.Errorf("tick before any metrics should leave elapsed at 0, got %v", m.liveElapsed)
	}
	if cmd == nil {
		t.Fatalf("tick should reschedule the next tick")
	}

	s := fullSnapshot()
	s.Elapsed = 10 * time.Second
	m, _ = step(t, m, metricsMsg{s: s})
	if m.liveElapsed != 10*time.Second {
		t.Fatalf("metricsMsg should seed liveElapsed from the Snapshot, got %v", m.liveElapsed)
	}

	m, _ = step(t, m, tickMsg{})
	m, _ = step(t, m, tickMsg{})
	if m.liveElapsed != 12*time.Second {
		t.Errorf("two ticks should advance elapsed to 12s, got %v", m.liveElapsed)
	}
	if !strings.Contains(m.View(), "elapsed 12s") {
		t.Errorf("View should reflect tick-advanced elapsed:\n%s", m.View())
	}
}

func TestScrollKeyForwardsToViewport(t *testing.T) {
	m := sized(t, 40, 10)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	for i := 0; i < 50; i++ {
		m, _ = step(t, m, logLineMsg{line: "line-" + strconv.Itoa(i)})
	}
	if !m.vp.AtBottom() {
		t.Fatalf("auto-follow should leave the viewport at the bottom")
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.vp.AtBottom() {
		t.Errorf("an up keypress should scroll the viewport off the bottom")
	}
}
