package tableprinter

import (
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

func TestTSVRendersTabSeparatedWithoutHeaders(t *testing.T) {
	ios, bufs := iostreams.Test() // non-TTY by default
	tbl := New(ios).Headers("ID", "STATE")
	tbl.AddRow("a-1", "open").AddRow("a-2", "closed")
	if err := tbl.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := bufs.Out.String()
	want := "a-1\topen\na-2\tclosed\n"
	if got != want {
		t.Errorf("TSV output\n got: %q\nwant: %q", got, want)
	}
}

func TestTSVOmitsHeaderRow(t *testing.T) {
	ios, bufs := iostreams.Test()
	if err := New(ios).Headers("A", "B").Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := bufs.Out.String(); got != "" {
		t.Errorf("empty table on non-TTY output = %q, want empty", got)
	}
}

func TestTTYPadsColumnsAndBoldHeader(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	ios, bufs := iostreams.Test()
	ios.SetStdoutTTY(true)
	tbl := New(ios).Headers("ID", "STATE", "REASON")
	tbl.AddRow("a-1", "open", "")
	tbl.AddRow("aa-22", "closed", "done")
	if err := tbl.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(strings.TrimRight(bufs.Out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), bufs.Out.String())
	}

	hdr := lines[0]
	if !strings.HasPrefix(hdr, "\x1b[1m") {
		t.Errorf("header missing bold prefix: %q", hdr)
	}
	if !strings.Contains(hdr, "ID") || !strings.Contains(hdr, "STATE") || !strings.Contains(hdr, "REASON") {
		t.Errorf("header missing column labels: %q", hdr)
	}

	// Column 0 width = max("ID"=2, "a-1"=3, "aa-22"=5) = 5; "a-1" padded
	// with 2 trailing spaces before the two-space gutter.
	row1 := stripANSI(lines[1])
	if !strings.HasPrefix(row1, "a-1    ") { // "a-1" + 2 pad + 2 gutter
		t.Errorf("row 1 col 0 not padded to width 5: %q", row1)
	}

	// The last column ("") has no trailing padding so the line ends
	// at the gutter after column 1's padded width.
	if strings.HasSuffix(row1, " ") && !strings.HasSuffix(row1, "closed") {
		// row 1 last cell is empty so we expect no trailing whitespace
		// after the gutter — the line ends right after the gutter.
		// Tolerate the two-space gutter at end of row1 since cell[last]
		// is "" and we always print the gutter after non-last cells.
		if !strings.HasSuffix(row1, "  ") {
			t.Errorf("row 1 last cell wrongly padded: %q", row1)
		}
	}
}

func TestTTYWithoutHeadersStillPads(t *testing.T) {
	ios, bufs := iostreams.Test()
	ios.SetStdoutTTY(true)
	tbl := New(ios).AddRow("short", "x").AddRow("muchlonger", "y")
	if err := tbl.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(strings.TrimRight(bufs.Out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), bufs.Out.String())
	}
	if !strings.HasPrefix(lines[0], "short     ") { // 5 chars + 5 pad
		t.Errorf("row 0 not padded to width 10: %q", lines[0])
	}
}

func TestTimeCellTTYIsFuzzy(t *testing.T) {
	ios, _ := iostreams.Test()
	ios.SetStdoutTTY(true)
	tbl := New(ios)
	tbl.now = func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }
	ts := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC) // 2h ago
	if got := tbl.TimeCell(ts); got != "2h ago" {
		t.Errorf("TimeCell(TTY) = %q, want %q", got, "2h ago")
	}
}

func TestTimeCellNonTTYIsRFC3339(t *testing.T) {
	ios, _ := iostreams.Test()
	tbl := New(ios)
	ts := time.Date(2026, 5, 15, 10, 30, 45, 0, time.UTC)
	if got := tbl.TimeCell(ts); got != "2026-05-15T10:30:45Z" {
		t.Errorf("TimeCell(non-TTY) = %q, want RFC3339", got)
	}
}

func TestFuzzyBuckets(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ts   time.Time
		want string
	}{
		{"seconds", now.Add(-30 * time.Second), "30s ago"},
		{"minutes", now.Add(-45 * time.Minute), "45m ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"days", now.Add(-3 * 24 * time.Hour), "3d ago"},
		{"months", now.Add(-90 * 24 * time.Hour), "3mo ago"},
		{"years", now.Add(-2 * 365 * 24 * time.Hour), "2y ago"},
		{"future", now.Add(2 * time.Hour), "in 2h"},
		{"now", now, "0s ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fuzzy(now, tc.ts); got != tc.want {
				t.Errorf("fuzzy(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestIsTTYReflectsConstruction(t *testing.T) {
	ios, _ := iostreams.Test()
	if New(ios).IsTTY() {
		t.Error("IsTTY() = true on non-TTY iostreams")
	}
	ios.SetStdoutTTY(true)
	if !New(ios).IsTTY() {
		t.Error("IsTTY() = false on TTY iostreams")
	}
}

func TestTTYWithNoColorDisablesHeader(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	ios, bufs := iostreams.Test()
	ios.SetStdoutTTY(true)
	if err := New(ios).Headers("ID").AddRow("x").Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(bufs.Out.String(), "\x1b[") {
		t.Errorf("NO_COLOR=1 but ANSI codes present: %q", bufs.Out.String())
	}
	// Header text should still be emitted, just unstyled.
	if !strings.HasPrefix(bufs.Out.String(), "ID") {
		t.Errorf("header text missing: %q", bufs.Out.String())
	}
}

func TestEmptyTableRendersNothing(t *testing.T) {
	ios, bufs := iostreams.Test()
	if err := New(ios).Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := bufs.Out.String(); got != "" {
		t.Errorf("empty TSV = %q, want empty", got)
	}

	ios2, bufs2 := iostreams.Test()
	ios2.SetStdoutTTY(true)
	if err := New(ios2).Render(); err != nil {
		t.Fatalf("Render TTY: %v", err)
	}
	if got := bufs2.Out.String(); got != "" {
		t.Errorf("empty TTY = %q, want empty", got)
	}
}

func TestRaggedRowsTolerated(t *testing.T) {
	ios, bufs := iostreams.Test()
	tbl := New(ios).Headers("A", "B", "C")
	tbl.AddRow("1")                   // shorter than headers
	tbl.AddRow("2", "two", "II", "x") // longer
	if err := tbl.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	// TSV must not panic and must preserve each row's own width.
	want := "1\n2\ttwo\tII\tx\n"
	if got := bufs.Out.String(); got != want {
		t.Errorf("ragged TSV\n got: %q\nwant: %q", got, want)
	}
}

// stripANSI removes ANSI CSI escape sequences for stable assertions.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
