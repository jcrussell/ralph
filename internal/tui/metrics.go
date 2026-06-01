// Package tui renders the live `ralph run` display.
//
// This file is the metrics foundation for the live-run UI (epic
// ralph-g3s): a pure, TTY-adaptive Formatter that renders the top metrics
// panel from a loop.Snapshot — the value the loop hands across its
// Observer seam (see internal/loop/seams.go), already carrying everything
// the panel needs without reaching back into loop internals. The
// formatter has no Bubble Tea dependency and performs no terminal
// control: it turns a Snapshot plus a target width into a string, so it
// is unit-testable on its own. Color degrades to identity when the
// IOStreams color scheme is disabled (the byob-output.1 TTY-adaptive
// pattern, mirroring pkg/tableprinter); width drives graceful truncation.
// Terminal wiring lands in follow-up beads.
package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/internal/narrative"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Formatter renders a loop.Snapshot as the live-run metrics panel. It
// holds the color scheme to use; whether color actually emits ANSI is a
// property of that scheme, so the formatter never branches on isatty
// itself (byob-output.1). Construct via NewFormatter.
type Formatter struct {
	cs *iostreams.ColorScheme
}

// NewFormatter builds a Formatter that colors using the ErrOut scheme.
// The live TUI renders to ErrOut (chatter, per byob-iostreams.3), so
// color tracks the stream the panel lands on.
func NewFormatter(ios *iostreams.IOStreams) *Formatter {
	return &Formatter{cs: ios.ErrColorScheme()}
}

// seg is one colorable run of text within a line. A nil color renders the
// text unchanged.
type seg struct {
	text  string
	color func(string) string
}

// fieldSep separates fields within a panel line.
const fieldSep = "  ·  "

// Render renders the metrics panel for s, wrapping each line to width.
// width <= 0 disables truncation (full content). The result has no
// trailing newline; the caller owns line termination and update cadence.
//
// Lines are emitted top-to-bottom: status, budget/time, last-iteration,
// bead deltas (omitted when empty), and the one-line narrative (reused
// from internal/narrative). A line whose plain width exceeds the target
// is rendered without color and truncated with an ellipsis, so a clipped
// edge never severs an ANSI escape.
func (f *Formatter) Render(s loop.Snapshot, width int) string {
	var lines []string

	lines = append(lines, f.line(width, []seg{
		{text: "iter " + iterField(s.Iter, s.MaxIterations), color: f.cs.Bold},
		{text: stateText(s), color: f.stateColor(s)},
		{text: "gate " + gateText(s.LastGateResult), color: f.gateColor(s.LastGateResult)},
	}))

	budget := []seg{
		{text: "cost " + moneyCap(s.CumulativeCostUSD, s.MaxCostUSD)},
		{text: "elapsed " + dur(s.Elapsed)},
		{text: "wall " + durCap(secs(s.CumulativeWallclockSecs), s.MaxWallclockSecs)},
	}
	if s.ConsecutiveDirty > 0 {
		budget = append(budget, seg{text: fmt.Sprintf("dirty×%d", s.ConsecutiveDirty), color: f.cs.Yellow})
	}
	lines = append(lines, f.line(width, budget))

	if r := s.Record; r.RunnerMode != "" || r.CostUSD > 0 || r.DurationSecs > 0 || r.Commits > 0 {
		mode := r.RunnerMode
		if mode == "" {
			mode = "—"
		}
		lines = append(lines, f.line(width, []seg{
			{text: "runner " + mode},
			{text: money(r.CostUSD)},
			{text: fmt.Sprintf("%.1fs", r.DurationSecs)},
			{text: fmt.Sprintf("%d commits", r.Commits)},
		}))
	}

	if d := beadDelta(s); d != "" {
		lines = append(lines, f.line(width, []seg{{text: "beads: " + d}}))
	}

	// The narrative line is omitted for the pre-first-iteration seed (iter 0,
	// empty record), which would otherwise render a redundant "iter 0000".
	if s.Iter > 0 || s.Record.Narrative != "" || s.Record.State != "" {
		lines = append(lines, truncatePlain(
			narrative.FormatNarrative(s.Iter, s.Record.Narrative, s.Record.State), width))
	}

	return strings.Join(lines, "\n")
}

// line renders one panel line from its segments, joined by fieldSep.
// Empty-text segments drop out so an omitted field leaves no dangling
// separator. When the joined plain text fits within width (or width is
// unbounded) it renders with color; otherwise it falls back to plain
// truncated text so a clip never cuts through an ANSI escape.
func (*Formatter) line(width int, segs []seg) string {
	nonEmpty := segs[:0:0]
	for _, s := range segs {
		if s.text != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	plain := joinSegs(nonEmpty, false)
	if width <= 0 || utf8.RuneCountInString(plain) <= width {
		return joinSegs(nonEmpty, true)
	}
	return truncatePlain(plain, width)
}

// joinSegs concatenates segments with fieldSep, applying each segment's
// color only when color is true and the segment has one.
func joinSegs(segs []seg, color bool) string {
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			b.WriteString(fieldSep)
		}
		if color && s.color != nil {
			b.WriteString(s.color(s.text))
		} else {
			b.WriteString(s.text)
		}
	}
	return b.String()
}

// iterField renders the iteration counter, with a denominator only when a
// finite ceiling is configured.
func iterField(iter, max int) string {
	if max > 0 {
		return fmt.Sprintf("%d/%d", iter, max)
	}
	return strconv.Itoa(iter)
}

// stateText renders the FSM state, appending the reason in parentheses
// when present. An empty state renders as a dash.
func stateText(s loop.Snapshot) string {
	st := string(s.State)
	if st == "" {
		st = "—"
	}
	if s.Reason != "" {
		st += " (" + string(s.Reason) + ")"
	}
	return st
}

// stateColor highlights terminal states: red for failure, green for a
// clean finish, neutral otherwise.
func (f *Formatter) stateColor(s loop.Snapshot) func(string) string {
	switch {
	case strings.HasPrefix(string(s.State), "failed"):
		return f.cs.Red
	case strings.HasPrefix(string(s.State), "done"):
		return f.cs.Green
	default:
		return nil
	}
}

// gateText maps a gate result to a compact label. It recognizes the
// internal/narrative vocabulary and the green/red synonyms, passing any
// unknown value through unchanged so nothing is silently dropped.
func gateText(g string) string {
	switch g {
	case "", narrative.GateNotRun:
		return "—"
	case narrative.GatePassed, "green":
		return "pass"
	case narrative.GateFailed, "red":
		return "fail"
	case narrative.GateSkipped:
		return "skip"
	default:
		return g
	}
}

// gateColor colors a passing gate green and a failing gate red.
func (f *Formatter) gateColor(g string) func(string) string {
	switch g {
	case narrative.GatePassed, "green":
		return f.cs.Green
	case narrative.GateFailed, "red":
		return f.cs.Red
	default:
		return nil
	}
}

// money formats a dollar amount to cents.
func money(v float64) string { return fmt.Sprintf("$%.2f", v) }

// moneyCap renders a cost, appending "/cap" only when a cap is set.
func moneyCap(cur, limit float64) string {
	if limit > 0 {
		return money(cur) + "/" + money(limit)
	}
	return money(cur)
}

// dur rounds a duration to whole seconds for stable display, clamping
// negatives to zero.
func dur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

// secs converts a whole-second count to a Duration.
func secs(n int) time.Duration { return time.Duration(n) * time.Second }

// durCap renders a duration, appending "/cap" only when a cap is set.
func durCap(cur time.Duration, limitSecs int) string {
	if limitSecs > 0 {
		return dur(cur) + "/" + dur(secs(limitSecs))
	}
	return dur(cur)
}

// beadDelta summarizes the iteration's bead-level changes as
// "N created, M closed, ...", listing only non-empty categories. Returns
// "" when there is no diff or it is empty.
func beadDelta(s loop.Snapshot) string {
	d := s.Record.BDDiff
	if d == nil {
		return ""
	}
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(len(d.Created), "created")
	add(len(d.Closed), "closed")
	add(len(d.InProgress), "in-progress")
	add(len(d.Opened), "opened")
	add(len(d.Blocked), "blocked")
	add(len(d.Deferred), "deferred")
	return strings.Join(parts, ", ")
}

// truncatePlain clips s to width runes, marking a clip with a trailing
// ellipsis. width <= 0 returns s unchanged.
func truncatePlain(s string, width int) string {
	if width <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}
