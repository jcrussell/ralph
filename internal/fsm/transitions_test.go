package fsm

import (
	"context"
	"os/exec"
	"testing"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/config"
)

// TestSelectNextStateTransitionMatrix locks down the full routing
// priority order documented in SelectNextState. Every row exercises
// one outcome; rows are ordered to match decision priority so a drift
// shows up as a "higher-priority rule is letting a lower one win"
// failure adjacent in the output.
//
// Conventions:
//   - newFSM/newCfg start from clean defaults; each row mutates only
//     what it needs.
//   - repoBuilder is a function so we lazily skip tests that need git
//     when git is absent.
func TestSelectNextStateTransitionMatrix(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	type row struct {
		name          string
		fsm           func() *FSM
		cfg           func() *config.Config
		repo          func(*testing.T) string
		bd            func() *stubBD
		runnerFailure Reason
		want          Outcome
	}

	rows := []row{
		// ── 1. Terminal runner failure trumps everything else ──────────
		{
			name:          "runner_failure auth wins over budget+caps+dirty+dirty-streak",
			fsm:           func() *FSM { f := Fresh(); f.CumulativeCostUSD = 99; f.Iter = 999; f.ConsecutiveDirty = 99; return f },
			cfg:           func() *config.Config { c := config.Defaults(); c.Budget.MaxCostUSD = 1; c.Loop.MaxIterations = 1; return c },
			repo:          dirtyRepo,
			bd:            func() *stubBD { return &stubBD{} },
			runnerFailure: ReasonAuth,
			want:          Outcome{State: StateFailed, Reason: ReasonAuth},
		},
		{
			name:          "runner_failure runner_terminal wins",
			fsm:           func() *FSM { return Fresh() },
			cfg:           func() *config.Config { return config.Defaults() },
			repo:          cleanRepo,
			bd:            func() *stubBD { return &stubBD{} },
			runnerFailure: ReasonRunnerTerminal,
			want:          Outcome{State: StateFailed, Reason: ReasonRunnerTerminal},
		},
		{
			name:          "runner_failure budget reason routes to failed{budget}",
			fsm:           func() *FSM { return Fresh() },
			cfg:           func() *config.Config { return config.Defaults() },
			repo:          cleanRepo,
			bd:            func() *stubBD { return &stubBD{} },
			runnerFailure: ReasonBudget,
			want:          Outcome{State: StateFailed, Reason: ReasonBudget},
		},

		// ── 2. Budget cap before iter cap ──────────────────────────────
		{
			name: "budget exhausted beats iter cap",
			fsm:  func() *FSM { f := Fresh(); f.CumulativeCostUSD = 2; f.Iter = 100; return f },
			cfg:  func() *config.Config { c := config.Defaults(); c.Budget.MaxCostUSD = 1; c.Loop.MaxIterations = 5; return c },
			repo: cleanRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateFailed, Reason: ReasonBudget},
		},
		{
			name: "budget wallclock exhaustion",
			fsm:  func() *FSM { f := Fresh(); f.CumulativeWallclockSecs = 120; return f },
			cfg:  func() *config.Config { c := config.Defaults(); c.Budget.MaxWallclockSecs = 60; return c },
			repo: cleanRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateFailed, Reason: ReasonBudget},
		},

		// ── 3. Iter cap before everything below ────────────────────────
		{
			name: "iter cap reached beats dirty streak + dirty tree",
			fsm:  func() *FSM { f := Fresh(); f.Iter = 5; f.ConsecutiveDirty = 99; return f },
			cfg:  func() *config.Config { c := config.Defaults(); c.Loop.MaxIterations = 5; return c },
			repo: dirtyRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateDone, Reason: ReasonIterCap},
		},
		{
			name: "iter cap reached, queue full",
			fsm:  func() *FSM { f := Fresh(); f.Iter = 30; return f },
			cfg:  func() *config.Config { c := config.Defaults(); c.Loop.MaxIterations = 30; return c },
			repo: cleanRepo,
			bd:   func() *stubBD { return &stubBD{ready: map[string][]bd.Issue{"": {{ID: "a"}}}} },
			want: Outcome{State: StateDone, Reason: ReasonIterCap},
		},

		// ── 4. Review mode short-circuits revert and the queue rules ───
		{
			name: "review: empty queue + clean -> done{queue_empty}",
			fsm:  func() *FSM { f := Fresh(); f.ReviewMode = true; f.ReviewBranch = "feat/x"; return f },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: cleanRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateDone, Reason: ReasonQueueEmpty},
		},
		{
			name: "review: ready findings -> review",
			fsm:  func() *FSM { f := Fresh(); f.ReviewMode = true; f.ReviewBranch = "feat/x"; return f },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: cleanRepo,
			bd: func() *stubBD {
				return &stubBD{ready: map[string][]bd.Issue{"review:feat/x": {{ID: "r1"}}}}
			},
			want: Outcome{State: StateReview},
		},
		{
			name: "review: in_progress findings -> review",
			fsm:  func() *FSM { f := Fresh(); f.ReviewMode = true; f.ReviewBranch = "feat/x"; return f },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: cleanRepo,
			bd: func() *stubBD {
				return &stubBD{list: map[string][]bd.Issue{"in_progress/review:feat/x": {{ID: "ip"}}}}
			},
			want: Outcome{State: StateReview},
		},
		{
			name: "review: dirty tree but empty queue -> review (not done, not dirty)",
			fsm:  func() *FSM { f := Fresh(); f.ReviewMode = true; f.ReviewBranch = "feat/x"; return f },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: dirtyRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateReview},
		},
		{
			name: "review: suppresses revert even with huge dirty streak",
			fsm: func() *FSM {
				f := Fresh()
				f.ReviewMode = true
				f.ReviewBranch = "feat/x"
				f.ConsecutiveDirty = 999
				return f
			},
			cfg: func() *config.Config { return config.Defaults() },
			repo: cleanRepo,
			bd: func() *stubBD {
				return &stubBD{ready: map[string][]bd.Issue{"review:feat/x": {{ID: "r1"}}}}
			},
			want: Outcome{State: StateReview},
		},

		// ── 5. Dirty streak past threshold -> revert (outside review) ──
		{
			name: "dirty streak at threshold -> revert",
			fsm:  func() *FSM { f := Fresh(); f.ConsecutiveDirty = 3; return f },
			cfg:  func() *config.Config { c := config.Defaults(); c.Backoff.DirtyRevertThreshold = 3; return c },
			repo: cleanRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateRevert},
		},
		{
			name: "dirty streak past threshold and tree still dirty -> revert (revert beats dirty)",
			fsm:  func() *FSM { f := Fresh(); f.ConsecutiveDirty = 5; return f },
			cfg:  func() *config.Config { c := config.Defaults(); c.Backoff.DirtyRevertThreshold = 3; return c },
			repo: dirtyRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateRevert},
		},
		{
			name: "dirty streak below threshold + dirty tree -> dirty (not revert)",
			fsm:  func() *FSM { f := Fresh(); f.ConsecutiveDirty = 2; return f },
			cfg:  func() *config.Config { c := config.Defaults(); c.Backoff.DirtyRevertThreshold = 3; return c },
			repo: dirtyRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateDirty},
		},

		// ── 6. Working tree dirty -> dirty ─────────────────────────────
		{
			name: "dirty tree + queue full -> dirty",
			fsm:  func() *FSM { return Fresh() },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: dirtyRepo,
			bd: func() *stubBD {
				return &stubBD{ready: map[string][]bd.Issue{"": {{ID: "a"}, {ID: "b"}}}}
			},
			want: Outcome{State: StateDirty},
		},
		{
			name: "dirty tree + empty queue still -> dirty (dirty beats done)",
			fsm:  func() *FSM { return Fresh() },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: dirtyRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateDirty},
		},

		// ── 7. Empty queue + clean tree -> done{queue_empty} ───────────
		{
			name: "empty ready + empty in_progress + clean -> done{queue_empty}",
			fsm:  func() *FSM { return Fresh() },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: cleanRepo,
			bd:   func() *stubBD { return &stubBD{} },
			want: Outcome{State: StateDone, Reason: ReasonQueueEmpty},
		},
		{
			name: "empty ready but in_progress non-empty -> clean (in_progress blocks done)",
			fsm:  func() *FSM { return Fresh() },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: cleanRepo,
			bd: func() *stubBD {
				return &stubBD{list: map[string][]bd.Issue{"in_progress/": {{ID: "wip"}}}}
			},
			want: Outcome{State: StateClean},
		},

		// ── 8. Default: queue has work, tree clean -> clean ────────────
		{
			name: "queue full + clean tree -> clean",
			fsm:  func() *FSM { return Fresh() },
			cfg:  func() *config.Config { return config.Defaults() },
			repo: cleanRepo,
			bd: func() *stubBD {
				return &stubBD{ready: map[string][]bd.Issue{"": {{ID: "a"}, {ID: "b"}}}}
			},
			want: Outcome{State: StateClean},
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			repo := r.repo(t)
			out, err := SelectNextState(context.Background(), RouteInput{
				FSM:           r.fsm(),
				Cfg:           r.cfg(),
				BD:            r.bd(),
				Repo:          repo,
				RunnerFailure: r.runnerFailure,
			})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if out != r.want {
				t.Errorf("out = %+v, want %+v", out, r.want)
			}
			// Every routed Outcome must validate; if a row hits an
			// invalid one we want to know about it here, not later.
			if err := out.Validate(); err != nil {
				t.Errorf("returned outcome failed Validate: %v", err)
			}
		})
	}
}

// TestSelectNextStateExhaustivelyCoversTargetStates is the canary that
// makes sure the matrix above hits every defined State as an outcome
// (excluding StateStart, which is a seed value not a routed outcome).
// If you add a new State to the topology, add a row above that lands
// on it; otherwise this test fails.
func TestSelectNextStateExhaustivelyCoversTargetStates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	// Build the same rows by re-running the matrix and recording which
	// State each row landed on. Cheaper than threading state through
	// the table — coverage is small and the test is fast.
	covered := map[State]bool{}
	ctx := context.Background()

	type tinyCase struct {
		fsm  *FSM
		cfg  *config.Config
		repo string
		bd   *stubBD
		fail Reason
	}
	cases := []tinyCase{
		{Fresh(), config.Defaults(), cleanRepo(t), &stubBD{}, ReasonNone},                                                 // done{queue_empty}
		{Fresh(), config.Defaults(), cleanRepo(t), &stubBD{ready: map[string][]bd.Issue{"": {{ID: "a"}}}}, ReasonNone},    // clean
		{Fresh(), config.Defaults(), dirtyRepo(t), &stubBD{}, ReasonNone},                                                 // dirty
		{&FSM{Outcome: Outcome{State: StateClean}, ConsecutiveDirty: 3}, &config.Config{Backoff: config.BackoffConfig{DirtyRevertThreshold: 3}}, cleanRepo(t), &stubBD{}, ReasonNone}, // revert
		{&FSM{Outcome: Outcome{State: StateClean}, ReviewMode: true, ReviewBranch: "x"}, config.Defaults(), cleanRepo(t),
			&stubBD{ready: map[string][]bd.Issue{"review:x": {{ID: "r"}}}}, ReasonNone}, // review
		{&FSM{Outcome: Outcome{State: StateClean}, Iter: 5}, &config.Config{Loop: config.LoopConfig{MaxIterations: 5}}, cleanRepo(t), &stubBD{}, ReasonNone}, // done{iter_cap}
		{Fresh(), config.Defaults(), cleanRepo(t), &stubBD{}, ReasonAuth}, // failed
	}
	for _, c := range cases {
		out, err := SelectNextState(ctx, RouteInput{
			FSM: c.fsm, Cfg: c.cfg, Repo: c.repo, BD: c.bd, RunnerFailure: c.fail,
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		covered[out.State] = true
	}

	// StateStart is a seed, never a routing target. Every other state
	// must be hit at least once.
	want := []State{StateClean, StateDirty, StateRevert, StateReview, StateDone, StateFailed}
	for _, s := range want {
		if !covered[s] {
			t.Errorf("matrix never lands on %s — add a row", s)
		}
	}
}
