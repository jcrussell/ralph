// Package tableprinter renders rows in a TTY-adaptive format: padded
// columns with a colored header and fuzzy timestamps when stdout is a
// TTY, tab-separated columns with RFC3339 timestamps otherwise. The
// caller never branches on isatty — the printer does (sealed by
// byob-output.1).
package tableprinter

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Table buffers headers and rows for TTY-adaptive rendering. Cells are
// pre-formatted strings; tests for color, alignment, or timestamp shape
// belong on Render, not on the call site. Cells must not contain ANSI
// codes — coloring is the table's job.
type Table struct {
	out     io.Writer
	tty     bool
	cs      *iostreams.ColorScheme
	now     func() time.Time
	headers []string
	rows    [][]string
}

// New returns a Table that writes to io.Out and renders TTY-style when
// IsStdoutTTY reports true at construction time.
func New(io *iostreams.IOStreams) *Table {
	return &Table{
		out: io.Out,
		tty: io.IsStdoutTTY(),
		cs:  io.ColorScheme(),
		now: time.Now,
	}
}

// IsTTY reports whether Render will use the TTY path. Useful for
// call-site decisions like fuzzy-vs-RFC3339 outside the printer's
// built-in TimeCell helper.
func (t *Table) IsTTY() bool { return t.tty }

// Headers sets column headers. They print in TTY mode only — emitting
// them in TSV would break awk/cut pipelines.
func (t *Table) Headers(h ...string) *Table {
	t.headers = h
	return t
}

// AddRow appends one row of pre-formatted cells.
func (t *Table) AddRow(cells ...string) *Table {
	t.rows = append(t.rows, cells)
	return t
}

// TimeCell formats ts for use inside AddRow. TTY: fuzzy ("2h ago");
// non-TTY: RFC3339 in UTC.
func (t *Table) TimeCell(ts time.Time) string {
	if t.tty {
		return fuzzy(t.now(), ts)
	}
	return ts.UTC().Format(time.RFC3339)
}

// Render writes the table.
func (t *Table) Render() error {
	if t.tty {
		return t.renderTTY()
	}
	return t.renderTSV()
}

func (t *Table) renderTSV() error {
	for _, r := range t.rows {
		if _, err := fmt.Fprintln(t.out, strings.Join(r, "\t")); err != nil {
			return err
		}
	}
	return nil
}

func (t *Table) renderTTY() error {
	widths := t.colWidths()
	if len(t.headers) > 0 {
		if _, err := fmt.Fprintln(t.out, joinPadded(t.headers, widths, t.cs.Bold)); err != nil {
			return err
		}
	}
	for _, r := range t.rows {
		if _, err := fmt.Fprintln(t.out, joinPadded(r, widths, nil)); err != nil {
			return err
		}
	}
	return nil
}

func (t *Table) colWidths() []int {
	n := len(t.headers)
	for _, r := range t.rows {
		if len(r) > n {
			n = len(r)
		}
	}
	widths := make([]int, n)
	for i, h := range t.headers {
		if w := len(h); w > widths[i] {
			widths[i] = w
		}
	}
	for _, r := range t.rows {
		for i, c := range r {
			if w := len(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}

// joinPadded space-pads cells to widths and joins with a two-space
// gutter. The last cell is never padded so trailing whitespace doesn't
// confuse copy-paste. decorate, if non-nil, wraps each padded cell.
func joinPadded(cells []string, widths []int, decorate func(string) string) string {
	var b strings.Builder
	last := len(cells) - 1
	for i, c := range cells {
		text := c
		if i < last && i < len(widths) {
			text = c + strings.Repeat(" ", widths[i]-len(c))
		}
		if decorate != nil {
			text = decorate(text)
		}
		b.WriteString(text)
		if i < last {
			b.WriteString("  ")
		}
	}
	return b.String()
}

// fuzzy returns a coarse human-readable delta between now and ts.
// Buckets: seconds, minutes, hours, days, months, years. Future times
// render as "in Nx"; past times as "Nx ago".
func fuzzy(now, ts time.Time) string {
	d := now.Sub(ts)
	future := d < 0
	if future {
		d = -d
	}
	var label string
	switch {
	case d < time.Minute:
		label = fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		label = fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		label = fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		label = fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		label = fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		label = fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
	if future {
		return "in " + label
	}
	return label + " ago"
}
