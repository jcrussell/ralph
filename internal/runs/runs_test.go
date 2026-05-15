package runs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/jcrussell/ralph/internal/fsm"
)

func TestBeginCreatesDirAndManifest(t *testing.T) {
	repo := t.TempDir()
	r, err := Begin(repo)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if r.ID() == "" {
		t.Fatal("empty id")
	}
	if !filepath.IsAbs(r.Dir()) {
		t.Fatalf("Dir not absolute: %s", r.Dir())
	}
	if _, serr := os.Stat(filepath.Join(r.Dir(), "manifest.json")); serr != nil {
		t.Fatalf("manifest missing: %v", serr)
	}

	m, err := r.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Version != SchemaVersion {
		t.Errorf("Version = %d, want %d", m.Version, SchemaVersion)
	}
	if m.ID != r.ID() {
		t.Errorf("manifest ID mismatch")
	}
	if m.StartTime.IsZero() {
		t.Error("StartTime not set")
	}
	if m.EndTime != nil {
		t.Errorf("EndTime should be nil before Finalize, got %v", *m.EndTime)
	}
}

func TestAppendAndReadTransitions(t *testing.T) {
	repo := t.TempDir()
	r, err := Begin(repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	want := []Transition{
		{TS: time.Unix(1, 0).UTC(), Iter: 1, From: "start", To: "clean"},
		{TS: time.Unix(2, 0).UTC(), Iter: 2, From: "clean", To: "dirty", Reason: ""},
		{TS: time.Unix(3, 0).UTC(), Iter: 3, From: "dirty", To: "clean", RunnerMode: "ok", CostUSDDelta: 0.42},
	}
	for _, tr := range want {
		if aerr := r.AppendTransition(tr); aerr != nil {
			t.Fatalf("Append: %v", aerr)
		}
	}
	got, err := r.ReadTransitions()
	if err != nil {
		t.Fatalf("ReadTransitions: %v", err)
	}
	if diff := cmp.Diff(want, got, cmpopts.EquateApproxTime(time.Millisecond)); diff != "" {
		t.Errorf("transitions mismatch (-want +got):\n%s", diff)
	}
}

func TestReadTransitionsMissingIsEmpty(t *testing.T) {
	repo := t.TempDir()
	r, err := Begin(repo)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadTransitions()
	if err != nil {
		t.Fatalf("ReadTransitions on empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d transitions, want 0", len(got))
	}
}

func TestUpdateManifestAtomic(t *testing.T) {
	repo := t.TempDir()
	r, err := Begin(repo)
	if err != nil {
		t.Fatal(err)
	}
	err = r.UpdateManifest(func(m *Manifest) {
		m.TotalIters = 7
		m.CumulativeCostUSD = 1.25
		m.StateCounts["clean"] = 3
		m.StateCounts["dirty"] = 2
	})
	if err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
	m, _ := r.ReadManifest()
	if m.TotalIters != 7 || m.CumulativeCostUSD != 1.25 {
		t.Errorf("manifest not mutated: %+v", m)
	}
	if m.StateCounts["clean"] != 3 || m.StateCounts["dirty"] != 2 {
		t.Errorf("state counts wrong: %v", m.StateCounts)
	}

	// No stray temp files left in the run directory.
	entries, _ := os.ReadDir(r.Dir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestFinalize(t *testing.T) {
	repo := t.TempDir()
	r, err := Begin(repo)
	if err != nil {
		t.Fatal(err)
	}
	// Three transitions: state_counts should be {clean: 2, dirty: 1}.
	for _, tr := range []Transition{
		{Iter: 1, From: "start", To: "clean"},
		{Iter: 2, From: "clean", To: "dirty"},
		{Iter: 3, From: "dirty", To: "clean"},
	} {
		if aerr := r.AppendTransition(tr); aerr != nil {
			t.Fatal(aerr)
		}
	}
	out := fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}
	in := FinalizeInput{
		Outcome:                 &out,
		TotalIters:              3,
		CumulativeCostUSD:       2.50,
		CumulativeWallclockSecs: 600,
	}
	if err := r.Finalize(in); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	m, _ := r.ReadManifest()
	if m.EndTime == nil {
		t.Fatal("EndTime nil after Finalize")
	}
	if m.ExitOutcome == nil || *m.ExitOutcome != out {
		t.Errorf("ExitOutcome = %v, want %v", m.ExitOutcome, out)
	}
	if m.TotalIters != 3 {
		t.Errorf("TotalIters = %d, want 3", m.TotalIters)
	}
	if m.CumulativeCostUSD != 2.50 {
		t.Errorf("CumulativeCostUSD = %v, want 2.50", m.CumulativeCostUSD)
	}
	if m.CumulativeWallclockSecs != 600 {
		t.Errorf("CumulativeWallclockSecs = %d, want 600", m.CumulativeWallclockSecs)
	}
	wantCounts := map[string]int{"clean": 2, "dirty": 1}
	if diff := cmp.Diff(wantCounts, m.StateCounts); diff != "" {
		t.Errorf("StateCounts mismatch (-want +got):\n%s", diff)
	}
}

func TestFinalizePartialRunOmitsExitOutcome(t *testing.T) {
	// --once exit (or ctx cancel) finishes the run mid-flight: nil
	// Outcome means "no terminal verdict recorded" — not a failure.
	repo := t.TempDir()
	r, _ := Begin(repo)
	if err := r.AppendTransition(Transition{Iter: 1, From: "start", To: "clean"}); err != nil {
		t.Fatal(err)
	}
	in := FinalizeInput{
		Outcome:           nil,
		TotalIters:        1,
		CumulativeCostUSD: 1.29,
	}
	if err := r.Finalize(in); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	m, _ := r.ReadManifest()
	if m.EndTime == nil {
		t.Fatal("EndTime nil after Finalize")
	}
	if m.ExitOutcome != nil {
		t.Errorf("ExitOutcome = %v, want nil for partial run", *m.ExitOutcome)
	}
	if m.TotalIters != 1 || m.CumulativeCostUSD != 1.29 {
		t.Errorf("aggregates not populated: %+v", m)
	}
	if m.StateCounts["clean"] != 1 {
		t.Errorf("StateCounts = %v, want clean=1", m.StateCounts)
	}
}

func TestFinalizeRejectsInvalidOutcome(t *testing.T) {
	repo := t.TempDir()
	r, _ := Begin(repo)
	bad := fsm.Outcome{State: fsm.StateDone} // missing reason
	err := r.Finalize(FinalizeInput{Outcome: &bad})
	if err == nil {
		t.Fatal("Finalize on invalid Outcome: nil err, want failure")
	}
}

func TestFinalizeNoTransitionsYieldsEmptyCounts(t *testing.T) {
	repo := t.TempDir()
	r, _ := Begin(repo)
	out := fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}
	if err := r.Finalize(FinalizeInput{Outcome: &out}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	m, _ := r.ReadManifest()
	if m.StateCounts == nil {
		t.Fatal("StateCounts is nil")
	}
	if len(m.StateCounts) != 0 {
		t.Errorf("StateCounts = %v, want empty", m.StateCounts)
	}
}

func TestListEmpty(t *testing.T) {
	got, err := List(t.TempDir())
	if err != nil {
		t.Fatalf("List on empty: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestListAndLatest(t *testing.T) {
	repo := t.TempDir()
	_, err := Latest(repo)
	if !errors.Is(err, ErrNoRuns) {
		t.Errorf("Latest empty: %v, want ErrNoRuns", err)
	}

	first, _ := Begin(repo)
	// Sleep is the simplest way to guarantee a distinct timestamp;
	// the ID format is second-resolution.
	time.Sleep(1100 * time.Millisecond)
	second, _ := Begin(repo)

	metas, err := List(repo)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("got %d metas, want 2", len(metas))
	}
	if metas[0].ID != first.ID() || metas[1].ID != second.ID() {
		t.Errorf("metas ordering = [%s,%s], want [%s,%s]",
			metas[0].ID, metas[1].ID, first.ID(), second.ID())
	}

	got, err := Latest(repo)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.ID() != second.ID() {
		t.Errorf("Latest = %s, want %s", got.ID(), second.ID())
	}
}

func TestOpenMissing(t *testing.T) {
	_, err := Open(t.TempDir(), "nonexistent")
	if err == nil {
		t.Fatal("Open missing: nil err, want failure")
	}
}

func TestReadManifestRejectsNewerSchema(t *testing.T) {
	repo := t.TempDir()
	r, _ := Begin(repo)

	// Stamp the manifest with a future schema version.
	path := filepath.Join(r.Dir(), "manifest.json")
	b, _ := os.ReadFile(path)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	raw["version"] = SchemaVersion + 1
	b2, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(path, b2, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := r.ReadManifest()
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("err = %v, want ErrSchemaTooNew", err)
	}
}

func TestIDFormat(t *testing.T) {
	repo := t.TempDir()
	r, err := Begin(repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseID(r.ID())
	if err != nil {
		t.Errorf("parseID(%q): %v", r.ID(), err)
	}
	if parsed.IsZero() {
		t.Errorf("parseID empty for %q", r.ID())
	}
}
