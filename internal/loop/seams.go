// Package loop drives ralph's FSM through one iteration at a time. The
// entry point is Run; callers (`ralph run`, `ralph review`) construct
// Options with the runtime overrides they parsed from argv.
//
// Test seams (Runner, BDClient, Clock) live here so it is obvious which
// behaviors tests can stub. They are defined consumer-side, per
// byob-interfaces.1 — *runner.Runner, *bd.Client, and the production
// clock all structurally satisfy these without changes to their
// packages.
package loop

import (
	"context"
	"time"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/runner"
)

// Runner is the narrow surface the loop needs from internal/runner.
// *runner.Runner satisfies it.
type Runner interface {
	Run(ctx context.Context, opts runner.RunOpts) (*runner.Session, error)
}

// BDClient is the bd surface the loop needs: enough to satisfy the
// FSM's predicates plus Snapshot for diff. *bd.Client satisfies it.
type BDClient interface {
	fsm.BDLister
	Snapshot(ctx context.Context) (*bd.Snapshot, error)
}

// Clock is the loop's time source. Production passes defaultClock{};
// tests inject a stub so backoff sleeps don't make tests slow.
//
// Sleep takes a context so a Ctrl-C / SIGTERM during a long backoff
// returns immediately instead of stranding the loop for minutes
// (byob-lifecycle.2). The loop's top-level ctx.Err() check picks up
// the cancellation on the next iteration.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration)
}

type defaultClock struct{}

func (defaultClock) Now() time.Time { return time.Now() }

// Observer is an optional live-run sink for per-iteration metrics,
// consumer-defined here alongside the other seams (byob-interfaces.1).
// The loop calls Observe exactly once per iteration — including skipped
// and quota-wait ticks — as it emits that iteration's summary line.
// Production `ralph run` leaves Options.Observer nil and the loop swaps
// in a no-op, so the off-TTY / unset path stays byte-identical. The live
// TUI (ralph-g3s) is the first real implementation.
//
// Observe must not block: it runs synchronously on the iteration path.
// Implementations should hand the Snapshot off (e.g. tea.Program.Send)
// and return immediately.
type Observer interface {
	Observe(Snapshot)
}

// Snapshot is the immutable per-iteration metrics value handed to an
// Observer. It bundles the iteration's summary record with the cumulative
// FSM counters, the configured caps those counters are measured against,
// and the wallclock elapsed since Run started — everything the live TUI's
// top panel renders without reaching back into loop internals.
type Snapshot struct {
	// Record is this iteration's summary row (the latest IterRecord).
	Record IterRecord

	// Cumulative FSM state as of this iteration.
	Iter                    int
	State                   fsm.State
	Reason                  fsm.Reason
	CumulativeCostUSD       float64
	CumulativeWallclockSecs int
	ConsecutiveDirty        int
	LastGateResult          string

	// Configured caps the cumulative fields are measured against
	// (0 == unset / no cap), copied by value so the Observer needn't
	// retain a *config.Config.
	MaxIterations    int
	MaxCostUSD       float64
	MaxWallclockSecs int

	// Elapsed is the wallclock since Run started, per the loop's Clock.
	Elapsed time.Duration
}

// noopObserver is the nil-default Observer: it drops every Snapshot so an
// unset Options.Observer leaves behavior unchanged.
type noopObserver struct{}

func (noopObserver) Observe(Snapshot) {}

func (defaultClock) Sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
