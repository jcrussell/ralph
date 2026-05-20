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
