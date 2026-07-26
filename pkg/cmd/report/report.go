// Package report implements `ralph report`: an activity summary over a time
// window. It reads:
//
//	.ralph/state/logs/summary.jsonl   for narrative + bd_diff
//	.ralph/state/runs/<id>/manifest.json  for state distribution + cost
//	.ralph/state/incidents/*.md       for incident headlines
//	internal/git.Log                  for commits in window
//
// The default rendering is markdown (optionally sliced with --section); --json
// emits the same aggregated shape as a single object, post-filterable with the
// built-in --jq / --template engine.
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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/git"
	ralphlog "github.com/jcrussell/ralph/internal/log"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/internal/paths"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Section names — the addressable slices of a report, shared by --section
// (markdown) and --json <fields> (they match the Report json tags below).
const (
	secWorkDone          = "work_done"
	secCommits           = "commits"
	secIncidents         = "incidents"
	secStateDistribution = "state_distribution"
	secCost              = "cost"
)

// sectionNames is the --section allowlist (the report body sections, excluding
// the "since" metadata key).
var sectionNames = []string{secWorkDone, secCommits, secIncidents, secStateDistribution, secCost}

// reportFields is the --json field allowlist, derived from Report's json tags
// so it can never drift (includes "since" plus every section).
var reportFields = cmdutil.JSONFieldNames(Report{})

// Report is the aggregated shape rendered by both the markdown writer and the
// --json exporter. The unexported flags carry markdown-only "no data"
// distinctions (git not a repo vs. no commits; no run manifests vs. zero cost)
// that the JSON form expresses as an empty list / zero value.
type Report struct {
	Since             time.Time      `json:"since"`
	WorkDone          WorkDone       `json:"work_done"`
	Commits           []Commit       `json:"commits"`
	Incidents         []Incident     `json:"incidents"`
	StateDistribution map[string]int `json:"state_distribution"`
	Cost              Cost           `json:"cost"`

	gitUnavailable bool // git.Log failed (not a repo); markdown-only
	hasRuns        bool // at least one run manifest in window; markdown-only
}

// WorkDone is the deduplicated bd-diff rollup over the window.
type WorkDone struct {
	Closed   []string `json:"closed"`
	Created  []string `json:"created"`
	Reopened []string `json:"reopened"`
	Deferred []string `json:"deferred"`
}

// Commit is one git commit in the window.
type Commit struct {
	ShortHash string `json:"short_hash"`
	Subject   string `json:"subject"`
}

// Incident is one incident write-up in the window.
type Incident struct {
	File  string `json:"file"`
	Title string `json:"title"`
}

// Cost is the aggregate cost/wallclock/iteration rollup over run manifests.
type Cost struct {
	Iters         int     `json:"iters"`
	WallclockSecs int     `json:"wallclock_secs"`
	CostUSD       float64 `json:"cost_usd"`
}

// ExportData satisfies cmdutil.RowExporter, shaping the Report to the requested
// --json fields.
func (r Report) ExportData(fields []string) map[string]any {
	return cmdutil.StructFields(r, fields)
}

// Options is the three-part command shape's Options struct.
type Options struct {
	F     *cmdutil.Factory
	Since string

	// Sections limits the markdown output to these sections; empty = all.
	Sections []string

	// Exporter is non-nil when --json is set; it renders the Report as JSON,
	// optionally post-filtered by --jq or --template.
	Exporter cmdutil.Exporter
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if _, err := cmdutil.ParseSince(o.Since); err != nil {
		return cmdutil.FlagErrorf("--since: %v", err)
	}
	for _, s := range o.Sections {
		if !slices.Contains(sectionNames, s) {
			return cmdutil.FlagErrorf("unknown --section %q; available: %s", s, strings.Join(sectionNames, ", "))
		}
	}
	return nil
}

// NewCmdReport returns the cobra command for `ralph report`.
func NewCmdReport(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f, Since: "24h"}
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Activity summary (markdown or JSON) over a time window",
		Long: `report aggregates orchestrator activity over a time window: bd issues
closed/created/reopened/deferred, commits, recent incidents, FSM state
distribution, and aggregate cost / wallclock / iteration count. Inputs are
summary.jsonl, run manifests under .ralph/state/runs/, incident files under
.ralph/state/incidents/, and 'git log'.

Default output is markdown — pipe to a markdown viewer or commit it to a
journal. --section=work_done,commits,... limits the markdown to specific
sections. --json takes a comma-separated list of sections (the flag help
lists them) and emits them as one JSON object (sections are keys), which
--jq / --template post-process with a built-in engine (no external jq
needed). --since takes a Go duration (24h, 7d → 168h) or an RFC3339 timestamp.`,
		Example: `  # last 24h markdown (default)
  ralph report

  # only the commits and incidents sections
  ralph report --section=commits,incidents

  # JSON for specific sections
  ralph report --json work_done,commits,cost

  # how many commits has ralph made in the last week?
  ralph report --since=168h --json commits --jq '.commits | length'

  # just the incidents, as JSON
  ralph report --json incidents --jq '.incidents'

  # commit a daily report to a journal
  ralph report > journal/$(date -I).md`,
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
	cmd.Flags().StringSliceVar(&opts.Sections, "section", nil, fmt.Sprintf("limit markdown to these sections (%s)", strings.Join(sectionNames, ",")))
	cmdutil.AddJSONFlags(cmd, &opts.Exporter, reportFields)
	// In JSON mode you slice with --json <fields> or --jq, so --section (a
	// markdown-only knob) would be redundant and ambiguous.
	cmd.MarkFlagsMutuallyExclusive("section", "json")
	cmdutil.MustRegisterFlagCompletion(cmd, "section",
		cobra.FixedCompletions(sectionNames, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func run(ctx context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	since, err := cmdutil.ParseSince(opts.Since)
	if err != nil {
		return err
	}
	rpt, err := compute(ctx, repo, since)
	if err != nil {
		return err
	}
	io := opts.F.IOStreams
	if opts.Exporter != nil {
		return cmdutil.WriteRow(io, opts.Exporter, rpt)
	}
	return renderMarkdown(io.Out, rpt, opts.Sections)
}

// Render writes an all-sections markdown report for activity at repo since the
// given time. Retained for callers that want the markdown directly.
func Render(ctx context.Context, repo string, since time.Time, w io.Writer) error {
	rpt, err := compute(ctx, repo, since)
	if err != nil {
		return err
	}
	return renderMarkdown(w, rpt, nil)
}

// compute gathers every section of the report. A missing state dir is not an
// error — the corresponding section is simply empty.
func compute(ctx context.Context, repo string, since time.Time) (Report, error) {
	rpt := Report{Since: since, StateDistribution: map[string]int{}}

	wd, err := buildWorkDone(repo, since)
	if err != nil {
		return Report{}, err
	}
	rpt.WorkDone = wd

	commits, gitOK, err := buildCommits(ctx, repo, since)
	if err != nil {
		return Report{}, err
	}
	rpt.Commits = commits
	rpt.gitUnavailable = !gitOK

	incidents, err := buildIncidents(repo, since)
	if err != nil {
		return Report{}, err
	}
	rpt.Incidents = incidents

	manifests, err := readManifests(repo, since)
	if err != nil {
		return Report{}, err
	}
	rpt.hasRuns = len(manifests) > 0
	rpt.StateDistribution = sumStateCounts(manifests)
	rpt.Cost = sumCost(manifests)

	return rpt, nil
}

// ----- Work done (bd_diff aggregation from summary.jsonl) ----------

// readSummary parses summary.jsonl, filtering to records at-or-after
// since. A missing file is not an error; returns an empty slice.
// Lines that fail to decode against loop.IterRecord are skipped.
func readSummary(repo string, since time.Time) ([]loop.IterRecord, error) {
	path := paths.New(repo).Summary()
	f, err := os.Open(path) //nolint:gosec // path joined from repo root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("report: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []loop.IterRecord
	sc := ralphlog.NewSummaryScanner(f)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		rec, _, err := loop.DecodeRecord(line)
		if err != nil {
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

func buildWorkDone(repo string, since time.Time) (WorkDone, error) {
	records, err := readSummary(repo, since)
	if err != nil {
		return WorkDone{}, err
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
	return WorkDone{
		Closed:   sortedKeys(closed),
		Created:  sortedKeys(created),
		Reopened: sortedKeys(opened),
		Deferred: sortedKeys(deferred),
	}, nil
}

func renderWorkDone(w io.Writer, wd WorkDone) {
	_, _ = fmt.Fprintln(w, "## Work done")
	bullets := []struct {
		label string
		ids   []string
	}{
		{"closed", wd.Closed},
		{"created", wd.Created},
		{"reopened", wd.Reopened},
		{"deferred", wd.Deferred},
	}
	any := false
	for _, b := range bullets {
		if len(b.ids) == 0 {
			continue
		}
		any = true
		_, _ = fmt.Fprintf(w, "- %s: %s\n", b.label, strings.Join(b.ids, ", "))
	}
	if !any {
		_, _ = fmt.Fprintln(w, "_(no bead activity in window)_")
	}
	_, _ = fmt.Fprintln(w)
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

// buildCommits returns the commits in the window. The bool is false when git
// is unavailable (not a repo / worktree issue), which markdown surfaces
// distinctly from an empty window.
func buildCommits(ctx context.Context, repo string, since time.Time) ([]Commit, bool, error) {
	entries, err := git.Log(ctx, repo, since)
	if err != nil {
		// Empty (non-nil) slice so JSON emits `[]`, matching the Report doc and
		// the no-commits case; markdown tells the two apart via the bool.
		return []Commit{}, false, nil
	}
	out := make([]Commit, 0, len(entries))
	for _, e := range entries {
		out = append(out, Commit{ShortHash: e.ShortHash, Subject: e.Subject})
	}
	return out, true, nil
}

func renderCommits(w io.Writer, commits []Commit, unavailable bool) {
	_, _ = fmt.Fprintln(w, "## Commits")
	switch {
	case unavailable:
		_, _ = fmt.Fprintln(w, "_(git log unavailable)_")
	case len(commits) == 0:
		_, _ = fmt.Fprintln(w, "_(no commits in window)_")
	default:
		for _, c := range commits {
			_, _ = fmt.Fprintf(w, "- %s  %s\n", c.ShortHash, c.Subject)
		}
	}
	_, _ = fmt.Fprintln(w)
}

// ----- Incidents -----------------------------------------------------

func buildIncidents(repo string, since time.Time) ([]Incident, error) {
	dir := paths.New(repo).IncidentsDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("report: read incidents: %w", err)
	}
	var out []Incident
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		out = append(out, Incident{
			File:  e.Name(),
			Title: firstHeader(filepath.Join(dir, e.Name())),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
}

func renderIncidents(w io.Writer, incidents []Incident) {
	_, _ = fmt.Fprintln(w, "## Incidents")
	if len(incidents) == 0 {
		_, _ = fmt.Fprintln(w, "_(none)_")
	} else {
		for _, inc := range incidents {
			_, _ = fmt.Fprintf(w, "- %s — %s\n", inc.File, inc.Title)
		}
	}
	_, _ = fmt.Fprintln(w)
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

func readManifests(repo string, since time.Time) ([]runs.Manifest, error) {
	dir := paths.New(repo).RunsDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("report: read runs: %w", err)
	}
	var out []runs.Manifest
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
		var m runs.Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		if m.EndTime != nil && m.EndTime.Before(since) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func sumStateCounts(manifests []runs.Manifest) map[string]int {
	total := map[string]int{}
	for _, m := range manifests {
		for k, v := range m.StateCounts {
			total[k] += v
		}
	}
	return total
}

func renderStateDistribution(w io.Writer, dist map[string]int) {
	_, _ = fmt.Fprintln(w, "## State distribution")
	if len(dist) == 0 {
		_, _ = fmt.Fprintln(w, "_(no run manifests in window)_")
	} else {
		keys := make([]string, 0, len(dist))
		for k := range dist {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = fmt.Fprintf(w, "- %s: %d\n", k, dist[k])
		}
	}
	_, _ = fmt.Fprintln(w)
}

func sumCost(manifests []runs.Manifest) Cost {
	var c Cost
	for _, m := range manifests {
		c.CostUSD += m.CumulativeCostUSD
		c.WallclockSecs += m.CumulativeWallclockSecs
		c.Iters += m.TotalIters
	}
	return c
}

func renderCost(w io.Writer, cost Cost, hasRuns bool) {
	_, _ = fmt.Fprintln(w, "## Cost")
	if !hasRuns {
		_, _ = fmt.Fprintln(w, "_(no run manifests in window)_")
		_, _ = fmt.Fprintln(w)
		return
	}
	_, _ = fmt.Fprintf(w, "- iters: %d\n", cost.Iters)
	_, _ = fmt.Fprintf(w, "- wallclock: %s\n", time.Duration(cost.WallclockSecs)*time.Second)
	_, _ = fmt.Fprintf(w, "- cost: $%.4f\n", cost.CostUSD)
	_, _ = fmt.Fprintln(w)
}

// ----- Markdown rendering -------------------------------------------

// renderMarkdown writes the report as markdown. A nil/empty sections slice
// renders every section; otherwise only the named sections are written (the
// title header is always written).
func renderMarkdown(w io.Writer, rpt Report, sections []string) error {
	_, _ = fmt.Fprintf(w, "# ralph report (since %s)\n\n", rpt.Since.Format(time.RFC3339))

	if wants(sections, secWorkDone) {
		renderWorkDone(w, rpt.WorkDone)
	}
	if wants(sections, secCommits) {
		renderCommits(w, rpt.Commits, rpt.gitUnavailable)
	}
	if wants(sections, secIncidents) {
		renderIncidents(w, rpt.Incidents)
	}
	if wants(sections, secStateDistribution) {
		renderStateDistribution(w, rpt.StateDistribution)
	}
	if wants(sections, secCost) {
		renderCost(w, rpt.Cost, rpt.hasRuns)
	}
	return nil
}

func wants(sections []string, name string) bool {
	return len(sections) == 0 || slices.Contains(sections, name)
}
