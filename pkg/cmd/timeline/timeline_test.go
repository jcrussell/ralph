package timeline

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// scaffoldRunWith creates a repo with one run dir under
// .ralph/state/runs/, writing the given transitions to its
// transitions.jsonl and any narratives to summary.jsonl.
func scaffoldRunWith(t *testing.T, transitions []runs.Transition, narratives map[int]string) (repo string) {
	t.Helper()
	repo = t.TempDir()

	// One run dir; the id format must satisfy runs.parseID so Latest
	// returns it. Use the recommended 20060102T150405Z layout.
	runID := "20260514T120000Z"
	runDir := filepath.Join(repo, ".ralph", "state", "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a minimal manifest so runs.Open's ReadManifest succeeds.
	man := `{"version":1,"id":"` + runID + `","start_time":"2026-05-14T12:00:00Z","total_iters":0,"state_counts":{},"cumulative_cost_usd":0,"cumulative_wallclock_secs":0}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write transitions.
	var buf bytes.Buffer
	for _, tr := range transitions {
		b, err := json.Marshal(tr)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(runDir, "transitions.jsonl"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write narratives via summary.jsonl when provided.
	if len(narratives) > 0 {
		var sum bytes.Buffer
		for iter, narr := range narratives {
			rec := map[string]any{"iter": iter, "narrative": narr}
			b, _ := json.Marshal(rec)
			sum.Write(b)
			sum.WriteByte('\n')
		}
		summaryPath := filepath.Join(repo, ".ralph", "state", "logs", "summary.jsonl")
		if err := os.MkdirAll(filepath.Dir(summaryPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(summaryPath, sum.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func factoryAt(t *testing.T, repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	t.Helper()
	ios, bufs := iostreams.Test()
	rootFn := func() (string, error) { return repo, nil }
	f := &cmdutil.Factory{
		IOStreams: ios,
		RepoRoot:  rootFn,
		Paths:     cmdutil.LazyPaths(rootFn),
	}
	return f, bufs
}

func TestTimeline_FiltersBySince(t *testing.T) {
	old := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 5, 14, 14, 0, 0, 0, time.UTC)
	repo := scaffoldRunWith(t, []runs.Transition{
		{TS: old, Iter: 1, From: "start", To: "clean"},
		{TS: recent, Iter: 2, From: "clean", To: "dirty"},
	}, nil)

	since := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	f, bufs := factoryAt(t, repo)
	err := run(context.Background(), &Options{F: f, Since: since.Format(time.RFC3339)})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := bufs.Out.String()
	if strings.Contains(out, "iter 0001") {
		t.Errorf("old transition not filtered:\n%s", out)
	}
	if !strings.Contains(out, "iter 0002") {
		t.Errorf("recent transition missing:\n%s", out)
	}
}

func TestTimeline_FiltersByStateAndReason(t *testing.T) {
	repo := scaffoldRunWith(t, []runs.Transition{
		{TS: time.Unix(1, 0).UTC(), Iter: 1, From: "start", To: "clean"},
		{TS: time.Unix(2, 0).UTC(), Iter: 2, From: "clean", To: "dirty"},
		{TS: time.Unix(3, 0).UTC(), Iter: 3, From: "dirty", To: "done", Reason: "queue_empty"},
	}, nil)

	// --state=dirty matches both rows touching dirty.
	f, bufs := factoryAt(t, repo)
	if err := run(context.Background(), &Options{F: f, State: "dirty"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := bufs.Out.String()
	if strings.Contains(out, "iter 0001") || !strings.Contains(out, "iter 0002") || !strings.Contains(out, "iter 0003") {
		t.Errorf("--state=dirty filter wrong:\n%s", out)
	}

	// --reason=queue_empty matches only the done row.
	f2, bufs2 := factoryAt(t, repo)
	if err := run(context.Background(), &Options{F: f2, Reason: "queue_empty"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	out2 := bufs2.Out.String()
	if strings.Contains(out2, "iter 0001") || strings.Contains(out2, "iter 0002") {
		t.Errorf("--reason filter leaks unrelated rows:\n%s", out2)
	}
	if !strings.Contains(out2, "iter 0003") {
		t.Errorf("--reason match missing:\n%s", out2)
	}
}

func TestTimeline_JoinsNarrative(t *testing.T) {
	repo := scaffoldRunWith(t, []runs.Transition{
		{TS: time.Unix(1, 0).UTC(), Iter: 1, From: "start", To: "clean"},
		{TS: time.Unix(2, 0).UTC(), Iter: 2, From: "clean", To: "clean"},
	}, map[int]string{
		2: "clean → clean: claimed x, 1 commit, gate green",
	})

	f, bufs := factoryAt(t, repo)
	if err := run(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := bufs.Out.String()
	if !strings.Contains(out, "claimed x, 1 commit, gate green") {
		t.Errorf("narrative not joined:\n%s", out)
	}
	// iter 1 has no narrative in the map; ensure it still appears but
	// without a tail.
	if !strings.Contains(out, "iter 0001  start → clean") {
		t.Errorf("iter 1 missing or malformatted:\n%s", out)
	}
}

func TestTimeline_NoTransitionsFile(t *testing.T) {
	// Repo with no runs/ dir at all.
	repo := t.TempDir()
	f, bufs := factoryAt(t, repo)
	if err := run(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(bufs.ErrOut.String(), "no runs found") {
		t.Errorf("expected 'no runs found' on ErrOut, got: %q", bufs.ErrOut.String())
	}
}

func TestTimeline_JSONMode(t *testing.T) {
	repo := scaffoldRunWith(t, []runs.Transition{
		{TS: time.Unix(1, 0).UTC(), Iter: 1, From: "start", To: "clean"},
	}, nil)

	f, bufs := factoryAt(t, repo)
	if err := run(context.Background(), &Options{F: f, JSON: true}); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := strings.TrimSpace(bufs.Out.String())
	var got runs.Transition
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal JSON line %q: %v", out, err)
	}
	if got.Iter != 1 || got.From != "start" || got.To != "clean" {
		t.Errorf("JSON row = %+v, want iter=1 start→clean", got)
	}
}

func TestTimelineStateFlagComplete(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdTimeline(f, nil)
	fn, ok := cmd.GetFlagCompletionFunc("state")
	if !ok {
		t.Fatal("no completion func for --state")
	}
	got, _ := fn(cmd, nil, "")
	for _, want := range []string{"clean", "dirty", "review", "done", "failed"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("--state missing %q (got %v)", want, got)
		}
	}
}
