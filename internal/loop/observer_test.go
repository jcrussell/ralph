package loop

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// readyOne is a fakeBD whose default-label queue has one issue, so the
// start state routes to clean and the loop actually iterates.
func readyOne() *fakeBD {
	return &fakeBD{ReadyByLabel: map[string][]bd.Issue{"": {{ID: "x"}}}}
}

// The Observer is notified exactly once per iteration, with monotonically
// increasing Iter, the cumulative cost rolled forward, the configured caps
// copied in, and the terminal iteration carrying the done outcome. Driven
// to done{iter_cap} via MaxIterations=2 so we observe an ordered sequence
// including the terminal tick.
func TestRun_ObserverNotifiedPerIterationWithFields(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Loop.MaxIterations = 2
	opts.Runner = &fakeRunner{} // default session: ModeOK, cost 0.01
	opts.Clock = newFakeClock()
	opts.BD = readyOne()
	obs := &fakeObserver{}
	opts.Observer = obs

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonIterCap}) {
		t.Fatalf("outcome = %+v, want done{iter_cap}", out)
	}

	snaps := obs.Snapshots
	if len(snaps) != 2 {
		t.Fatalf("Observe calls = %d, want 2 (one per iteration)", len(snaps))
	}

	// Iter increases 1, 2 and matches the embedded record.
	for i, s := range snaps {
		wantIter := i + 1
		if s.Iter != wantIter {
			t.Errorf("snap[%d].Iter = %d, want %d", i, s.Iter, wantIter)
		}
		if s.Record.Iter != wantIter {
			t.Errorf("snap[%d].Record.Iter = %d, want %d", i, s.Record.Iter, wantIter)
		}
		if s.MaxIterations != 2 {
			t.Errorf("snap[%d].MaxIterations = %d, want 2 (config cap copied in)", i, s.MaxIterations)
		}
		if s.Elapsed < 0 {
			t.Errorf("snap[%d].Elapsed = %v, want >= 0", i, s.Elapsed)
		}
	}

	// First tick stays in clean; second hits the cap and is terminal.
	if snaps[0].State != fsm.StateClean {
		t.Errorf("snap[0].State = %s, want clean", snaps[0].State)
	}
	if snaps[1].State != fsm.StateDone || snaps[1].Reason != fsm.ReasonIterCap {
		t.Errorf("snap[1] outcome = %s{%s}, want done{iter_cap}", snaps[1].State, snaps[1].Reason)
	}

	// Cumulative cost rolls the per-iteration 0.01 forward (0.01 -> 0.02).
	if snaps[0].CumulativeCostUSD <= 0 {
		t.Errorf("snap[0].CumulativeCostUSD = %v, want > 0", snaps[0].CumulativeCostUSD)
	}
	if snaps[1].CumulativeCostUSD <= snaps[0].CumulativeCostUSD {
		t.Errorf("cumulative cost did not increase: %v then %v",
			snaps[0].CumulativeCostUSD, snaps[1].CumulativeCostUSD)
	}
}

// Skipped ticks (pre-iteration hook exits non-zero) still notify the
// Observer, with the iteration counter honest and Record.Skipped set, so
// the live metrics panel doesn't silently stall on a skipped tick.
func TestRun_ObserverNotifiedOnSkippedTick(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Once = true
	opts.Runner = &fakeRunner{}
	opts.Clock = newFakeClock()
	opts.BD = readyOne()
	obs := &fakeObserver{}
	opts.Observer = obs

	// Pre-iteration hook exits 1 -> the runner is skipped this tick.
	writeExecutableHook(t,
		filepath.Join(repo, ".ralph", "hooks", "pre-iteration"),
		"#!/bin/sh\nexit 1\n",
	)

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(obs.Snapshots) != 1 {
		t.Fatalf("Observe calls = %d, want 1 (the skipped tick)", len(obs.Snapshots))
	}
	s := obs.Snapshots[0]
	if s.Iter != 1 {
		t.Errorf("skipped snap.Iter = %d, want 1", s.Iter)
	}
	if s.Record.Skipped == "" {
		t.Errorf("skipped snap.Record.Skipped is empty, want a skip reason")
	}
}

// A nil Observer (the production default) leaves ErrOut byte-identical to a
// run with an Observer attached: the no-op default and notifyObserver only
// add a side-channel, never touch the progress lines. Both runs use the
// same fake clock origin so the iteration stems (and thus every byte on
// ErrOut) match.
func TestRun_NilObserverLeavesErrOutByteIdentical(t *testing.T) {
	run := func(obs Observer) string {
		repo := scaffoldRepo(t)
		ios, bufs := iostreams.Test()
		opts := baseOpts(t, repo)
		opts.IO = ios
		opts.Cfg.Loop.MaxIterations = 2
		opts.Runner = &fakeRunner{}
		opts.Clock = newFakeClock()
		opts.BD = readyOne()
		opts.Observer = obs
		if _, err := Run(context.Background(), opts); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return bufs.ErrOut.String()
	}

	withNil := run(nil)
	withObs := run(&fakeObserver{})
	if withNil != withObs {
		t.Errorf("ErrOut differs with vs without Observer:\n nil: %q\n obs: %q", withNil, withObs)
	}
	if withNil == "" {
		t.Errorf("ErrOut empty; expected per-iteration progress lines")
	}
}
