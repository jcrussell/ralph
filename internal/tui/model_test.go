package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/internal/loop"
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

// newTestModel builds a model over non-TTY test streams (color off), seeded
// with the zero Snapshot so hasSnap is true from construction (as production is,
// via the caps seed from run.go).
func newTestModel(t *testing.T) model {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	ios, _ := iostreams.Test()
	return newModel(ios, loop.Snapshot{})
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

	if m.logs.count() != 2 {
		t.Fatalf("logs = %d, want 2", m.logs.count())
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
	m := newModel(ios, loop.Snapshot{})
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

	// The seed makes hasSnap true from t0, so a tick advances the elapsed clock
	// immediately (no real metricsMsg needed to start it).
	m, cmd := step(t, m, tickMsg{})
	if m.liveElapsed != tickInterval {
		t.Errorf("tick on the seed should advance elapsed to %v, got %v", tickInterval, m.liveElapsed)
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

// seededModel builds a non-TTY model seeded with the given Snapshot, mirroring
// how run.go seeds the configured caps.
func seededModel(t *testing.T, seed loop.Snapshot) model {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	ios, _ := iostreams.Test()
	return newModel(ios, seed)
}

func TestInitialPanelRendersCapsAtT0(t *testing.T) {
	m := seededModel(t, loop.Snapshot{MaxIterations: 20, MaxCostUSD: 5})
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})

	view := m.View()
	for _, w := range []string{"iter 0/20", "cost $0.00/$5.00", "elapsed 0s"} {
		if !strings.Contains(view, w) {
			t.Errorf("seed View missing %q before any metricsMsg\n--- view ---\n%s", w, view)
		}
	}
	// The empty seed must not render the redundant narrative "iter 0000" line.
	if strings.Contains(view, "iter 0000") {
		t.Errorf("seed View should omit the narrative line, got:\n%s", view)
	}
}

func TestDoneKeepsModelAlive(t *testing.T) {
	m := sized(t, 80, 20)
	m, cmd := step(t, m, doneMsg{})

	if !m.done {
		t.Error("doneMsg should set done")
	}
	if m.quitting {
		t.Error("doneMsg must not set quitting (the run stays on screen)")
	}
	if cmd != nil {
		t.Errorf("doneMsg must not return a command (no tea.Quit), got %T", cmd())
	}
}

func TestDoneStopsElapsedTicker(t *testing.T) {
	m := sized(t, 100, 24)
	s := fullSnapshot()
	s.Elapsed = 10 * time.Second
	m, _ = step(t, m, metricsMsg{s: s})

	m, _ = step(t, m, doneMsg{})
	m, cmd := step(t, m, tickMsg{})

	if m.liveElapsed != 10*time.Second {
		t.Errorf("tick after done should leave elapsed frozen at 10s, got %v", m.liveElapsed)
	}
	if cmd != nil {
		t.Error("tick after done should not reschedule the ticker")
	}
}

func TestHelpTextChangesAfterDone(t *testing.T) {
	m := sized(t, 100, 24)
	if !strings.Contains(m.View(), "e expand") {
		t.Fatalf("running help should offer expand:\n%s", m.View())
	}

	m, _ = step(t, m, doneMsg{})
	if !strings.Contains(m.View(), "run complete") {
		t.Errorf("finished help should read 'run complete':\n%s", m.View())
	}
}

func TestPanesAreSeparated(t *testing.T) {
	const w = 100
	m := sized(t, w, 24)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})

	rule := strings.Repeat("─", w)
	if n := strings.Count(m.View(), rule); n != 2 {
		t.Errorf("View should contain two full-width %d-col rules (panel/log + log/help), got %d:\n%s", w, n, m.View())
	}
}

func TestViewHasNoTrailingWhitespace(t *testing.T) {
	m := sized(t, 100, 24)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	m, _ = step(t, m, logLineMsg{line: "short log line"})

	for i, ln := range strings.Split(m.View(), "\n") {
		if ln != strings.TrimRight(ln, " ") {
			t.Errorf("View line %d has trailing whitespace: %q", i, ln)
		}
	}
}

func TestIsIterMarker(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"iter 0001  clean → clean", true},
		{"iter 0042  ", true},
		{"iter 12  too few digits", false}, // %04d is always >=4 digits
		{"iter 0001 single space", false},  // emitIterLine writes two spaces
		{"iteration 0001  x", false},
		{"some runner output", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isIterMarker(c.line); got != c.want {
			t.Errorf("isIterMarker(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

// seedIterLogs pushes 32 log lines with iteration-summary markers at slice
// indices 10 and 21, so iterStartLines() == [0, 11, 22]. The window is sized so
// every boundary is reachable: with fullSnapshot the panel is 5 lines, relayout
// reserves 1+1+5+1=8, so vp.Height = 16-8 = 8 and maxYOffset = 32-8 = 24 >= 22.
func seedIterLogs(t *testing.T) model {
	t.Helper()
	m := sized(t, 80, 16)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	for i := 0; i < 32; i++ {
		var line string
		switch i {
		case 10:
			line = "iter 0001  first summary"
		case 21:
			line = "iter 0002  second summary"
		default:
			line = "log-" + strconv.Itoa(i)
		}
		m, _ = step(t, m, logLineMsg{line: line})
	}
	if got := m.iterStartLines(); len(got) != 3 || got[0] != 0 || got[1] != 11 || got[2] != 22 {
		t.Fatalf("iterStartLines() = %v, want [0 11 22]", got)
	}
	if m.vp.Height != 8 {
		t.Fatalf("vp.Height = %d, want 8 (panel layout changed — fix the window size)", m.vp.Height)
	}
	return m
}

func TestJumpIteration(t *testing.T) {
	m := seedIterLogs(t)
	m.vp.SetYOffset(0)

	(&m).jumpIteration(true)
	if m.vp.YOffset != 11 {
		t.Fatalf("forward from top: YOffset = %d, want 11", m.vp.YOffset)
	}
	(&m).jumpIteration(true)
	if m.vp.YOffset != 22 {
		t.Fatalf("forward again: YOffset = %d, want 22", m.vp.YOffset)
	}
	(&m).jumpIteration(true)
	if !m.vp.AtBottom() {
		t.Fatalf("forward past last boundary should snap to bottom, YOffset = %d", m.vp.YOffset)
	}

	// Now walk back up from the bottom.
	(&m).jumpIteration(false)
	if m.vp.YOffset != 22 {
		t.Fatalf("backward from bottom: YOffset = %d, want 22", m.vp.YOffset)
	}
	(&m).jumpIteration(false)
	if m.vp.YOffset != 11 {
		t.Fatalf("backward again: YOffset = %d, want 11", m.vp.YOffset)
	}
	(&m).jumpIteration(false)
	if m.vp.YOffset != 0 {
		t.Fatalf("backward to first boundary: YOffset = %d, want 0", m.vp.YOffset)
	}
}

func TestJumpIterationNoMarkersIsNoop(t *testing.T) {
	m := sized(t, 80, 16)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	for i := 0; i < 20; i++ {
		m, _ = step(t, m, logLineMsg{line: "plain-" + strconv.Itoa(i)})
	}
	m.vp.SetYOffset(3)
	(&m).jumpIteration(true) // no markers -> snaps to live tail
	if !m.vp.AtBottom() {
		t.Errorf("forward with no markers should go to bottom, YOffset = %d", m.vp.YOffset)
	}
	m.vp.SetYOffset(3)
	(&m).jumpIteration(false) // no boundary above 3 except 0
	if m.vp.YOffset != 0 {
		t.Errorf("backward with no markers should go to top, YOffset = %d", m.vp.YOffset)
	}
}

func TestArrowKeysJumpIterations(t *testing.T) {
	m := seedIterLogs(t)
	m.vp.SetYOffset(0)

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.vp.YOffset != 11 {
		t.Errorf("→ should jump to next iteration (11), got %d", m.vp.YOffset)
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.vp.YOffset != 0 {
		t.Errorf("← should jump to previous iteration (0), got %d", m.vp.YOffset)
	}

	// h/l are the viewport's horizontal-scroll aliases; with horizontalStep==0
	// they are no-ops here and must NOT trigger an iteration jump.
	before := m.vp.YOffset
	m, _ = step(t, m, runeKey('l'))
	m, _ = step(t, m, runeKey('h'))
	if m.vp.YOffset != before {
		t.Errorf("h/l must not change the vertical offset, got %d want %d", m.vp.YOffset, before)
	}
}
