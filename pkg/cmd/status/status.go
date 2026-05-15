// Package status implements `ralph status`, the single-screen dashboard
// over .ralph/state/fsm.json plus the latest run's transitions.jsonl.
package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F    *cmdutil.Factory
	JSON bool

	// Tail bounds how many recent transitions to render. Default 5.
	Tail int
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if o.Tail < 0 {
		return cmdutil.FlagErrorf("--tail must be >= 0, got %d", o.Tail)
	}
	return nil
}

// NewCmdStatus returns the cobra command for `ralph status`.
func NewCmdStatus(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f, Tail: 5}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show ralph's current FSM state, counters, and recent transitions",
		Long: `status reads .ralph/state/fsm.json plus the latest run's
transitions.jsonl and renders a single-screen dashboard:

  - current state (and reason for terminal states)
  - iteration counter vs. cap
  - review mode + branch/base
  - cumulative cost and wallclock against their caps
  - consecutive dirty streak
  - the last N transitions (default 5)

Pass --json to emit the same data in machine-readable form. The JSON
shape is stable enough to script against; treat unknown fields as
informational.`,
		Example: `  # human-readable (default)
  ralph status

  # JSON for scripts
  ralph status --json | jq '.state, .iter'

  # last 20 transitions
  ralph status --tail=20`,
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return runStatus(c.Context(), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON")
	cmd.Flags().IntVar(&opts.Tail, "tail", 5, "number of recent transitions to render")
	return cmd
}

// View is the data shape rendered by both --json and the table writer.
// Decoupling the view from the renderer keeps the table writer pure
// and the JSON output a one-liner.
type View struct {
	RunID                   string            `json:"run_id,omitempty"`
	State                   string            `json:"state"`
	Reason                  string            `json:"reason,omitempty"`
	Iter                    int               `json:"iter"`
	MaxIterations           int               `json:"max_iterations,omitempty"`
	ReviewMode              bool              `json:"review_mode"`
	ReviewBranch            string            `json:"review_branch,omitempty"`
	ReviewBase              string            `json:"review_base,omitempty"`
	CumulativeCostUSD       float64           `json:"cumulative_cost_usd"`
	MaxCostUSD              float64           `json:"max_cost_usd,omitempty"`
	CumulativeWallclockSecs int               `json:"cumulative_wallclock_secs"`
	MaxWallclockSecs        int               `json:"max_wallclock_secs,omitempty"`
	ConsecutiveDirty        int               `json:"consecutive_dirty"`
	LastGateResult          string            `json:"last_gate_result,omitempty"`
	Transitions             []runs.Transition `json:"transitions,omitempty"`
}

func runStatus(_ context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	cfg, err := config.Load(repo)
	if err != nil {
		return err
	}
	state, err := fsm.Load(repo)
	if err != nil {
		return err
	}

	v := buildView(state, cfg)

	if r, lerr := runs.Latest(repo); lerr == nil {
		v.RunID = r.ID()
		all, terr := r.ReadTransitions()
		if terr != nil {
			return terr
		}
		v.Transitions = tailN(all, opts.Tail)
	} else if !errors.Is(lerr, runs.ErrNoRuns) {
		return lerr
	}

	io := opts.F.IOStreams
	if opts.JSON {
		return writeJSON(io.Out, v)
	}
	return writeText(io.Out, v)
}

func buildView(f *fsm.FSM, cfg *config.Config) View {
	return View{
		State:                   string(f.State),
		Reason:                  string(f.Reason),
		Iter:                    f.Iter,
		MaxIterations:           cfg.Loop.MaxIterations,
		ReviewMode:              f.ReviewMode,
		ReviewBranch:            f.ReviewBranch,
		ReviewBase:              f.ReviewBase,
		CumulativeCostUSD:       f.CumulativeCostUSD,
		MaxCostUSD:              cfg.Budget.MaxCostUSD,
		CumulativeWallclockSecs: f.CumulativeWallclockSecs,
		MaxWallclockSecs:        cfg.Budget.MaxWallclockSecs,
		ConsecutiveDirty:        f.ConsecutiveDirty,
		LastGateResult:          f.LastGateResult,
	}
}

func tailN(all []runs.Transition, n int) []runs.Transition {
	if n <= 0 || len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}

func writeJSON(w io.Writer, v View) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeText(w io.Writer, v View) error {
	if v.RunID != "" {
		_, _ = fmt.Fprintf(w, "ralph status — run %s\n\n", v.RunID)
	} else {
		_, _ = fmt.Fprintf(w, "ralph status — no runs yet\n\n")
	}

	stateLine := v.State
	if v.Reason != "" {
		stateLine = fmt.Sprintf("%s{%s}", v.State, v.Reason)
	}
	_, _ = fmt.Fprintf(w, "state:             %s\n", stateLine)
	_, _ = fmt.Fprintf(w, "iter:              %s\n", iterLine(v.Iter, v.MaxIterations))
	_, _ = fmt.Fprintf(w, "review:            %s\n", reviewLine(v))
	_, _ = fmt.Fprintf(w, "cost:              %s\n", costLine(v.CumulativeCostUSD, v.MaxCostUSD))
	_, _ = fmt.Fprintf(w, "wallclock:         %s\n", wallclockLine(v.CumulativeWallclockSecs, v.MaxWallclockSecs))
	_, _ = fmt.Fprintf(w, "consecutive_dirty: %d\n", v.ConsecutiveDirty)
	if v.LastGateResult != "" {
		_, _ = fmt.Fprintf(w, "last_gate:         %s\n", v.LastGateResult)
	}

	if len(v.Transitions) > 0 {
		_, _ = fmt.Fprintf(w, "\nlast %d transitions:\n", len(v.Transitions))
		for _, t := range v.Transitions {
			reason := ""
			if t.Reason != "" {
				reason = "  reason=" + t.Reason
			}
			_, _ = fmt.Fprintf(w, "  %-4d %-7s → %-7s%s\n", t.Iter, t.From, t.To, reason)
		}
	}
	return nil
}

func iterLine(iter, cap int) string {
	if cap <= 0 {
		return fmt.Sprintf("%d / unlimited", iter)
	}
	return fmt.Sprintf("%d / %d", iter, cap)
}

func reviewLine(v View) string {
	if !v.ReviewMode {
		return "off"
	}
	if v.ReviewBranch != "" && v.ReviewBase != "" {
		return fmt.Sprintf("on (branch=%s, base=%s)", v.ReviewBranch, v.ReviewBase)
	}
	if v.ReviewBranch != "" {
		return "on (branch=" + v.ReviewBranch + ")"
	}
	return "on"
}

func costLine(cur, cap float64) string {
	if cap <= 0 {
		return fmt.Sprintf("$%.2f / unlimited", cur)
	}
	return fmt.Sprintf("$%.2f / $%.2f", cur, cap)
}

func wallclockLine(cur, cap int) string {
	if cap <= 0 {
		return fmt.Sprintf("%s / unlimited", fmtSecs(cur))
	}
	return fmt.Sprintf("%s / %s", fmtSecs(cur), fmtSecs(cap))
}

func fmtSecs(s int) string {
	if s == 0 {
		return "0s"
	}
	d := time.Duration(s) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := s % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, sec)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}
