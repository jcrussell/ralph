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
		ReadyBeads:              3,
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
	f := nonTTY()
	s := fullSnapshot()
	cum := f.RenderCumulative(s, 0)
	sess := f.RenderSession(s, 0)

	cumWants := []string{
		"iter 5/20",        // iteration + max
		"cost $1.23/$5.00", // cumulative cost + cap
		"elapsed 2m3s",     // elapsed
		"wall 2m3s/10m0s",  // cumulative wallclock + cap
		"8 commits",        // run-total commits
		"3 ready",          // ready-queue depth
	}
	for _, w := range cumWants {
		if !strings.Contains(cum, w) {
			t.Errorf("cumulative render missing %q\n--- cumulative ---\n%s", w, cum)
		}
	}

	sessWants := []string{
		"clean (ready)", // state + reason
		"gate pass",     // last gate result
		"runner claude", // runner mode this tick
		"$0.42",         // this-iteration cost
		"12.3s",         // this-iteration duration
		"3 commits",     // this-iteration commits
		"dirty×2",       // consecutive dirty
		"2 created",     // bd diff totals
		"1 closed",
		"iter 0005", // narrative line (FormatNarrative)
		"clean → clean: 1 commit",
	}
	for _, w := range sessWants {
		if !strings.Contains(sess, w) {
			t.Errorf("session render missing %q\n--- session ---\n%s", w, sess)
		}
	}
}

func TestRenderCumulativeOmitsCapsWhenUnset(t *testing.T) {
	s := fullSnapshot()
	s.MaxIterations = 0
	s.MaxCostUSD = 0
	s.MaxWallclockSecs = 0
	got := nonTTY().RenderCumulative(s, 0)

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

// TestRunTotalCommitsSurviveQuotaWait pins the ralph-n09 fix under the split
// layout: on a quota-wait tick the per-iteration record carries 0 commits (the
// session runner row reads "0 commits"), but the run-total in the CUMULATIVE
// block reflects the accumulated count and does NOT read as a reset.
func TestRunTotalCommitsSurviveQuotaWait(t *testing.T) {
	f := nonTTY()
	s := fullSnapshot()
	s.CumulativeCommits = 8
	// Simulate the quota-wait row: no per-iteration cost/commits.
	s.Record = loop.IterRecord{RunnerMode: "quota", Skipped: "quota-wait"}

	if cum := f.RenderCumulative(s, 0); !strings.Contains(cum, "8 commits") {
		t.Errorf("cumulative block should keep the run-total %q during a quota wait, got:\n%s", "8 commits", cum)
	}
	if sess := f.RenderSession(s, 0); !strings.Contains(sess, "0 commits") {
		t.Errorf("session runner row should show this tick's 0 commits during a quota wait, got:\n%s", sess)
	}
}

func TestRenderSessionOmitsDirtyWhenZero(t *testing.T) {
	s := fullSnapshot()
	s.ConsecutiveDirty = 0
	if strings.Contains(nonTTY().RenderSession(s, 0), "dirty") {
		t.Errorf("dirty should be omitted when zero")
	}
}

func TestRenderSessionOmitsBeadLineWhenEmpty(t *testing.T) {
	s := fullSnapshot()
	s.Record.BDDiff = nil
	if strings.Contains(nonTTY().RenderSession(s, 0), "beads:") {
		t.Errorf("bead line should be omitted when diff is nil")
	}
}

// TestRenderSeedShowsCumulativeOnly pins the t0 seed shape: the cumulative block
// is a single line (caps, no narrative) and the session block is omitted
// entirely (nothing iteration-specific to show), so the View drops it + its rule.
func TestRenderSeedShowsCumulativeOnly(t *testing.T) {
	f := nonTTY()
	seed := loop.Snapshot{MaxIterations: 30}

	cum := f.RenderCumulative(seed, 0)
	for _, w := range []string{"iter 0/30", "cost"} {
		if !strings.Contains(cum, w) {
			t.Errorf("seed cumulative missing %q:\n%s", w, cum)
		}
	}
	if strings.Contains(cum, "iter 0000") {
		t.Errorf("seed cumulative should not carry a narrative, got:\n%s", cum)
	}
	if n := len(strings.Split(cum, "\n")); n != 1 {
		t.Errorf("seed cumulative should be exactly 1 line, got %d:\n%s", n, cum)
	}

	if sess := f.RenderSession(seed, 0); sess != "" {
		t.Errorf("seed should render no session block, got:\n%s", sess)
	}
}

func TestRenderCumulativeTruncatesAtNarrowWidth(t *testing.T) {
	const width = 12
	got := nonTTY().RenderCumulative(fullSnapshot(), width)

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
		t.Errorf("expected the cumulative line to be clipped with an ellipsis:\n%s", got)
	}
}

func TestRenderSessionColorOnTTYPath(t *testing.T) {
	// The passing gate lives in the session block now; it emits green ANSI.
	got := colorFormatter().RenderSession(fullSnapshot(), 0)
	if !strings.Contains(got, "\x1b[32m") {
		t.Errorf("passing gate should emit green ANSI on the color path:\n%q", got)
	}
}

func TestRenderTruncatedLineHasNoANSI(t *testing.T) {
	// A clipped line falls back to plain text so a cut never severs an escape
	// sequence — check both blocks at a narrow width.
	f := colorFormatter()
	s := fullSnapshot()
	for _, got := range []string{f.RenderCumulative(s, 8), f.RenderSession(s, 8)} {
		for _, line := range strings.Split(got, "\n") {
			if strings.HasSuffix(line, "…") && strings.Contains(line, "\x1b") {
				t.Errorf("truncated line must not contain ANSI: %q", line)
			}
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
