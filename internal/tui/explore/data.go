package explore

// data.go is the read layer behind the explore TUI. It is hidden behind
// the dataSource interface so the model can be tested with an in-memory
// fake; fsData is the production implementation over .ralph/state.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jcrussell/ralph/internal/paths"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmd/trace"
)

// WriteListing prints a plain, static summary of the three categories to
// w. It is the non-TTY fallback for `ralph explore` so the command stays
// pipeable and usable in CI; interactive drill-in is TUI-only.
func WriteListing(w io.Writer, repo string, p *paths.Paths) error {
	d := newFSData(repo, p)

	rs := d.runs()
	_, _ = fmt.Fprintf(w, "Runs (%d):\n", len(rs))
	for _, r := range rs {
		when := "—"
		if !r.begun.IsZero() {
			when = r.begun.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "  %s  %s\n", r.id, when)
	}

	incs := d.incidents()
	_, _ = fmt.Fprintf(w, "\nIncidents (%d):\n", len(incs))
	for _, it := range incs {
		_, _ = fmt.Fprintf(w, "  %s\n", it.name)
	}

	iters := d.iterations()
	_, _ = fmt.Fprintf(w, "\nIterations (%d):\n", len(iters))
	for _, it := range iters {
		_, _ = fmt.Fprintf(w, "  %s\n", it.stem)
	}
	return nil
}

// runItem, incidentItem, and iterItem are the list rows for each
// category. They carry just enough to render the row and to load the
// detail pane lazily on drill-in.
type runItem struct {
	id    string
	begun time.Time
}

type incidentItem struct {
	name string // base filename
	path string // absolute path
	kind string // parsed from "<nanos>-<kind>.md"
	when time.Time
}

type iterItem struct {
	stem string // "iter-NNNN-<ts>"
	num  int
	when time.Time
}

// dataSource is everything the model reads. fsData is the disk-backed
// implementation; tests inject a fake.
type dataSource interface {
	runs() []runItem
	incidents() []incidentItem
	iterations() []iterItem

	runDetail(id string) (string, error)
	incidentDetail(path string) (string, error)
	iterDetail(stem string) (string, error)
}

// fsData reads explore's three categories from .ralph/state.
type fsData struct {
	repo string
	p    *paths.Paths
}

func newFSData(repo string, p *paths.Paths) *fsData { return &fsData{repo: repo, p: p} }

func (d *fsData) runs() []runItem {
	metas, err := runs.List(d.repo)
	if err != nil || len(metas) == 0 {
		return nil
	}
	out := make([]runItem, 0, len(metas))
	for _, m := range metas {
		out = append(out, runItem{id: m.ID, begun: m.Begun})
	}
	// Newest first — most operators want the latest run on top.
	sort.Slice(out, func(i, j int) bool { return out[i].id > out[j].id })
	return out
}

var incidentNameRE = regexp.MustCompile(`^(\d+)-(.*)\.md$`)

func (d *fsData) incidents() []incidentItem {
	dir := d.p.IncidentsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []incidentItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		it := incidentItem{name: e.Name(), path: filepath.Join(dir, e.Name())}
		if m := incidentNameRE.FindStringSubmatch(e.Name()); m != nil {
			it.kind = m[2]
			if nanos, perr := strconv.ParseInt(m[1], 10, 64); perr == nil {
				it.when = time.Unix(0, nanos).UTC()
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].when.After(out[j].when) })
	return out
}

var iterStemRE = regexp.MustCompile(`^(iter-(\d{4})-(\d{8}T\d{6}Z))`)

func (d *fsData) iterations() []iterItem {
	entries, err := os.ReadDir(d.p.LogsDir())
	if err != nil {
		return nil
	}
	seen := map[string]iterItem{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := iterStemRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		stem := m[1]
		if _, ok := seen[stem]; ok {
			continue
		}
		num, _ := strconv.Atoi(m[2])
		when, _ := time.Parse("20060102T150405Z", m[3])
		seen[stem] = iterItem{stem: stem, num: num, when: when}
	}
	out := make([]iterItem, 0, len(seen))
	for _, it := range seen {
		out = append(out, it)
	}
	// Newest first (stems are timestamp-sortable within an iter number;
	// the full stem string keeps it deterministic).
	sort.Slice(out, func(i, j int) bool { return out[i].stem > out[j].stem })
	return out
}

func (d *fsData) runDetail(id string) (string, error) {
	r, err := runs.Open(d.repo, id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if m, merr := r.ReadManifest(); merr == nil {
		fmt.Fprintf(&b, "run %s\n", m.ID)
		fmt.Fprintf(&b, "started:  %s\n", m.StartTime.Format(time.RFC3339))
		if m.EndTime != nil {
			fmt.Fprintf(&b, "ended:    %s\n", m.EndTime.Format(time.RFC3339))
		}
		if m.ExitOutcome != nil {
			fmt.Fprintf(&b, "outcome:  %s\n", m.ExitOutcome.String())
		}
		fmt.Fprintf(&b, "iters:    %d\n", m.TotalIters)
		fmt.Fprintf(&b, "cost:     $%.2f\n", m.CumulativeCostUSD)
		b.WriteString("\n")
	}
	trs, terr := r.ReadTransitions()
	if terr != nil {
		return "", terr
	}
	fmt.Fprintf(&b, "transitions (%d):\n", len(trs))
	for _, t := range trs {
		reason := ""
		if t.Reason != "" {
			reason = "  reason=" + t.Reason
		}
		fmt.Fprintf(&b, "  %-4d %-8s → %-8s%s\n", t.Iter, t.From, t.To, reason)
	}
	return b.String(), nil
}

func (*fsData) incidentDetail(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path comes from a readdir of the incidents dir
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (d *fsData) iterDetail(stem string) (string, error) {
	var b strings.Builder
	// RenderStem keys on the exact stem, so the iteration the user
	// selected is the one rendered even when iteration numbers collide.
	if err := trace.RenderStem(os.DirFS(d.p.LogsDir()), stem, 200, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}
