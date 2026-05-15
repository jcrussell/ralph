// Package incidents writes short markdown postmortems for surprising
// orchestrator events to .ralph/state/incidents/<ts>-<kind>.md. It does
// not decide *when* an incident happened — callers (the loop) classify
// and call Write.
package incidents

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnknownKind is returned by Write when Kind isn't a defined
// constant. Callers test with errors.Is.
var ErrUnknownKind = errors.New("incidents: unknown kind")

// Kind is the incident class. The wire form (filename suffix and h1
// label) is the lowercase form of the constant value.
type Kind string

// Incident kinds.
const (
	KindRevert          Kind = "revert"
	KindTerminalFailure Kind = "terminal-failure"
	KindGateRegression  Kind = "gate-regression"
	KindDeadStreak      Kind = "dead-streak"
)

// Valid reports whether k is a known incident kind.
func (k Kind) Valid() bool {
	switch k {
	case KindRevert, KindTerminalFailure, KindGateRegression, KindDeadStreak:
		return true
	}
	return false
}

// Incident is what the loop hands to Write.
type Incident struct {
	Kind      Kind
	Iter      int
	Summary   string    // one-line headline
	Body      string    // optional longer markdown
	IterIDs   []string  // iteration record IDs referenced
	Timestamp time.Time // zero → time.Now() in UTC
}

// Dir is the on-disk directory of incidents under repoRoot.
func Dir(repoRoot string) string {
	return filepath.Join(repoRoot, ".ralph", "state", "incidents")
}

// Write persists inc to .ralph/state/incidents/ and returns the
// absolute path of the created file. An unknown Kind is an error;
// missing Summary defaults to a kind-derived placeholder.
func Write(repoRoot string, inc Incident) (string, error) {
	if !inc.Kind.Valid() {
		return "", fmt.Errorf("%w: %q", ErrUnknownKind, inc.Kind)
	}
	ts := inc.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	ts = ts.UTC()

	dir := Dir(repoRoot)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("incidents: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%s.md", ts.UnixNano(), inc.Kind))
	if err := writeAtomic(path, func(w io.Writer) error {
		return format(w, inc, ts)
	}); err != nil {
		return "", err
	}
	return path, nil
}

func format(w io.Writer, inc Incident, ts time.Time) error {
	summary := inc.Summary
	if summary == "" {
		summary = string(inc.Kind)
	}
	if _, err := fmt.Fprintf(w, "# %s: %s\n\n", inc.Kind, summary); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "- iter: %d\n", inc.Iter); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "- timestamp: %s\n", ts.Format(time.RFC3339)); err != nil {
		return err
	}
	ids := "(none)"
	if len(inc.IterIDs) > 0 {
		ids = strings.Join(inc.IterIDs, ", ")
	}
	if _, err := fmt.Fprintf(w, "- iteration records: %s\n", ids); err != nil {
		return err
	}
	if inc.Body != "" {
		if _, err := fmt.Fprintf(w, "\n%s\n", strings.TrimRight(inc.Body, "\n")); err != nil {
			return err
		}
	}
	return nil
}

// writeAtomic writes via a temp file in the same dir + fsync + rename.
func writeAtomic(path string, fn func(io.Writer) error) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".incident-*.tmp")
	if err != nil {
		return fmt.Errorf("incidents: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if err := fn(tmp); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("incidents: write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("incidents: fsync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("incidents: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("incidents: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}
