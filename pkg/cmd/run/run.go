// Package run implements `ralph run`, the normal-mode loop entry point.
// The cobra command is a thin wrapper: load config, apply CLI overrides,
// hand off to loop.Run, map the terminal Outcome to a process exit code.
// Locking, logging, fsm load, runs.Begin/Finalize, and the failure hook
// are all owned by loop.Run.
package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/internal/ralphcmd/build"
	"github.com/jcrussell/ralph/internal/tui"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	Once         bool
	SkipGate     bool
	DryRun       bool
	Fresh        bool
	WaitOnQuota  bool
	WaitForBeads bool
	NoTUI        bool
	Unlimited    bool
	Label        string

	MaxIterations  int    // 0 = use config
	SessionTimeout int    // seconds; 0 = use config
	MemoryLimit    string // "" = use config
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if o.MaxIterations < 0 {
		return cmdutil.FlagErrorf("--max-iterations must be >= 0, got %d", o.MaxIterations)
	}
	if o.Unlimited && o.MaxIterations > 0 {
		return cmdutil.FlagErrorf("--unlimited conflicts with --max-iterations=%d (use one or the other)", o.MaxIterations)
	}
	if o.SessionTimeout < 0 {
		return cmdutil.FlagErrorf("--timeout must be >= 0, got %d", o.SessionTimeout)
	}
	if _, err := config.ParseBytes(o.MemoryLimit); err != nil {
		return cmdutil.FlagErrorf("--memory: %v", err)
	}
	return nil
}

// NewCmdRun returns the cobra command for `ralph run`.
func NewCmdRun(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the ralph loop in normal mode",
		Long: `run drives the FSM-orchestrated loop against the current repo's
bd queue. The loop acquires .ralph/state/pid.lock, opens the JSONL
summary writer and orchestrator log, restores fsm.json (or starts
fresh), then runs iterations until a terminal outcome — done{*} on
graceful exit or failed{*} on budget exhaustion / auth / runner-
terminal failure. The user-facing iteration narrative is written by
the loop itself; this command adds no chatter of its own.

Display: on an interactive terminal (both stdin and stderr are TTYs)
run shows a live UI — a metrics panel (iteration, FSM state, cost vs
budget, elapsed, last gate result) above a scrollable log pane fed by
the loop's output. The panel appears immediately, seeded with the
configured caps, and updates each iteration. Keys: up/down scroll, e or
tab expand/collapse the log pane, q or Ctrl-C quit (mid-run this cancels
the loop, then waits for it to unwind). When the run finishes the UI
stays up so you can scroll back through the logs and review what
happened; press q to exit. Off a TTY (CI, pipes, redirects) or with
--no-tui, run streams the one-line-per-iteration narrative instead; that
path is byte-identical to the pre-TUI behavior. Either way the artifact
files under .ralph/ (summary.jsonl, the orchestrator log) stay the
durable data sink — the TUI is a view, not a record.

Exit codes: 0 on done{*}; 1 on failed{*}; non-zero infrastructure
errors (lock contention, disk full, malformed config) print to stderr
and exit 1.`,
		Example: `  # normal autonomous run, using all .ralph/config.toml defaults
  ralph run

  # one iteration only, useful for smoke-testing prompts and hooks
  ralph run --once

  # render prompts and route states but don't invoke the runner
  ralph run --once --dry-run

  # cap iterations at 5 for this invocation without editing config.toml
  ralph run --max-iterations=5

  # run with no iteration cap (equivalent to [loop] max_iterations = 0)
  ralph run --unlimited

  # unattended: don't exit when the queue drains, wait for new beads
  ralph run --wait-for-beads

  # disable the live TUI and stream the iteration narrative instead
  ralph run --no-tui`,
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return runRun(c.Context(), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.Once, "once", false, "run one iteration then exit")
	cmd.Flags().BoolVar(&opts.SkipGate, "skip-gate", false, "skip the per-state gate hook")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "render prompts and route states without invoking the runner")
	cmd.Flags().BoolVar(&opts.Fresh, "fresh", false, "reset fsm.json before starting (required after failed{*}; done{*} auto-resets)")
	cmd.Flags().BoolVar(&opts.WaitOnQuota, "wait-on-quota", false, "on a runner quota cap, sleep until it resets and resume instead of exiting failed (overrides [loop] wait_on_quota)")
	cmd.Flags().BoolVar(&opts.WaitForBeads, "wait-for-beads", false, "on done{idle} or done{queue_empty}, park polling `bd ready` and resume when the queue changes instead of exiting (overrides [loop] wait_for_beads)")
	cmd.Flags().BoolVar(&opts.NoTUI, "no-tui", false, "disable the live terminal UI; stream the one-line-per-iteration narrative instead (auto-disabled off a TTY)")
	cmd.Flags().StringVar(&opts.Label, "label", "", "iteration label recorded in summary.jsonl")
	cmd.Flags().IntVar(&opts.MaxIterations, "max-iterations", 0, "override [loop] max_iterations (0 = use config; pass --unlimited to disable the cap)")
	cmd.Flags().BoolVar(&opts.Unlimited, "unlimited", false, "disable the iteration cap for this invocation (sets [loop] max_iterations = 0); not recommended without a budget cap")
	cmd.Flags().IntVar(&opts.SessionTimeout, "timeout", 0, "override [loop] session_timeout_secs (0 = config)")
	cmd.Flags().StringVar(&opts.MemoryLimit, "memory", "", "override [loop] memory_limit_bytes ('' = config)")
	return cmd
}

func runRun(ctx context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	cfg, err := opts.F.Config()
	if err != nil {
		return err
	}
	applyOverrides(cfg, opts)

	// Refuse a second concurrent orchestrator before anything visible happens.
	// loop.Run would refuse too, but only after the TUI has taken over the
	// terminal — and the TUI can't show the reason until the operator quits.
	if err := cmdutil.CheckRepoFree(repo); err != nil {
		return err
	}

	lopts := loop.Options{
		Repo:     repo,
		Cfg:      cfg,
		Label:    opts.Label,
		Once:     opts.Once,
		SkipGate: opts.SkipGate,
		DryRun:   opts.DryRun,
		Fresh:    opts.Fresh,
	}

	if shouldUseTUI(opts.F.IOStreams, opts) {
		// Seed the metrics panel with the configured caps so it renders
		// immediately, before the loop's first iteration produces a Snapshot.
		// These caps match what notifyObserver copies, so the first real
		// metricsMsg overwrites the seed seamlessly.
		seed := loop.Snapshot{
			MaxIterations:    cfg.Loop.MaxIterations,
			MaxCostUSD:       cfg.Budget.MaxCostUSD,
			MaxWallclockSecs: cfg.Budget.MaxWallclockSecs,
			ReadyBeads:       -1, // overwritten by the sample below, or shown as "… ready"
		}
		// Seed the header's ready count before the first iteration (which can
		// take minutes). Build the bd client exactly as the loop does
		// (loop.go) so the configured exclude_types filter is honored, matching
		// the loop's own per-iteration ready-queue query.
		bdc := bd.New("", repo, bd.WithExcludeTypes(cfg.Beads.ExcludeTypes...))
		if n, err := fsm.BDReadyCount(ctx, bdc, ""); err == nil {
			seed.ReadyBeads = n
		}
		hdr := tui.Header{Version: build.Info().Version, Dir: repo}
		// Bound the log pane's scrollback from config. The byte size was already
		// validated at load (Config.Validate), so a parse error here is not
		// expected; fall back to the default cap rather than fail the run.
		caps := tui.DefaultLogCaps()
		caps.MaxLines = cfg.UI.LogScrollbackLines
		if b, err := config.ParseBytes(cfg.UI.LogScrollbackBytes); err == nil && b > 0 {
			caps.MaxBytes = int(b)
		}
		ui := tui.New(opts.F.IOStreams, seed, hdr, caps)
		return orchestrate(ctx, ui, loop.Run, lopts, opts.F.IOStreams.ErrOut)
	}

	// Non-TTY / --no-tui: the original synchronous path. loop.Run streams the
	// one-line-per-iteration narrative straight to the operator streams, and
	// the result maps byte-identically to the pre-TUI behavior.
	lopts.IO = opts.F.IOStreams
	return mapLoopResult(loop.Run(ctx, lopts))
}

// shouldUseTUI is the activation gate (ralph-g3s.1): the live UI auto-enables
// only when BOTH stdin and stderr are TTYs and --no-tui was not passed. Off a
// TTY (CI, pipes, redirects) or when explicitly disabled, run stays on the
// synchronous narrative path.
func shouldUseTUI(io *iostreams.IOStreams, opts *Options) bool {
	return !opts.NoTUI && io.IsStdinTTY() && io.IsStderrTTY()
}

// liveUI is the narrow consumer seam the activation path drives. *tui.Program
// satisfies it; orchestrate is unit-tested against a fake, so the
// control-inversion dance needs no real terminal. LoopIO/Observer feed
// loop.Run; Run renders in the foreground until quit; Finish signals loop
// completion; Tail surfaces the captured pane lines for the post-quit flush.
type liveUI interface {
	LoopIO() *iostreams.IOStreams
	Observer() loop.Observer
	Run() error
	Finish(err error, observed bool)
	Tail() []string
}

// observedObserver forwards Snapshots to the UI and records whether the loop
// ever got as far as an iteration. orchestrate uses that to tell a startup
// failure (nothing on screen, tear down and print) from a mid-run error (keep
// the run up for review). Observe must not block (loop seams.go) and this adds
// only an atomic store.
type observedObserver struct {
	inner loop.Observer
	saw   *atomic.Bool
}

func (o observedObserver) Observe(s loop.Snapshot) {
	if s.Iter > 0 {
		o.saw.Store(true)
	}
	o.inner.Observe(s)
}

// loopFn is loop.Run's signature, injected into orchestrate so tests stand in
// a fake loop (immediate return, ctx-blocking, or panicking) without acquiring
// the repo lock or touching disk.
type loopFn func(context.Context, loop.Options) (fsm.Outcome, error)

// runResult is the loop goroutine's outcome, delivered over a buffered channel
// so the goroutine never blocks on the orchestration reading it.
type runResult struct {
	outcome fsm.Outcome
	err     error
}

// orchestrate inverts control for the live-UI path (ralph-g3s.1): loop.Run
// runs in a goroutine while the TUI renders in the foreground. The ordering is
// quit -> cancel -> wait: the UI returns only when the user quits (q/Ctrl-C) —
// a finished run stays on screen for review until then — so we cancel the
// context (a no-op if the loop already returned), wait for the loop to unwind
// and report, then map the exit code. The loop's IO and Observer come from the
// UI so its output streams into the pane.
func orchestrate(ctx context.Context, ui liveUI, run loopFn, lopts loop.Options, realErr io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var observed atomic.Bool
	lopts.IO = ui.LoopIO()
	lopts.Observer = observedObserver{inner: ui.Observer(), saw: &observed}

	resultCh := make(chan runResult, 1)
	go func() {
		// Finish always fires exactly once (LIFO defer, after recover) so the
		// UI unblocks even if loop.Run panics; otherwise Run would hang on a
		// dead loop. finErr is set by both the normal and the panic path so the
		// badge reports the real reason either way.
		var finErr error
		defer func() { ui.Finish(finErr, observed.Load()) }()
		defer func() {
			if r := recover(); r != nil {
				finErr = fmt.Errorf("ralph run: loop panicked: %v", r)
				resultCh <- runResult{err: finErr}
			}
		}()
		out, err := run(ctx, lopts)
		finErr = err
		resultCh <- runResult{outcome: out, err: err}
	}()

	uiErr := ui.Run()
	cancel()          // quit -> cancel: unwind a still-running loop
	res := <-resultCh // -> wait: never exit mid-iteration

	// The TUI renders inline and clears its frame on quit; an early return or
	// panic can race the first paint, so its notices may never have shown.
	// Re-emit the captured pane tail to the real stderr on any error path so
	// they survive teardown. A clean run's final frame stays on screen, so we
	// don't duplicate it there.
	if uiErr != nil || res.err != nil {
		for _, line := range ui.Tail() {
			_, _ = fmt.Fprintln(realErr, line)
		}
	}
	if uiErr != nil {
		return uiErr
	}
	return mapLoopResult(res.outcome, res.err)
}

// mapLoopResult folds a loop.Run outcome+error into the command's return: the
// ErrTerminalState refusal becomes a silent non-zero exit (its notice already
// reached ErrOut), other errors propagate, and a terminal failed{*} maps to
// its exit code. Shared by the synchronous and TUI paths so both exit
// identically.
func mapLoopResult(out fsm.Outcome, err error) error {
	if err != nil {
		if errors.Is(err, loop.ErrTerminalState) {
			return cmdutil.ErrSilent
		}
		return err
	}
	if code := out.ExitCode(); code != 0 {
		return &cmdutil.ExitCodeError{Code: code}
	}
	return nil
}

// applyOverrides folds non-zero CLI flag values onto cfg. Zero/empty
// values preserve the value layered in by config.Load.
func applyOverrides(cfg *config.Config, opts *Options) {
	if opts.MaxIterations > 0 {
		cfg.Loop.MaxIterations = opts.MaxIterations
	}
	// --unlimited forces the cap off via the established 0 sentinel. Validate
	// rejects combining it with --max-iterations>0, so the branches never race.
	if opts.Unlimited {
		cfg.Loop.MaxIterations = 0
	}
	if opts.SessionTimeout > 0 {
		cfg.Loop.SessionTimeoutSecs = opts.SessionTimeout
	}
	if opts.MemoryLimit != "" {
		cfg.Loop.MemoryLimit = opts.MemoryLimit
	}
	// Boolean override is enable-only: the flag can turn wait-on-quota on
	// for one invocation, but disabling is config-only (a false flag is
	// indistinguishable from unset, so it must not clobber a true config).
	if opts.WaitOnQuota {
		cfg.Loop.WaitOnQuota = true
	}
	if opts.WaitForBeads {
		cfg.Loop.WaitForBeads = true
	}
}
