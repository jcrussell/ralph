package fsm

import (
	"context"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/git"
)

// BDLister is the narrow surface predicates need from bd. The full
// *bd.Client satisfies it; tests inject a stub. Defined here (consumer
// side) per byob-interfaces.1.
type BDLister interface {
	Ready(ctx context.Context, label string) ([]bd.Issue, error)
	List(ctx context.Context, status, label string) ([]bd.Issue, error)
}

// GitClean reports whether the working tree at repo is clean, ignoring
// untracked files. Thin wrapper over internal/git.Clean kept here so
// predicates form one coherent surface.
func GitClean(ctx context.Context, repo string) (bool, error) {
	return git.Clean(ctx, repo)
}

// GitDirty is !GitClean.
func GitDirty(ctx context.Context, repo string) (bool, error) {
	clean, err := GitClean(ctx, repo)
	if err != nil {
		return false, err
	}
	return !clean, nil
}

// BDReadyCount returns len(bd ready [-l label]).
func BDReadyCount(ctx context.Context, b BDLister, label string) (int, error) {
	issues, err := b.Ready(ctx, label)
	if err != nil {
		return 0, err
	}
	return len(issues), nil
}

// BDInProgressCount returns len(bd list --status in_progress [-l label]).
func BDInProgressCount(ctx context.Context, b BDLister, label string) (int, error) {
	issues, err := b.List(ctx, "in_progress", label)
	if err != nil {
		return 0, err
	}
	return len(issues), nil
}

// ReviewQueueEmpty is true when no issues are ready or in progress
// under the label "review:<branch>".
func ReviewQueueEmpty(ctx context.Context, b BDLister, branch string) (bool, error) {
	label := "review:" + branch
	ready, err := BDReadyCount(ctx, b, label)
	if err != nil {
		return false, err
	}
	if ready > 0 {
		return false, nil
	}
	inProg, err := BDInProgressCount(ctx, b, label)
	if err != nil {
		return false, err
	}
	return inProg == 0, nil
}

// DirtyStreakExceeded is true when consecutive_dirty has hit the
// configured revert threshold.
func DirtyStreakExceeded(f *FSM, cfg *config.Config) bool {
	if cfg == nil || cfg.Backoff.DirtyRevertThreshold <= 0 {
		return false
	}
	return f.ConsecutiveDirty >= cfg.Backoff.DirtyRevertThreshold
}

// CapsExceeded is true when the iteration counter has hit the
// configured cap.
func CapsExceeded(f *FSM, cfg *config.Config) bool {
	if cfg == nil || cfg.Loop.MaxIterations <= 0 {
		return false
	}
	return f.Iter >= cfg.Loop.MaxIterations
}

// BudgetExhausted is true when cumulative cost or wallclock has
// crossed a non-zero cap.
func BudgetExhausted(f *FSM, cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.Budget.MaxCostUSD > 0 && f.CumulativeCostUSD > cfg.Budget.MaxCostUSD {
		return true
	}
	if cfg.Budget.MaxWallclockSecs > 0 && f.CumulativeWallclockSecs > cfg.Budget.MaxWallclockSecs {
		return true
	}
	return false
}
