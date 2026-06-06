package explore

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/paths"
	"github.com/jcrussell/ralph/internal/runs"
)

func newFSDataFixture(t *testing.T) *fsData {
	t.Helper()
	repo := t.TempDir()
	p := paths.New(repo)

	// A real run with a manifest + one transition.
	r, err := runs.Begin(repo)
	if err != nil {
		t.Fatalf("runs.Begin: %v", err)
	}
	if err := r.AppendTransition(runs.Transition{Iter: 1, From: "start", To: "clean", Reason: "ok"}); err != nil {
		t.Fatalf("AppendTransition: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Iteration artifacts (only the .json is required for RenderStem).
	logs := p.LogsDir()
	if err := os.MkdirAll(logs, 0o750); err != nil {
		t.Fatal(err)
	}
	stem := "iter-0001-20260101T000000Z"
	writeFileT(t, filepath.Join(logs, stem+".json"), `{"iter":1,"narrative":"hello iter"}`)
	writeFileT(t, filepath.Join(logs, stem+"-prompt.txt"), "the prompt\n")
	writeFileT(t, filepath.Join(logs, "summary.jsonl"), "{}\n") // must be ignored

	// An incident.
	inc := p.IncidentsDir()
	if err := os.MkdirAll(inc, 0o750); err != nil {
		t.Fatal(err)
	}
	nanos := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	writeFileT(t, filepath.Join(inc, strconv.FormatInt(nanos, 10)+"-revert.md"), "# incident body\n")

	return newFSData(repo, p)
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFSDataLists(t *testing.T) {
	d := newFSDataFixture(t)

	if got := d.runs(); len(got) != 1 {
		t.Errorf("runs() = %d items, want 1", len(got))
	}
	incs := d.incidents()
	if len(incs) != 1 || incs[0].kind != "revert" {
		t.Errorf("incidents() = %+v, want 1 item kind=revert", incs)
	}
	iters := d.iterations()
	if len(iters) != 1 || iters[0].stem != "iter-0001-20260101T000000Z" || iters[0].num != 1 {
		t.Errorf("iterations() = %+v, want 1 item stem=iter-0001-... num=1", iters)
	}
}

func TestFSDataDetails(t *testing.T) {
	d := newFSDataFixture(t)

	runID := d.runs()[0].id
	rd, err := d.runDetail(runID)
	if err != nil {
		t.Fatalf("runDetail: %v", err)
	}
	for _, want := range []string{"run " + runID, "transitions (1)", "start", "clean", "reason=ok"} {
		if !strings.Contains(rd, want) {
			t.Errorf("runDetail missing %q\n%s", want, rd)
		}
	}

	id, err := d.iterDetail("iter-0001-20260101T000000Z")
	if err != nil {
		t.Fatalf("iterDetail: %v", err)
	}
	if !strings.Contains(id, "hello iter") || !strings.Contains(id, "the prompt") {
		t.Errorf("iterDetail missing content\n%s", id)
	}

	incPath := d.incidents()[0].path
	body, err := d.incidentDetail(incPath)
	if err != nil {
		t.Fatalf("incidentDetail: %v", err)
	}
	if !strings.Contains(body, "incident body") {
		t.Errorf("incidentDetail missing content\n%s", body)
	}
}

func TestFSDataIterRaw(t *testing.T) {
	d := newFSDataFixture(t)
	raw, err := d.iterRaw("iter-0001-20260101T000000Z")
	if err != nil {
		t.Fatalf("iterRaw: %v", err)
	}
	// Concatenates the raw .json + -prompt.txt for the stem.
	for _, want := range []string{"hello iter", "the prompt"} {
		if !strings.Contains(raw, want) {
			t.Errorf("iterRaw missing %q\n%s", want, raw)
		}
	}
	// Must not pull in unrelated files (summary.jsonl is not stem-prefixed).
	if strings.Contains(raw, "summary") {
		t.Errorf("iterRaw leaked a non-stem file\n%s", raw)
	}
}

func TestSearchCorpusOverFSData(t *testing.T) {
	d := newFSDataFixture(t)

	// "hello iter" lives only in the iteration's raw .json body.
	hits := searchCorpus(d, "hello iter")
	if len(hits) != 1 || hits[0].cat != catIterations {
		t.Fatalf("searchCorpus(hello iter) = %+v, want 1 iteration hit", hits)
	}

	// "incident body" lives only in the incident markdown.
	hits = searchCorpus(d, "incident body")
	if len(hits) != 1 || hits[0].cat != catIncidents {
		t.Fatalf("searchCorpus(incident body) = %+v, want 1 incident hit", hits)
	}

	// A transition reason lives in the run detail (manifest + transitions).
	hits = searchCorpus(d, "reason=ok")
	if len(hits) != 1 || hits[0].cat != catRuns {
		t.Fatalf("searchCorpus(reason=ok) = %+v, want 1 run hit", hits)
	}
}

func TestFSDataMissingDirs(t *testing.T) {
	repo := t.TempDir() // no .ralph at all
	d := newFSData(repo, paths.New(repo))
	if got := d.runs(); got != nil {
		t.Errorf("runs() = %v, want nil", got)
	}
	if got := d.incidents(); got != nil {
		t.Errorf("incidents() = %v, want nil", got)
	}
	if got := d.iterations(); got != nil {
		t.Errorf("iterations() = %v, want nil", got)
	}
}
