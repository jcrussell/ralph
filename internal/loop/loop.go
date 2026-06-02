package loop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/hooks"
	"github.com/jcrussell/ralph/internal/lock"
	ralphlog "github.com/jcrussell/ralph/internal/log"
	"github.com/jcrussell/ralph/internal/paths"
	"github.com/jcrussell/ralph/internal/runner"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Options configures one Run. Repo, Cfg, and IO are required; everything
// else has a sensible zero value. Runner, BD, and Clock are test seams
// — production callers leave them nil and Run constructs defaults.
type Options struct {
	Repo string
	Cfg  *config.Config
	IO   *iostreams.IOStreams

	ReviewMode   bool
	ReviewBranch string
	ReviewBase   string

	Label    string
	Once     bool
	SkipGate bool
	DryRun   bool
	Fresh    bool

	Runner Runner
	BD     BDClient
	Clock  Clock

	// Observer is an optional live-run metrics sink (see seams.go). nil
	// means no observation; Run substitutes a no-op so the iteration path
	// never branches on it.
	Observer Observer
}

// ErrTerminalState is returned by Run when fsm.json is already in a
// terminal state (done{*} or failed{*}) and Options.Fresh is false. The
// human-facing notice is written to Options.IO.ErrOut before the error
// is returned, so command wrappers should map this to cmdutil.ErrSilent
// (silent exit, non-zero code) rather than printing again.
var ErrTerminalState = errors.New("loop: fsm is in terminal state without --fresh")

// runContext is the per-Run scratch space passed to iteration helpers.
// Unexported because nothing outside the loop package constructs it.
type runContext struct {
	opts     Options
	cfg      *config.Config
	repo     string
	io       *iostreams.IOStreams
	log      *slog.Logger
	fsm      *fsm.FSM
	run      *runs.Run
	sum      *ralphlog.JSONL
	bdClient BDClient
	runr     Runner
	clock    Clock
	observer Observer

	// started is the run-entry Clock.Now(), captured once so Snapshot
	// elapsed is measured from a single origin.
	started time.Time

	// Per-iteration scratch (reset at the top of runIteration).
	paths iterPaths

	// Streaks the FSM doesn't track — owned by the loop.
	consecFailures int
	deadStreak     int
	// consecNoops counts consecutive no-op iterations (runner OK, zero
	// commits, empty bd diff, unchanged state). Fed into RouteInput so
	// SelectNextState can exit done{idle} once it reaches MaxNoopIters.
	consecNoops      int
	lastEnteredState fsm.State
	lastGateResult   string
	// lastGateStdoutFile is the absolute path of the previous
	// iteration's gate-hook stdout file (when a gate ran). composePrompt
	// reads its tail to populate {{.GateOutput}} for the next iteration.
	lastGateStdoutFile string

	// lastReadyBeads is the most recent `bd ready` count, sampled in
	// routeAndPersist and surfaced to the Observer's Snapshot. -1 until the
	// first sample so the live UI can render "not yet sampled".
	lastReadyBeads int
}

// Run drives the FSM until a terminal outcome (done or failed). Run
// returns the terminal Outcome it persisted. Errors are returned only
// for infrastructure failures (lock contention, disk-full when
// persisting, invalid options) — runner failures get routed through
// the FSM, not returned here.
func Run(ctx context.Context, opts Options) (fsm.Outcome, error) {
	if err := validateOptions(&opts); err != nil {
		return fsm.Outcome{}, err
	}
	if opts.Runner == nil {
		opts.Runner = runner.New(opts.Cfg.Runner.Command, opts.Cfg.Runner.Args, opts.Cfg.Runner.Model, opts.Cfg.Loop.MemoryLimit)
	}
	if opts.BD == nil {
		opts.BD = bd.New("", opts.Repo, bd.WithExcludeTypes(opts.Cfg.Beads.ExcludeTypes...))
	}
	if opts.Clock == nil {
		opts.Clock = defaultClock{}
	}
	if opts.Observer == nil {
		opts.Observer = noopObserver{}
	}

	// Capture run-start time once so every Snapshot.Elapsed is measured
	// from a single origin rather than re-reading the clock per field.
	started := opts.Clock.Now()

	p := paths.New(opts.Repo)

	// Acquire the repo lock first — bail fast if another orchestrator
	// is running. ErrHeld is surfaced via %w so callers can distinguish.
	l, err := lock.Acquire(p.PidLock())
	if err != nil {
		return fsm.Outcome{}, fmt.Errorf("loop: acquire lock: %w", err)
	}
	defer func() { _ = l.Release() }()

	// Open slog → orchestrator.log; warnings & errors go here.
	logger, logCloser, err := ralphlog.NewSlog(p.OrchestratorLog(), slog.LevelInfo)
	if err != nil {
		return fsm.Outcome{}, fmt.Errorf("loop: open orchestrator.log: %w", err)
	}
	defer func() { _ = logCloser.Close() }()

	// Open summary.jsonl once for the whole run.
	sum, err := ralphlog.OpenJSONL(p.Summary())
	if err != nil {
		return fsm.Outcome{}, fmt.Errorf("loop: open summary.jsonl: %w", err)
	}
	defer func() { _ = sum.Close() }()

	// Load FSM (or Fresh()), apply --fresh / done auto-reset / review-mode
	// setup, and refuse a pre-existing failed{*} terminal. Done before
	// runs.Begin so no zombie run dir is created. Resource-acquisition
	// defers above stay in Run; this block is pure FSM preparation.
	f, err := loadAndPrepareFSM(opts, opts.IO.ErrOut)
	if err != nil {
		return fsm.Outcome{}, err
	}

	// Open the latest open run when resuming a mid-loop FSM, otherwise
	// begin a new one. Keeps one FSM journey within one runs/<id>/ dir
	// across --once / crash restarts. Finalize is deferred so a panic or
	// early error still stamps end_time + exit_outcome.
	r, err := openOrBeginRun(opts.Repo, f, logger)
	if err != nil {
		return fsm.Outcome{}, err
	}
	finalOutcome := f.Outcome
	defer func() {
		// Only record an ExitOutcome for runs that actually reached a
		// terminal FSM state. --once and ctx-cancel exits stop mid-flight
		// — synthesizing a failure here would mislabel them. The manifest
		// still gets EndTime + aggregates so the run is well-formed.
		var oc *fsm.Outcome
		if finalOutcome.State.Terminal() {
			v := finalOutcome
			oc = &v
		}
		if err := r.Finalize(runs.FinalizeInput{
			Outcome:                 oc,
			TotalIters:              f.Iter,
			CumulativeCostUSD:       f.CumulativeCostUSD,
			CumulativeWallclockSecs: f.CumulativeWallclockSecs,
		}); err != nil {
			logger.Error("runs.Finalize failed", "err", err)
		}
	}()

	rc := &runContext{
		opts:     opts,
		cfg:      opts.Cfg,
		repo:     opts.Repo,
		io:       opts.IO,
		log:      logger,
		fsm:      f,
		run:      r,
		sum:      sum,
		bdClient: opts.BD,
		runr:     opts.Runner,
		clock:    opts.Clock,
		observer: opts.Observer,
		started:  started,

		lastReadyBeads: -1, // not yet sampled
	}

	// Sample the ready-queue depth once at startup so the live UI's header
	// shows a real count from the first Snapshot — even if the first tick is
	// skipped or quota-waited (those paths don't re-sample). Best-effort:
	// routeAndPersist refreshes it each normal iteration.
	if n, err := fsm.BDReadyCount(ctx, rc.bdClient, ""); err == nil {
		rc.lastReadyBeads = n
	}

	// Handle the virtual start state inline: route immediately without
	// running the runner, hooks, or rendering a prompt.
	if rc.fsm.State == fsm.StateStart {
		if err := handleStartState(ctx, rc); err != nil {
			return rc.fsm.Outcome, err
		}
		finalOutcome = rc.fsm.Outcome
	}

	// Main loop — terminates when the FSM enters a terminal state, ctx
	// is cancelled, or --once after one non-terminal iteration.
	for !rc.fsm.State.Terminal() {
		if err := ctx.Err(); err != nil {
			finalOutcome = rc.fsm.Outcome
			return rc.fsm.Outcome, err
		}
		next, err := runIteration(ctx, rc)
		if err != nil {
			finalOutcome = next
			return next, err
		}
		finalOutcome = next
		if rc.opts.Once && !next.State.Terminal() {
			return next, nil
		}
	}

	// Terminal dispatch: failure hook fires on failed{}.
	if rc.fsm.State == fsm.StateFailed {
		env := hooks.Env{
			Repo:          rc.repo,
			Iter:          rc.fsm.Iter,
			State:         string(rc.fsm.State),
			FailureMode:   string(rc.fsm.Reason), // best mapping we have post-routing
			FailureReason: string(rc.fsm.Reason),
		}
		if _, hErr := hooks.Run(ctx, hooks.GlobalPath(rc.repo, "failure"), env, nil, nil, nil); hErr != nil {
			logger.WarnContext(ctx, "failure hook error", "err", hErr)
		}
	}
	return rc.fsm.Outcome, nil
}

// loadAndPrepareFSM loads the persisted FSM and applies the pre-run
// adjustments Run needs before beginning a run: --fresh resets to a Fresh()
// state; a graceful done{*} terminal is auto-reset (prior-run history, not
// in-flight state); review-mode fields are stamped on; and a pre-existing
// failed{*} terminal is refused with ErrTerminalState. --fresh is handled
// first so it composes with ReviewMode (review --fresh re-enters review mode
// from a fresh FSM). Resets are saved immediately so an early crash doesn't
// leave stale terminal state on disk. Notices are written to errOut. This is
// the pure FSM-preparation block — it holds no resources, so callers keep the
// lock/log/summary acquisition defers in Run.
func loadAndPrepareFSM(opts Options, errOut io.Writer) (*fsm.FSM, error) {
	f, err := fsm.Load(opts.Repo)
	if err != nil {
		return nil, fmt.Errorf("loop: load fsm: %w", err)
	}
	switch {
	case opts.Fresh:
		f = fsm.Fresh()
		if serr := f.Save(opts.Repo); serr != nil {
			return nil, fmt.Errorf("loop: reset fsm: %w", serr)
		}
	case f.State == fsm.StateDone:
		// Auto-reset so the next `ralph run` just works; notice on stderr
		// keeps the transition visible. failed{*} still falls through to
		// the explicit refusal below.
		_, _ = fmt.Fprintf(errOut, "ralph: prior run ended at %s; resetting fsm.json\n", f.Outcome)
		f = fsm.Fresh()
		if serr := f.Save(opts.Repo); serr != nil {
			return nil, fmt.Errorf("loop: reset fsm: %w", serr)
		}
	}
	if opts.ReviewMode {
		f.ReviewMode = true
		f.ReviewBranch = opts.ReviewBranch
		f.ReviewBase = opts.ReviewBase
	}
	if f.State.Terminal() {
		_, _ = fmt.Fprintf(errOut,
			"ralph: fsm is in terminal state %s; pass --fresh to reset, or inspect .ralph/state/fsm.json\n",
			f.Outcome)
		return nil, ErrTerminalState
	}
	return f, nil
}

// handleStartState routes out of the virtual start state. No runner,
// no hooks, no prompt — just one SelectNextState call.
func handleStartState(ctx context.Context, rc *runContext) error {
	prev := rc.fsm.Outcome
	next, err := fsm.SelectNextState(ctx, fsm.RouteInput{
		FSM:  rc.fsm,
		Cfg:  rc.cfg,
		BD:   rc.bdClient,
		Repo: rc.repo,
	})
	if err != nil {
		return fmt.Errorf("loop: start route: %w", err)
	}
	rc.fsm.ObserveTransition(next.State)
	rc.fsm.Outcome = next
	if err := rc.fsm.Save(rc.repo); err != nil {
		return fmt.Errorf("loop: start save: %w", err)
	}
	now := rc.clock.Now().UTC()
	if err := rc.run.AppendTransition(runs.Transition{
		TS:     now,
		Iter:   rc.fsm.Iter,
		From:   string(prev.State),
		To:     string(next.State),
		Reason: string(next.Reason),
	}); err != nil {
		rc.log.ErrorContext(ctx, "start: append transition", "err", err)
	}
	return nil
}

// openOrBeginRun returns the runs.Run the loop will write to. When the
// FSM is mid-loop (non-terminal and not at the virtual start state) and
// the latest run on disk has no ExitOutcome stamped, that run is reused
// so an FSM journey spanning a --once exit or a crashed orchestrator
// stays in a single runs/<id>/ directory. Otherwise a new run begins.
func openOrBeginRun(repo string, f *fsm.FSM, logger *slog.Logger) (*runs.Run, error) {
	if f.State != fsm.StateStart && !f.State.Terminal() {
		latest, err := runs.Latest(repo)
		switch {
		case err == nil:
			m, merr := latest.ReadManifest()
			if merr == nil && m.ExitOutcome == nil {
				logger.Info("runs: reusing latest open run for mid-loop FSM",
					"id", latest.ID(), "fsm_state", f.State)
				return latest, nil
			}
		case errors.Is(err, runs.ErrNoRuns):
			// Mid-loop FSM but no prior run dir — fall through to Begin.
		default:
			return nil, fmt.Errorf("loop: latest run: %w", err)
		}
	}
	r, err := runs.Begin(repo)
	if err != nil {
		return nil, fmt.Errorf("loop: begin run: %w", err)
	}
	return r, nil
}

// validateOptions enforces the byob-input-validation.5 contract:
// Options gets vetted before any side effect (lock, run dir, hooks).
func validateOptions(opts *Options) error {
	if opts == nil {
		return errors.New("loop: nil Options")
	}
	if opts.Repo == "" {
		return errors.New("loop: Options.Repo is required")
	}
	if !filepath.IsAbs(opts.Repo) {
		return fmt.Errorf("loop: Options.Repo must be absolute, got %q", opts.Repo)
	}
	if info, err := os.Stat(opts.Repo); err != nil {
		return fmt.Errorf("loop: Options.Repo: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("loop: Options.Repo is not a directory: %s", opts.Repo)
	}
	if opts.Cfg == nil {
		return errors.New("loop: Options.Cfg is required")
	}
	if opts.IO == nil {
		return errors.New("loop: Options.IO is required")
	}
	if opts.ReviewMode {
		if opts.ReviewBranch == "" {
			return errors.New("loop: ReviewMode requires ReviewBranch")
		}
		if opts.ReviewBranch == opts.ReviewBase {
			return fmt.Errorf("loop: ReviewBranch %q must differ from ReviewBase", opts.ReviewBranch)
		}
	}
	return nil
}
