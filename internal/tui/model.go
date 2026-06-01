package tui

// model.go is the Bubble Tea skeleton for the live `ralph run` display
// (epic ralph-g3s): a top metrics panel rendered by the package's
// Formatter over an expandable/scrollable log pane (bubbles/viewport).
// It is the byob-progress.3 carve-out — the one sanctioned reach past
// stdlib — so the concrete tea/lipgloss types stay unexported here; only
// the narrow seam a caller needs leaves the package (in a follow-up bead).
//
// The model is a pure state machine over messages: it touches no real
// terminal and starts no tea.Program, so Update/View are unit-testable by
// feeding messages and asserting on the returned View and state. Live
// elapsed advances off a one-second tea.Tick rather than a wall clock, so
// it stays deterministic under test and authoritative metrics from each
// metricsMsg reset the local offset.

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Layout constants. Below the minimums the split layout collapses to a
// single clipped metrics block so a tiny window never panics or renders
// garbage.
const (
	minWidth     = 24
	minHeight    = 6
	helpHeight   = 1
	tickInterval = time.Second
)

// metricsMsg carries a fresh per-iteration Snapshot into the model. The
// future Observer (bead 7) wraps each loop.Snapshot and Sends it; it
// resets the live-elapsed offset because the Snapshot's Elapsed is
// authoritative as of that iteration.
type metricsMsg struct{ s loop.Snapshot }

// logLineMsg carries one already-split log line into the pane. It lands in
// the bounded, ANSI-tolerant logRing (logring.go), which caps scrollback so
// a long run cannot OOM the TUI.
type logLineMsg struct{ line string }

// tickMsg drives the once-a-second elapsed bump between iterations.
type tickMsg struct{}

// model is the package-private Bubble Tea model. It satisfies tea.Model
// with value receivers (the bubbletea convention); Update returns the
// mutated copy.
type model struct {
	f  *Formatter
	vp viewport.Model
	r  *lipgloss.Renderer

	colorEnabled bool

	snap        loop.Snapshot
	hasSnap     bool
	liveElapsed time.Duration

	logs *logRing

	width, height int
	ready         bool
	expanded      bool
	quitting      bool
}

// newModel builds the model for the given streams. The live TUI renders
// to ErrOut (chatter, per byob-iostreams.3), so color is gated on the
// stderr TTY and NO_COLOR exactly as the metrics Formatter is; the
// lipgloss renderer's color profile is pinned to match so View emits no
// ANSI when color is off.
func newModel(ios *iostreams.IOStreams) model {
	colorEnabled := ios.IsStderrTTY() && iostreams.EnvAllowsColor()
	r := lipgloss.NewRenderer(ios.ErrOut)
	if !colorEnabled {
		r.SetColorProfile(termenv.Ascii)
	}
	return model{
		f:            NewFormatter(ios),
		vp:           viewport.New(0, 0),
		r:            r,
		colorEnabled: colorEnabled,
		logs:         newLogRing(),
	}
}

// Init schedules the first elapsed tick.
func (model) Init() tea.Cmd { return tick() }

// tick produces the once-a-second elapsed command.
func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// Update folds one message into the model. It is the sole serialization
// point for metrics, log, key, resize, and tick events (no shared state),
// per the ralph-g3s.1 transport decision.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.relayout()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "e", "tab":
			m.expanded = !m.expanded
			m.relayout()
			return m, nil
		default:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}

	case metricsMsg:
		m.snap = msg.s
		m.hasSnap = true
		m.liveElapsed = msg.s.Elapsed
		m.relayout() // panel height can change as fields populate
		return m, nil

	case logLineMsg:
		atBottom := m.vp.AtBottom()
		m.logs.push(msg.line)
		m.vp.SetContent(m.logs.content())
		if atBottom {
			m.vp.GotoBottom() // follow the tail unless the user scrolled up
		}
		return m, nil

	case tickMsg:
		if m.hasSnap {
			m.liveElapsed += tickInterval
		}
		return m, tick()
	}
	return m, nil
}

// relayout recomputes the viewport size from the current window size and
// the height the metrics panel currently occupies. It is a no-op until
// the first WindowSizeMsg arrives.
func (m *model) relayout() {
	if !m.ready {
		return
	}
	reserved := helpHeight
	if !m.expanded {
		reserved += lineCount(m.panelView())
	}
	h := m.height - reserved
	if h < 1 {
		h = 1
	}
	m.vp.Width = m.width
	m.vp.Height = h
}

// View renders the whole display. Before the first resize it shows a
// placeholder; below the size minimums it degrades to a clipped metrics
// block; otherwise it stacks the panel (unless expanded), the log
// viewport, and the help line.
func (m model) View() string {
	if !m.ready {
		return "initializing…"
	}
	if m.width < minWidth || m.height < minHeight {
		return m.degradedView()
	}

	sections := make([]string, 0, 3)
	if !m.expanded {
		if p := m.panelView(); p != "" {
			sections = append(sections, p)
		}
	}
	sections = append(sections, m.vp.View(), m.helpView())
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// degradedView is the tiny-window fallback: the metrics panel clipped to
// the available width (panelView already wraps to width) and height,
// dropping the scroll pane entirely. With no snapshot yet it shows the
// bare tool name.
func (m model) degradedView() string {
	content := m.panelView()
	if content == "" {
		return truncatePlain("ralph run", m.width)
	}
	lines := strings.Split(content, "\n")
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// panelView renders the metrics panel at the current width, substituting
// the live (tick-advanced) elapsed for the Snapshot's iteration-stamped
// value. It returns "" until the first metricsMsg arrives.
func (m model) panelView() string {
	if !m.hasSnap {
		return ""
	}
	s := m.snap
	s.Elapsed = m.liveElapsed
	return m.f.Render(s, m.width)
}

// helpView renders the key hints, faint when color is enabled.
func (m model) helpView() string {
	expand := "e expand"
	if m.expanded {
		expand = "e collapse"
	}
	help := truncatePlain("↑/↓ scroll · "+expand+" · q quit", m.width)
	return m.r.NewStyle().Faint(true).Render(help)
}

// lineCount counts the lines in s (0 for empty).
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
