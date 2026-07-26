// Package gc implements `ralph gc`: age-based pruning of run
// directories, iteration artifacts, and incidents under .ralph/state.
//
// Like `git clean`, gc is dry-run by default — with no --force it prints
// exactly what it would remove and touches nothing. summary.jsonl and
// orchestrator.log are never deleted: the iteration stream stays the
// durable audit trail even when the per-iteration artifacts it
// references are gone.
package gc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/lock"
	ralphlog "github.com/jcrussell/ralph/internal/log"
	"github.com/jcrussell/ralph/internal/paths"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F         *cmdutil.Factory
	OlderThan string
	Force     bool

	// Exporter is non-nil when --json is set; it renders the Plan as JSON,
	// optionally post-filtered by --jq or --template.
	Exporter cmdutil.Exporter

	now func() time.Time // test seam; defaults to time.Now
	dur time.Duration    // parsed from OlderThan by Validate
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if o.OlderThan == "" {
		return cmdutil.FlagErrorf("--older-than is required (e.g. 30d, 2w, 72h)")
	}
	d, err := cmdutil.ParseDuration(o.OlderThan)
	if err != nil {
		return cmdutil.FlagErrorf("--older-than: %v", err)
	}
	o.dur = d
	return nil
}

// NewCmdGC returns the cobra command for `ralph gc`.
func NewCmdGC(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Prune old runs, iteration artifacts, and incidents from .ralph/state",
		Long: `gc removes aged orchestrator state under .ralph/state:

  - run directories      .ralph/state/runs/<RUN_ID>/
  - iteration artifacts  .ralph/state/logs/iter-*
  - incidents            .ralph/state/incidents/*.md

An item is eligible when the timestamp encoded in its directory or file
name is older than --older-than (e.g. 30d, 2w, 72h). summary.jsonl and
orchestrator.log are never touched.

Like 'git clean', gc is dry-run by default: with no --force it prints
exactly what it would delete and exits without changing anything. Pass
-f/--force to actually delete.

If an orchestrator is currently running (a live pid.lock), its run is
held back regardless of age. Run directories whose names don't parse as
timestamps are left alone for manual inspection.`,
		Example: `  # preview what is older than 30 days (deletes nothing)
  ralph gc --older-than 30d

  # actually delete it
  ralph gc --older-than 30d --force

  # machine-readable plan
  ralph gc --older-than 2w --json runs,iterations,incidents,total_bytes,total_count

  # just the byte total, via built-in jq
  ralph gc --older-than 2w --json total_bytes --jq '.total_bytes'`,
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return gcRun(c.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.OlderThan, "older-than", "", "delete state older than this age (e.g. 30d, 2w, 72h); required")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "actually delete; without it gc only prints what would be removed")
	cmdutil.AddJSONFlags(cmd, &opts.Exporter, gcFields)
	return cmd
}

// gcFields is the --json field allowlist, derived from Plan's json tags.
var gcFields = cmdutil.JSONFieldNames(Plan{})

// ExportData satisfies cmdutil.RowExporter, shaping the Plan to the requested
// --json fields.
func (pl Plan) ExportData(fields []string) map[string]any {
	return cmdutil.StructFields(pl, fields)
}

// Plan is the set of deletion targets gc found, grouped by category. It
// is the data shape rendered by both --json and the text writer, and
// (with Applied) the record of what gc did.
type Plan struct {
	Cutoff        time.Time `json:"cutoff"`
	OlderThan     string    `json:"older_than"`
	Runs          []Target  `json:"runs"`
	Iterations    []Target  `json:"iterations"`
	Incidents     []Target  `json:"incidents"`
	SkippedActive string    `json:"skipped_active_run,omitempty"`
	TotalBytes    int64     `json:"total_bytes"`
	TotalCount    int       `json:"total_count"`
	Applied       bool      `json:"applied"`
}

// Target is one deletion unit: a run directory, an iteration-artifact
// group (all files sharing a stem), or an incident file.
type Target struct {
	Path  string `json:"path"`            // display path, relative to repo root
	Bytes int64  `json:"bytes"`           // total size of the unit
	Begun string `json:"begun,omitempty"` // RFC3339; "" when derived from mtime

	abs []string // absolute paths to remove (not serialized)
}

func gcRun(ctx context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	p, err := opts.F.Paths()
	if err != nil {
		return err
	}
	now := time.Now
	if opts.now != nil {
		now = opts.now
	}
	pl := Plan{Cutoff: now().UTC().Add(-opts.dur), OlderThan: opts.OlderThan}

	metas, err := runs.List(repo)
	if err != nil {
		return err
	}

	// Hold back the in-progress run: a live pid.lock means an
	// orchestrator owns the newest run dir and is appending to it. Run
	// ids are RFC3339-flat, so the newest is the last meta with a
	// parseable timestamp — derived from the directory name alone, so a
	// mid-write or malformed manifest can't cause us to delete the live
	// run. (An unparseable dir name must not be mistaken for the newest.)
	protected := ""
	if _, alive, lerr := lock.ActivePID(p.PidLock()); lerr == nil && alive {
		for i := len(metas) - 1; i >= 0; i-- {
			if !metas[i].Begun.IsZero() {
				protected = metas[i].ID
				pl.SkippedActive = protected
				break
			}
		}
	}

	collectRuns(repo, metas, pl.Cutoff, protected, &pl)
	if err := collectIterations(repo, p, pl.Cutoff, &pl); err != nil {
		return err
	}
	if err := collectIncidents(repo, p, pl.Cutoff, &pl); err != nil {
		return err
	}

	for _, t := range pl.all() {
		pl.TotalBytes += t.Bytes
		pl.TotalCount++
	}

	if opts.Force {
		pl.Applied = true
		deleteTargets(ctx, &pl)
	}
	return render(opts, &pl)
}

func (pl *Plan) all() []Target {
	out := make([]Target, 0, len(pl.Runs)+len(pl.Iterations)+len(pl.Incidents))
	out = append(out, pl.Runs...)
	out = append(out, pl.Iterations...)
	out = append(out, pl.Incidents...)
	return out
}

func collectRuns(repo string, metas []runs.Meta, cutoff time.Time, protected string, pl *Plan) {
	for _, m := range metas {
		if m.Begun.IsZero() { // unparseable id — leave for manual inspection
			continue
		}
		if m.ID == protected || !m.Begun.Before(cutoff) {
			continue
		}
		pl.Runs = append(pl.Runs, Target{
			Path:  relTo(repo, m.Dir),
			Bytes: dirSize(m.Dir),
			Begun: m.Begun.Format(time.RFC3339),
			abs:   []string{m.Dir},
		})
	}
}

// iterStemRE captures the stem ("iter-NNNN-<TS>") and the timestamp of
// an iteration artifact. The same stem is shared by the .json snapshot
// and every -*.txt sibling, so matches group naturally.
var iterStemRE = regexp.MustCompile(`^(iter-\d{4}-(\d{8}T\d{6}Z))`)

func collectIterations(repo string, p *paths.Paths, cutoff time.Time, pl *Plan) error {
	logsDir := p.LogsDir()
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("gc: read logs dir: %w", err)
	}

	type group struct {
		files []string
		bytes int64
		begun time.Time // from stem TS when parseable, else newest mtime
		hasTS bool
	}
	groups := map[string]*group{}
	var order []string

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := iterStemRE.FindStringSubmatch(e.Name())
		if m == nil { // summary.jsonl, orchestrator.log, anything else
			continue
		}
		stem := m[1]
		g := groups[stem]
		if g == nil {
			g = &group{}
			groups[stem] = g
			order = append(order, stem)
			if ts, perr := time.Parse("20060102T150405Z", m[2]); perr == nil {
				g.begun, g.hasTS = ts, true
			}
		}
		g.files = append(g.files, filepath.Join(logsDir, e.Name()))
		if info, ierr := e.Info(); ierr == nil {
			g.bytes += info.Size()
			if mt := info.ModTime(); !g.hasTS && mt.After(g.begun) {
				g.begun = mt // mtime fallback for an unparseable stem
			}
		}
	}

	sort.Strings(order)
	for _, stem := range order {
		g := groups[stem]
		if g.begun.IsZero() || !g.begun.Before(cutoff) {
			continue
		}
		sort.Strings(g.files)
		t := Target{
			Path:  relTo(repo, filepath.Join(logsDir, stem)) + "*",
			Bytes: g.bytes,
			abs:   g.files,
		}
		if g.hasTS {
			t.Begun = g.begun.Format(time.RFC3339)
		}
		pl.Iterations = append(pl.Iterations, t)
	}
	return nil
}

// incidentRE captures the leading time.UnixNano() stamp of an incident
// filename: "<nanos>-<kind>.md".
var incidentRE = regexp.MustCompile(`^(\d+)-.*\.md$`)

func collectIncidents(repo string, p *paths.Paths, cutoff time.Time, pl *Plan) error {
	dir := p.IncidentsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("gc: read incidents dir: %w", err)
	}
	cutoffNanos := cutoff.UnixNano()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := incidentRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		nanos, perr := strconv.ParseInt(m[1], 10, 64)
		if perr != nil || nanos >= cutoffNanos {
			continue
		}
		full := filepath.Join(dir, e.Name())
		var size int64
		if info, ierr := e.Info(); ierr == nil {
			size = info.Size()
		}
		pl.Incidents = append(pl.Incidents, Target{
			Path:  relTo(repo, full),
			Bytes: size,
			Begun: time.Unix(0, nanos).UTC().Format(time.RFC3339),
			abs:   []string{full},
		})
	}
	return nil
}

// deleteTargets removes every collected target, best-effort: a failure
// is logged and the sweep continues (mirrors the loop's incident-write
// tolerance). Already-gone files are not errors.
func deleteTargets(ctx context.Context, pl *Plan) {
	logger := ralphlog.From(ctx)
	rm := func(path string, dir bool) {
		var err error
		if dir {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			logger.WarnContext(ctx, "gc: delete failed", "path", path, "err", err)
		}
	}
	for _, t := range pl.Runs {
		for _, a := range t.abs {
			rm(a, true)
		}
	}
	for _, t := range append(append([]Target{}, pl.Iterations...), pl.Incidents...) {
		for _, a := range t.abs {
			rm(a, false)
		}
	}
}

// --- rendering & helpers ---

func render(opts *Options, pl *Plan) error {
	if opts.Exporter != nil {
		return cmdutil.WriteRow(opts.F.IOStreams, opts.Exporter, *pl)
	}
	return writeText(opts.F.IOStreams.Out, pl)
}

func writeText(w io.Writer, pl *Plan) error {
	_, _ = fmt.Fprintf(w, "ralph gc — cutoff %s (older than %s)\n\n",
		pl.Cutoff.Format(time.RFC3339), pl.OlderThan)

	skipped := func() {
		if pl.SkippedActive != "" {
			_, _ = fmt.Fprintf(w, "(skipped active run %s — an orchestrator is running)\n", pl.SkippedActive)
		}
	}

	if pl.TotalCount == 0 {
		_, _ = fmt.Fprintf(w, "nothing older than the cutoff to delete\n")
		skipped()
		return nil
	}

	section := func(name string, ts []Target) {
		if len(ts) == 0 {
			return
		}
		var b int64
		for _, t := range ts {
			b += t.Bytes
		}
		_, _ = fmt.Fprintf(w, "%s (%d, %s):\n", name, len(ts), humanBytes(b))
		for _, t := range ts {
			if t.Begun != "" {
				_, _ = fmt.Fprintf(w, "  %-48s %9s  %s\n", t.Path, humanBytes(t.Bytes), t.Begun)
			} else {
				_, _ = fmt.Fprintf(w, "  %-48s %9s\n", t.Path, humanBytes(t.Bytes))
			}
		}
	}
	section("runs", pl.Runs)
	section("iterations", pl.Iterations)
	section("incidents", pl.Incidents)

	_, _ = fmt.Fprintf(w, "\ntotal: %d items, %s\n", pl.TotalCount, humanBytes(pl.TotalBytes))
	if pl.Applied {
		_, _ = fmt.Fprintf(w, "deleted %d items, freed %s\n", pl.TotalCount, humanBytes(pl.TotalBytes))
	} else {
		_, _ = fmt.Fprintf(w, "nothing deleted (dry-run); re-run with --force to apply\n")
	}
	skipped()
	return nil
}

// dirSize sums the sizes of all regular files under dir, best-effort.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort sizing; skip unreadable entries
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// humanBytes formats n with 1024-based units (B, K, M, G, …).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

func relTo(repo, path string) string {
	if rel, err := filepath.Rel(repo, path); err == nil {
		return rel
	}
	return path
}
