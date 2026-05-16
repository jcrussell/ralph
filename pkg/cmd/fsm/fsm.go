// Package fsm implements `ralph fsm`, the FSM introspection command.
// Two subcommands:
//
//   - show — pretty-print state/fsm.json (default if no subcommand).
//     --json emits the raw file.
//   - graph — render the topology as a Mermaid stateDiagram with edge
//     counts pulled from transitions.jsonl. --run picks the source run
//     (default latest); --no-counts omits the tallies.
//
// Mermaid is the only output format (KISS).
package fsm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	corefsm "github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// NewCmdFSM returns the `ralph fsm` command with its two subcommands.
func NewCmdFSM(f *cmdutil.Factory, runShowF, runGraphF func(context.Context, *Options) error) *cobra.Command {
	showCmd := newShow(f, runShowF)
	graphCmd := newGraph(f, runGraphF)
	cmd := &cobra.Command{
		Use:   "fsm",
		Short: "Inspect the orchestrator state machine",
		Long: `fsm has two subcommands:

  ralph fsm show    pretty-print .ralph/state/fsm.json (default)
  ralph fsm graph   render the FSM topology as a Mermaid diagram,
                    with edge counts tallied from transitions.jsonl

Without a subcommand, "show" runs.`,
		RunE: showCmd.RunE,
	}
	cmd.AddCommand(showCmd, graphCmd)
	return cmd
}

// Options carries the flag values from both subcommands so tests can
// drive each without going through cobra parsing.
type Options struct {
	F *cmdutil.Factory

	// show
	JSON bool

	// graph
	Run      string // "latest" (default) | "all" | "<id>"
	NoCounts bool
}

func newShow(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Pretty-print .ralph/state/fsm.json",
		Long: `show reads .ralph/state/fsm.json — the orchestrator's persisted
state — and prints it as indented JSON. Use this when you want the
full record (every counter, every cumulative total); use 'ralph
status' instead when you just want the dashboard view.

--json is a no-op for show: the output is JSON either way. The
flag is kept for consistency with other commands that gate JSON
output.`,
		Example: `  # pretty-print fsm.json
  ralph fsm show

  # pipe to jq for ad-hoc queries
  ralph fsm show | jq '.cumulative_cost_usd, .iter'`,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return runShow(c.Context(), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "emit raw fsm.json")
	return cmd
}

func newGraph(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f, Run: "latest"}
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Render the FSM topology as a Mermaid diagram with edge counts",
		Long: `graph prints a Mermaid stateDiagram with every state defined in
internal/fsm and every transition observed in transitions.jsonl.

Edge labels carry the observed count; the current state (from
fsm.json) is highlighted with the "current" classDef.

--run selects the source for edge counts:
  latest  the most recent run (default)
  all     aggregate every run under state/runs/
  <id>    a specific run id (e.g. 20260513T180405Z)

Use --no-counts to render bare topology.`,
		Example: `  # default: latest run, with counts
  ralph fsm graph

  # all runs aggregated
  ralph fsm graph --run=all

  # bare topology
  ralph fsm graph --no-counts`,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return runGraph(c.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Run, "run", "latest", "source run for edge counts: latest|all|<id>")
	cmd.Flags().BoolVar(&opts.NoCounts, "no-counts", false, "omit edge counts")
	cmdutil.MustRegisterFlagCompletion(cmd, "run", completeRunSpec(f))
	return cmd
}

// completeRunSpec offers "latest", "all", and every run id under
// .ralph/state/runs/. A missing or unreadable runs dir falls back to the
// two static spec keywords.
func completeRunSpec(f *cmdutil.Factory) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		out := []string{"latest", "all"}
		repo, err := f.RepoRoot()
		if err != nil {
			return out, cobra.ShellCompDirectiveNoFileComp
		}
		metas, err := runs.List(repo)
		if err != nil {
			return out, cobra.ShellCompDirectiveNoFileComp
		}
		for _, m := range metas {
			out = append(out, m.ID)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// runShow prints fsm.json or, with --json, the raw bytes.
func runShow(_ context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	state, err := corefsm.Load(repo)
	if err != nil {
		return err
	}
	io := opts.F.IOStreams
	enc := json.NewEncoder(io.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(state)
}

// runGraph renders the topology as Mermaid, optionally overlaying
// edge counts from transitions.jsonl.
func runGraph(_ context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	state, err := corefsm.Load(repo)
	if err != nil {
		return err
	}

	counts := map[edge]int{}
	if !opts.NoCounts {
		counts, err = loadCounts(repo, opts.Run)
		if err != nil {
			return err
		}
	}

	return writeMermaid(opts.F.IOStreams.Out, state, counts, opts.NoCounts)
}

// edge is a (from, to) pair used as a map key for tallying.
type edge struct{ From, To string }

// loadCounts reads transitions from the run(s) selected by spec and
// tallies edges. Returns an empty map when no run exists.
func loadCounts(repo, spec string) (map[edge]int, error) {
	counts := map[edge]int{}
	switch spec {
	case "all":
		metas, err := runs.List(repo)
		if err != nil {
			return nil, err
		}
		for _, m := range metas {
			r, err := runs.Open(repo, m.ID)
			if err != nil {
				return nil, err
			}
			if err := tallyOne(r, counts); err != nil {
				return nil, err
			}
		}
	case "latest", "":
		r, err := runs.Latest(repo)
		if err != nil {
			// No runs yet — bare topology is still useful.
			return counts, nil
		}
		if err := tallyOne(r, counts); err != nil {
			return nil, err
		}
	default:
		r, err := runs.Open(repo, spec)
		if err != nil {
			return nil, fmt.Errorf("fsm graph: %w", err)
		}
		if err := tallyOne(r, counts); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

func tallyOne(r *runs.Run, counts map[edge]int) error {
	ts, err := r.ReadTransitions()
	if err != nil {
		return err
	}
	for _, t := range ts {
		counts[edge{From: t.From, To: t.To}]++
	}
	return nil
}

// writeMermaid emits a Mermaid stateDiagram. All declared states get
// nodes; edges come from counts (or the canonical edge set when
// counts is empty so the diagram is still informative).
func writeMermaid(w io.Writer, state *corefsm.FSM, counts map[edge]int, bare bool) error {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, "```mermaid")
	fmt.Fprintln(&buf, "stateDiagram-v2")
	fmt.Fprintln(&buf, "  [*] --> start")

	// When we have edge counts, emit only what was observed (plus the
	// terminal arrows). When --no-counts is set or counts is empty,
	// emit the canonical edge set so the topology is visible.
	edges := sortedEdges(counts)
	if bare || len(edges) == 0 {
		for _, e := range canonicalEdges() {
			fmt.Fprintf(&buf, "  %s --> %s\n", e.From, e.To)
		}
	} else {
		for _, e := range edges {
			fmt.Fprintf(&buf, "  %s --> %s: %d\n", e.From, e.To, counts[e])
		}
	}

	fmt.Fprintln(&buf, "  done --> [*]")
	fmt.Fprintln(&buf, "  failed --> [*]")

	// Highlight the current state. classDef + class lines work in
	// Mermaid 10+ for stateDiagram-v2.
	fmt.Fprintln(&buf, "  classDef current fill:#88d,stroke:#225,color:#fff")
	if state != nil && state.State != "" {
		fmt.Fprintf(&buf, "  class %s current\n", state.State)
	}
	fmt.Fprintln(&buf, "```")
	_, err := w.Write(buf.Bytes())
	return err
}

// canonicalEdges is the fixed FSM topology — mirrors
// docs/concepts/state-machine.md. Used when no transitions have been
// observed yet, so the bare diagram is informative.
func canonicalEdges() []edge {
	return []edge{
		{"start", "clean"},
		{"start", "dirty"},
		{"start", "review"},
		{"start", "done"},
		{"clean", "clean"},
		{"clean", "dirty"},
		{"clean", "done"},
		{"clean", "failed"},
		{"dirty", "clean"},
		{"dirty", "dirty"},
		{"dirty", "revert"},
		{"dirty", "failed"},
		{"revert", "clean"},
		{"review", "review"},
		{"review", "done"},
		{"review", "failed"},
	}
}

// sortedEdges returns the keys of counts sorted (From, To) so output
// is deterministic.
func sortedEdges(counts map[edge]int) []edge {
	out := make([]edge, 0, len(counts))
	for e := range counts {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}
