// Package timeline implements `ralph timeline`: chronological state
// transitions with one-line iteration narratives. Reads the latest
// run's transitions.jsonl and joins narratives from summary.jsonl on
// the iter number.
package timeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/fsm"
	ralphlog "github.com/jcrussell/ralph/internal/log"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	Since  string // duration ("1h") or RFC3339; empty = all
	State  string // filter rows whose From==state OR To==state
	Reason string // filter rows whose Reason==reason

	// Exporter is non-nil when --json is set; it renders the filtered
	// transitions as a JSON array, optionally post-filtered by --jq/--template.
	Exporter cmdutil.Exporter
}

// timelineFields is the --json field allowlist, derived from Transition's tags.
var timelineFields = cmdutil.JSONFieldNames(runs.Transition{})

// transitionRow adapts runs.Transition to cmdutil.RowExporter (a method can't
// be defined on a type from another package).
type transitionRow struct{ runs.Transition }

func (r transitionRow) ExportData(fields []string) map[string]any {
	return cmdutil.StructFields(r.Transition, fields)
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if o.Since != "" {
		if _, err := cmdutil.ParseSince(o.Since); err != nil {
			return cmdutil.FlagErrorf("--since: %v", err)
		}
	}
	return nil
}

// NewCmdTimeline returns the cobra command for `ralph timeline`.
func NewCmdTimeline(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "timeline",
		Short: "Chronological state transitions with narrative",
		Long: `Reads .ralph/state/runs/<latest>/transitions.jsonl and prints one
line per FSM transition, joined with the narrative from summary.jsonl
on iter number.

Use 'ralph timeline' when you want the chronological FSM view; use
'ralph logs' when you want only the iteration narrative; use 'ralph
status' for the single-screen dashboard. --since takes a Go duration
(1h, 30m) or an RFC3339 timestamp; --state and --reason filter rows.

--json with a comma-separated field list emits the filtered transitions as
a JSON array; --jq filters that array with a built-in jq engine (no external
jq needed) and --template formats it with a Go template.`,
		Example: `  # all transitions for the latest run
  ralph timeline

  # only the last hour
  ralph timeline --since=1h

  # only failed-state transitions
  ralph timeline --state=failed

  # JSON array of transitions
  ralph timeline --json iter,ts,from,to,reason

  # built-in jq: only transitions into dirty
  ralph timeline --json to,iter --jq '.[] | select(.to=="dirty")'`,
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return run(c.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Since, "since", "", "duration (e.g. 1h) or RFC3339 timestamp; empty = all")
	cmd.Flags().StringVar(&opts.State, "state", "", "filter rows whose from or to matches this state")
	cmd.Flags().StringVar(&opts.Reason, "reason", "", "filter rows whose reason matches this string")
	cmdutil.AddJSONFlags(cmd, &opts.Exporter, timelineFields)
	cmdutil.MustRegisterFlagCompletion(cmd, "state",
		cobra.FixedCompletions(fsm.AllStateNames(), cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func run(_ context.Context, opts *Options) error {
	p, err := opts.F.Paths()
	if err != nil {
		return err
	}

	r, err := runs.Latest(p.RepoRoot())
	if err != nil {
		if errors.Is(err, runs.ErrNoRuns) {
			_, _ = fmt.Fprintln(opts.F.IOStreams.ErrOut, "timeline: no runs found")
			return nil
		}
		return fmt.Errorf("timeline: open latest run: %w", err)
	}

	transitions, err := r.ReadTransitions()
	if err != nil {
		return fmt.Errorf("timeline: read transitions: %w", err)
	}

	since := time.Time{}
	if opts.Since != "" {
		since, _ = cmdutil.ParseSince(opts.Since)
	}

	var matched []runs.Transition
	for _, t := range transitions {
		if !since.IsZero() && t.TS.Before(since) {
			continue
		}
		if opts.State != "" && t.From != opts.State && t.To != opts.State {
			continue
		}
		if opts.Reason != "" && t.Reason != opts.Reason {
			continue
		}
		matched = append(matched, t)
	}

	if opts.Exporter != nil {
		rows := make([]transitionRow, len(matched))
		for i, t := range matched {
			rows[i] = transitionRow{t}
		}
		return cmdutil.WriteRows(opts.F.IOStreams, opts.Exporter, rows)
	}

	narrByIter, _ := loadNarratives(p.Summary())
	for _, t := range matched {
		_, _ = fmt.Fprintln(opts.F.IOStreams.Out, formatLine(t, narrByIter[t.Iter]))
	}
	return nil
}

// loadNarratives builds an iter→narrative map from summary.jsonl. A
// missing file returns an empty map and no error — narrative is
// optional context.
func loadNarratives(path string) (map[int]string, error) {
	f, err := os.Open(path) //nolint:gosec // path joined from RepoRoot()
	if errors.Is(err, fs.ErrNotExist) {
		return map[int]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("timeline: open summary: %w", err)
	}
	defer func() { _ = f.Close() }()

	out := map[int]string{}
	sc := ralphlog.NewSummaryScanner(f)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		rec, _, err := loop.DecodeRecord(line)
		if err != nil {
			continue // tolerate malformed lines; observability commands always do
		}
		if rec.Iter > 0 {
			out[rec.Iter] = rec.Narrative
		}
	}
	return out, sc.Err()
}

// formatLine renders one transition row.
//
//	2026-05-14T12:00:42Z  iter 0042  clean → clean    claimed angr, 2 commits, gate green
//
// Reason, when set, appears in brackets after the to-state.
func formatLine(t runs.Transition, narr string) string {
	head := fmt.Sprintf("%s  iter %04d  %s → %s", t.TS.UTC().Format(time.RFC3339), t.Iter, t.From, t.To)
	if t.Reason != "" {
		head += " [" + t.Reason + "]"
	}
	if narr != "" {
		return head + "    " + narr
	}
	return head
}
