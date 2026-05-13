package fsm

import (
	"context"
	"fmt"

	"github.com/jcrussell/ralph/internal/config"
)

// RouteInput is everything SelectNextState needs to pick the next
// Outcome. Defined here (consumer-side) so the loop assembles it
// inline; predicates and counters stay split across their own files.
//
// RunnerFailure carries a fsm.Reason — the loop translates the
// runner-side classify.Mode to a fsm.Reason before calling
// SelectNextState. ReasonNone (== "") means "no terminal runner
// failure this iteration".
type RouteInput struct {
	FSM           *FSM
	Cfg           *config.Config
	BD            BDLister
	Repo          string
	RunnerFailure Reason
}

// SelectNextState returns the next Outcome given the current FSM,
// config, bd surface, repo path, and any terminal-runner failure
// flagged by the caller. Pure dispatch — counter mutation lives in
// counters.ObserveTransition (the loop calls it after this).
//
// Decision order matches docs/concepts/state-machine.md:
//
//  1. terminal runner failure  -> failed{<runner reason>}
//  2. budget exhausted          -> failed{budget}
//  3. caps exceeded             -> done{iter_cap}
//  4. review mode on            -> done{queue_empty} when empty + clean, else review
//  5. dirty streak exceeded     -> revert (suppressed in review mode by step 4)
//  6. git dirty                 -> dirty
//  7. queue empty + git clean   -> done{queue_empty}
//  8. otherwise                 -> clean
func SelectNextState(ctx context.Context, in RouteInput) (Outcome, error) {
	if in.FSM == nil {
		return Outcome{}, fmt.Errorf("fsm: SelectNextState: nil FSM")
	}
	if in.Cfg == nil {
		return Outcome{}, fmt.Errorf("fsm: SelectNextState: nil Cfg")
	}

	// 1. Terminal runner failure — caller has already mapped the
	// runner mode to a fsm.Reason. Validate it belongs on failed.
	if in.RunnerFailure != ReasonNone {
		out := Outcome{State: StateFailed, Reason: in.RunnerFailure}
		if err := out.Validate(); err != nil {
			return Outcome{}, fmt.Errorf("fsm: SelectNextState: invalid RunnerFailure %q: %w", in.RunnerFailure, err)
		}
		return out, nil
	}

	// 2. Budget cap (ralph's own cost/wallclock cap, not Claude's).
	if BudgetExhausted(in.FSM, in.Cfg) {
		return Outcome{State: StateFailed, Reason: ReasonBudget}, nil
	}

	// 3. Iteration cap — graceful exit, code 0.
	if CapsExceeded(in.FSM, in.Cfg) {
		return Outcome{State: StateDone, Reason: ReasonIterCap}, nil
	}

	// 4. Review mode short-circuits everything below. Revert is
	// suppressed by being unreachable when ReviewMode is true.
	if in.FSM.ReviewMode {
		empty, err := ReviewQueueEmpty(ctx, in.BD, in.FSM.ReviewBranch)
		if err != nil {
			return Outcome{}, fmt.Errorf("fsm: SelectNextState: review queue: %w", err)
		}
		clean, err := GitClean(ctx, in.Repo)
		if err != nil {
			return Outcome{}, fmt.Errorf("fsm: SelectNextState: git clean: %w", err)
		}
		if empty && clean {
			return Outcome{State: StateDone, Reason: ReasonQueueEmpty}, nil
		}
		return Outcome{State: StateReview}, nil
	}

	// 5. Auto-revert when consecutive_dirty hits the threshold.
	if DirtyStreakExceeded(in.FSM, in.Cfg) {
		return Outcome{State: StateRevert}, nil
	}

	// 6. Working tree dirty -> dirty.
	clean, err := GitClean(ctx, in.Repo)
	if err != nil {
		return Outcome{}, fmt.Errorf("fsm: SelectNextState: git clean: %w", err)
	}
	if !clean {
		return Outcome{State: StateDirty}, nil
	}

	// 7. Queue drained + tree clean -> done{queue_empty}.
	ready, err := BDReadyCount(ctx, in.BD, "")
	if err != nil {
		return Outcome{}, fmt.Errorf("fsm: SelectNextState: bd ready: %w", err)
	}
	inProg, err := BDInProgressCount(ctx, in.BD, "")
	if err != nil {
		return Outcome{}, fmt.Errorf("fsm: SelectNextState: bd in_progress: %w", err)
	}
	if ready == 0 && inProg == 0 {
		return Outcome{State: StateDone, Reason: ReasonQueueEmpty}, nil
	}

	// 8. Default — keep draining the queue.
	return Outcome{State: StateClean}, nil
}
