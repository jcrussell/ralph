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
	"os"
	"strings"
	"time"
	"unicode/utf8"

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
	headerHeight = 1
	helpHeight   = 1
	sepHeight    = 1 // a divider rule is one terminal row
	tickInterval = time.Second
)

// Header holds the static identity shown in the run header: the tool version
// and the working directory. The dynamic fields (ready-bead count, terminal
// state) come from the per-iteration Snapshot, not here.
type Header struct {
	Version string // build.Info().Version, e.g. "0.3.1" or "dev"
	Dir     string // repo root / cwd the run is operating on
}

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

// doneMsg signals that loop.Run has reached a terminal outcome. The program
// orchestration (bead ralph-g3s.7) Sends it when the loop goroutine returns.
// The model does NOT quit on it: the run stays on screen so the operator can
// scroll the log pane and review what happened, freezing the elapsed clock and
// switching the help line to "run complete". Run unblocks only when the user
// presses q / Ctrl-C; the orchestration's subsequent cancel is then a no-op
// (the loop already returned) and its buffered result is read immediately.
type doneMsg struct{}

// model is the package-private Bubble Tea model. It satisfies tea.Model
// with value receivers (the bubbletea convention); Update returns the
// mutated copy.
type model struct {
	f   *Formatter
	vp  viewport.Model
	r   *lipgloss.Renderer
	hdr Header

	colorEnabled bool

	snap        loop.Snapshot
	hasSnap     bool
	liveElapsed time.Duration

	logs *logRing

	width, height int
	ready         bool
	expanded      bool
	quitting      bool
	done          bool // loop.Run returned; keep rendering for review, quit only on q/ctrl+c

	// quotaWaiting is set while the loop sleeps on a quota cap (the latest
	// snapshot is a "quota-wait" row); quotaRemaining counts down to the
	// resume, advanced by the elapsed tick like liveElapsed.
	quotaWaiting   bool
	quotaRemaining time.Duration
}

// newModel builds the model for the given streams. The live TUI renders
// to ErrOut (chatter, per byob-iostreams.3), so color is gated on the
// stderr TTY and NO_COLOR exactly as the metrics Formatter is; the
// lipgloss renderer's color profile is pinned to match so View emits no
// ANSI when color is off.
//
// initial seeds the metrics panel so it renders at t0 (before the loop's
// first iteration) with the configured caps and zeroed counters; the first
// metricsMsg overwrites it. Seeding hasSnap also starts the elapsed ticker
// from construction. hdr carries the static header identity (version, cwd)
// rendered above the panel. caps bounds the scrollback ring (config-driven,
// with a default fallback) so a long run can page back further than the old
// fixed ceiling without letting the pane grow without bound.
func newModel(ios *iostreams.IOStreams, initial loop.Snapshot, hdr Header, caps LogCaps) model {
	colorEnabled := ios.IsStderrTTY() && iostreams.EnvAllowsColor()
	r := lipgloss.NewRenderer(ios.ErrOut)
	if !colorEnabled {
		r.SetColorProfile(termenv.Ascii)
	}
	return model{
		f:            NewFormatter(ios),
		vp:           viewport.New(0, 0),
		r:            r,
		hdr:          hdr,
		colorEnabled: colorEnabled,
		logs:         newLogRing(caps),
		snap:         initial,
		hasSnap:      true,
		liveElapsed:  initial.Elapsed, // 0 for the seed; tick advances from here
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
		case "left", "right":
			m.jumpIteration(msg.String() == "right")
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
		// A quota-wait row puts the loop to sleep; seed the countdown so the
		// header badge shows time-to-resume. Any other row clears it.
		m.quotaWaiting = msg.s.Record.Skipped == "quota-wait"
		if m.quotaWaiting {
			m.quotaRemaining = time.Duration(msg.s.Record.QuotaWaitSecs) * time.Second
		}
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
		if m.done {
			return m, nil // stop advancing elapsed and stop rescheduling once finished
		}
		if m.hasSnap {
			m.liveElapsed += tickInterval
		}
		if m.quotaWaiting && m.quotaRemaining > 0 {
			m.quotaRemaining -= tickInterval
			if m.quotaRemaining < 0 {
				m.quotaRemaining = 0
			}
		}
		return m, tick()

	case doneMsg:
		m.done = true // run finished; keep rendering so the user can scroll/review
		return m, nil // NO tea.Quit — the user quits with q/ctrl+c
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
	reserved := headerHeight + sepHeight // header line + its divider (always shown)
	reserved += helpHeight + sepHeight   // help line + its divider
	if !m.expanded {
		// Mirror View's gating exactly (same cumulativeView/sessionView calls) so
		// the reserved height and the rendered sections can never disagree.
		if cv := m.cumulativeView(); cv != "" {
			reserved += lineCount(cv) + sepHeight // cumulative block + its divider
		}
		if sv := m.sessionView(); sv != "" {
			reserved += lineCount(sv) + sepHeight // session block + its divider
		}
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

	sections := make([]string, 0, 9)
	sections = append(sections, m.headerView(), m.sepView())
	if !m.expanded {
		// Two metric blocks, each followed by its own rule: cumulative run totals,
		// then the current/last-iteration session block (dropped, with its rule,
		// at the t0 seed when sessionView is empty).
		if cv := m.cumulativeView(); cv != "" {
			sections = append(sections, cv, m.sepView())
		}
		if sv := m.sessionView(); sv != "" {
			sections = append(sections, sv, m.sepView())
		}
	}
	sections = append(sections, m.vp.View(), m.sepView(), m.helpView())
	return stripTrailingSpaces(lipgloss.JoinVertical(lipgloss.Left, sections...))
}

// stripTrailingSpaces removes the right-edge padding lipgloss.JoinVertical adds
// to align every block to the widest one (here the full-width separator).
// Content lines then render at their natural width; the ─ rules are all box
// glyphs with nothing to trim. Keeps copied frames clean and avoids spurious
// wraps when a padded line would overflow a slightly-stale width.
func stripTrailingSpaces(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

// degradedView is the tiny-window fallback: the two metric blocks joined and
// clipped to the available width (each block already wraps to width) and height,
// dropping the scroll pane and separators entirely. Joining preserves the normal
// ordering (cumulative totals first, then current-iteration status), so a tiny
// window still leads with the same line it would at full size. With no snapshot
// yet it shows the bare tool name.
func (m model) degradedView() string {
	content := m.cumulativeView()
	if sv := m.sessionView(); sv != "" {
		if content != "" {
			content += "\n"
		}
		content += sv
	}
	if content == "" {
		return truncatePlain("ralph run", m.width)
	}
	lines := strings.Split(content, "\n")
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// metricsSnap returns the latest snapshot with the live (tick-advanced) elapsed
// substituted for the Snapshot's iteration-stamped value, and ok=false until the
// first metricsMsg arrives. Both metric blocks render from it, so the live-
// elapsed substitution and the has-snapshot guard live in one place.
func (m model) metricsSnap() (loop.Snapshot, bool) {
	if !m.hasSnap {
		return loop.Snapshot{}, false
	}
	s := m.snap
	s.Elapsed = m.liveElapsed
	return s, true
}

// cumulativeView renders the whole-run totals block. Empty before the first
// snapshot.
func (m model) cumulativeView() string {
	s, ok := m.metricsSnap()
	if !ok {
		return ""
	}
	return m.f.RenderCumulative(s, m.width)
}

// sessionView renders the current/last-iteration block. Empty before the first
// snapshot and at the pre-first-iteration seed (RenderSession returns "" there).
func (m model) sessionView() string {
	s, ok := m.metricsSnap()
	if !ok {
		return ""
	}
	return m.f.RenderSession(s, m.width)
}

// headerView renders the top identity line: "ralph <version>  ·  <dir>", plus a
// colored terminal badge once the run has finished (or a quota-sleep badge while
// waiting). The ready-bead count moved into the cumulative block; the badge is
// driven by the authoritative FSM state in the latest Snapshot. Like the panel's
// lines, color is applied only when the plain text fits the width;
// otherwise it falls back to truncated plain text so a clip never severs an
// ANSI escape. Color degrades to plain under the renderer's ascii profile,
// exactly like helpView/sepView.
func (m model) headerView() string {
	bold := func(s string) string { return m.r.NewStyle().Bold(true).Render(s) }

	segs := []seg{
		{text: "ralph " + m.hdr.Version, color: bold},
		{text: abbrevHome(m.hdr.Dir)},
	}
	switch {
	case m.done:
		segs = append(segs, m.doneBadge())
	case m.quotaWaiting:
		segs = append(segs, m.quotaBadge())
	}

	// Drop empty segments (e.g. an unset dir) so no dangling separator shows,
	// then apply the fit-or-truncate rule the metrics panel uses.
	nonEmpty := segs[:0:0]
	for _, s := range segs {
		if s.text != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	plain := joinSegs(nonEmpty, false)
	if m.width <= 0 || utf8.RuneCountInString(plain) <= m.width {
		return joinSegs(nonEmpty, true)
	}
	return truncatePlain(plain, m.width)
}

// doneBadge renders the terminal-state badge shown once the run has finished.
// done{*} is green, failed{*} red, anything else (e.g. a mid-run quit) a faint
// "stopped"; the reason, when present, is parenthesized.
func (m model) doneBadge() seg {
	st := string(m.snap.State)
	reason := ""
	if m.snap.Reason != "" {
		reason = " (" + string(m.snap.Reason) + ")"
	}
	fg := func(c string) func(string) string {
		return func(s string) string { return m.r.NewStyle().Foreground(lipgloss.Color(c)).Render(s) }
	}
	switch {
	case strings.HasPrefix(st, "done"):
		return seg{text: "✓ done" + reason, color: fg("2")}
	case strings.HasPrefix(st, "failed"):
		return seg{text: "✗ failed" + reason, color: fg("1")}
	default:
		return seg{text: "■ stopped", color: func(s string) string { return m.r.NewStyle().Faint(true).Render(s) }}
	}
}

// quotaBadge renders the header badge shown while the loop sleeps on a quota
// cap. It counts down to the resume; once the countdown bottoms out (the loop
// hasn't sent its next snapshot yet) it shows a bare "resuming…". Yellow,
// degrading to plain under the ascii profile like doneBadge. Plain text only —
// every rune is single-width so the header's rune-count fit check in headerView
// stays accurate.
func (m model) quotaBadge() seg {
	text := "sleeping (quota) — resuming…"
	if m.quotaRemaining > 0 {
		text = "sleeping (quota) — resuming in " + dur(m.quotaRemaining)
	}
	yellow := func(s string) string { return m.r.NewStyle().Foreground(lipgloss.Color("3")).Render(s) }
	return seg{text: text, color: yellow}
}

// abbrevHome shortens a leading $HOME to "~" for a compact header. A dir
// outside home, or an undeterminable home, is returned unchanged.
func abbrevHome(dir string) string {
	if dir == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	if dir == home || dir == home+string(os.PathSeparator) {
		return "~"
	}
	if strings.HasPrefix(dir, home+string(os.PathSeparator)) {
		return "~" + dir[len(home):]
	}
	return dir
}

// helpView renders the key hints, faint when color is enabled. Once the run
// has finished a "run complete" prefix is added; the scroll and expand/collapse
// affordances stay so a user who finished in expanded mode can still collapse
// back to the frozen metrics panel (which shows the terminal state).
func (m model) helpView() string {
	expand := "e expand"
	if m.expanded {
		expand = "e collapse"
	}
	help := "↑/↓ scroll · ←/→ iter · " + expand + " · q quit"
	if m.done {
		help = "run complete · " + help
	}
	help = truncatePlain(help, m.width)
	return m.r.NewStyle().Faint(true).Render(help)
}

// sepView renders a faint full-width horizontal rule separating the panel,
// log pane, and help line. Faint degrades to plain under the ascii profile,
// exactly like helpView; the ─ rune prints regardless (UTF-8, like the · and
// ↑/↓ glyphs the panel and help already use).
func (m model) sepView() string {
	return m.r.NewStyle().Faint(true).Render(strings.Repeat("─", m.width))
}

// jumpIteration moves the log viewport to the start of the previous (forward
// false) or next (forward true) iteration's logs. Boundaries come from the
// loop's "iter NNNN" summary lines in the pane (iterStartLines). With no
// completed iteration yet it is a no-op; forward past the last boundary snaps to
// the live tail, backward past the first to the top. The viewport's YOffset
// indexes content lines 1:1 with the log ring (it does not soft-wrap), so a ring
// line index is a valid YOffset; SetYOffset clamps to range.
func (m *model) jumpIteration(forward bool) {
	starts := m.iterStartLines()
	cur := m.vp.YOffset
	if forward {
		for _, s := range starts {
			if s > cur {
				m.vp.SetYOffset(s)
				return
			}
		}
		m.vp.GotoBottom()
		return
	}
	for i := len(starts) - 1; i >= 0; i-- {
		if starts[i] < cur {
			m.vp.SetYOffset(starts[i])
			return
		}
	}
	m.vp.GotoTop()
}

// iterStartLines returns the log-pane line indices where each iteration's logs
// begin: index 0 (the first iteration) plus the line after every "iter NNNN"
// summary marker the loop emits at each iteration's end. Ascending. Recomputed
// per keypress so it stays correct as the bounded ring evicts old lines.
func (m model) iterStartLines() []int {
	starts := []int{0}
	for i, ln := range m.logs.lines {
		if i+1 < len(m.logs.lines) && isIterMarker(ln) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// isIterMarker reports whether line is one of the loop's per-iteration summary
// lines — "iter NNNN  …" with a zero-padded count and two trailing spaces, as
// emitIterLine writes (internal/loop). The prefix is plain ASCII (color, if any,
// lands inside the narrative text), so a raw prefix match is reliable.
func isIterMarker(line string) bool {
	rest, ok := strings.CutPrefix(line, "iter ")
	if !ok {
		return false
	}
	d := 0
	for d < len(rest) && rest[d] >= '0' && rest[d] <= '9' {
		d++
	}
	return d >= 4 && strings.HasPrefix(rest[d:], "  ") // %04d + two spaces
}

// lineCount counts the lines in s (0 for empty).
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
