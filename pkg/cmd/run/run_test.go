package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/lock"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/internal/tui"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// *tui.Program is the production liveUI; pin the seam at compile time so a
// signature drift in either side breaks the build, not a terminal at runtime.
var _ liveUI = (*tui.Program)(nil)

func TestOptionsValidate(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		ok   bool
	}{
		{"zero values ok", Options{}, true},
		{"positive overrides ok", Options{MaxIterations: 5, SessionTimeout: 30, MemoryLimit: "1G"}, true},
		{"negative max-iterations", Options{MaxIterations: -1}, false},
		{"negative timeout", Options{SessionTimeout: -1}, false},
		{"unparseable memory", Options{MemoryLimit: "bogus"}, false},
		{"unlimited alone ok", Options{Unlimited: true}, true},
		{"unlimited with max-iterations conflicts", Options{Unlimited: true, MaxIterations: 5}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.opts.Validate()
			if c.ok && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !c.ok {
				var fe *cmdutil.FlagError
				if !errors.As(err, &fe) {
					t.Errorf("Validate() = %v, want *FlagError", err)
				}
			}
		})
	}
}

func TestNewCmdRunMetadata(t *testing.T) {
	c := NewCmdRun(&cmdutil.Factory{}, func(context.Context, *Options) error { return nil })
	if c.Use != "run" {
		t.Errorf("Use = %q, want %q", c.Use, "run")
	}
	for _, name := range []string{
		"once", "skip-gate", "dry-run", "fresh", "label",
		"max-iterations", "timeout", "memory", "no-tui", "unlimited",
	} {
		if c.Flags().Lookup(name) == nil {
			t.Errorf("--%s flag missing", name)
		}
	}
}

// TestNewCmdRunFlagsCaptured exercises the cobra flag-parser end-to-end
// via the runF seam (byob-testing.1): no real loop.Run runs, but every
// flag round-trips into Options.
func TestNewCmdRunFlagsCaptured(t *testing.T) {
	var got *Options
	c := NewCmdRun(&cmdutil.Factory{}, func(_ context.Context, o *Options) error {
		got = o
		return nil
	})
	c.SetArgs([]string{
		"--once",
		"--skip-gate",
		"--dry-run",
		"--fresh",
		"--label=foo",
		"--max-iterations=7",
		"--timeout=30",
		"--memory=512m",
	})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil {
		t.Fatal("runF was not invoked")
	}
	if !got.Once || !got.SkipGate || !got.DryRun || !got.Fresh {
		t.Errorf("bool flags wrong: %+v", got)
	}
	if got.Label != "foo" {
		t.Errorf("Label = %q, want foo", got.Label)
	}
	if got.MaxIterations != 7 || got.SessionTimeout != 30 || got.MemoryLimit != "512m" {
		t.Errorf("override flags wrong: max=%d timeout=%d mem=%q",
			got.MaxIterations, got.SessionTimeout, got.MemoryLimit)
	}
}

// TestNewCmdRunUnlimitedFlag pins --unlimited round-tripping into Options
// through the cobra parser; it's exercised separately from the other override
// flags because Validate rejects pairing it with --max-iterations.
func TestNewCmdRunUnlimitedFlag(t *testing.T) {
	var got *Options
	c := NewCmdRun(&cmdutil.Factory{}, func(_ context.Context, o *Options) error {
		got = o
		return nil
	})
	c.SetArgs([]string{"--unlimited"})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil || !got.Unlimited {
		t.Errorf("Unlimited = false, want true")
	}
}

func TestApplyOverrides(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want config.LoopConfig
	}{
		{
			name: "all zero — preserves config defaults",
			opts: Options{},
			want: config.LoopConfig{
				MaxIterations:      30,
				SessionTimeoutSecs: 3600,
				MaxNoopIters:       10,
				MemoryLimit:        "7G",
				SleepBetweenSecs:   5,
				QuotaWaitSecs:      1800,
				BeadPollSecs:       60,
			},
		},
		{
			name: "max-iterations only",
			opts: Options{MaxIterations: 7},
			want: config.LoopConfig{
				MaxIterations:      7,
				SessionTimeoutSecs: 3600,
				MaxNoopIters:       10,
				MemoryLimit:        "7G",
				SleepBetweenSecs:   5,
				QuotaWaitSecs:      1800,
				BeadPollSecs:       60,
			},
		},
		{
			name: "timeout only",
			opts: Options{SessionTimeout: 30},
			want: config.LoopConfig{
				MaxIterations:      30,
				SessionTimeoutSecs: 30,
				MaxNoopIters:       10,
				MemoryLimit:        "7G",
				SleepBetweenSecs:   5,
				QuotaWaitSecs:      1800,
				BeadPollSecs:       60,
			},
		},
		{
			name: "memory only",
			opts: Options{MemoryLimit: "1G"},
			want: config.LoopConfig{
				MaxIterations:      30,
				SessionTimeoutSecs: 3600,
				MaxNoopIters:       10,
				MemoryLimit:        "1G",
				SleepBetweenSecs:   5,
				QuotaWaitSecs:      1800,
				BeadPollSecs:       60,
			},
		},
		{
			name: "all three",
			opts: Options{MaxIterations: 5, SessionTimeout: 60, MemoryLimit: "2G"},
			want: config.LoopConfig{
				MaxIterations:      5,
				SessionTimeoutSecs: 60,
				MaxNoopIters:       10,
				MemoryLimit:        "2G",
				SleepBetweenSecs:   5,
				QuotaWaitSecs:      1800,
				BeadPollSecs:       60,
			},
		},
		{
			// Acceptance (1): --unlimited disables the cap via the 0 sentinel,
			// so loop.Run sees MaxIterations == 0 and never reports CapsExceeded.
			name: "unlimited disables the cap",
			opts: Options{Unlimited: true},
			want: config.LoopConfig{
				MaxIterations:      0,
				SessionTimeoutSecs: 3600,
				MaxNoopIters:       10,
				MemoryLimit:        "7G",
				SleepBetweenSecs:   5,
				QuotaWaitSecs:      1800,
				BeadPollSecs:       60,
			},
		},
		{
			name: "wait-on-quota flag enables, never disables",
			opts: Options{WaitOnQuota: true},
			want: config.LoopConfig{
				MaxIterations:      30,
				SessionTimeoutSecs: 3600,
				MaxNoopIters:       10,
				MemoryLimit:        "7G",
				SleepBetweenSecs:   5,
				WaitOnQuota:        true,
				QuotaWaitSecs:      1800,
				BeadPollSecs:       60,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			applyOverrides(cfg, &tc.opts)
			if cfg.Loop != tc.want {
				t.Errorf("Loop = %+v, want %+v", cfg.Loop, tc.want)
			}
		})
	}
}

// TestShouldUseTUI pins the activation gate: auto-on only when BOTH stdin and
// stderr are TTYs and --no-tui is unset.
func TestShouldUseTUI(t *testing.T) {
	cases := []struct {
		name             string
		stdinTTY, errTTY bool
		noTUI            bool
		want             bool
	}{
		{"both TTY, flag unset", true, true, false, true},
		{"both TTY, --no-tui", true, true, true, false},
		{"stdin not TTY", false, true, false, false},
		{"stderr not TTY", true, false, false, false},
		{"neither TTY", false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			io, _ := iostreams.Test()
			io.SetStdinTTY(tc.stdinTTY)
			io.SetStderrTTY(tc.errTTY)
			if got := shouldUseTUI(io, &Options{NoTUI: tc.noTUI}); got != tc.want {
				t.Errorf("shouldUseTUI = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeUI is the scripted liveUI that exercises orchestrate without a terminal
// (byob-testing.1). Its LoopIO ErrOut feeds an in-memory buffer that doubles
// as the captured pane (Tail). orchestrate is decoupled from *why* Run returns
// — it always cancels-and-waits afterward — so the fake collapses the two real
// exit triggers into one knob: with quitEarly set Run returns immediately
// (a user q/Ctrl-C before the loop finishes); otherwise Run returns when Done
// fires, standing in for the user quitting the post-run review screen. (The
// real Program.Run blocks past Done until the user quits; that stay-alive
// lifetime is covered in internal/tui/program_test.go.)
type fakeUI struct {
	ios       *iostreams.IOStreams
	captured  *strings.Builder
	quitEarly bool

	doneOnce sync.Once
	doneCh   chan struct{}

	// finishErr / finishObserved record the single Finish call orchestrate is
	// required to make on every exit path, including the panic path.
	finishCalls    int
	finishErr      error
	finishObserved bool
}

func newFakeUI(quitEarly bool) *fakeUI {
	buf := &strings.Builder{}
	return &fakeUI{
		ios:       iostreams.NewIOStreams(nil, buf, buf),
		captured:  buf,
		quitEarly: quitEarly,
		doneCh:    make(chan struct{}),
	}
}

func (f *fakeUI) LoopIO() *iostreams.IOStreams { return f.ios }
func (f *fakeUI) Observer() loop.Observer      { return noopObserver{} }
func (f *fakeUI) Finish(err error, observed bool) {
	f.doneOnce.Do(func() {
		f.finishCalls++
		f.finishErr, f.finishObserved = err, observed
		close(f.doneCh)
	})
}

func (f *fakeUI) Run() error {
	if f.quitEarly {
		return nil
	}
	<-f.doneCh
	return nil
}

func (f *fakeUI) Tail() []string {
	s := strings.TrimRight(f.captured.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type noopObserver struct{}

func (noopObserver) Observe(loop.Snapshot) {}

// TestOrchestrateStartupFailure covers the lock-contention shape (ralph-62i):
// loop.Run returns an infrastructure error before any iteration is observed, so
// Finish must report it as a startup failure (observed=false) — that is what
// tells the TUI to tear down instead of parking on a "stopped" badge with the
// reason trapped behind a keypress. The error still propagates to the caller.
func TestOrchestrateStartupFailure(t *testing.T) {
	ui := newFakeUI(false)
	run := func(context.Context, loop.Options) (fsm.Outcome, error) {
		return fsm.Outcome{}, fmt.Errorf("loop: acquire lock: %w", lock.ErrHeld)
	}
	err := orchestrate(context.Background(), ui, run, loop.Options{}, &strings.Builder{})
	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("orchestrate = %v, want the lock error", err)
	}
	if ui.finishCalls != 1 {
		t.Errorf("Finish called %d times, want exactly 1", ui.finishCalls)
	}
	if !errors.Is(ui.finishErr, lock.ErrHeld) || ui.finishObserved {
		t.Errorf("Finish(%v, %v), want (lock error, false)", ui.finishErr, ui.finishObserved)
	}
}

// TestOrchestrateMidRunErrorIsObserved is the other half: an error that lands
// after the loop has produced iterations (a transient git/bd failure inside
// routing, a cancelled context) must NOT be reported as a startup failure — the
// finished screen stays up for review. The observed flag comes from the wrapped
// Observer, so this also pins that orchestrate wires it into loop.Options.
func TestOrchestrateMidRunErrorIsObserved(t *testing.T) {
	ui := newFakeUI(false)
	run := func(_ context.Context, o loop.Options) (fsm.Outcome, error) {
		o.Observer.Observe(loop.Snapshot{Iter: 3})
		return fsm.Outcome{}, errors.New("bd exploded")
	}
	err := orchestrate(context.Background(), ui, run, loop.Options{}, &strings.Builder{})
	if err == nil {
		t.Fatal("orchestrate = nil, want the loop error")
	}
	if !ui.finishObserved {
		t.Error("Finish observed = false, want true (the loop reported an iteration)")
	}
}

// TestOrchestrateSeedSnapshotIsNotAnIteration guards the boundary: the pre-run
// seed Snapshot carries Iter 0, so it must not count as "the loop started".
func TestOrchestrateSeedSnapshotIsNotAnIteration(t *testing.T) {
	ui := newFakeUI(false)
	run := func(_ context.Context, o loop.Options) (fsm.Outcome, error) {
		o.Observer.Observe(loop.Snapshot{Iter: 0})
		return fsm.Outcome{}, errors.New("died before iterating")
	}
	_ = orchestrate(context.Background(), ui, run, loop.Options{}, &strings.Builder{})
	if ui.finishObserved {
		t.Error("Finish observed = true for an Iter 0 snapshot, want false")
	}
}

// TestOrchestrateLoopCompletes is acceptance (a): the loop reaches a terminal
// outcome, signals Done so the UI quits, and the collected outcome maps to the
// exit code — done{*} -> nil, failed{*} -> ExitCodeError.
func TestOrchestrateLoopCompletes(t *testing.T) {
	t.Run("done -> exit 0", func(t *testing.T) {
		ui := newFakeUI(false)
		run := func(context.Context, loop.Options) (fsm.Outcome, error) {
			return fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}, nil
		}
		if err := orchestrate(context.Background(), ui, run, loop.Options{}, &strings.Builder{}); err != nil {
			t.Errorf("orchestrate = %v, want nil", err)
		}
	})
	t.Run("failed -> exit code 1", func(t *testing.T) {
		ui := newFakeUI(false)
		run := func(context.Context, loop.Options) (fsm.Outcome, error) {
			return fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonBudget}, nil
		}
		err := orchestrate(context.Background(), ui, run, loop.Options{}, &strings.Builder{})
		var ec *cmdutil.ExitCodeError
		if !errors.As(err, &ec) || ec.Code != 1 {
			t.Errorf("orchestrate = %v, want ExitCodeError{1}", err)
		}
	})
}

// TestOrchestrateUIQuitCancelsLoop is acceptance (b): when the UI returns
// first (user quit), the context is cancelled, the loop unwinds, and its
// outcome is collected before orchestrate returns — proven by the failed{*}
// exit code, which is only knowable if the wait happened (no early exit).
func TestOrchestrateUIQuitCancelsLoop(t *testing.T) {
	ui := newFakeUI(true) // Run returns immediately, before the loop finishes
	var cancelled bool
	run := func(ctx context.Context, _ loop.Options) (fsm.Outcome, error) {
		<-ctx.Done() // blocks until orchestrate cancels on UI quit
		cancelled = true
		return fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonBudget}, nil
	}
	err := orchestrate(context.Background(), ui, run, loop.Options{}, &strings.Builder{})
	if !cancelled {
		t.Fatal("loop was not cancelled / waited on after UI quit")
	}
	var ec *cmdutil.ExitCodeError
	if !errors.As(err, &ec) || ec.Code != 1 {
		t.Errorf("orchestrate = %v, want ExitCodeError{1} from the collected outcome", err)
	}
}

// TestOrchestrateLoopPanic is acceptance (c): a panic in loop.Run is recovered
// into runResult.err and surfaced after teardown, and Done still fires so the
// UI unblocks rather than hanging on a dead loop.
func TestOrchestrateLoopPanic(t *testing.T) {
	ui := newFakeUI(false) // Run blocks until Done — must still fire post-panic
	run := func(context.Context, loop.Options) (fsm.Outcome, error) {
		panic("boom")
	}
	err := orchestrate(context.Background(), ui, run, loop.Options{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("orchestrate = %v, want error carrying the panic value", err)
	}
}

// TestOrchestrateTerminalStateFlush is acceptance (d): an ErrTerminalState
// early return maps to a silent non-zero exit, and the refusal the loop wrote
// to its redirected ErrOut is re-flushed to the real stderr so the operator
// still sees it after the TUI tears down.
func TestOrchestrateTerminalStateFlush(t *testing.T) {
	ui := newFakeUI(false)
	const refusal = "fsm is in terminal state; pass --fresh"
	run := func(_ context.Context, o loop.Options) (fsm.Outcome, error) {
		fmt.Fprintln(o.IO.ErrOut, refusal)
		return fsm.Outcome{}, loop.ErrTerminalState
	}
	var realErr strings.Builder
	err := orchestrate(context.Background(), ui, run, loop.Options{}, &realErr)
	if !errors.Is(err, cmdutil.ErrSilent) {
		t.Errorf("orchestrate = %v, want ErrSilent", err)
	}
	if !strings.Contains(realErr.String(), refusal) {
		t.Errorf("real stderr = %q, want it to contain the refusal %q", realErr.String(), refusal)
	}
}
