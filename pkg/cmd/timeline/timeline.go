// Package timeline implements `ralph timeline`: chronological state
// transitions with one-line iteration narratives. Reads the latest
// run's transitions.jsonl and joins narratives from summary.jsonl on
// the iter number.
package timeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

const summaryRel = ".ralph/state/logs/summary.jsonl"

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	Since  string // duration ("1h") or RFC3339; empty = all
	State  string // filter rows whose From==state OR To==state
	Reason string // filter rows whose Reason==reason
	JSON   bool   // emit raw transition JSONL
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if o.Since != "" {
		if _, err := parseSince(o.Since); err != nil {
			return cmdutil.FlagErrorf("--since: %v", err)
		}
	}
	return nil
}

// NewCmdTimeline returns the cobra command for `ralph timeline`.
func NewCmdTimeline(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "timeline",
		Short: "Chronological state transitions with narrative",
		Long: `Reads .ralph/state/runs/<latest>/transitions.jsonl and prints one
line per FSM transition, joined with the narrative from summary.jsonl
on iter number.`,
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return run(c.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Since, "since", "", "duration (e.g. 1h) or RFC3339 timestamp; empty = all")
	cmd.Flags().StringVar(&opts.State, "state", "", "filter rows whose from or to matches this state")
	cmd.Flags().StringVar(&opts.Reason, "reason", "", "filter rows whose reason matches this string")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "emit raw transition JSONL")
	return cmd
}

func run(_ context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}

	r, err := runs.Latest(repo)
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
		since, _ = parseSince(opts.Since)
	}

	narrByIter, _ := loadNarratives(filepath.Join(repo, summaryRel))

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
		if opts.JSON {
			b, jerr := json.Marshal(t)
			if jerr != nil {
				return fmt.Errorf("timeline: marshal transition: %w", jerr)
			}
			_, _ = fmt.Fprintln(opts.F.IOStreams.Out, string(b))
			continue
		}
		_, _ = fmt.Fprintln(opts.F.IOStreams.Out, formatLine(t, narrByIter[t.Iter]))
	}
	return nil
}

// parseSince mirrors pkg/cmd/report.parseSince — duration first, then
// RFC3339. Kept here so the package has no cross-cmd dependency.
func parseSince(spec string) (time.Time, error) {
	if d, err := time.ParseDuration(spec); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, spec); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("not a duration or RFC3339 timestamp: %q", spec)
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
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Iter      int    `json:"iter"`
			Narrative string `json:"narrative"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
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
