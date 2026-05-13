package fsm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/config"
)

// fakeBDLister is the test double for BDLister. Stubbed responses
// keyed by (verb, label). byob-testing.1: inject; don't monkey-patch.
type fakeBDLister struct {
	ready map[string][]bd.Issue
	list  map[string][]bd.Issue // key = status + "/" + label
	err   error
}

func (f *fakeBDLister) Ready(_ context.Context, label string) ([]bd.Issue, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ready[label], nil
}
func (f *fakeBDLister) List(_ context.Context, status, label string) ([]bd.Issue, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list[status+"/"+label], nil
}

func TestDirtyStreakExceeded(t *testing.T) {
	cfg := &config.Config{Backoff: config.BackoffConfig{DirtyRevertThreshold: 3}}
	cases := []struct {
		dirt int
		want bool
	}{{0, false}, {2, false}, {3, true}, {5, true}}
	for _, c := range cases {
		got := DirtyStreakExceeded(&FSM{ConsecutiveDirty: c.dirt}, cfg)
		if got != c.want {
			t.Errorf("DirtyStreakExceeded(dirt=%d) = %v, want %v", c.dirt, got, c.want)
		}
	}
	// Zero/negative threshold disables the predicate.
	cfg.Backoff.DirtyRevertThreshold = 0
	if DirtyStreakExceeded(&FSM{ConsecutiveDirty: 99}, cfg) {
		t.Errorf("threshold=0 should disable")
	}
	if DirtyStreakExceeded(&FSM{ConsecutiveDirty: 99}, nil) {
		t.Errorf("nil cfg should disable")
	}
}

func TestCapsExceeded(t *testing.T) {
	cfg := &config.Config{Loop: config.LoopConfig{MaxIterations: 10}}
	if !CapsExceeded(&FSM{Iter: 10}, cfg) {
		t.Errorf("iter==max should be exceeded")
	}
	if CapsExceeded(&FSM{Iter: 9}, cfg) {
		t.Errorf("iter<max should not be exceeded")
	}
	cfg.Loop.MaxIterations = 0
	if CapsExceeded(&FSM{Iter: 99}, cfg) {
		t.Errorf("max=0 should disable")
	}
}

func TestBudgetExhausted(t *testing.T) {
	cfg := &config.Config{Budget: config.BudgetConfig{MaxCostUSD: 1.0, MaxWallclockSecs: 60}}
	cases := []struct {
		cost float64
		wall int
		want bool
	}{
		{0.5, 30, false},
		{1.5, 30, true},  // cost cap
		{0.5, 90, true},  // wallclock cap
		{1.0, 60, false}, // boundary: > not >=
	}
	for _, c := range cases {
		got := BudgetExhausted(&FSM{CumulativeCostUSD: c.cost, CumulativeWallclockSecs: c.wall}, cfg)
		if got != c.want {
			t.Errorf("Budget(cost=%v,wall=%d) = %v, want %v", c.cost, c.wall, got, c.want)
		}
	}
	if BudgetExhausted(&FSM{CumulativeCostUSD: 999}, &config.Config{}) {
		t.Errorf("zero caps should disable")
	}
}

func TestBDReadyCount(t *testing.T) {
	b := &fakeBDLister{ready: map[string][]bd.Issue{
		"":         {{ID: "a"}, {ID: "b"}, {ID: "c"}},
		"review:x": {{ID: "d"}},
	}}
	if n, err := BDReadyCount(context.Background(), b, ""); err != nil || n != 3 {
		t.Errorf("Ready unscoped: n=%d err=%v", n, err)
	}
	if n, err := BDReadyCount(context.Background(), b, "review:x"); err != nil || n != 1 {
		t.Errorf("Ready labelled: n=%d err=%v", n, err)
	}
	// Error propagates.
	b.err = errors.New("boom")
	if _, err := BDReadyCount(context.Background(), b, ""); err == nil {
		t.Errorf("expected error")
	}
}

func TestBDInProgressCount(t *testing.T) {
	b := &fakeBDLister{list: map[string][]bd.Issue{
		"in_progress/": {{ID: "a"}, {ID: "b"}},
	}}
	n, err := BDInProgressCount(context.Background(), b, "")
	if err != nil || n != 2 {
		t.Errorf("n=%d err=%v", n, err)
	}
}

func TestReviewQueueEmpty(t *testing.T) {
	// Empty queue.
	b := &fakeBDLister{ready: map[string][]bd.Issue{}, list: map[string][]bd.Issue{}}
	if empty, err := ReviewQueueEmpty(context.Background(), b, "feat/x"); err != nil || !empty {
		t.Errorf("empty: got=%v err=%v", empty, err)
	}
	// Ready has work → not empty.
	b.ready = map[string][]bd.Issue{"review:feat/x": {{ID: "r1"}}}
	if empty, _ := ReviewQueueEmpty(context.Background(), b, "feat/x"); empty {
		t.Errorf("ready non-empty: got empty=true")
	}
	// Only in_progress has work → not empty.
	b.ready = map[string][]bd.Issue{}
	b.list = map[string][]bd.Issue{"in_progress/review:feat/x": {{ID: "ip"}}}
	if empty, _ := ReviewQueueEmpty(context.Background(), b, "feat/x"); empty {
		t.Errorf("in_progress non-empty: got empty=true")
	}
}

func TestGitCleanAndDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "commit", "--allow-empty", "-q", "-m", "init")

	ctx := context.Background()

	// Fresh repo: clean.
	clean, err := GitClean(ctx, repo)
	if err != nil || !clean {
		t.Fatalf("fresh: clean=%v err=%v", clean, err)
	}
	dirty, _ := GitDirty(ctx, repo)
	if dirty {
		t.Errorf("fresh: dirty=true, want false")
	}

	// Untracked file: still clean per --untracked-files=no.
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	clean, _ = GitClean(ctx, repo)
	if !clean {
		t.Errorf("untracked-only: clean=false, want true (untracked ignored)")
	}

	// Commit and then modify tracked file: dirty.
	tracked := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("v1"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-q", "-m", "track")
	if err := os.WriteFile(tracked, []byte("v2"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	clean, _ = GitClean(ctx, repo)
	if clean {
		t.Errorf("modified tracked: clean=true, want false")
	}
	dirty, _ = GitDirty(ctx, repo)
	if !dirty {
		t.Errorf("modified tracked: dirty=false, want true")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// Compile-time assertion: *bd.Client satisfies BDLister.
var _ BDLister = (*bd.Client)(nil)
