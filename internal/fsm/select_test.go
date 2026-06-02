package fsm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/config"
)

// stubBD lets tests set Ready/List output by (status, label).
type stubBD struct {
	ready map[string][]bd.Issue
	list  map[string][]bd.Issue
	err   error
}

func (s *stubBD) Ready(_ context.Context, label string) ([]bd.Issue, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.ready[label], nil
}
func (s *stubBD) List(_ context.Context, status, label string) ([]bd.Issue, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.list[status+"/"+label], nil
}

// cleanRepo initializes a throwaway git repo with one empty commit on
// main so GitClean returns true. Skips when git is absent.
func cleanRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// dirtyRepo is cleanRepo + a tracked-and-modified file so GitClean is false.
func dirtyRepo(t *testing.T) string {
	t.Helper()
	dir := cleanRepo(t)
	tracked := dir + "/file.txt"
	for _, args := range [][]string{
		{"-C", dir, "checkout", "-q", "main"},
	} {
		_ = exec.Command("git", args...).Run() // best effort
	}
	// Write, add, commit, then modify so the working tree is dirty.
	if err := writeFile(tracked, "v1"); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{
		{"add", "file.txt"},
		{"commit", "-q", "-m", "track"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := writeFile(tracked, "v2-dirty"); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

func TestSelectNextStateNilGuards(t *testing.T) {
	ctx := context.Background()
	if _, err := SelectNextState(ctx, RouteInput{}); err == nil {
		t.Error("nil FSM: nil err, want failure")
	}
	if _, err := SelectNextState(ctx, RouteInput{FSM: Fresh()}); err == nil {
		t.Error("nil Cfg: nil err, want failure")
	}
}

func TestSelectNextStateRunnerFailure(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		reason Reason
		want   Outcome
		errIs  bool
	}{
		{"auth", ReasonAuth, Outcome{State: StateFailed, Reason: ReasonAuth}, false},
		{"budget", ReasonBudget, Outcome{State: StateFailed, Reason: ReasonBudget}, false},
		{"runner_terminal", ReasonRunnerTerminal, Outcome{State: StateFailed, Reason: ReasonRunnerTerminal}, false},
		{"invalid-reason-for-failed", ReasonQueueEmpty, Outcome{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := SelectNextState(ctx, RouteInput{
				FSM:           Fresh(),
				Cfg:           config.Defaults(),
				BD:            &stubBD{},
				RunnerFailure: c.reason,
			})
			if c.errIs {
				if err == nil {
					t.Fatalf("got %v, want error", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if out != c.want {
				t.Errorf("out = %+v, want %+v", out, c.want)
			}
		})
	}
}

func TestSelectNextStateBudgetBeforeCaps(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults()
	cfg.Budget.MaxCostUSD = 1.0
	cfg.Loop.MaxIterations = 5
	f := Fresh()
	f.CumulativeCostUSD = 2.0
	f.Iter = 99 // would also exceed cap; budget should win
	out, err := SelectNextState(ctx, RouteInput{FSM: f, Cfg: cfg, BD: &stubBD{}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if (out != Outcome{State: StateFailed, Reason: ReasonBudget}) {
		t.Errorf("out = %+v, want failed{budget}", out)
	}
}

func TestSelectNextStateCapsExceeded(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults()
	cfg.Loop.MaxIterations = 5
	f := Fresh()
	f.Iter = 5
	out, err := SelectNextState(ctx, RouteInput{FSM: f, Cfg: cfg, BD: &stubBD{}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if (out != Outcome{State: StateDone, Reason: ReasonIterCap}) {
		t.Errorf("out = %+v, want done{iter_cap}", out)
	}
}

func TestSelectNextStateReviewMode(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults()
	repo := cleanRepo(t)

	t.Run("empty queue + clean -> done{queue_empty}", func(t *testing.T) {
		f := Fresh()
		f.ReviewMode = true
		f.ReviewBranch = "feat/x"
		out, err := SelectNextState(ctx, RouteInput{
			FSM: f, Cfg: cfg, Repo: repo, BD: &stubBD{},
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if (out != Outcome{State: StateDone, Reason: ReasonQueueEmpty}) {
			t.Errorf("out = %+v, want done{queue_empty}", out)
		}
	})

	t.Run("ready non-empty -> review", func(t *testing.T) {
		f := Fresh()
		f.ReviewMode = true
		f.ReviewBranch = "feat/x"
		b := &stubBD{ready: map[string][]bd.Issue{"review:feat/x": {{ID: "x"}}}}
		out, err := SelectNextState(ctx, RouteInput{
			FSM: f, Cfg: cfg, Repo: repo, BD: b,
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if out.State != StateReview {
			t.Errorf("out = %+v, want review", out)
		}
	})

	t.Run("review mode suppresses revert", func(t *testing.T) {
		// Even with consecutive_dirty past threshold, review wins.
		f := Fresh()
		f.ReviewMode = true
		f.ReviewBranch = "feat/x"
		f.ConsecutiveDirty = 99 // would normally trigger revert
		b := &stubBD{ready: map[string][]bd.Issue{"review:feat/x": {{ID: "x"}}}}
		out, err := SelectNextState(ctx, RouteInput{
			FSM: f, Cfg: cfg, Repo: repo, BD: b,
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if out.State != StateReview {
			t.Errorf("out = %+v, want review (revert suppressed)", out)
		}
	})

	t.Run("dirty tree -> review (not done)", func(t *testing.T) {
		dirty := dirtyRepo(t)
		f := Fresh()
		f.ReviewMode = true
		f.ReviewBranch = "feat/x"
		out, err := SelectNextState(ctx, RouteInput{
			FSM: f, Cfg: cfg, Repo: dirty, BD: &stubBD{},
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if out.State != StateReview {
			t.Errorf("out = %+v, want review", out)
		}
	})
}

func TestSelectNextStateDirtyStreak(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults()
	cfg.Backoff.DirtyRevertThreshold = 3
	repo := cleanRepo(t)
	f := Fresh()
	f.ConsecutiveDirty = 3
	out, err := SelectNextState(ctx, RouteInput{FSM: f, Cfg: cfg, Repo: repo, BD: &stubBD{}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.State != StateRevert {
		t.Errorf("out = %+v, want revert", out)
	}
}

func TestSelectNextStateGitDirty(t *testing.T) {
	ctx := context.Background()
	repo := dirtyRepo(t)
	f := Fresh()
	out, err := SelectNextState(ctx, RouteInput{
		FSM: f, Cfg: config.Defaults(), Repo: repo, BD: &stubBD{},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.State != StateDirty {
		t.Errorf("out = %+v, want dirty", out)
	}
}

func TestSelectNextStateQueueEmpty(t *testing.T) {
	ctx := context.Background()
	repo := cleanRepo(t)
	out, err := SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: config.Defaults(), Repo: repo, BD: &stubBD{},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if (out != Outcome{State: StateDone, Reason: ReasonQueueEmpty}) {
		t.Errorf("out = %+v, want done{queue_empty}", out)
	}
}

func TestSelectNextStateClean(t *testing.T) {
	ctx := context.Background()
	repo := cleanRepo(t)
	b := &stubBD{ready: map[string][]bd.Issue{"": {{ID: "a"}, {ID: "b"}}}}
	out, err := SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: config.Defaults(), Repo: repo, BD: b,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.State != StateClean {
		t.Errorf("out = %+v, want clean", out)
	}
}

func TestSelectNextStateNoopIdle(t *testing.T) {
	ctx := context.Background()
	repo := cleanRepo(t)
	// Ready queue is non-empty (would otherwise route clean), so only the
	// no-op streak can trigger the idle exit.
	b := &stubBD{ready: map[string][]bd.Issue{"": {{ID: "a"}}}}
	cfg := config.Defaults()
	cfg.Loop.MaxNoopIters = 3

	// At the cap -> done{idle}.
	out, err := SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: cfg, Repo: repo, BD: b, NoopStreak: 3,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if (out != Outcome{State: StateDone, Reason: ReasonIdle}) {
		t.Errorf("at cap: out = %+v, want done{idle}", out)
	}

	// Below the cap -> keep going (clean, queue non-empty).
	out, err = SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: cfg, Repo: repo, BD: b, NoopStreak: 2,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.State != StateClean {
		t.Errorf("below cap: out = %+v, want clean", out)
	}
}

func TestSelectNextStateNoopIdleDisabled(t *testing.T) {
	// MaxNoopIters == 0 disables the idle exit even with a huge streak.
	ctx := context.Background()
	repo := cleanRepo(t)
	b := &stubBD{ready: map[string][]bd.Issue{"": {{ID: "a"}}}}
	cfg := config.Defaults()
	cfg.Loop.MaxNoopIters = 0
	out, err := SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: cfg, Repo: repo, BD: b, NoopStreak: 999,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.State != StateClean {
		t.Errorf("disabled: out = %+v, want clean", out)
	}
}

func TestSelectNextStateNoopIdleDirtyWins(t *testing.T) {
	// A dirty tree takes precedence over the idle exit.
	ctx := context.Background()
	repo := dirtyRepo(t)
	cfg := config.Defaults()
	cfg.Loop.MaxNoopIters = 3
	out, err := SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: cfg, Repo: repo, BD: &stubBD{}, NoopStreak: 5,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.State != StateDirty {
		t.Errorf("out = %+v, want dirty (dirty beats idle)", out)
	}
}

func TestSelectNextStateInProgressBlocksDone(t *testing.T) {
	// Empty ready queue but in_progress holds work -> clean, not done.
	ctx := context.Background()
	repo := cleanRepo(t)
	b := &stubBD{list: map[string][]bd.Issue{"in_progress/": {{ID: "wip"}}}}
	out, err := SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: config.Defaults(), Repo: repo, BD: b,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.State != StateClean {
		t.Errorf("out = %+v, want clean (in_progress blocks done)", out)
	}
}

func TestSelectNextStatePropagatesBDError(t *testing.T) {
	ctx := context.Background()
	repo := cleanRepo(t)
	b := &stubBD{err: errors.New("bd offline")}
	_, err := SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: config.Defaults(), Repo: repo, BD: b,
	})
	if err == nil || !strings.Contains(err.Error(), "bd offline") {
		t.Errorf("err = %v, want bd offline propagated", err)
	}
}

func TestSelectNextStatePropagatesGitError(t *testing.T) {
	// Non-repo directory makes GitClean fail.
	ctx := context.Background()
	_, err := SelectNextState(ctx, RouteInput{
		FSM: Fresh(), Cfg: config.Defaults(), Repo: t.TempDir(), BD: &stubBD{},
	})
	if err == nil {
		t.Fatal("nil err on non-repo, want failure")
	}
}
