// Package logs implements `ralph logs`: raw access to the JSONL
// iteration stream at .ralph/state/logs/summary.jsonl.
package logs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	Iter int  // 0 = all; >0 = single iter
	Tail bool // follow appends
	JSON bool // raw JSONL instead of pretty narrative

	// Test seam: override the poll cadence in --tail mode.
	pollInterval time.Duration
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
// Flag-relationship checks (e.g. --iter and --tail are mutually
// exclusive) live on the cobra.Command via MarkFlagsMutuallyExclusive
// — see NewCmdLogs.
func (o *Options) Validate() error {
	if o.Iter < 0 {
		return cmdutil.FlagErrorf("--iter must be non-negative")
	}
	return nil
}

// NewCmdLogs returns the cobra command for `ralph logs`.
func NewCmdLogs(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f, pollInterval: 200 * time.Millisecond}
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Stream the per-iteration log",
		Long: `Reads .ralph/state/logs/summary.jsonl.
Default: one narrative line per iteration.
--iter N selects a single record (pretty JSON).
--tail follows appends until interrupted.
--json emits raw JSONL.

Use 'ralph logs' when you want the per-iteration narrative stream;
use 'ralph timeline' when you want the FSM transition view joined
with narratives; use 'ralph trace <iter>' when you want every
captured artifact (prompt, stdout, stderr) for one iteration.`,
		Example: `  # one narrative line per iteration (default)
  ralph logs

  # follow a live run
  ralph logs --tail

  # pretty-print iter 42 in full
  ralph logs --iter=42

  # raw JSONL for scripting
  ralph logs --json | jq 'select(.state=="dirty")'`,
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
	cmd.Flags().IntVar(&opts.Iter, "iter", 0, "only print the record with this iter")
	cmd.Flags().BoolVar(&opts.Tail, "tail", false, "follow new records as they're appended")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "emit raw JSONL")
	cmd.MarkFlagsMutuallyExclusive("iter", "tail")
	return cmd
}

func run(ctx context.Context, opts *Options) error {
	p, err := opts.F.Paths()
	if err != nil {
		return err
	}
	path := p.Summary()
	if opts.Tail {
		return tailFile(ctx, path, opts)
	}
	return scanFile(path, opts)
}

func scanFile(path string, opts *Options) error {
	f, err := os.Open(path) //nolint:gosec // path joined from RepoRoot()/.ralph/state/logs/summary.jsonl
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("logs: no records at %s", path)
	}
	if err != nil {
		return fmt.Errorf("logs: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return emit(f, opts)
}

func emit(r io.Reader, opts *Options) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := emitLine(line, opts); err != nil {
			return err
		}
	}
	return sc.Err()
}

func emitLine(line []byte, opts *Options) error {
	rec, raw, err := decode(line)
	if err != nil {
		// Skip unparseable lines but surface to ErrOut so they're not
		// lost.
		_, _ = fmt.Fprintf(opts.F.IOStreams.ErrOut, "logs: skip malformed line: %v\n", err)
		return nil
	}
	if opts.Iter > 0 && rec.Iter != opts.Iter {
		return nil
	}
	switch {
	case opts.JSON:
		_, _ = fmt.Fprintf(opts.F.IOStreams.Out, "%s\n", line)
	case opts.Iter > 0:
		// Single-record mode: pretty JSON from the raw map so unknown
		// fields survive forward-compat.
		pretty, perr := json.MarshalIndent(raw, "", "  ")
		if perr != nil {
			return perr
		}
		_, _ = fmt.Fprintf(opts.F.IOStreams.Out, "%s\n", pretty)
	default:
		_, _ = fmt.Fprintln(opts.F.IOStreams.Out, formatNarrative(rec))
	}
	return nil
}

// decode parses one summary.jsonl line into the canonical loop.IterRecord
// and (in parallel) a raw map so unknown fields survive pretty-printing.
func decode(line []byte) (loop.IterRecord, map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return loop.IterRecord{}, nil, err
	}
	var rec loop.IterRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return loop.IterRecord{}, nil, err
	}
	return rec, raw, nil
}

func formatNarrative(rec loop.IterRecord) string {
	if rec.Narrative != "" {
		return fmt.Sprintf("iter %04d  %s", rec.Iter, rec.Narrative)
	}
	return fmt.Sprintf("iter %04d  %s", rec.Iter, rec.State)
}

// tailFile prints the entire file once, then polls for appends until
// ctx is canceled. KISS — simple seek+sleep loop, no inotify.
func tailFile(ctx context.Context, path string, opts *Options) error {
	f, err := os.Open(path) //nolint:gosec // path joined from RepoRoot()/.ralph/state/logs/summary.jsonl
	if errors.Is(err, fs.ErrNotExist) {
		// File may not exist yet; create-and-wait.
		f, err = waitForFile(ctx, path, opts.pollInterval)
		if err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("logs: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	rdr := bufio.NewReader(f)
	for {
		line, err := rdr.ReadBytes('\n')
		if len(line) > 0 {
			if e := emitLine(bytes.TrimSpace(line), opts); e != nil {
				return e
			}
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("logs: read %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(opts.pollInterval):
		}
	}
}

func waitForFile(ctx context.Context, path string, every time.Duration) (*os.File, error) {
	for {
		f, err := os.Open(path) //nolint:gosec // path joined from RepoRoot()/.ralph/state/logs/summary.jsonl
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("logs: open %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(every):
		}
	}
}
