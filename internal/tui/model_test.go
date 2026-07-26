package tui

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/internal/fsm"
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
	return newModel(ios, loop.Snapshot{}, Header{}, DefaultLogCaps())
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

// isQuit reports whether cmd, when run, yields tea.Quit.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestQuitKeyArmsConfirmation(t *testing.T) {
	// q and ctrl+c no longer quit outright — they arm the yes/no prompt.
	for _, key := range []tea.KeyMsg{runeKey('q'), {Type: tea.KeyCtrlC}} {
		m, cmd := step(t, sized(t, 80, 20), key)
		if !m.confirmingQuit {
			t.Errorf("%v should set confirmingQuit", key)
		}
		if m.quitting {
			t.Errorf("%v should not quit yet", key)
		}
		if isQuit(cmd) {
			t.Errorf("%v should not return tea.Quit before confirmation", key)
		}
	}
}

func TestConfirmYesQuits(t *testing.T) {
	// Only an explicit y/Y quits once the prompt is armed.
	for _, yes := range []tea.KeyMsg{runeKey('y'), runeKey('Y')} {
		m, _ := step(t, sized(t, 80, 20), runeKey('q'))
		m, cmd := step(t, m, yes)
		if !m.quitting {
			t.Errorf("%v should set quitting", yes)
		}
		if !isQuit(cmd) {
			t.Errorf("%v should return tea.Quit", yes)
		}
	}
}

func TestConfirmCancelDoesNotQuit(t *testing.T) {
	// The default is NO: n, esc, and enter all dismiss without quitting.
	for _, no := range []tea.KeyMsg{runeKey('n'), runeKey('N'), {Type: tea.KeyEsc}, {Type: tea.KeyEnter}} {
		m, _ := step(t, sized(t, 80, 20), runeKey('q'))
		m, cmd := step(t, m, no)
		if m.confirmingQuit {
			t.Errorf("%v should dismiss the prompt", no)
		}
		if m.quitting || isQuit(cmd) {
			t.Errorf("%v must not quit (default is no)", no)
		}
	}
}

func TestSecondCtrlCForceQuits(t *testing.T) {
	m, _ := step(t, sized(t, 80, 20), tea.KeyMsg{Type: tea.KeyCtrlC})
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting || !isQuit(cmd) {
		t.Errorf("second ctrl+c should force quit; quitting=%v cmd=%v", m.quitting, isQuit(cmd))
	}
}

func TestKeysSwallowedWhileConfirming(t *testing.T) {
	// Non-answer keys do nothing while the prompt is up — the loop can't be
	// reconfigured out from under a pending quit decision.
	m := sized(t, 100, 24)
	m, _ = step(t, m, runeKey('q'))
	before := m.expanded
	m, _ = step(t, m, runeKey('e'))
	if m.expanded != before {
		t.Errorf("e should be swallowed while confirming")
	}
	if !m.confirmingQuit {
		t.Errorf("prompt should stay up after a swallowed key")
	}
}

func TestConfirmPromptWording(t *testing.T) {
	// Running: a warning that the loop is still going. Finished: a plain quit
	// prompt. Both are armed by q.
	running, _ := step(t, sized(t, 80, 20), runeKey('q'))
	if v := running.View(); !strings.Contains(v, "still running") {
		t.Errorf("running confirm should warn the loop is live:\n%s", v)
	}

	done := sized(t, 80, 20)
	done, _ = step(t, done, doneMsg{})
	done, _ = step(t, done, runeKey('q'))
	if v := done.View(); !strings.Contains(v, "Run complete") {
		t.Errorf("finished confirm should say the run is complete:\n%s", v)
	}
}

func TestExpandTogglesPanel(t *testing.T) {
	m := sized(t, 100, 24)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	// Probe one token from each metric block: the cumulative ("iter 5/20") and
	// the session ("gate pass"). 'e' must collapse BOTH.
	if v := m.View(); !strings.Contains(v, "iter 5/20") || !strings.Contains(v, "gate pass") {
		t.Fatalf("collapsed View should show both metric blocks:\n%s", v)
	}

	m, _ = step(t, m, runeKey('e'))
	if !m.expanded {
		t.Fatalf("'e' should set expanded")
	}
	if v := m.View(); strings.Contains(v, "iter 5/20") || strings.Contains(v, "gate pass") {
		t.Errorf("expanded View should hide both metric blocks:\n%s", v)
	}

	m, _ = step(t, m, runeKey('e'))
	if m.expanded {
		t.Fatalf("'e' should toggle expanded back off")
	}
	if v := m.View(); !strings.Contains(v, "iter 5/20") || !strings.Contains(v, "gate pass") {
		t.Errorf("re-collapsed View should show both metric blocks again:\n%s", v)
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
	m := newModel(ios, loop.Snapshot{}, Header{}, DefaultLogCaps())
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
	return newModel(ios, seed, Header{}, DefaultLogCaps())
}

func TestInitialPanelRendersCapsAtT0(t *testing.T) {
	const w = 100
	m := seededModel(t, loop.Snapshot{MaxIterations: 20, MaxCostUSD: 5})
	m, _ = step(t, m, tea.WindowSizeMsg{Width: w, Height: 24})

	view := m.View()
	// Caps now live in the cumulative block; it renders from t0.
	for _, want := range []string{"iter 0/20", "cost $0.00/$5.00", "elapsed 0s"} {
		if !strings.Contains(view, want) {
			t.Errorf("seed View missing %q before any metricsMsg\n--- view ---\n%s", want, view)
		}
	}
	// The empty seed must not render the redundant narrative "iter 0000" line.
	if strings.Contains(view, "iter 0000") {
		t.Errorf("seed View should omit the narrative line, got:\n%s", view)
	}
	// No session block at t0 → only THREE rules (header/cumulative, cumulative/log,
	// log/help), one fewer than a populated snapshot (TestPanesAreSeparated).
	rule := strings.Repeat("─", w)
	if n := strings.Count(view, rule); n != 3 {
		t.Errorf("seed View should have three rules (no session block), got %d:\n%s", n, view)
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

// headerModel builds a sized model with the given header, then folds in a
// snapshot carrying the given ready count and FSM state/reason. done marks the
// run finished (so the terminal badge shows).
func headerModel(t *testing.T, hdr Header, ready int, state, reason string, done bool) model {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	ios, _ := iostreams.Test()
	m, _ := step(t, newModel(ios, loop.Snapshot{}, hdr, DefaultLogCaps()), tea.WindowSizeMsg{Width: 120, Height: 24})
	m, _ = step(t, m, metricsMsg{s: loop.Snapshot{
		Iter:       1,
		State:      fsm.State(state),
		Reason:     fsm.Reason(reason),
		ReadyBeads: ready,
	}})
	if done {
		m, _ = step(t, m, doneMsg{})
	}
	return m
}

func TestHeaderRendersIdentityAndReady(t *testing.T) {
	m := headerModel(t, Header{Version: "1.2.3", Dir: "/srv/proj"}, 4, "clean", "", false)
	view := m.View()
	for _, w := range []string{"ralph 1.2.3", "/srv/proj", "4 ready"} {
		if !strings.Contains(view, w) {
			t.Errorf("header missing %q:\n%s", w, view)
		}
	}
	// No terminal badge while the run is live.
	if strings.Contains(view, "done") || strings.Contains(view, "failed") {
		t.Errorf("running header should carry no terminal badge:\n%s", view)
	}
}

func TestHeaderReadyUnsampled(t *testing.T) {
	m := headerModel(t, Header{Version: "dev", Dir: "/p"}, -1, "clean", "", false)
	if !strings.Contains(m.View(), "… ready") {
		t.Errorf("unsampled ready count should render '… ready':\n%s", m.View())
	}
}

func TestHeaderDoneBadge(t *testing.T) {
	m := headerModel(t, Header{Version: "dev", Dir: "/p"}, 0, "done", "queue_empty", true)
	if !strings.Contains(m.View(), "✓ done (queue_empty)") {
		t.Errorf("finished header should show the done badge:\n%s", m.View())
	}
}

func TestHeaderFailedBadge(t *testing.T) {
	m := headerModel(t, Header{Version: "dev", Dir: "/p"}, 3, "failed", "budget", true)
	if !strings.Contains(m.View(), "✗ failed (budget)") {
		t.Errorf("finished-with-failure header should show the failed badge:\n%s", m.View())
	}
}

func TestHeaderStoppedBadge(t *testing.T) {
	// done with a non-terminal FSM state (e.g. ctx-cancel before a terminal
	// outcome) renders the neutral "stopped" badge, not done/failed.
	m := headerModel(t, Header{Version: "dev", Dir: "/p"}, 2, "clean", "ready", true)
	view := m.View()
	if !strings.Contains(view, "■ stopped") {
		t.Errorf("non-terminal finish should show the stopped badge:\n%s", view)
	}
	if strings.Contains(view, "✓ done") || strings.Contains(view, "✗ failed") {
		t.Errorf("stopped finish must not claim done/failed:\n%s", view)
	}
}

func TestFinishedHeaderNoANSIWhenColorOff(t *testing.T) {
	// The done/failed badge styles (Foreground/Faint) must degrade to plain
	// text under the renderer's ascii profile (non-TTY here), so a finished
	// header emits no escape sequences.
	for _, tc := range []struct{ state, reason string }{
		{"done", "queue_empty"},
		{"failed", "budget"},
		{"clean", "ready"}, // stopped branch
	} {
		m := headerModel(t, Header{Version: "1.0", Dir: "/p"}, 0, tc.state, tc.reason, true)
		if strings.Contains(m.View(), "\x1b") {
			t.Errorf("finished header for state %q leaked ANSI under NO_COLOR:\n%q", tc.state, m.View())
		}
	}
}

func TestAbbrevHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir resolvable")
	}
	if got := abbrevHome(home + "/repos/ralph"); got != "~/repos/ralph" {
		t.Errorf("abbrevHome under home = %q, want %q", got, "~/repos/ralph")
	}
	if got := abbrevHome("/etc/elsewhere"); got != "/etc/elsewhere" {
		t.Errorf("abbrevHome outside home should pass through, got %q", got)
	}
}

func TestPanesAreSeparated(t *testing.T) {
	const w = 100
	m := sized(t, w, 24)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})

	// With a populated snapshot both metric blocks render, so there are FOUR
	// rules: header/cumulative, cumulative/session, session/log, log/help. (At
	// the t0 seed the session block is absent, leaving three — see
	// TestInitialPanelRendersCapsAtT0.)
	rule := strings.Repeat("─", w)
	if n := strings.Count(m.View(), rule); n != 4 {
		t.Errorf("View should contain four full-width %d-col rules (header/cum + cum/session + session/log + log/help), got %d:\n%s", w, n, m.View())
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
// every boundary is reachable. With fullSnapshot the two metric blocks total 4
// content lines (cumulative 1 + session 3: status/beads/narrative); relayout
// reserves header+sep (1+1) + cumulative+sep (1+1) + session+sep (3+1) +
// help+sep (1+1) = 10, so vp.Height = 18-10 = 8 and maxYOffset = 32-8 = 24 >= 22.
func seedIterLogs(t *testing.T) model {
	t.Helper()
	m := sized(t, 80, 18)
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

// quotaWaitSnapshot returns a snapshot shaped like the loop's quota-wait row:
// a "quota-wait" Skipped marker carrying the wait duration the badge counts
// down from.
func quotaWaitSnapshot(secs int) loop.Snapshot {
	s := fullSnapshot()
	s.Record.Skipped = "quota-wait"
	s.Record.QuotaWaitSecs = secs
	return s
}

// A quota-wait snapshot lights the header sleep badge with a countdown; a
// subsequent ordinary snapshot clears it.
func TestQuotaWaitBadgeShowsAndClears(t *testing.T) {
	m := sized(t, 120, 24)
	m, _ = step(t, m, metricsMsg{s: quotaWaitSnapshot(1800)})

	if !m.waiting {
		t.Fatal("quota-wait snapshot should set waiting")
	}
	view := m.View()
	if !strings.Contains(view, "sleeping (quota)") {
		t.Errorf("header should show the sleep badge:\n%s", view)
	}
	if !strings.Contains(view, "resuming in 30m0s") {
		t.Errorf("badge should show the initial 30m countdown:\n%s", view)
	}

	// A normal iteration snapshot clears the badge.
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	if m.waiting {
		t.Error("an ordinary snapshot should clear waiting")
	}
	if strings.Contains(m.View(), "sleeping (quota)") {
		t.Errorf("badge should be gone after a normal snapshot:\n%s", m.View())
	}
}

// The countdown decrements one second per elapsed tick while waiting.
func TestQuotaWaitBadgeCountsDown(t *testing.T) {
	m := sized(t, 120, 24)
	m, _ = step(t, m, metricsMsg{s: quotaWaitSnapshot(120)})

	for i := 0; i < 5; i++ {
		m, _ = step(t, m, tickMsg{})
	}
	if got, want := m.waitRemaining, 115*time.Second; got != want {
		t.Errorf("waitRemaining = %s after 5 ticks, want %s", got, want)
	}
	if !strings.Contains(m.View(), "resuming in 1m55s") {
		t.Errorf("badge should reflect the decremented countdown:\n%s", m.View())
	}
}

// Once the countdown bottoms out before the loop's next snapshot, the badge
// shows a bare "resuming…" rather than "resuming in 0s".
func TestQuotaWaitBadgeFloorsToResuming(t *testing.T) {
	m := sized(t, 120, 24)
	m, _ = step(t, m, metricsMsg{s: quotaWaitSnapshot(2)})

	for i := 0; i < 5; i++ {
		m, _ = step(t, m, tickMsg{})
	}
	if m.waitRemaining != 0 {
		t.Errorf("waitRemaining = %s, want floored to 0", m.waitRemaining)
	}
	view := m.View()
	if !strings.Contains(view, "resuming…") || strings.Contains(view, "resuming in") {
		t.Errorf("badge should show bare 'resuming…' at zero:\n%s", view)
	}
}

// TestStartupFailureQuitsWithBadge covers ralph-62i: a loop.Run error with no
// iteration ever observed is a startup failure (lock contention, a terminal
// fsm.json). There is nothing on screen to review, so the model must quit
// immediately and badge the real reason — the old behavior parked on the
// zero-value "■ stopped" badge with the error trapped behind a keypress.
func TestStartupFailureQuitsWithBadge(t *testing.T) {
	m := sized(t, 100, 24)
	m, cmd := step(t, m, doneMsg{err: errors.New("lock: held by another process: pid 1558335"), observed: false})

	if !m.done || !m.quitting {
		t.Fatalf("done = %v, quitting = %v; want both true", m.done, m.quitting)
	}
	if cmd == nil {
		t.Fatal("startup failure must return tea.Quit")
	}
	view := m.View()
	if !strings.Contains(view, "failed to start") || !strings.Contains(view, "pid 1558335") {
		t.Errorf("View should badge the startup error, got:\n%s", view)
	}
	if strings.Contains(view, "stopped") {
		t.Errorf("startup failure must not render the empty-state badge, got:\n%s", view)
	}
}

// TestMidRunErrorKeepsScreenForReview is the other half: once the loop has
// reported iterations, an error is badged but the run stays on screen — the
// review affordance the model was built for.
func TestMidRunErrorKeepsScreenForReview(t *testing.T) {
	m := sized(t, 100, 24)
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	m, cmd := step(t, m, doneMsg{err: errors.New("bd exploded"), observed: true})

	if m.quitting {
		t.Error("a mid-run error must not quit; the finished screen stays up")
	}
	if cmd != nil {
		t.Errorf("a mid-run error must not return tea.Quit, got %T", cmd())
	}
	if view := m.View(); !strings.Contains(view, "✗ error: bd exploded") {
		t.Errorf("View should badge the mid-run error, got:\n%s", view)
	}
}

// beadWaitSnapshot is a park update from the bead wait: no summary row of its
// own, so the Record still describes the last real iteration and the park
// itself rides on Snapshot.Wait.
func beadWaitSnapshot(elapsed, remaining time.Duration) loop.Snapshot {
	s := fullSnapshot()
	s.Wait = &loop.WaitState{Kind: "beads", Elapsed: elapsed, Remaining: remaining}
	return s
}

// The bead park badge reports both halves an open-ended wait needs: how long
// it has been waiting, and when it will look again.
func TestBeadWaitBadgeShowsAndClears(t *testing.T) {
	m := sized(t, 120, 24)
	m, _ = step(t, m, metricsMsg{s: beadWaitSnapshot(14*time.Minute, 45*time.Second)})

	if !m.waiting || m.waitKind != "beads" {
		t.Fatalf("waiting = %v, kind = %q; want true/beads", m.waiting, m.waitKind)
	}
	view := m.View()
	if !strings.Contains(view, "waiting for beads") {
		t.Errorf("header should show the bead-wait badge:\n%s", view)
	}
	if !strings.Contains(view, "14m0s") || !strings.Contains(view, "next check in 45s") {
		t.Errorf("badge should show elapsed and next-check:\n%s", view)
	}

	// Resuming sends an ordinary iteration snapshot, which must clear it.
	m, _ = step(t, m, metricsMsg{s: fullSnapshot()})
	if m.waiting {
		t.Error("an ordinary snapshot should clear waiting")
	}
	if strings.Contains(m.View(), "waiting for beads") {
		t.Errorf("badge should be gone after resuming:\n%s", m.View())
	}
}

// A park can outlast many polls, so elapsed keeps climbing on ticks even after
// the next-check countdown bottoms out.
func TestBeadWaitBadgeAdvancesElapsed(t *testing.T) {
	m := sized(t, 120, 24)
	m, _ = step(t, m, metricsMsg{s: beadWaitSnapshot(time.Minute, 2*time.Second)})

	for i := 0; i < 5; i++ {
		m, _ = step(t, m, tickMsg{})
	}
	if got, want := m.waitElapsed, 65*time.Second; got != want {
		t.Errorf("waitElapsed = %s after 5 ticks, want %s", got, want)
	}
	if m.waitRemaining != 0 {
		t.Errorf("waitRemaining = %s, want floored to 0", m.waitRemaining)
	}
	if view := m.View(); strings.Contains(view, "next check in") {
		t.Errorf("badge should drop the countdown at zero:\n%s", view)
	}
}
