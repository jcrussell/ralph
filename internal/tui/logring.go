package tui

// logring.go is the bounded backing store for the live-run log pane (epic
// ralph-g3s, bead ralph-g3s.5). The loop streams every line of subprocess
// output and narrative chatter at the pane; over a long run that is an
// unbounded sequence, so a naive slice would grow without limit and
// eventually OOM the TUI. logRing keeps only the most recent lines, capping
// by BOTH a line count and a total byte size and evicting the oldest lines
// first, so memory stays bounded regardless of how much the runner emits.
//
// Lines are stored verbatim — ANSI escape sequences and all. The pane
// renders them through bubbles/viewport, whose truncation measures display
// width with ANSI-aware machinery (charmbracelet/x/ansi via cellbuf), so an
// escape contributes zero columns and is never severed mid-sequence. The
// ring therefore must NOT strip or rewrite escapes: doing so would corrupt
// color state. The byte accounting uses raw len() so the memory cap
// reflects true memory, escapes included — width math and the eviction
// policy stay independent, which is exactly why an escape-laden line leaves
// the layout unaffected.

import "strings"

// Default caps for the production ring. ~50k lines or 16 MiB of scrollback
// holds many iterations of a long run (a human hit the old 5k/1 MiB ceiling
// around 50 iterations) while staying trivially bounded. These are the
// fallback when config supplies nothing; config can raise or lower them
// (LogScrollbackLines / LogScrollbackBytes, internal/config) — see DefaultLogCaps.
const (
	defaultMaxLogLines = 50000
	defaultMaxLogBytes = 16 << 20 // 16 MiB
)

// LogCaps bounds the live log pane's in-memory scrollback ring. It crosses
// the package boundary (New/newModel) as a small value so the TUI never
// imports config internals — run.go derives it from the resolved config and
// hands it in, mirroring how the seed loop.Snapshot carries the metric caps.
type LogCaps struct {
	MaxLines int
	MaxBytes int
}

// DefaultLogCaps returns the production fallback caps. Callers without a
// config source (and tests) use it so a zero LogCaps never yields a ring
// that evicts every line.
func DefaultLogCaps() LogCaps {
	return LogCaps{MaxLines: defaultMaxLogLines, MaxBytes: defaultMaxLogBytes}
}

// logRing is a bounded FIFO of log lines. The zero value is unusable;
// build one with newLogRing (or, in tests, a struct literal with explicit
// caps). It is not safe for concurrent use — the model owns it and mutates
// it only from Update, the single serialization point.
type logRing struct {
	lines    []string
	bytes    int // sum of len(line) over lines; raw bytes, escapes included
	maxLines int
	maxBytes int
}

// newLogRing builds a ring with the given caps. Non-positive caps fall back
// to the production defaults so a misconfigured or zero-valued cap can never
// produce a ring that evicts down to a single line.
func newLogRing(caps LogCaps) *logRing {
	if caps.MaxLines <= 0 {
		caps.MaxLines = defaultMaxLogLines
	}
	if caps.MaxBytes <= 0 {
		caps.MaxBytes = defaultMaxLogBytes
	}
	return &logRing{maxLines: caps.MaxLines, maxBytes: caps.MaxBytes}
}

// push appends one line and evicts the oldest lines until both caps hold.
// At least one line is always retained: a single line larger than maxBytes
// is kept on its own rather than dropping the only (and most recent)
// output. Evicted slots are cleared before the reslice so their backing
// strings become collectable and the underlying array does not pin dropped
// scrollback.
func (r *logRing) push(line string) {
	r.lines = append(r.lines, line)
	r.bytes += len(line)
	for len(r.lines) > 1 && (len(r.lines) > r.maxLines || r.bytes > r.maxBytes) {
		r.bytes -= len(r.lines[0])
		r.lines[0] = ""
		r.lines = r.lines[1:]
	}
}

// content joins the retained lines with newlines for viewport.SetContent.
func (r *logRing) content() string {
	return strings.Join(r.lines, "\n")
}

// count reports how many lines are currently retained.
func (r *logRing) count() int { return len(r.lines) }

// snapshot returns a copy of the retained lines, oldest first. The program
// captures it at teardown so the run orchestration can re-flush the pane tail
// to the operator's real stderr after the TUI exits (program.go). It copies so
// the caller never aliases the ring's backing array.
func (r *logRing) snapshot() []string {
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}
