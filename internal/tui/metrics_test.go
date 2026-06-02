package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/internal/narrative"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// fullSnapshot is a representative populated Snapshot exercising every
// field the panel renders.
func fullSnapshot() loop.Snapshot {
	return loop.Snapshot{
		Iter:                    5,
		State:                   fsm.State("clean"),
		Reason:                  fsm.Reason("ready"),
		CumulativeCostUSD:       1.23,
		CumulativeWallclockSecs: 123,
		CumulativeCommits:       8,
		ConsecutiveDirty:        2,
		LastGateResult:          narrative.GatePassed,
		MaxIterations:           20,
		MaxCostUSD:              5,
		MaxWallclockSecs:        600,
		Elapsed:                 123 * time.Second,
		Record: loop.IterRecord{
			Iter:         5,
			State:        "clean",
			Narrative:    "clean → clean: 1 commit",
			RunnerMode:   "claude",
			GateResult:   narrative.GatePassed,
			CostUSD:      0.42,
			DurationSecs: 12.3,
			Commits:      3,
			BDDiff:       &bd.Diff{Created: []string{"a", "b"}, Closed: []string{"c"}},
		},
	}
}

func nonTTY() *Formatter {
	ios, _ := iostreams.Test()
	return NewFormatter(ios)
}

func colorFormatter() *Formatter {
	return &Formatter{cs: iostreams.NewColorScheme(true)}
}

func TestRenderAllFields(t *testing.T) {
	got := nonTTY().Render(fullSnapshot(), 0)

	wants := []string{
		"iter 5/20",        // iteration + max
		"clean (ready)",    // state + reason
		"gate pass",        // last gate result
		"cost $1.23/$5.00", // cumulative cost + cap
		"elapsed 2m3s",     // elapsed
		"wall 2m3s/10m0s",  // cumulative wallclock + cap
		"8 commits",        // run-total commits (budget row)
		"dirty×2",          // consecutive dirty
		"runner claude",    // last runner mode
		"$0.42",            // last-iteration cost
		"12.3s",            // last-iteration duration
		"3 commits",        // last-iteration commits (runner row)
		"2 created",        // bd diff totals
		"1 closed",
		"iter 0005", // narrative line (FormatNarrative)
		"clean → clean: 1 commit",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("render missing %q\n--- full ---\n%s", w, got)
		}
	}
}

func TestRenderOmitsCapsWhenUnset(t *testing.T) {
	s := fullSnapshot()
	s.MaxIterations = 0
	s.MaxCostUSD = 0
	s.MaxWallclockSecs = 0
	got := nonTTY().Render(s, 0)

	if !strings.Contains(got, "iter 5") || strings.Contains(got, "iter 5/") {
		t.Errorf("unbounded iteration should render without denominator, got:\n%s", got)
	}
	if strings.Contains(got, "/$") {
		t.Errorf("cost cap should be omitted when unset, got:\n%s", got)
	}
	if strings.Contains(got, "cost $1.23/") {
		t.Errorf("cost should have no cap suffix, got:\n%s", got)
	}
}

// TestRenderRunTotalCommitsSurviveQuotaWait pins the ralph-n09 fix: on a
// quota-wait tick the per-iteration record carries 0 commits (the runner row
// reads "0 commits"), but the run-total on the budget row reflects the
// cumulative count and does NOT read as a reset.
func TestRenderRunTotalCommitsSurviveQuotaWait(t *testing.T) {
	s := fullSnapshot()
	s.CumulativeCommits = 8
	// Simulate the quota-wait row: no per-iteration cost/commits.
	s.Record = loop.IterRecord{RunnerMode: "quota", Skipped: "quota-wait"}

	got := nonTTY().Render(s, 0)
	if !strings.Contains(got, "8 commits") {
		t.Errorf("budget row should keep the run-total %q during a quota wait, got:\n%s", "8 commits", got)
	}
}

func TestRenderOmitsDirtyWhenZero(t *testing.T) {
	s := fullSnapshot()
	s.ConsecutiveDirty = 0
	if strings.Contains(nonTTY().Render(s, 0), "dirty") {
		t.Errorf("dirty should be omitted when zero")
	}
}

func TestRenderOmitsBeadLineWhenEmpty(t *testing.T) {
	s := fullSnapshot()
	s.Record.BDDiff = nil
	if strings.Contains(nonTTY().Render(s, 0), "beads:") {
		t.Errorf("bead line should be omitted when diff is nil")
	}
}

func TestRenderOmitsNarrativeForSeed(t *testing.T) {
	// The pre-first-iteration seed (iter 0, empty record) carries only caps; the
	// narrative line must be omitted so it renders just the two metric lines and
	// no redundant "iter 0000".
	got := nonTTY().Render(loop.Snapshot{MaxIterations: 30}, 0)

	for _, w := range []string{"iter 0/30", "cost"} {
		if !strings.Contains(got, w) {
			t.Errorf("seed render missing %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "iter 0000") {
		t.Errorf("seed render should omit the narrative line, got:\n%s", got)
	}
	if n := len(strings.Split(got, "\n")); n != 2 {
		t.Errorf("seed render should be exactly 2 lines, got %d:\n%s", n, got)
	}
}

// sepOffsets returns the rune offsets of each fieldSep separator glyph in a
// plain (uncolored) line, used to check that columns line up across rows.
func sepOffsets(line string) []int {
	var offs []int
	for i, r := range []rune(line) {
		if r == '·' {
			offs = append(offs, i)
		}
	}
	return offs
}

func TestRenderColumnsAlign(t *testing.T) {
	// The seed snapshot renders just the status and budget lines. The budget row
	// carries one extra column (the run-total commit count) the status row lacks,
	// so the rows have different separator counts; their shared columns must still
	// sit at the same offsets so the panel reads as a grid.
	got := nonTTY().Render(loop.Snapshot{MaxIterations: 100}, 0)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), got)
	}
	a, b := sepOffsets(lines[0]), sepOffsets(lines[1])
	if len(a) == 0 {
		t.Fatalf("no separators found in %q", lines[0])
	}
	if !prefixAligned(a, b) {
		t.Errorf("shared column offsets differ:\n  %q -> %v\n  %q -> %v", lines[0], a, lines[1], b)
	}
}

func TestRenderColumnsAlignWithLongState(t *testing.T) {
	// A long state field widens column 1; the budget line must pad to match so
	// the shared separators still line up.
	s := loop.Snapshot{MaxIterations: 100, State: "clean", Reason: "queue_empty"}
	got := nonTTY().Render(s, 0)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), got)
	}
	if a, b := sepOffsets(lines[0]), sepOffsets(lines[1]); !prefixAligned(a, b) {
		t.Errorf("shared column offsets differ with long state:\n  %q -> %v\n  %q -> %v", lines[0], a, lines[1], b)
	}
}

// prefixAligned reports whether two separator-offset lists agree over their
// shared columns: the first min(len(a), len(b)) offsets are equal. Rows may
// have different column counts (e.g. the budget row's extra commit total), so a
// trailing separator the shorter row lacks is fine as long as the columns they
// do share line up.
func prefixAligned(a, b []int) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRenderTruncatesAtNarrowWidth(t *testing.T) {
	const width = 12
	got := nonTTY().Render(fullSnapshot(), width)

	clipped := false
	for _, line := range strings.Split(got, "\n") {
		if n := utf8.RuneCountInString(line); n > width {
			t.Errorf("line exceeds width %d (got %d): %q", width, n, line)
		}
		if strings.HasSuffix(line, "…") {
			clipped = true
		}
	}
	if !clipped {
		t.Errorf("expected at least one line to be clipped with an ellipsis:\n%s", got)
	}
}

func TestRenderColorOnTTYPath(t *testing.T) {
	got := colorFormatter().Render(fullSnapshot(), 0)
	if !strings.Contains(got, "\x1b[32m") {
		t.Errorf("passing gate should emit green ANSI on the color path:\n%q", got)
	}
}

func TestRenderTruncatedLineHasNoANSI(t *testing.T) {
	// A clipped line falls back to plain text so a cut never severs an
	// escape sequence.
	got := colorFormatter().Render(fullSnapshot(), 8)
	for _, line := range strings.Split(got, "\n") {
		if strings.HasSuffix(line, "…") && strings.Contains(line, "\x1b") {
			t.Errorf("truncated line must not contain ANSI: %q", line)
		}
	}
}

func TestIterField(t *testing.T) {
	if got := iterField(5, 20); got != "5/20" {
		t.Errorf("bounded: got %q", got)
	}
	if got := iterField(5, 0); got != "5" {
		t.Errorf("unbounded: got %q", got)
	}
}

func TestMoneyCap(t *testing.T) {
	if got := moneyCap(1.2, 5); got != "$1.20/$5.00" {
		t.Errorf("with cap: got %q", got)
	}
	if got := moneyCap(1.2, 0); got != "$1.20" {
		t.Errorf("no cap: got %q", got)
	}
}

func TestDurAndDurCap(t *testing.T) {
	if got := dur(123 * time.Second); got != "2m3s" {
		t.Errorf("dur: got %q", got)
	}
	if got := dur(-1 * time.Second); got != "0s" {
		t.Errorf("dur negative clamp: got %q", got)
	}
	if got := durCap(secs(123), 600); got != "2m3s/10m0s" {
		t.Errorf("durCap with cap: got %q", got)
	}
	if got := durCap(secs(123), 0); got != "2m3s" {
		t.Errorf("durCap no cap: got %q", got)
	}
}

func TestGateText(t *testing.T) {
	cases := map[string]string{
		"":                    "—",
		narrative.GateNotRun:  "—",
		narrative.GatePassed:  "pass",
		"green":               "pass",
		narrative.GateFailed:  "fail",
		"red":                 "fail",
		narrative.GateSkipped: "skip",
		"weird":               "weird",
	}
	for in, want := range cases {
		if got := gateText(in); got != want {
			t.Errorf("gateText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBeadDelta(t *testing.T) {
	s := loop.Snapshot{Record: loop.IterRecord{BDDiff: &bd.Diff{
		Created: []string{"a", "b"},
		Closed:  []string{"c"},
	}}}
	if got := beadDelta(s); got != "2 created, 1 closed" {
		t.Errorf("beadDelta: got %q", got)
	}
	if got := beadDelta(loop.Snapshot{}); got != "" {
		t.Errorf("nil diff: got %q", got)
	}
}

func TestTruncatePlain(t *testing.T) {
	cases := []struct {
		s     string
		width int
		want  string
	}{
		{"hello world", 0, "hello world"},
		{"hello world", 100, "hello world"},
		{"hello world", 5, "hell…"},
		{"hello", 5, "hello"},
		{"hello", 1, "…"},
	}
	for _, c := range cases {
		if got := truncatePlain(c.s, c.width); got != c.want {
			t.Errorf("truncatePlain(%q, %d) = %q, want %q", c.s, c.width, got, c.want)
		}
	}
}
