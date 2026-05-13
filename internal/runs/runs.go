// Package runs manages a per-run directory under
// <repo>/.ralph/state/runs/<run-id>/ containing:
//
//   - manifest.json — versioned summary of the run (start/end, exit
//     outcome, iteration counts, cost). Written atomically via
//     temp+fsync+rename (byob-runtime-directories.3) and versioned with
//     ErrSchemaTooNew (byob-runtime-directories.4).
//   - transitions.jsonl — one Transition record per FSM transition,
//     appended through internal/log.JSONL.
//
// Run IDs are RFC3339-flat UTC timestamps so the filesystem is the
// index — no separate registry, list order is lexicographic.
package runs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/log"
)

// SchemaVersion is the on-disk schema version for manifest.json.
const SchemaVersion = 1

// ErrSchemaTooNew is returned by ReadManifest when manifest.json was
// written by a future ralph this binary doesn't understand.
var ErrSchemaTooNew = errors.New("runs: schema version newer than this binary supports")

// ErrNoRuns is returned by Latest when the runs/ directory is empty
// or missing.
var ErrNoRuns = errors.New("runs: no runs found")

// Manifest is the persisted summary of one run. StateCounts maps each
// fsm.State string to the number of times the FSM was in that state.
type Manifest struct {
	Version                 int            `json:"version"`
	ID                      string         `json:"id"`
	StartTime               time.Time      `json:"start_time"`
	EndTime                 *time.Time     `json:"end_time,omitempty"`
	ExitOutcome             *fsm.Outcome   `json:"exit_outcome,omitempty"`
	TotalIters              int            `json:"total_iters"`
	StateCounts             map[string]int `json:"state_counts"`
	CumulativeCostUSD       float64        `json:"cumulative_cost_usd"`
	CumulativeWallclockSecs int            `json:"cumulative_wallclock_secs"`
}

// Transition is one row in transitions.jsonl.
type Transition struct {
	Ts           time.Time `json:"ts"`
	Iter         int       `json:"iter"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Reason       string    `json:"reason,omitempty"`
	RunnerMode   string    `json:"runner_mode,omitempty"`
	GateResult   string    `json:"gate_result,omitempty"`
	CostUSDDelta float64   `json:"cost_usd_delta,omitempty"`
}

// Meta is the directory-listing view of a run for List(). Begun comes
// from the run-id timestamp so listing is cheap (no manifest reads).
type Meta struct {
	ID    string
	Dir   string
	Begun time.Time
}

// Run is an open handle to one run directory. AppendTransition is
// safe for concurrent calls (the underlying *log.JSONL serializes).
type Run struct {
	id    string
	dir   string
	jsonl *log.JSONL // lazy
}

// ID returns the run id, e.g. "20260513T150405Z".
func (r *Run) ID() string { return r.id }

// Dir returns the absolute path to the run directory.
func (r *Run) Dir() string { return r.dir }

// runsDir is <repoRoot>/.ralph/state/runs.
func runsDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".ralph", "state", "runs")
}

// Begin creates a new run directory under repoRoot and writes the
// initial manifest with the start time. The returned Run is open for
// AppendTransition.
func Begin(repoRoot string) (*Run, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("runs: empty repoRoot")
	}
	id, err := newID(repoRoot)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(runsDir(repoRoot), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("runs: mkdir %s: %w", dir, err)
	}
	r := &Run{id: id, dir: dir}
	m := &Manifest{
		Version:     SchemaVersion,
		ID:          id,
		StartTime:   time.Now().UTC(),
		StateCounts: map[string]int{},
	}
	if err := writeManifest(dir, m); err != nil {
		return nil, err
	}
	return r, nil
}

// Open attaches to an existing run. Validates the manifest schema.
func Open(repoRoot, id string) (*Run, error) {
	if repoRoot == "" || id == "" {
		return nil, fmt.Errorf("runs: empty repoRoot or id")
	}
	dir := filepath.Join(runsDir(repoRoot), id)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("runs: stat %s: %w", dir, err)
	}
	r := &Run{id: id, dir: dir}
	if _, err := r.ReadManifest(); err != nil {
		return nil, err
	}
	return r, nil
}

// Latest returns the most recent run (lexicographically last id). Run
// ids are RFC3339-flat so lexicographic order == chronological order.
func Latest(repoRoot string) (*Run, error) {
	metas, err := List(repoRoot)
	if err != nil {
		return nil, err
	}
	if len(metas) == 0 {
		return nil, ErrNoRuns
	}
	return Open(repoRoot, metas[len(metas)-1].ID)
}

// List enumerates run directories under repoRoot, oldest first.
// Missing runs/ is not an error — returns empty slice.
func List(repoRoot string) ([]Meta, error) {
	root := runsDir(repoRoot)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("runs: readdir %s: %w", root, err)
	}
	out := make([]Meta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		begun, _ := parseID(id) // empty time on parse error; still listed
		out = append(out, Meta{ID: id, Dir: filepath.Join(root, id), Begun: begun})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// AppendTransition writes t to transitions.jsonl. The underlying
// *log.JSONL is opened lazily and reused across calls.
func (r *Run) AppendTransition(t Transition) error {
	if r.jsonl == nil {
		j, err := log.OpenJSONL(filepath.Join(r.dir, "transitions.jsonl"))
		if err != nil {
			return err
		}
		r.jsonl = j
	}
	return r.jsonl.Write(t)
}

// ReadTransitions parses transitions.jsonl. Missing file is empty
// slice. Lines that fail to parse are surfaced as errors so callers
// notice torn writes.
func (r *Run) ReadTransitions() ([]Transition, error) {
	b, err := os.ReadFile(filepath.Join(r.dir, "transitions.jsonl"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("runs: read transitions: %w", err)
	}
	var out []Transition
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		var t Transition
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			return nil, fmt.Errorf("runs: transitions line %d: %w", i+1, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// ReadManifest decodes manifest.json. Bumps unknown-older versions in
// place; rejects newer versions with ErrSchemaTooNew.
func (r *Run) ReadManifest() (*Manifest, error) {
	return readManifest(r.dir)
}

// UpdateManifest atomically applies fn to the manifest: read, mutate,
// write via temp+fsync+rename. The mutator must not retain the
// pointer — the value passes out of scope when fn returns.
func (r *Run) UpdateManifest(fn func(*Manifest)) error {
	m, err := readManifest(r.dir)
	if err != nil {
		return err
	}
	fn(m)
	return writeManifest(r.dir, m)
}

// Finalize stamps the end time and exit outcome on the manifest, then
// closes the transitions writer. Safe to call exactly once.
func (r *Run) Finalize(out fsm.Outcome) error {
	if err := out.Validate(); err != nil {
		return fmt.Errorf("runs: finalize: %w", err)
	}
	now := time.Now().UTC()
	if err := r.UpdateManifest(func(m *Manifest) {
		m.EndTime = &now
		m.ExitOutcome = &out
	}); err != nil {
		return err
	}
	return r.Close()
}

// Close closes the transitions writer if it was opened. Safe to call
// multiple times.
func (r *Run) Close() error {
	if r.jsonl == nil {
		return nil
	}
	err := r.jsonl.Close()
	r.jsonl = nil
	return err
}

// --- internals ---

func newID(repoRoot string) (string, error) {
	base := time.Now().UTC().Format("20060102T150405Z")
	candidate := base
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(runsDir(repoRoot), candidate)); errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		var buf [3]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("runs: rand: %w", err)
		}
		candidate = base + "-" + hex.EncodeToString(buf[:])
	}
	return "", fmt.Errorf("runs: could not pick unique id after 5 tries (base=%s)", base)
}

// parseID extracts the timestamp from an id; suffix after "-" (if any)
// is ignored. Returns zero Time on parse error.
func parseID(id string) (time.Time, error) {
	stem := id
	if i := strings.IndexByte(stem, '-'); i >= 0 {
		stem = stem[:i]
	}
	return time.Parse("20060102T150405Z", stem)
}

func manifestPath(dir string) string { return filepath.Join(dir, "manifest.json") }

func readManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		return nil, fmt.Errorf("runs: read manifest %s: %w", dir, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("runs: decode manifest %s: %w", dir, err)
	}
	if m.Version > SchemaVersion {
		return nil, fmt.Errorf("%w: file=%d binary=%d", ErrSchemaTooNew, m.Version, SchemaVersion)
	}
	if m.Version == 0 {
		// Pre-version file: stamp current and continue.
		m.Version = SchemaVersion
	}
	if m.StateCounts == nil {
		m.StateCounts = map[string]int{}
	}
	return &m, nil
}

// writeManifest writes the manifest atomically: temp-in-same-dir +
// fsync + rename. Mirrors fsm.Save (byob-runtime-directories.3).
func writeManifest(dir string, m *Manifest) error {
	if m.Version == 0 {
		m.Version = SchemaVersion
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("runs: marshal manifest: %w", err)
	}
	b = append(b, '\n')

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("runs: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "manifest-*.json.tmp")
	if err != nil {
		return fmt.Errorf("runs: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("runs: write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("runs: fsync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("runs: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, manifestPath(dir)); err != nil {
		cleanup()
		return fmt.Errorf("runs: rename: %w", err)
	}
	return nil
}
