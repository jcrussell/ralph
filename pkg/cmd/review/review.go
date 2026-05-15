// Package review implements `ralph review`, the review-mode loop entry
// point. The cobra command is a thin wrapper: optionally `gh pr checkout
// N`, resolve branch+base via internal/review, then hand off to loop.Run
// with ReviewMode=true. The agent files merge:<branch> from the review
// prompt; the orchestrator exits via done{queue_empty} once the review
// queue drains.
package review

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/loop"
	reviewlib "github.com/jcrussell/ralph/internal/review"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	Branch    string
	Base      string
	PR        int
	NoLabel   bool
	MaxRounds int

	Once     bool
	SkipGate bool
	DryRun   bool
	Fresh    bool
	Label    string
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if o.PR < 0 {
		return cmdutil.FlagErrorf("--pr must be >= 0, got %d", o.PR)
	}
	if o.MaxRounds < 0 {
		return cmdutil.FlagErrorf("--max-rounds must be >= 0, got %d", o.MaxRounds)
	}
	return nil
}

// NewCmdReview returns the cobra command for `ralph review`.
func NewCmdReview(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run the ralph loop in review mode against a branch",
		Long: `review drives the FSM-orchestrated loop with review_mode=true:
the loop suppresses auto-revert (would silently destroy work) and
routes via the review state, whose prompt has the agent ingest diff
findings against the base, claim and fix them one at a time, and
file a merge:<branch> bead when the queue is empty. The orchestrator
exits via done{queue_empty} once review:<branch> is drained and the
working tree is clean.

Branch resolution: --branch wins; else the current checkout; HEAD
detached without --branch is an error. Base resolution: --base wins;
else [review] base_branch from config; else "main". Reviewing the
base against itself is rejected.

--pr N is sugar for "gh pr checkout N" run before resolution; gh is
not a hard dependency of ralph itself.`,
		Example: `  # review the currently checked-out branch against [review] base_branch
  ralph review

  # review feat/foo against main, one iteration only
  ralph review --branch=feat/foo --base=main --once

  # check out PR 123 first, then review against [review] base_branch
  ralph review --pr=123

  # cap iterations at 10 for this run
  ralph review --max-rounds=10`,
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return runReview(c.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "branch under review (default: current)")
	cmd.Flags().StringVar(&opts.Base, "base", "", "base branch (default: [review] base_branch)")
	cmd.Flags().IntVar(&opts.PR, "pr", 0, "run `gh pr checkout N` before resolving the branch")
	cmd.Flags().BoolVar(&opts.NoLabel, "no-label", false, "do not auto-label iterations review:<branch>")
	cmd.Flags().IntVar(&opts.MaxRounds, "max-rounds", 0, "override [loop] max_iterations for this run")
	cmd.Flags().BoolVar(&opts.Once, "once", false, "run one iteration then exit")
	cmd.Flags().BoolVar(&opts.SkipGate, "skip-gate", false, "skip the per-state gate hook")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "render prompts and route states without invoking the runner")
	cmd.Flags().BoolVar(&opts.Fresh, "fresh", false, "reset fsm.json before starting (use when the prior run reached a terminal state)")
	cmd.Flags().StringVar(&opts.Label, "label", "", "explicit iteration label (overrides the review:<branch> default)")
	return cmd
}

func runReview(ctx context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	cfg, err := config.Load(repo)
	if err != nil {
		return err
	}
	if opts.MaxRounds > 0 {
		cfg.Loop.MaxIterations = opts.MaxRounds
	}

	if opts.PR != 0 {
		if cerr := reviewlib.CheckoutPR(ctx, repo, opts.PR); cerr != nil {
			return cerr
		}
	}

	baseFallback := opts.Base
	if baseFallback == "" {
		baseFallback = cfg.Review.BaseBranch
	}
	res, err := reviewlib.Resolve(ctx, repo, opts.Branch, baseFallback)
	if err != nil {
		return err
	}

	out, err := loop.Run(ctx, loop.Options{
		Repo:         repo,
		Cfg:          cfg,
		IO:           opts.F.IOStreams,
		ReviewMode:   true,
		ReviewBranch: res.Branch,
		ReviewBase:   res.Base,
		Label:        chooseLabel(opts, res.Branch),
		Once:         opts.Once,
		SkipGate:     opts.SkipGate,
		DryRun:       opts.DryRun,
		Fresh:        opts.Fresh,
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

// chooseLabel decides the iteration label:
//   - explicit --label wins (--no-label is ignored to match the
//     least-surprise principle: a value you typed beats a no-op flag);
//   - --no-label suppresses the auto-label;
//   - otherwise default to "review:<branch>" so summary.jsonl is
//     queryable by branch.
func chooseLabel(opts *Options, branch string) string {
	if opts.Label != "" {
		return opts.Label
	}
	if opts.NoLabel {
		return ""
	}
	return "review:" + branch
}
