// Package prompt implements `ralph prompt show <state>`: a render-only
// view of the per-state template, useful for previewing prompts during
// authoring.
package prompt

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/promptlib"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	State      string
	Iter       int
	GitHead    string
	GitDirty   bool
	GateResult string
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
// State is set from args[0] in RunE before Validate runs; start/done/
// failed are rejected because they have no prompt template.
func (o *Options) Validate() error {
	s := fsm.State(o.State)
	if !s.Valid() {
		return cmdutil.FlagErrorf("unknown state %q; valid: clean, dirty, revert, review", o.State)
	}
	switch s {
	case fsm.StateClean, fsm.StateDirty, fsm.StateRevert, fsm.StateReview:
		return nil
	}
	return cmdutil.FlagErrorf("state %q has no prompt; valid: clean, dirty, revert, review", o.State)
}

// NewCmdPrompt returns the `ralph prompt` command tree.
func NewCmdPrompt(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Inspect ralph prompts",
	}
	cmd.AddCommand(newCmdShow(f, runF))
	return cmd
}

func newCmdShow(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "show <state>",
		Short: "Render the prompt for <state> without running anything",
		Long: `Renders prompts/<state>.md from the current repo's .ralph/ directory,
wrapped with prompts/_header.md and prompts/_footer.md when present.
<state> must be one of: clean, dirty, revert, review.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.State = args[0]
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return showRun(c.Context(), opts)
		},
	}
	cmd.Flags().IntVar(&opts.Iter, "iter", 0, "value for .Iter")
	cmd.Flags().StringVar(&opts.GitHead, "git-head", "", "value for .GitHead")
	cmd.Flags().BoolVar(&opts.GitDirty, "git-dirty", false, "value for .GitDirty")
	cmd.Flags().StringVar(&opts.GateResult, "gate-result", "not-run", "value for .GateResult (passed|failed|not-run)")
	return cmd
}

func showRun(_ context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	root, err := promptlib.Open(repo)
	if err != nil {
		return fmt.Errorf("open prompts: %w", err)
	}
	defer func() { _ = root.Close() }()
	vars := promptlib.Vars{
		Iter:       opts.Iter,
		State:      opts.State,
		GitHead:    opts.GitHead,
		GitDirty:   opts.GitDirty,
		RepoRoot:   repo,
		GateResult: opts.GateResult,
	}
	out, err := promptlib.Render(root.FS(), opts.State, vars)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(opts.F.IOStreams.Out, out)
	if len(out) == 0 || out[len(out)-1] != '\n' {
		_, _ = fmt.Fprintln(opts.F.IOStreams.Out)
	}
	return nil
}
