// Package report implements `ralph report`: a human-readable markdown
// summary of orchestrator activity over a time window. Reads:
//
//	.ralph/state/logs/summary.jsonl   for narrative + bd_diff
//	.ralph/state/runs/<id>/manifest.json  for state distribution + cost
//	.ralph/state/incidents/*.md       for incident headlines
//	git log --since=<spec>            for commits
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
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type Options struct {
	F     *cmdutil.Factory
	Since string
}

func NewCmdReport(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f, Since: "24h"}
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Markdown summary of orchestrator activity",
		RunE: func(c *cobra.Command, args []string) error {
			if _, err := parseSince(opts.Since); err != nil {
				return cmdutil.FlagErrorf("--since: %v", err)
			}
			if runF != nil {
				return runF(opts)
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
	return Render(ctx, repo, since, opts.Since, opts.F.IOStreams.Out)
}

// Render writes a markdown report for activity at repo since the
// given time. spec is the original --since string (used only for the
// "Commits" git invocation, which accepts duration strings natively).
func Render(ctx context.Context, repo string, since time.Time, spec string, w io.Writer) error {
	fmt.Fprintf(w, "# ralph report (since %s)\n\n", since.Format(time.RFC3339))

	if err := writeWorkDone(repo, since, w); err != nil {
		return err
	}
	if err := writeCommits(ctx, repo, spec, since, w); err != nil {
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

type iterRecord struct {
	Iter      int             `json:"iter"`
	State     string          `json:"state"`
	Timestamp string          `json:"timestamp,omitempty"`
	BDDiff    json.RawMessage `json:"bd_diff,omitempty"`
	Cost      float64         `json:"cost_usd,omitempty"`
}

type bdDiff struct {
	Created    []string `json:"created,omitempty"`
	Closed     []string `json:"closed,omitempty"`
	Opened     []string `json:"opened,omitempty"`
	Deferred   []string `json:"deferred,omitempty"`
	InProgress []string `json:"in_progress,omitempty"`
}

// readSummary parses summary.jsonl, filtering to records at-or-after
// since. A missing file is not an error; returns an empty slice.
func readSummary(repo string, since time.Time) ([]iterRecord, error) {
	path := filepath.Join(repo, ".ralph", "state", "logs", "summary.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("report: open %s: %w", path, err)
	}
	defer f.Close()
	var out []iterRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec iterRecord
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
		if len(r.BDDiff) == 0 {
			continue
		}
		var d bdDiff
		if err := json.Unmarshal(r.BDDiff, &d); err != nil {
			continue
		}
		for _, id := range d.Created {
			created[id] = struct{}{}
		}
		for _, id := range d.Closed {
			closed[id] = struct{}{}
		}
		for _, id := range d.Opened {
			opened[id] = struct{}{}
		}
		for _, id := range d.Deferred {
			deferred[id] = struct{}{}
		}
	}
	fmt.Fprintln(w, "## Work done")
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
		fmt.Fprintf(w, "- %s: %s\n", b.label, strings.Join(sortedKeys(b.set), ", "))
	}
	if !any {
		fmt.Fprintln(w, "_(no bead activity in window)_")
	}
	fmt.Fprintln(w)
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

func writeCommits(ctx context.Context, repo, spec string, since time.Time, w io.Writer) error {
	fmt.Fprintln(w, "## Commits")
	sinceArg := spec
	if _, err := time.ParseDuration(spec); err != nil {
		// Not a duration; pass RFC3339.
		sinceArg = since.Format(time.RFC3339)
	}
	cmd := exec.CommandContext(ctx, "git", "log",
		"--since="+sinceArg,
		"--pretty=format:- %h  %s",
		"--no-merges")
	cmd.Dir = repo
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		// Not a git repo or no commits — degrade gracefully.
		fmt.Fprintln(w, "_(git log unavailable)_")
		fmt.Fprintln(w)
		return nil
	}
	body := strings.TrimSpace(out.String())
	if body == "" {
		fmt.Fprintln(w, "_(no commits in window)_")
	} else {
		fmt.Fprintln(w, body)
	}
	fmt.Fprintln(w)
	return nil
}

// ----- Incidents -----------------------------------------------------

func writeIncidents(repo string, since time.Time, w io.Writer) error {
	dir := filepath.Join(repo, ".ralph", "state", "incidents")
	fmt.Fprintln(w, "## Incidents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(w, "_(none)_")
		fmt.Fprintln(w)
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
		fmt.Fprintln(w, "_(none)_")
	} else {
		sort.Strings(lines)
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	}
	fmt.Fprintln(w)
	return nil
}

func firstHeader(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return path
	}
	defer f.Close()
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
		b, err := os.ReadFile(path)
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
	fmt.Fprintln(w, "## State distribution")
	total := map[string]int{}
	for _, m := range manifests {
		for k, v := range m.StateDistribution {
			total[k] += v
		}
	}
	if len(total) == 0 {
		fmt.Fprintln(w, "_(no run manifests in window)_")
	} else {
		for _, k := range sortedKeys(toSet(total)) {
			fmt.Fprintf(w, "- %s: %d\n", k, total[k])
		}
	}
	fmt.Fprintln(w)
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
	fmt.Fprintln(w, "## Cost")
	if len(manifests) == 0 {
		fmt.Fprintln(w, "_(no run manifests in window)_")
		fmt.Fprintln(w)
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
	fmt.Fprintf(w, "- iters: %d\n", iters)
	fmt.Fprintf(w, "- wallclock: %s\n", time.Duration(wall)*time.Second)
	fmt.Fprintf(w, "- cost: $%.4f\n", cost)
	fmt.Fprintln(w)
	return nil
}
