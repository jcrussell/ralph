package fsm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jcrussell/ralph/internal/atomicfile"
	"github.com/jcrussell/ralph/internal/paths"
)

// SchemaVersion is the on-disk schema version for FSM (byob-runtime-
// directories.4). Bump only when a loader change is needed; older files
// are upgraded in place, newer files are rejected with ErrSchemaTooNew.
const SchemaVersion = 1

// ErrSchemaTooNew is returned by Load when fsm.json was written by a
// future ralph that this binary doesn't know how to read. Callers use
// errors.Is.
var ErrSchemaTooNew = errors.New("fsm: schema version newer than this binary supports")

// FSM is the persisted state of the orchestrator. One file per repo at
// <repo>/.ralph/state/fsm.json.
type FSM struct {
	Version int `json:"version"`
	Outcome     // embedded: promotes "state" and "reason" to top level

	Iter                    int     `json:"iter"`
	ConsecutiveDirty        int     `json:"consecutive_dirty"`
	ReviewMode              bool    `json:"review_mode"`
	ReviewBranch            string  `json:"review_branch,omitempty"`
	ReviewBase              string  `json:"review_base,omitempty"`
	CumulativeCostUSD       float64 `json:"cumulative_cost_usd"`
	CumulativeWallclockSecs int     `json:"cumulative_wallclock_secs"`
	LastGateResult          string  `json:"last_gate_result,omitempty"`
}

// Path is the on-disk location of fsm.json under repoRoot.
func Path(repoRoot string) string {
	return paths.New(repoRoot).FSM()
}

// Fresh returns the start-of-life FSM. Used when fsm.json is absent.
func Fresh() *FSM {
	return &FSM{
		Version: SchemaVersion,
		Outcome: Outcome{State: StateStart},
	}
}

// Load reads fsm.json under repoRoot. A missing file is not an error —
// Load returns Fresh(). The Outcome is validated; an invalid file is an
// error.
func Load(repoRoot string) (*FSM, error) {
	path := Path(repoRoot)
	b, err := os.ReadFile(path) //nolint:gosec // path joined from repoRoot + literal
	if errors.Is(err, os.ErrNotExist) {
		return Fresh(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("fsm: read %s: %w", path, err)
	}
	var f FSM
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("fsm: decode %s: %w", path, err)
	}
	if f.Version > SchemaVersion {
		return nil, fmt.Errorf("%w: file=%d binary=%d", ErrSchemaTooNew, f.Version, SchemaVersion)
	}
	if f.Version < SchemaVersion {
		// No upgrades defined yet; bump to current.
		f.Version = SchemaVersion
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("fsm: %s: %w", path, err)
	}
	return &f, nil
}

// Save writes fsm.json atomically via internal/atomicfile (byob-runtime-
// directories.3). Parent directories are created if absent.
func (f *FSM) Save(repoRoot string) error {
	if f.Version == 0 {
		f.Version = SchemaVersion
	}
	if err := f.Validate(); err != nil {
		return fmt.Errorf("fsm: refuse to save invalid outcome: %w", err)
	}

	path := Path(repoRoot)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("fsm: mkdir %s: %w", dir, err)
	}

	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("fsm: marshal: %w", err)
	}
	b = append(b, '\n')

	if err := atomicfile.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("fsm: %w", err)
	}
	return nil
}
