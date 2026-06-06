// Package explore implements `ralph explore`: a read-only browser over
// .ralph/state. Interactive on a TTY (a Bubble Tea category → list →
// detail browser); off a TTY (or with --no-tui) it prints a plain
// listing so the command stays pipeable.
package explore

import (
	"context"

	"github.com/spf13/cobra"

	tuiexplore "github.com/jcrussell/ralph/internal/tui/explore"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F     *cmdutil.Factory
	NoTUI bool
}

// Validate enforces flag-value invariants before any side effects. There
// are none yet; the method is kept for shape consistency.
func (*Options) Validate() error { return nil }

// NewCmdExplore returns the cobra command for `ralph explore`.
func NewCmdExplore(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "explore",
		Short: "Browse runs, incidents, and iteration artifacts under .ralph/state",
		Long: `explore opens a read-only browser over .ralph/state with three
categories — Runs, Incidents, and Iterations — plus a Search entry. Pick a
category to list its items, then open an item to see its detail: a run's
transitions, an incident's report, or an iteration's full artifact dump.

It is interactive: arrow keys move, enter opens, esc goes back, and q quits
(with a y/N confirmation). tab/shift+tab switch category in a list and flip
between items in a detail view; / filters a list; the Search entry runs a
full-text search across everything, and / inside a detail finds within it
(n/N jump between matches); r re-reads .ralph/state. Off a TTY (or with
--no-tui) it prints a plain listing of the categories instead; for scripted
detail use 'ralph trace', 'ralph logs', or 'ralph status'.`,
		Example: `  # browse interactively
  ralph explore

  # plain listing (pipeable)
  ralph explore --no-tui`,
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return exploreRun(c.Context(), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.NoTUI, "no-tui", false,
		"print a plain listing instead of the interactive browser (auto-enabled off a TTY)")
	return cmd
}

func exploreRun(_ context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	p, err := opts.F.Paths()
	if err != nil {
		return err
	}
	io := opts.F.IOStreams

	// The browser renders to stdout, so gate on the stdout (and stdin)
	// TTY. Without one, fall back to a plain, pipeable listing.
	if opts.NoTUI || !io.IsStdoutTTY() || !io.IsStdinTTY() {
		return tuiexplore.WriteListing(io.Out, repo, p)
	}
	return tuiexplore.New(io, repo, p).Run()
}
