package status

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func TestOptionsValidate(t *testing.T) {
	if err := (&Options{Tail: 5}).Validate(); err != nil {
		t.Errorf("positive tail rejected: %v", err)
	}
	if err := (&Options{Tail: 0}).Validate(); err != nil {
		t.Errorf("zero tail rejected: %v", err)
	}
	err := (&Options{Tail: -1}).Validate()
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("negative tail: err = %v, want *FlagError", err)
	}
}

// scaffold sets up a fake repo with .ralph/state/fsm.json and (when
// run!=nil) a runs/<id>/ directory containing a manifest + some
// transitions. Returns the repo root.
func scaffold(t *testing.T, state *fsm.FSM, withRun bool, trans []runs.Transition) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".ralph", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if state != nil {
		if err := state.Save(repo); err != nil {
			t.Fatalf("save fsm: %v", err)
		}
	}
	if withRun {
		r, err := runs.Begin(repo)
		if err != nil {
			t.Fatalf("runs.Begin: %v", err)
		}
		for _, tr := range trans {
			if err := r.AppendTransition(tr); err != nil {
				t.Fatalf("append: %v", err)
			}
		}
		_ = r.Close()
	}
	return repo
}

func newFactory(t *testing.T, repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	t.Helper()
	ios, bufs := iostreams.Test()
	rootFn := func() (string, error) { return repo, nil }
	return &cmdutil.Factory{
		IOStreams: ios,
		RepoRoot:  rootFn,
		Config:    cmdutil.LazyConfig(rootFn),
	}, bufs
}

func TestStatusTextHappyPath(t *testing.T) {
	state := fsm.Fresh()
	state.State = fsm.StateClean
	state.Iter = 7
	state.ConsecutiveDirty = 0
	state.CumulativeCostUSD = 0.42
	state.CumulativeWallclockSecs = 130

	trans := []runs.Transition{
		{Iter: 6, From: "clean", To: "dirty"},
		{Iter: 7, From: "dirty", To: "clean", RunnerMode: "ok"},
	}
	repo := scaffold(t, state, true, trans)
	f, bufs := newFactory(t, repo)

	opts := &Options{F: f, Tail: 5}
	if err := runStatus(context.Background(), opts); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	out := bufs.Out.String()
	for _, want := range []string{
		"ralph status — run",
		"state:             clean",
		"iter:              7 / 30",
		"review:            off",
		"$0.42 / unlimited",
		"2m 10s / unlimited",
		"consecutive_dirty: 0",
		"last 2 transitions",
		"6    clean   → dirty",
		"7    dirty   → clean",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestStatusTextNoRunsYet(t *testing.T) {
	state := fsm.Fresh()
	repo := scaffold(t, state, false, nil)
	f, bufs := newFactory(t, repo)

	if err := runStatus(context.Background(), &Options{F: f, Tail: 5}); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	out := bufs.Out.String()
	if !strings.Contains(out, "no runs yet") {
		t.Errorf("want 'no runs yet' header, got:\n%s", out)
	}
	if strings.Contains(out, "last") && strings.Contains(out, "transitions") {
		t.Errorf("transitions section should be absent, got:\n%s", out)
	}
}

func TestStatusTextTerminalShowsReason(t *testing.T) {
	state := fsm.Fresh()
	state.State = fsm.StateDone
	state.Reason = fsm.ReasonQueueEmpty
	repo := scaffold(t, state, false, nil)
	f, bufs := newFactory(t, repo)

	if err := runStatus(context.Background(), &Options{F: f, Tail: 5}); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if !strings.Contains(bufs.Out.String(), "state:             done{queue_empty}") {
		t.Errorf("want terminal state line, got:\n%s", bufs.Out.String())
	}
}

func TestStatusJSON(t *testing.T) {
	state := fsm.Fresh()
	state.State = fsm.StateClean
	state.Iter = 3
	state.CumulativeCostUSD = 0.10
	repo := scaffold(t, state, true, []runs.Transition{{Iter: 3, From: "start", To: "clean"}})
	f, bufs := newFactory(t, repo)

	opts := &Options{F: f, JSON: true, Tail: 5}
	if err := runStatus(context.Background(), opts); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	var v View
	if err := json.Unmarshal(bufs.Out.Bytes(), &v); err != nil {
		t.Fatalf("decode JSON: %v\noutput:%s", err, bufs.Out.String())
	}
	if v.State != "clean" || v.Iter != 3 || v.CumulativeCostUSD != 0.10 {
		t.Errorf("decoded View wrong: %+v", v)
	}
	if v.RunID == "" {
		t.Error("RunID empty")
	}
	if len(v.Transitions) != 1 {
		t.Errorf("Transitions len = %d, want 1", len(v.Transitions))
	}
}

func TestStatusReviewLine(t *testing.T) {
	state := fsm.Fresh()
	state.ReviewMode = true
	state.ReviewBranch = "feat/x"
	state.ReviewBase = "main"
	repo := scaffold(t, state, false, nil)
	f, bufs := newFactory(t, repo)
	_ = runStatus(context.Background(), &Options{F: f, Tail: 5})
	if !strings.Contains(bufs.Out.String(), "on (branch=feat/x, base=main)") {
		t.Errorf("review line wrong:\n%s", bufs.Out.String())
	}
}

func TestTailNBounds(t *testing.T) {
	xs := []runs.Transition{{Iter: 1}, {Iter: 2}, {Iter: 3}, {Iter: 4}}
	cases := []struct {
		n    int
		want int
	}{
		{0, 4}, // <=0 means all
		{1, 1},
		{4, 4},
		{99, 4},
	}
	for _, c := range cases {
		got := tailN(xs, c.n)
		if len(got) != c.want {
			t.Errorf("tailN(_,%d) len = %d, want %d", c.n, len(got), c.want)
		}
	}
	if len(tailN(xs, 2)) != 2 || tailN(xs, 2)[0].Iter != 3 {
		t.Errorf("tailN(_,2) should be last two: %v", tailN(xs, 2))
	}
}

func TestNewCmdStatusFlags(t *testing.T) {
	c := NewCmdStatus(&cmdutil.Factory{}, func(context.Context, *Options) error { return nil })
	if c.Use != "status" {
		t.Errorf("Use = %s, want status", c.Use)
	}
	if c.Flags().Lookup("json") == nil {
		t.Error("--json flag missing")
	}
	if c.Flags().Lookup("tail") == nil {
		t.Error("--tail flag missing")
	}
}

// stampCaps records caps on the latest run's manifest, optionally closing the
// run, the way loop.Run does at entry and Finalize does at exit.
func stampCaps(t *testing.T, repo string, caps *runs.Caps, closed bool) {
	t.Helper()
	r, err := runs.Latest(repo)
	if err != nil {
		t.Fatalf("runs.Latest: %v", err)
	}
	if err := r.UpdateManifest(func(m *runs.Manifest) {
		m.Caps = caps
		if closed {
			m.ExitOutcome = &fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}
		}
	}); err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
}

// The point of ralph-594: a run started with --unlimited reports "unlimited"
// even though config on disk still says 30. Without the manifest, status has no
// way to know about an override that only ever lived in the loop's memory.
func TestStatusPrefersOpenRunCaps(t *testing.T) {
	state := fsm.Fresh()
	state.State = fsm.StateClean
	state.Iter = 7
	repo := scaffold(t, state, true, nil)
	stampCaps(t, repo, &runs.Caps{MaxIterations: 0}, false)

	f, bufs := newFactory(t, repo)
	if err := runStatus(context.Background(), &Options{F: f, Tail: 5}); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if out := bufs.Out.String(); !strings.Contains(out, "iter:              7 / unlimited") {
		t.Errorf("want the run's uncapped iter line, got:\n%s", out)
	}
}

// Once the run has ended, config is the truth again — it is what the next run
// will use, so continuing to echo the finished run's override would mislead.
func TestStatusFallsBackToConfigForClosedRun(t *testing.T) {
	state := fsm.Fresh()
	state.State = fsm.StateDone
	state.Reason = fsm.ReasonQueueEmpty
	state.Iter = 7
	repo := scaffold(t, state, true, nil)
	stampCaps(t, repo, &runs.Caps{MaxIterations: 0}, true)

	f, bufs := newFactory(t, repo)
	if err := runStatus(context.Background(), &Options{F: f, Tail: 5}); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if out := bufs.Out.String(); !strings.Contains(out, "iter:              7 / 30") {
		t.Errorf("want the config cap for a finished run, got:\n%s", out)
	}
}

// A manifest predating the Caps field must not read as an unlimited run: nil
// and 0 are different answers, which is why Caps is a pointer.
func TestStatusIgnoresManifestWithoutCaps(t *testing.T) {
	state := fsm.Fresh()
	state.State = fsm.StateClean
	state.Iter = 7
	repo := scaffold(t, state, true, nil)

	f, bufs := newFactory(t, repo)
	if err := runStatus(context.Background(), &Options{F: f, Tail: 5}); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if out := bufs.Out.String(); !strings.Contains(out, "iter:              7 / 30") {
		t.Errorf("want the config cap when the manifest records none, got:\n%s", out)
	}
}

// --json must emit an explicit 0 for an uncapped run rather than dropping the
// key, so a script can tell "unlimited" from "field absent".
func TestStatusJSONEmitsZeroCaps(t *testing.T) {
	state := fsm.Fresh()
	state.State = fsm.StateClean
	repo := scaffold(t, state, true, nil)
	stampCaps(t, repo, &runs.Caps{}, false)

	f, bufs := newFactory(t, repo)
	if err := runStatus(context.Background(), &Options{F: f, JSON: true, Tail: 5}); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(bufs.Out.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"max_iterations", "max_cost_usd", "max_wallclock_secs"} {
		v, ok := raw[k]
		if !ok {
			t.Errorf("%s missing from --json; unlimited must be an explicit 0", k)
			continue
		}
		if v != float64(0) {
			t.Errorf("%s = %v, want 0", k, v)
		}
	}
}
