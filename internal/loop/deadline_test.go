package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/runner"
	"github.com/jcrussell/ralph/internal/runs"
)

var (
	doneDeadline   = fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonDeadline}
	runnerTerminal = fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonRunnerTerminal}
)

func deadSession() *runner.Session {
	return &runner.Session{ExitCode: 1, Duration: time.Second, Stderr: "crashed", StderrTail: "crashed"}
}

func okSession(cost float64) *runner.Session {
	return &runner.Session{
		ExitCode: 0, Duration: time.Second,
		Stdout:   `{"subtype":"success"}`,
		Envelope: &runner.Envelope{TotalCostUSD: cost, NumTurns: 1, Subtype: "success", Raw: map[string]any{"subtype": "success"}},
	}
}

func busyBD() *fakeBD { return &fakeBD{ReadyByLabel: map[string][]bd.Issue{"": {{ID: "x"}}}} }

// A deadline already behind us ends the run at the start state without
// running the runner, and is recorded on the manifest.
func TestRun_DeadlineInThePastEndsAtStart(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.BD = busyBD()
	fr := &fakeRunner{}
	opts.Runner = fr
	clk := newFakeClock()
	opts.Clock = clk
	opts.Until = clk.now.Add(-time.Minute)

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != doneDeadline {
		t.Errorf("outcome = %v, want done{deadline}", out)
	}
	if fr.Calls != 0 {
		t.Errorf("runner ran %d times past the deadline", fr.Calls)
	}
	r, err := runs.Latest(repo)
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.Caps == nil || m.Caps.Until == nil || !m.Caps.Until.Equal(opts.Until) {
		t.Errorf("manifest caps = %+v, want until %v", m.Caps, opts.Until)
	}
}

// With work to do, the loop iterates until the deadline and stops there.
func TestRun_DeadlineStopsBetweenIterations(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Loop.MaxIterations = 0
	opts.Cfg.Loop.MaxNoopIters = 0
	opts.BD = busyBD()
	fr := &fakeRunner{}
	opts.Runner = fr
	clk := newFakeClock()
	opts.Clock = clk
	opts.Until = clk.now.Add(time.Minute)

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != doneDeadline {
		t.Fatalf("outcome = %v, want done{deadline}", out)
	}
	if fr.Calls == 0 || fr.Calls > 20 {
		t.Errorf("runner calls = %d, want a few before the deadline", fr.Calls)
	}
	if !clk.now.Before(opts.Until.Add(30 * time.Second)) {
		t.Errorf("clock at %v, ran well past the deadline %v", clk.now, opts.Until)
	}
}

// A quota wait longer than the time left is cut short at the deadline.
func TestRun_DeadlineCutsQuotaWaitShort(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Loop.WaitOnQuota = true
	opts.Cfg.Loop.QuotaWaitSecs = 1800
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{quotaSession()}}
	clk := newFakeClock()
	opts.Clock = clk
	opts.Until = clk.now.Add(5 * time.Minute)

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != doneDeadline {
		t.Fatalf("outcome = %v, want done{deadline}", out)
	}
	for _, d := range clk.Sleeps {
		if d > 5*time.Minute {
			t.Errorf("slept %s, past the 5m deadline (sleeps %v)", d, clk.Sleeps)
		}
	}
}

// runner_terminal is absorbed into a cooldown up to the retry budget; a
// healthy session refills the budget; once spent, the run fails as
// before.
func TestRun_RunnerTerminalRetryPolicy(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Backoff.DeadSessionThreshold = 1
	opts.Cfg.Loop.RunnerTerminalRetries = 1
	opts.Cfg.Loop.RunnerTerminalCooldownSecs = 900
	opts.BD = busyBD()
	// dead → retry 1/1; ok refills; dead → retry 1/1; dead → exhausted.
	fr := &fakeRunner{Sessions: []*runner.Session{deadSession(), okSession(0.01), deadSession(), deadSession()}}
	opts.Runner = fr
	clk := newFakeClock()
	opts.Clock = clk

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != runnerTerminal {
		t.Fatalf("outcome = %v, want failed{runner_terminal}", out)
	}
	if fr.Calls != 4 {
		t.Errorf("runner calls = %d, want 4", fr.Calls)
	}
	cooldowns := 0
	for _, d := range clk.Sleeps {
		if d == 900*time.Second {
			cooldowns++
		}
	}
	if cooldowns != 2 {
		t.Errorf("cooldown sleeps = %d, want 2 (sleeps %v)", cooldowns, clk.Sleeps)
	}
	var retried int
	for _, r := range readSummary(t, repo) {
		if strings.Contains(r.Narrative, "runner_terminal: retry 1/1 after 15m0s cooldown") {
			retried++
			if r.State == string(fsm.StateFailed) {
				t.Errorf("a retried iteration persisted failed: %+v", r)
			}
		}
	}
	if retried != 2 {
		t.Errorf("rows noting a retry = %d, want 2", retried)
	}
}

// Retries keep the run's cost total, so [budget] still ends a run that
// keeps dying and recovering.
func TestRun_RunnerTerminalRetryKeepsBudget(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Backoff.DeadSessionThreshold = 1
	opts.Cfg.Loop.RunnerTerminalRetries = 5
	opts.Cfg.Loop.RunnerTerminalCooldownSecs = 1
	opts.Cfg.Budget.MaxCostUSD = 0.015
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{okSession(0.01), deadSession(), okSession(0.01), okSession(0.01)}}
	opts.Clock = newFakeClock()

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonBudget}) {
		t.Errorf("outcome = %v, want failed{budget}", out)
	}
}

// --once keeps runner_terminal terminal even with retries configured.
func TestRun_RunnerTerminalRetryOffUnderOnce(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Once = true
	opts.Cfg.Backoff.DeadSessionThreshold = 1
	opts.Cfg.Loop.RunnerTerminalRetries = 3
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{deadSession()}}
	opts.Clock = newFakeClock()

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != runnerTerminal {
		t.Errorf("outcome = %v, want failed{runner_terminal}", out)
	}
}

// A bead park on an empty queue ends at the deadline instead of waiting
// forever.
func TestRun_DeadlineEndsBeadPark(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Loop.WaitForBeads = true
	opts.Cfg.Loop.BeadPollSecs = 60
	opts.BD = &fakeBD{}
	opts.Runner = &fakeRunner{}
	clk := newFakeClock()
	opts.Clock = clk
	opts.Until = clk.now.Add(10 * time.Minute)

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != doneDeadline {
		t.Errorf("outcome = %v, want done{deadline}", out)
	}
	if len(clk.Sleeps) > 15 {
		t.Errorf("parked for %d polls; want it to stop at the 10m deadline", len(clk.Sleeps))
	}
}

// Cancelling the run while a session is in flight unwinds the iteration:
// the killed session is not classified, routed or recorded, so a
// `systemctl stop` can't count as a dead session or persist failed{*}.
func TestRun_CancelDuringSessionUnwinds(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Backoff.DeadSessionThreshold = 1
	opts.BD = busyBD()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Runner = &fakeRunner{
		Sessions: []*runner.Session{{ExitCode: -1, Signal: "SIGKILL", Duration: time.Minute,
			Envelope: &runner.Envelope{TotalCostUSD: 0.25}}},
		OnRun: func(int, runner.RunOpts) { cancel() },
	}
	opts.Clock = newFakeClock()

	out, err := Run(ctx, opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	if out.State.Terminal() {
		t.Errorf("outcome = %v, want the pre-iteration state", out)
	}
	st, lerr := fsm.Load(repo)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if st.State.Terminal() {
		t.Errorf("fsm.json persisted %v after a cancel", st.Outcome)
	}
	// The spend still counts: totals saved, and one interrupted row.
	if st.CumulativeCostUSD != 0.25 || st.CumulativeWallclockSecs != 60 {
		t.Errorf("fsm totals = $%v / %ds, want $0.25 / 60s", st.CumulativeCostUSD, st.CumulativeWallclockSecs)
	}
	rows := readSummaryIfAny(t, repo)
	if len(rows) != 1 || rows[0].Skipped != "interrupted" || rows[0].RunnerMode != "" || rows[0].CostUSD != 0.25 {
		t.Errorf("summary rows = %+v, want one unclassified interrupted row with the cost", rows)
	}
}

// A stop while parked for beads leaves the FSM where it was instead of
// persisting the done{*} the park was sitting on.
func TestRun_CancelDuringBeadParkKeepsState(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Loop.WaitForBeads = true
	opts.BD = &fakeBD{}
	opts.Runner = &fakeRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Clock = &cancellingClock{fakeClock: newFakeClock(), cancel: cancel}

	_, err := Run(ctx, opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	st, lerr := fsm.Load(repo)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if st.State.Terminal() {
		t.Errorf("fsm.json = %v after a stop during the bead park, want non-terminal", st.Outcome)
	}
	// Nothing moved, so no start → start transition row.
	if r, rerr := runs.Latest(repo); rerr == nil {
		if ts, _ := r.ReadTransitions(); len(ts) != 0 {
			t.Errorf("transitions = %+v, want none", ts)
		}
	}
}

// cancellingClock cancels the run on its first Sleep, standing in for a
// SIGTERM that lands during a wait.
type cancellingClock struct {
	*fakeClock
	cancel context.CancelFunc
}

func (c *cancellingClock) Sleep(ctx context.Context, d time.Duration) {
	c.cancel()
	c.fakeClock.Sleep(ctx, d)
}

// The retry count and the cooldown's end are persisted: a restart
// mid-cooldown (a new Run on the same repo) finishes the cooldown before
// the next session, and finds the retry budget already spent.
func TestRun_RunnerTerminalRetrySurvivesRestart(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Backoff.DeadSessionThreshold = 1
	opts.Cfg.Loop.RunnerTerminalRetries = 1
	opts.Cfg.Loop.RunnerTerminalCooldownSecs = 900
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{deadSession()}}
	// Stop the first process during its cooldown (the first sleep).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Clock = &cancellingClock{fakeClock: newFakeClock(), cancel: cancel}
	if _, err := Run(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run err = %v, want context.Canceled", err)
	}
	st, err := fsm.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if st.RunnerTerminalRetries != 1 || st.CooldownUntil == "" || st.State.Terminal() {
		t.Fatalf("after a stop mid-cooldown: retries=%d cooldown_until=%q state=%v", st.RunnerTerminalRetries, st.CooldownUntil, st.Outcome)
	}

	// Restart: the rest of the cooldown is slept first, then the next
	// dead session finds the budget spent.
	fr := &fakeRunner{Sessions: []*runner.Session{deadSession()}}
	opts.Runner = fr
	clk := newFakeClock()
	opts.Clock = clk
	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if out != runnerTerminal || fr.Calls != 1 {
		t.Errorf("after restart: outcome = %v after %d calls, want failed{runner_terminal} after 1", out, fr.Calls)
	}
	if len(clk.Sleeps) == 0 || clk.Sleeps[0] < 10*time.Minute {
		t.Errorf("restart didn't finish the cooldown first (sleeps %v)", clk.Sleeps)
	}
	if st, _ := fsm.Load(repo); st.CooldownUntil != "" {
		t.Errorf("cooldown_until = %q after the cooldown finished", st.CooldownUntil)
	}
}

// A stop that lands while the gate runs kills the gate through ctx; the
// killed gate must not be recorded as a gate failure. The iteration
// unwinds, keeping the session's commits in the totals.
func TestRun_CancelDuringGateUnwinds(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Gate.RunWhen = "always"
	opts.BD = busyBD()
	writeExecutableHook(t, filepath.Join(repo, ".ralph", "hooks", "states", "clean", "gate"), "#!/bin/sh\nsleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Runner = &fakeRunner{OnRun: func(_ int, _ runner.RunOpts) {
		commitFile(t, repo, "work.txt", "done")
		time.AfterFunc(300*time.Millisecond, cancel) // lands during the gate
	}}
	opts.Clock = newFakeClock()

	_, err := Run(ctx, opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	st, lerr := fsm.Load(repo)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if st.LastGateResult != "" || st.ConsecutiveGateFail != 0 || st.State.Terminal() {
		t.Errorf("fsm recorded the killed gate: %+v", st)
	}
	if st.CumulativeCommits != 1 {
		t.Errorf("cumulative commits = %d, want the session's 1", st.CumulativeCommits)
	}
	rows := readSummaryIfAny(t, repo)
	last := rows[len(rows)-1]
	if last.Skipped != "interrupted" || last.Commits != 1 || last.GateResult != "" {
		t.Errorf("last row = %+v, want an interrupted row with 1 commit and no gate result", last)
	}
	if st.GateBase == "" {
		t.Fatal("gate_base not set: the interrupted commit would skip the gate")
	}

	// Resume with the commits-only gate and a session that commits
	// nothing: the gate still runs, over the interrupted commit.
	opts.Cfg.Gate.RunWhen = "commits-only"
	opts.Once = true
	writeExecutableHook(t, filepath.Join(repo, ".ralph", "hooks", "states", "clean", "gate"), "#!/bin/sh\nexit 0\n")
	opts.Runner = &fakeRunner{}
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	st, _ = fsm.Load(repo)
	if st.LastGateResult != "passed" || st.GateBase != "" {
		t.Errorf("after resume: last_gate=%q gate_base=%q, want the gate run over the carried commit", st.LastGateResult, st.GateBase)
	}
	if st.CumulativeCommits != 1 {
		t.Errorf("cumulative commits = %d, want 1 (the carried commit isn't recounted)", st.CumulativeCommits)
	}
}

// A runner out of credit maps to runner_terminal too, but a cooldown
// won't fix it: the retry policy leaves it terminal.
func TestRun_RunnerTerminalRetrySkipsRunnerBudget(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Loop.RunnerTerminalRetries = 3
	opts.Cfg.Loop.RunnerTerminalCooldownSecs = 900
	opts.BD = busyBD()
	fr := &fakeRunner{Sessions: []*runner.Session{{ExitCode: 1, Duration: time.Second, Stderr: "Credit balance is too low"}}}
	opts.Runner = fr
	clk := newFakeClock()
	opts.Clock = clk

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != runnerTerminal || fr.Calls != 1 {
		t.Errorf("outcome = %v after %d calls, want failed{runner_terminal} after 1", out, fr.Calls)
	}
	for _, d := range clk.Sleeps {
		if d == 900*time.Second {
			t.Errorf("cooled down for a runner budget failure (sleeps %v)", clk.Sleeps)
		}
	}
}

// Ending at the deadline between iterations (here after a quota wait)
// fires the left state's exit hook and tells the observer, like any
// other transition.
func TestRun_EndAtDeadlineRunsExitHookAndNotifies(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Loop.WaitOnQuota = true
	opts.Cfg.Loop.QuotaWaitSecs = 1800
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{quotaSession()}}
	marker := filepath.Join(t.TempDir(), "exit-ran")
	writeExecutableHook(t, filepath.Join(repo, ".ralph", "hooks", "states", "clean", "exit"),
		"#!/bin/sh\necho \"$RALPH_NEXT_STATE\" > "+marker+"\n")
	obs := &fakeObserver{}
	opts.Observer = obs
	clk := newFakeClock()
	opts.Clock = clk
	opts.Until = clk.now.Add(5 * time.Minute)

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != doneDeadline {
		t.Fatalf("outcome = %v, want done{deadline}", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("clean/exit hook did not run at the deadline: %v", err)
	}
	last := obs.Snapshots[len(obs.Snapshots)-1]
	if last.State != fsm.StateDone || last.Reason != fsm.ReasonDeadline || last.Wait != nil {
		t.Errorf("last snapshot = %v{%v} wait=%v, want done{deadline} with no wait", last.State, last.Reason, last.Wait)
	}
}

// The cooldown badge counts down only to the deadline, and is cleared
// once the cooldown ends (before the next session starts).
func TestRun_CooldownWaitClampedAndCleared(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Backoff.DeadSessionThreshold = 1
	opts.Cfg.Loop.RunnerTerminalRetries = 1
	opts.Cfg.Loop.RunnerTerminalCooldownSecs = 900
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{deadSession()}}
	obs := &fakeObserver{}
	opts.Observer = obs
	clk := newFakeClock()
	opts.Clock = clk
	opts.Until = clk.now.Add(2 * time.Minute)

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sawCooldown, clearedAfter := false, false
	for _, s := range obs.Snapshots {
		switch {
		case s.Wait != nil && s.Wait.Kind == "cooldown":
			sawCooldown = true
			if s.Wait.Remaining > 2*time.Minute {
				t.Errorf("cooldown badge counts %s, past the 2m deadline", s.Wait.Remaining)
			}
		case sawCooldown && s.Wait == nil:
			clearedAfter = true
		}
	}
	if !sawCooldown || !clearedAfter {
		t.Errorf("cooldown badge shown=%v cleared=%v", sawCooldown, clearedAfter)
	}
}

// --once that is stopped during its backoff sleep exits as a stop.
func TestRun_OnceStoppedInBackoffReturnsCancel(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Once = true
	opts.Cfg.Loop.SleepBetweenSecs = 5
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Clock = &cancellingClock{fakeClock: newFakeClock(), cancel: cancel}

	if _, err := Run(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Errorf("Run err = %v, want context.Canceled", err)
	}
}

// A retry that routes to a terminal anyway never happens, so it isn't
// counted.
func TestRun_RunnerTerminalRetryNotCountedWhenRouteEnds(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Backoff.DeadSessionThreshold = 1
	opts.Cfg.Loop.RunnerTerminalRetries = 1
	opts.Cfg.Loop.MaxIterations = 1
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{deadSession()}}
	opts.Clock = newFakeClock()

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	st, _ := fsm.Load(repo)
	if (out != fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonIterCap}) || st.RunnerTerminalRetries != 0 {
		t.Errorf("outcome %v, retries %d; want done{iter_cap} and 0", out, st.RunnerTerminalRetries)
	}
}

// Two stops in a row: commits left ungated by the first keep their gate
// base when the second stop kills the gate that was covering them.
func TestRun_GateBaseSurvivesStopDuringItsGate(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Gate.RunWhen = "commits-only"
	opts.BD = busyBD()

	// 1st run: the session commits, then the run is stopped.
	ctx, cancel := context.WithCancel(context.Background())
	opts.Runner = &fakeRunner{OnRun: func(int, runner.RunOpts) {
		commitFile(t, repo, "a.txt", "a")
		cancel()
	}}
	opts.Clock = newFakeClock()
	if _, err := Run(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("1st Run err = %v", err)
	}
	st, _ := fsm.Load(repo)
	base := st.GateBase
	if base == "" {
		t.Fatal("no gate base after the 1st stop")
	}

	// 2nd run: no new commits; the gate starts over the carried commit
	// and a stop kills it.
	writeExecutableHook(t, filepath.Join(repo, ".ralph", "hooks", "states", "clean", "gate"), "#!/bin/sh\nsleep 30\n")
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	opts.Runner = &fakeRunner{OnRun: func(int, runner.RunOpts) { time.AfterFunc(300*time.Millisecond, cancel2) }}
	opts.Clock = newFakeClock()
	if _, err := Run(ctx2, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("2nd Run err = %v", err)
	}
	st, _ = fsm.Load(repo)
	if st.GateBase != base {
		t.Errorf("gate base = %q after the killed gate, want the original %q", st.GateBase, base)
	}
}

// A stop after the gate reached a real verdict (here, during the
// post-iteration hook) doesn't make those commits pending again.
func TestRun_StopAfterRealGateDoesNotCarryCommits(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Cfg.Gate.RunWhen = "commits-only"
	opts.BD = busyBD()
	writeExecutableHook(t, filepath.Join(repo, ".ralph", "hooks", "states", "clean", "gate"), "#!/bin/sh\nexit 0\n")
	writeExecutableHook(t, filepath.Join(repo, ".ralph", "hooks", "post-iteration"), "#!/bin/sh\nsleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Runner = &fakeRunner{OnRun: func(int, runner.RunOpts) {
		commitFile(t, repo, "b.txt", "b")
		time.AfterFunc(500*time.Millisecond, cancel) // after the quick gate, during post-iteration
	}}
	opts.Clock = newFakeClock()
	if _, err := Run(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v", err)
	}
	if st, _ := fsm.Load(repo); st.GateBase != "" {
		t.Errorf("gate base = %q, want none: the gate already passed these commits", st.GateBase)
	}
}

// A persisted cooldown isn't slept by --once, and is dropped once the
// retry policy is turned off.
func TestRun_ResumeCooldownSkippedOrDropped(t *testing.T) {
	repo := scaffoldRepo(t)
	st := fsm.Fresh()
	st.Outcome = fsm.Outcome{State: fsm.StateClean}
	st.CooldownUntil = time.Date(2026, 5, 14, 13, 0, 0, 0, time.UTC).Format(time.RFC3339) // 1h after the fake clock
	if err := st.Save(repo); err != nil {
		t.Fatal(err)
	}
	opts := baseOpts(t, repo)
	opts.Once = true
	opts.Cfg.Loop.RunnerTerminalRetries = 3
	opts.BD = busyBD()
	opts.Runner = &fakeRunner{}
	clk := newFakeClock()
	opts.Clock = clk
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("--once Run: %v", err)
	}
	for _, d := range clk.Sleeps {
		if d > 30*time.Minute {
			t.Errorf("--once slept the stale cooldown (%s)", d)
		}
	}
	if st, _ := fsm.Load(repo); st.CooldownUntil == "" {
		t.Error("--once dropped the pending cooldown; the next real run should still honor it")
	}

	// Policy off: the next run drops it and doesn't wait.
	opts.Cfg.Loop.RunnerTerminalRetries = 0
	clk = newFakeClock()
	opts.Clock = clk
	opts.Once = false
	opts.Cfg.Loop.MaxIterations = 1
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, d := range clk.Sleeps {
		if d > 30*time.Minute {
			t.Errorf("slept a cooldown with retries turned off (%s)", d)
		}
	}
	if st, _ := fsm.Load(repo); st.CooldownUntil != "" {
		t.Errorf("cooldown_until = %q with retries off, want cleared", st.CooldownUntil)
	}
}
