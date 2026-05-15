// Package report implements `ralph report`: a human-readable markdown
// summary of orchestrator activity over a time window. Reads:
//
//	.ralph/state/logs/summary.jsonl   for narrative + bd_diff
//	.ralph/state/runs/<id>/manifest.json  for state distribution + cost
//	.ralph/state/incidents/*.md       for incident headlines
//	internal/git.Log                  for commits in window
package report

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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/git"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F     *cmdutil.Factory
	Since string
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if _, err := parseSince(o.Since); err != nil {
		return cmdutil.FlagErrorf("--since: %v", err)
	}
	return nil
}

// NewCmdReport returns the cobra command for `ralph report`.
func NewCmdReport(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f, Since: "24h"}
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Markdown summary of orchestrator activity",
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
	cmd.Flags().StringVar(&opts.Since, "since", "24h", "duration (e.g. 24h) or RFC3339 timestamp")
	return cmd
}

func parseSince(spec string) (time.Time, error) {
	if d, err := time.ParseDuration(spec); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, spec); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("not a duration or RFC3339 timestamp: %q", spec)
}

func run(ctx context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	since, err := parseSince(opts.Since)
	if err != nil {
		return err
	}
	return Render(ctx, repo, since, opts.F.IOStreams.Out)
}

// Render writes a markdown report for activity at repo since the
// given time.
func Render(ctx context.Context, repo string, since time.Time, w io.Writer) error {
	_, _ = fmt.Fprintf(w, "# ralph report (since %s)\n\n", since.Format(time.RFC3339))

	if err := writeWorkDone(repo, since, w); err != nil {
		return err
	}
	if err := writeCommits(ctx, repo, since, w); err != nil {
		return err
	}
	if err := writeIncidents(repo, since, w); err != nil {
		return err
	}
	if err := writeStateDistribution(repo, since, w); err != nil {
		return err
	}
	if err := writeCost(repo, since, w); err != nil {
		return err
	}
	return nil
}

// ----- Work done (bd_diff aggregation from summary.jsonl) ----------

// readSummary parses summary.jsonl, filtering to records at-or-after
// since. A missing file is not an error; returns an empty slice.
// Lines that fail to decode against loop.IterRecord are skipped.
func readSummary(repo string, since time.Time) ([]loop.IterRecord, error) {
	path := filepath.Join(repo, ".ralph", "state", "logs", "summary.jsonl")
	f, err := os.Open(path) //nolint:gosec // path joined from repo root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("report: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []loop.IterRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec loop.IterRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // tolerate stray lines
		}
		if rec.Timestamp != "" {
			ts, err := time.Parse(time.RFC3339, rec.Timestamp)
			if err == nil && ts.Before(since) {
				continue
			}
		}
		out = append(out, rec)
	}
	return out, sc.Err()
}

func writeWorkDone(repo string, since time.Time, w io.Writer) error {
	records, err := readSummary(repo, since)
	if err != nil {
		return err
	}
	created := map[string]struct{}{}
	closed := map[string]struct{}{}
	opened := map[string]struct{}{}
	deferred := map[string]struct{}{}
	for _, r := range records {
		if r.BDDiff == nil {
			continue
		}
		for _, id := range r.BDDiff.Created {
			created[id] = struct{}{}
		}
		for _, id := range r.BDDiff.Closed {
			closed[id] = struct{}{}
		}
		for _, id := range r.BDDiff.Opened {
			opened[id] = struct{}{}
		}
		for _, id := range r.BDDiff.Deferred {
			deferred[id] = struct{}{}
		}
	}
	_, _ = fmt.Fprintln(w, "## Work done")
	bullets := []struct {
		label string
		set   map[string]struct{}
	}{
		{"closed", closed},
		{"created", created},
		{"reopened", opened},
		{"deferred", deferred},
	}
	any := false
	for _, b := range bullets {
		if len(b.set) == 0 {
			continue
		}
		any = true
		_, _ = fmt.Fprintf(w, "- %s: %s\n", b.label, strings.Join(sortedKeys(b.set), ", "))
	}
	if !any {
		_, _ = fmt.Fprintln(w, "_(no bead activity in window)_")
	}
	_, _ = fmt.Fprintln(w)
	return nil
}

func sortedKeys(s map[string]struct{}) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ----- Commits (git log) ---------------------------------------------

func writeCommits(ctx context.Context, repo string, since time.Time, w io.Writer) error {
	_, _ = fmt.Fprintln(w, "## Commits")
	entries, err := git.Log(ctx, repo, since)
	if err != nil {
		// Not a git repo or worktree issue — degrade gracefully.
		_, _ = fmt.Fprintln(w, "_(git log unavailable)_")
		_, _ = fmt.Fprintln(w)
		return nil
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(w, "_(no commits in window)_")
	} else {
		for _, e := range entries {
			_, _ = fmt.Fprintf(w, "- %s  %s\n", e.ShortHash, e.Subject)
		}
	}
	_, _ = fmt.Fprintln(w)
	return nil
}

// ----- Incidents -----------------------------------------------------

func writeIncidents(repo string, since time.Time, w io.Writer) error {
	dir := filepath.Join(repo, ".ralph", "state", "incidents")
	_, _ = fmt.Fprintln(w, "## Incidents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		_, _ = fmt.Fprintln(w, "_(none)_")
		_, _ = fmt.Fprintln(w)
		return nil
	}
	if err != nil {
		return fmt.Errorf("report: read incidents: %w", err)
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		title := firstHeader(filepath.Join(dir, e.Name()))
		lines = append(lines, fmt.Sprintf("- %s — %s", e.Name(), title))
	}
	if len(lines) == 0 {
		_, _ = fmt.Fprintln(w, "_(none)_")
	} else {
		sort.Strings(lines)
		for _, l := range lines {
			_, _ = fmt.Fprintln(w, l)
		}
	}
	_, _ = fmt.Fprintln(w)
	return nil
}

func firstHeader(path string) string {
	f, err := os.Open(path) //nolint:gosec // path constructed from dirent under incidents/
	if err != nil {
		return path
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return filepath.Base(path)
}

// ----- State distribution + cost (from manifests) -------------------

type manifest struct {
	Start             string         `json:"start,omitempty"`
	End               string         `json:"end,omitempty"`
	Iters             int            `json:"iters,omitempty"`
	StateDistribution map[string]int `json:"state_distribution,omitempty"`
	CostUSD           float64        `json:"cost_usd,omitempty"`
	WallclockSecs     int            `json:"wallclock_secs,omitempty"`
}

func readManifests(repo string, since time.Time) ([]manifest, error) {
	dir := filepath.Join(repo, ".ralph", "state", "runs")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("report: read runs: %w", err)
	}
	var out []manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "manifest.json")
		b, err := os.ReadFile(path) //nolint:gosec // dirent under .ralph/state/runs/
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("report: read %s: %w", path, err)
		}
		var m manifest
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		if m.End != "" {
			if t, err := time.Parse(time.RFC3339, m.End); err == nil && t.Before(since) {
				continue
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func writeStateDistribution(repo string, since time.Time, w io.Writer) error {
	manifests, err := readManifests(repo, since)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, "## State distribution")
	total := map[string]int{}
	for _, m := range manifests {
		for k, v := range m.StateDistribution {
			total[k] += v
		}
	}
	if len(total) == 0 {
		_, _ = fmt.Fprintln(w, "_(no run manifests in window)_")
	} else {
		for _, k := range sortedKeys(toSet(total)) {
			_, _ = fmt.Fprintf(w, "- %s: %d\n", k, total[k])
		}
	}
	_, _ = fmt.Fprintln(w)
	return nil
}

func toSet(m map[string]int) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func writeCost(repo string, since time.Time, w io.Writer) error {
	manifests, err := readManifests(repo, since)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, "## Cost")
	if len(manifests) == 0 {
		_, _ = fmt.Fprintln(w, "_(no run manifests in window)_")
		_, _ = fmt.Fprintln(w)
		return nil
	}
	var cost float64
	var wall int
	var iters int
	for _, m := range manifests {
		cost += m.CostUSD
		wall += m.WallclockSecs
		iters += m.Iters
	}
	_, _ = fmt.Fprintf(w, "- iters: %d\n", iters)
	_, _ = fmt.Fprintf(w, "- wallclock: %s\n", time.Duration(wall)*time.Second)
	_, _ = fmt.Fprintf(w, "- cost: $%.4f\n", cost)
	_, _ = fmt.Fprintln(w)
	return nil
}
