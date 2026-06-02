package fsm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestPathLayout(t *testing.T) {
	got := Path("/repo")
	want := filepath.Join("/repo", ".ralph", "state", "fsm.json")
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestLoadMissingReturnsFresh(t *testing.T) {
	repo := t.TempDir()
	f, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.State != StateStart {
		t.Errorf("State = %q, want %q", f.State, StateStart)
	}
	if f.Version != SchemaVersion {
		t.Errorf("Version = %d, want %d", f.Version, SchemaVersion)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	repo := t.TempDir()
	in := &FSM{
		Version:                 SchemaVersion,
		Outcome:                 Outcome{State: StateClean},
		Iter:                    7,
		ConsecutiveDirty:        2,
		ReviewMode:              true,
		ReviewBranch:            "feat/foo",
		ReviewBase:              "main",
		CumulativeCostUSD:       1.25,
		CumulativeWallclockSecs: 600,
		CumulativeCommits:       9,
		LastGateResult:          "passed",
		ConsecutiveGateFail:     3,
	}
	if err := in.Save(repo); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(in, out); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

// TestLoadBackCompatMissingCumulativeCommits pins that an fsm.json written by
// an older ralph (before the cumulative_commits field existed) loads cleanly
// with the new counter zeroed — the additive-field, no-schema-bump contract.
func TestLoadBackCompatMissingCumulativeCommits(t *testing.T) {
	repo := t.TempDir()
	path := Path(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A minimal valid fsm.json with NO cumulative_commits key.
	old := `{"version":1,"state":"clean","iter":4,"cumulative_cost_usd":3.5,"cumulative_wallclock_secs":120}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.CumulativeCommits != 0 {
		t.Errorf("CumulativeCommits = %d, want 0 for a pre-field file", out.CumulativeCommits)
	}
	// The fields that were present must still load.
	if out.Iter != 4 || out.CumulativeCostUSD != 3.5 {
		t.Errorf("existing fields not preserved: iter=%d cost=%v", out.Iter, out.CumulativeCostUSD)
	}
}

func TestSaveAtomic(t *testing.T) {
	// Save with a stray tmp file from a prior crash; final fsm.json must
	// still be valid and the stray cleaned up by the next Save's tmp
	// pattern (we don't proactively clean other tmpfiles — verify the
	// real one disappears).
	repo := t.TempDir()
	stateDir := filepath.Join(repo, ".ralph", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stray := filepath.Join(stateDir, "fsm-stale.json.tmp")
	if err := os.WriteFile(stray, []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	f := Fresh()
	if err := f.Save(repo); err != nil {
		t.Fatalf("Save: %v", err)
	}

	final := Path(repo)
	b, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	var decoded FSM
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Errorf("final fsm.json is not valid JSON: %v", err)
	}
	// The unrelated stray is left alone — we only manage our own tmp.
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("unrelated stray was removed: %v", err)
	}
}

func TestLoadCorruptJSON(t *testing.T) {
	repo := t.TempDir()
	path := Path(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(repo); err == nil {
		t.Errorf("Load(corrupt) = nil error, want decode error")
	}
}

func TestLoadInvalidOutcome(t *testing.T) {
	repo := t.TempDir()
	path := Path(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Terminal state without a reason → Validate() rejects.
	body := `{"version":1,"state":"done"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(repo)
	if err == nil || !strings.Contains(err.Error(), "requires reason") {
		t.Errorf("Load(invalid outcome) err = %v, want substring %q", err, "requires reason")
	}
}

func TestLoadSchemaTooNew(t *testing.T) {
	repo := t.TempDir()
	path := Path(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"version":999,"state":"clean"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(repo)
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("Load(version=999) err = %v, want errors.Is(_, ErrSchemaTooNew)", err)
	}
}

func TestLoadOlderSchemaUpgrades(t *testing.T) {
	repo := t.TempDir()
	path := Path(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"version":0,"state":"clean"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Version != SchemaVersion {
		t.Errorf("upgrade: Version = %d, want %d", f.Version, SchemaVersion)
	}
}

func TestSaveRefusesInvalidOutcome(t *testing.T) {
	repo := t.TempDir()
	f := &FSM{Outcome: Outcome{State: StateDone}} // no Reason
	if err := f.Save(repo); err == nil {
		t.Errorf("Save(invalid) = nil, want error")
	}
	if _, err := os.Stat(Path(repo)); err == nil {
		t.Errorf("Save(invalid) wrote a file; want no file")
	}
}

func TestSaveJSONShape(t *testing.T) {
	// State and reason must promote to top level (embedded Outcome).
	repo := t.TempDir()
	f := &FSM{Outcome: Outcome{State: StateDone, Reason: ReasonQueueEmpty}, Iter: 3}
	if err := f.Save(repo); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(Path(repo))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw["state"] != "done" {
		t.Errorf("raw[state] = %v, want %q", raw["state"], "done")
	}
	if raw["reason"] != "queue_empty" {
		t.Errorf("raw[reason] = %v, want %q", raw["reason"], "queue_empty")
	}
	if raw["iter"].(float64) != 3 {
		t.Errorf("raw[iter] = %v, want 3", raw["iter"])
	}
}
