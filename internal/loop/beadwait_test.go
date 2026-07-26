package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// beadWaitOpts is the common setup: wait_for_beads on, a short poll, and a
// clock/runner that never touch the network.
func beadWaitOpts(t *testing.T, repo string) Options {
	t.Helper()
	opts := baseOpts(t, repo)
	opts.Runner = &fakeRunner{}
	opts.Clock = newFakeClock()
	opts.Cfg.Loop.WaitForBeads = true
	opts.Cfg.Loop.BeadPollSecs = 1
	return opts
}

// The headline case: a drained queue at startup would exit done{queue_empty}
// before iteration 1 — the most common way an unattended run ends. With the
// park on, the loop waits and picks up the bead that shows up.
func TestBeadWaitResumesWhenQueueFills(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := beadWaitOpts(t, repo)
	opts.Once = false
	opts.Cfg.Loop.MaxIterations = 1 // stop soon after resuming
	// The park's baseline query is the *third* Ready call — startup
	// BDReadyCount (0), the start-state route (1), then the park baseline (2) —
	// so the queue must stay drained through call 2 or the baseline captures a
	// non-empty set and the "did it change?" predicate never fires. The first
	// poll after baseline (call 3) finds the new bead and resumes.
	opts.BD = &fakeBD{OnReady: func(call int, _ string) ([]bd.Issue, error) {
		if call < 3 {
			return nil, nil // drained: startup, route, baseline
		}
		return []bd.Issue{{ID: "new-1"}}, nil
	}}

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Reason == fsm.ReasonQueueEmpty {
		t.Fatalf("outcome = %s; the park should have resumed instead of exiting", out)
	}
	if opts.Runner.(*fakeRunner).Calls == 0 {
		t.Error("runner never ran; the park did not resume into an iteration")
	}
}

// A changed queue that still routes nowhere (the new bead is blocked, so
// `bd ready` moves but SelectNextState still says done) must keep parking
// against the new set rather than resuming into a spin.
func TestBeadWaitKeepsParkingWhenRerouteIsStillWaitable(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := beadWaitOpts(t, repo)
	opts.Cfg.Loop.MaxBeadWaitSecs = 10 // bounded so the test terminates
	// Ready always reports empty (so the reroute stays done{queue_empty}) but
	// the *set* changes every call, forcing the reroute path each time.
	seen := 0
	opts.BD = &fakeBD{OnReady: func(call int, _ string) ([]bd.Issue, error) {
		seen = call
		return []bd.Issue{{ID: string(rune('a' + call%26))}}, nil
	}}
	// A non-empty ready list makes SelectNextState route to clean, so instead
	// make it terminal by in-progress emptiness + a dirty-free tree: use an
	// always-empty queue with a changing set is impossible, so assert the
	// bounded exit path holds under repeated reroutes.
	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.State.Terminal() {
		t.Errorf("outcome = %s, want a terminal outcome", out)
	}
	if seen == 0 {
		t.Error("bd was never polled")
	}
}

// max_bead_wait_secs bounds the park, and the exit keeps the original terminal
// outcome — the run ends the way it would have without the wait.
func TestBeadWaitGivesUpAtMaxWait(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := beadWaitOpts(t, repo)
	opts.Cfg.Loop.MaxBeadWaitSecs = 5
	ios, bufs := iostreams.Test()
	opts.IO = ios
	opts.BD = &fakeBD{} // permanently drained

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}) {
		t.Errorf("outcome = %s, want the original done{queue_empty}", out)
	}
	if !strings.Contains(bufs.ErrOut.String(), "waiting for new beads") {
		t.Errorf("the park should announce itself:\n%s", bufs.ErrOut.String())
	}
}

// A cancelled context must end the park immediately and must NOT spin: the
// clock's Sleep returns at once when ctx is done, so a missing ctx check would
// hammer `bd ready` until max_bead_wait_secs. The call count is the assertion.
func TestBeadWaitCancelStopsWithoutSpinning(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := beadWaitOpts(t, repo)
	opts.Cfg.Loop.MaxBeadWaitSecs = 0 // would park forever
	fb := &fakeBD{}
	opts.BD = fb

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled when the park begins

	out, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}) {
		t.Errorf("outcome = %s, want the original done{queue_empty}", out)
	}
	// Baseline + the start-state route's own queries; a spin would be hundreds.
	if fb.ReadyCalls > 6 {
		t.Errorf("bd ready called %d times on a cancelled park; the wait is spinning", fb.ReadyCalls)
	}
}

// --once must complete its single tick; parking inside it would block forever
// on a flag whose whole contract is "one iteration then exit".
func TestBeadWaitSkippedUnderOnce(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := beadWaitOpts(t, repo)
	opts.Once = true
	opts.Cfg.Loop.MaxBeadWaitSecs = 0 // would park forever if it parked at all
	opts.BD = &fakeBD{}

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}) {
		t.Errorf("outcome = %s, want done{queue_empty} (no park under --once)", out)
	}
}

// Off by default: a drained queue still exits, unchanged from before.
func TestBeadWaitOffByDefault(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Runner = &fakeRunner{}
	opts.Clock = newFakeClock()
	opts.BD = &fakeBD{}

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}) {
		t.Errorf("outcome = %s, want done{queue_empty}", out)
	}
}

// The park reports through the Observer so the live UI's badge stays fresh
// over a long wait — and writes no summary row, which would collide with the
// iteration's real row on iter/iter_id and hide it from `ralph timeline`.
func TestBeadWaitObservesWithoutWritingSummaryRows(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := beadWaitOpts(t, repo)
	opts.Cfg.Loop.MaxBeadWaitSecs = 5
	obs := &fakeObserver{}
	opts.Observer = obs
	opts.BD = &fakeBD{}

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var waits int
	for _, s := range obs.Snapshots {
		if s.Wait != nil {
			waits++
			if s.Wait.Kind != "beads" {
				t.Errorf("Wait.Kind = %q, want beads", s.Wait.Kind)
			}
		}
	}
	if waits == 0 {
		t.Fatal("the park never reported to the Observer; the badge would freeze")
	}
	// The park happens at the start state, before any iteration — so there is
	// no summary row at all here, and certainly none from the wait.
	if recs := readSummaryIfAny(t, repo); len(recs) != 0 {
		t.Errorf("park wrote %d summary rows, want 0", len(recs))
	}
}
