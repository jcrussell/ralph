// Package run implements `ralph run`, the normal-mode loop entry point.
// The cobra command is a thin wrapper: load config, apply CLI overrides,
// hand off to loop.Run, map the terminal Outcome to a process exit code.
// Locking, logging, fsm load, runs.Begin/Finalize, and the failure hook
// are all owned by loop.Run.
package run

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	Once     bool
	SkipGate bool
	DryRun   bool
	Fresh    bool
	Label    string

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
  ralph run --max-iterations=5`,
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
	cmd.Flags().StringVar(&opts.Label, "label", "", "iteration label recorded in summary.jsonl")
	cmd.Flags().IntVar(&opts.MaxIterations, "max-iterations", 0, "override [loop] max_iterations (0 = config)")
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

	out, err := loop.Run(ctx, loop.Options{
		Repo:     repo,
		Cfg:      cfg,
		IO:       opts.F.IOStreams,
		Label:    opts.Label,
		Once:     opts.Once,
		SkipGate: opts.SkipGate,
		DryRun:   opts.DryRun,
		Fresh:    opts.Fresh,
	})
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
	if opts.SessionTimeout > 0 {
		cfg.Loop.SessionTimeoutSecs = opts.SessionTimeout
	}
	if opts.MemoryLimit != "" {
		cfg.Loop.MemoryLimit = opts.MemoryLimit
	}
}
