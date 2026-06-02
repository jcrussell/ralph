package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestLogRingDropsOldestByLineCap verifies the line cap evicts the oldest
// lines first and keeps exactly the newest maxLines.
func TestLogRingDropsOldestByLineCap(t *testing.T) {
	r := &logRing{maxLines: 3, maxBytes: 1 << 20}
	for _, ln := range []string{"a", "b", "c", "d", "e"} {
		r.push(ln)
	}
	if got, want := r.count(), 3; got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
	if got, want := r.content(), "c\nd\ne"; got != want {
		t.Errorf("content = %q, want %q (oldest should be dropped)", got, want)
	}
}

// TestLogRingDropsOldestByByteCap verifies the byte cap bounds memory:
// pushing well past the cap retains only the most recent lines and the
// tracked byte total never exceeds maxBytes.
func TestLogRingDropsOldestByByteCap(t *testing.T) {
	r := &logRing{maxLines: 1_000_000, maxBytes: 10} // 10 bytes of scrollback
	for i := 0; i < 1000; i++ {
		r.push("abcde") // 5 bytes each
	}
	if r.bytes > r.maxBytes {
		t.Errorf("tracked bytes = %d, must stay <= cap %d", r.bytes, r.maxBytes)
	}
	if got, want := r.count(), 2; got != want { // floor(10/5)
		t.Errorf("count = %d, want %d under a 10-byte cap", got, want)
	}
	if total := len(r.content()) - (r.count() - 1); total > r.maxBytes {
		// content() adds count-1 newline separators; subtract them to compare payload bytes.
		t.Errorf("retained payload %d exceeds cap %d", total, r.maxBytes)
	}
}

// TestLogRingKeepsAtLeastOneOversizeLine verifies a single line larger than
// the byte cap is retained on its own rather than dropping all output — the
// most recent line always survives.
func TestLogRingKeepsAtLeastOneOversizeLine(t *testing.T) {
	r := &logRing{maxLines: 1_000_000, maxBytes: 4}
	big := strings.Repeat("x", 100)
	r.push(big)
	if r.count() != 1 {
		t.Fatalf("count = %d, want 1 oversize line retained", r.count())
	}
	if r.content() != big {
		t.Errorf("oversize line should be kept verbatim")
	}
	// A subsequent small line still evicts the oversize one (newest wins).
	r.push("ok")
	if r.count() != 1 || r.content() != "ok" {
		t.Errorf("after a fresh line, content = %q (count %d), want %q (1)", r.content(), r.count(), "ok")
	}
}

// TestNewLogRingHonorsCaps verifies the constructor applies the injected caps
// (so config can raise scrollback) and that a non-positive cap falls back to
// the production default rather than yielding a ring that evicts to one line.
func TestNewLogRingHonorsCaps(t *testing.T) {
	r := newLogRing(LogCaps{MaxLines: 7, MaxBytes: 1234})
	if r.maxLines != 7 || r.maxBytes != 1234 {
		t.Errorf("caps not applied: maxLines=%d maxBytes=%d, want 7/1234", r.maxLines, r.maxBytes)
	}

	// A zero LogCaps falls back to the (raised) defaults.
	def := newLogRing(LogCaps{})
	if def.maxLines != defaultMaxLogLines || def.maxBytes != defaultMaxLogBytes {
		t.Errorf("zero caps should fall back to defaults, got maxLines=%d maxBytes=%d", def.maxLines, def.maxBytes)
	}
	if got := DefaultLogCaps(); got.MaxLines != defaultMaxLogLines || got.MaxBytes != defaultMaxLogBytes {
		t.Errorf("DefaultLogCaps() = %+v, want %d/%d", got, defaultMaxLogLines, defaultMaxLogBytes)
	}
}

// TestLogRingStoresANSIVerbatim verifies escape sequences survive the ring
// unmodified, and that the width math the pane relies on (lipgloss, the same
// ANSI-aware machinery bubbles/viewport truncates with) counts only visible
// columns — so an escape-laden line neither inflates the layout nor is
// severed. The byte cap, by contrast, counts raw bytes including escapes.
func TestLogRingStoresANSIVerbatim(t *testing.T) {
	const colored = "\x1b[31mERROR\x1b[0m boom"
	r := newLogRing(DefaultLogCaps())
	r.push(colored)

	if got := r.content(); got != colored {
		t.Errorf("ANSI line mangled: stored %q, want verbatim %q", got, colored)
	}
	// Display width ignores the escapes: "ERROR boom" is 10 columns.
	if w := lipgloss.Width(r.content()); w != len("ERROR boom") {
		t.Errorf("display width = %d, want %d (escapes must be zero-width)", w, len("ERROR boom"))
	}
	// Memory accounting counts the raw bytes, escapes included.
	if r.bytes != len(colored) {
		t.Errorf("byte accounting = %d, want raw len %d", r.bytes, len(colored))
	}
}

// TestLogRingEvictionIndependentOfDisplayWidth pins the invariant behind
// "ANSI leaves layout unaffected": eviction is driven by line/byte caps, not
// display width. A heavily-escaped line and a plain line of the same byte
// length evict identically, so color codes never perturb the drop policy.
func TestLogRingEvictionIndependentOfDisplayWidth(t *testing.T) {
	colored := "\x1b[32mok\x1b[0m"             // many bytes, 2 display columns
	plain := strings.Repeat("z", len(colored)) // same bytes, len(colored) columns

	rc := &logRing{maxLines: 1_000_000, maxBytes: 2 * len(colored)}
	rp := &logRing{maxLines: 1_000_000, maxBytes: 2 * len(colored)}
	for i := 0; i < 5; i++ {
		rc.push(colored)
		rp.push(plain)
	}
	if rc.count() != rp.count() {
		t.Errorf("ANSI vs plain retained different counts (%d vs %d); eviction must not depend on display width",
			rc.count(), rp.count())
	}
}
