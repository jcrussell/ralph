package gc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// fixedNow is the clock used by tests: cutoff with --older-than=30d is
// 2026-05-03T00:00:00Z, so "old" < that < "recent".
var fixedNow = time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)

const (
	oldTS    = "20260101T000000Z" // well before the cutoff
	recentTS = "20260601T000000Z" // after the cutoff
)

func newFactory(repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	io, bufs := iostreams.Test()
	rootFn := func() (string, error) { return repo, nil }
	return &cmdutil.Factory{IOStreams: io, RepoRoot: rootFn, Paths: cmdutil.LazyPaths(rootFn)}, bufs
}

// writeFile creates path (and parents) with some bytes.
func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fixture builds a .ralph/state tree with old + recent runs, iteration
// artifacts, incidents, a summary.jsonl, and a few unparseable names.
func fixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	state := filepath.Join(repo, ".ralph", "state")

	// Runs: old (eligible), recent (kept), unparseable (skipped).
	writeFile(t, filepath.Join(state, "runs", oldTS, "manifest.json"))
	writeFile(t, filepath.Join(state, "runs", recentTS, "manifest.json"))
	writeFile(t, filepath.Join(state, "runs", "not-a-timestamp", "manifest.json"))

	// Iteration artifacts: old group (eligible) with all sibling files,
	// recent group (kept), plus non-iter files that must be left alone.
	logs := filepath.Join(state, "logs")
	for _, suffix := range []string{".json", "-prompt.txt", "-stdout.txt", "-stderr.txt"} {
		writeFile(t, filepath.Join(logs, "iter-0001-"+oldTS+suffix))
	}
	writeFile(t, filepath.Join(logs, "iter-0002-"+recentTS+".json"))
	writeFile(t, filepath.Join(logs, "summary.jsonl"))
	writeFile(t, filepath.Join(logs, "orchestrator.log"))

	// Incidents: old (eligible), recent (kept), unparseable (skipped).
	inc := filepath.Join(state, "incidents")
	writeFile(t, filepath.Join(inc, nanoName(2026, 1, 1, "revert")))
	writeFile(t, filepath.Join(inc, nanoName(2026, 6, 1, "terminal-failure")))
	writeFile(t, filepath.Join(inc, "weird.md"))

	return repo
}

func nanoName(year, month, day int, kind string) string {
	nanos := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).UnixNano()
	return strings.Join([]string{itoa(nanos), kind}, "-") + ".md"
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func optsFor(repo string, force bool) (*Options, *iostreams.TestBuffers) {
	f, bufs := newFactory(repo)
	dur, _ := cmdutil.ParseDuration("30d")
	return &Options{F: f, OlderThan: "30d", Force: force, dur: dur, now: func() time.Time { return fixedNow }}, bufs
}

func TestGCDryRunListsOldOnlyAndDeletesNothing(t *testing.T) {
	repo := fixture(t)
	opts, bufs := optsFor(repo, false)
	if err := gcRun(context.Background(), opts); err != nil {
		t.Fatalf("gcRun: %v", err)
	}
	out := bufs.Out.String()
	for _, want := range []string{oldTS, "iter-0001-" + oldTS, "revert.md", "nothing deleted (dry-run)"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q\n%s", want, out)
		}
	}
	// Recent items must not be listed.
	if strings.Contains(out, "iter-0002-"+recentTS) || strings.Contains(out, "runs/"+recentTS) {
		t.Errorf("dry-run listed a recent item\n%s", out)
	}
	// Nothing was actually removed.
	if _, err := os.Stat(filepath.Join(repo, ".ralph", "state", "runs", oldTS)); err != nil {
		t.Errorf("dry-run deleted the old run: %v", err)
	}
}

func TestGCForceDeletesOldKeepsRecentAndSummary(t *testing.T) {
	repo := fixture(t)
	opts, bufs := optsFor(repo, true)
	if err := gcRun(context.Background(), opts); err != nil {
		t.Fatalf("gcRun: %v", err)
	}
	state := filepath.Join(repo, ".ralph", "state")

	// Gone:
	for _, gone := range []string{
		filepath.Join(state, "runs", oldTS),
		filepath.Join(state, "logs", "iter-0001-"+oldTS+".json"),
		filepath.Join(state, "logs", "iter-0001-"+oldTS+"-prompt.txt"),
		filepath.Join(state, "incidents", nanoName(2026, 1, 1, "revert")),
	} {
		if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected %s removed, stat err=%v", gone, err)
		}
	}
	// Kept:
	for _, kept := range []string{
		filepath.Join(state, "runs", recentTS),
		filepath.Join(state, "logs", "iter-0002-"+recentTS+".json"),
		filepath.Join(state, "logs", "summary.jsonl"),
		filepath.Join(state, "logs", "orchestrator.log"),
		filepath.Join(state, "incidents", nanoName(2026, 6, 1, "terminal-failure")),
		filepath.Join(state, "runs", "not-a-timestamp"),
		filepath.Join(state, "incidents", "weird.md"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("expected %s kept, stat err=%v", kept, err)
		}
	}
	if !strings.Contains(bufs.Out.String(), "deleted ") {
		t.Errorf("force output missing deletion summary\n%s", bufs.Out.String())
	}
}

func TestGCMissingDirsEmptyPlan(t *testing.T) {
	repo := t.TempDir() // no .ralph at all
	opts, bufs := optsFor(repo, false)
	if err := gcRun(context.Background(), opts); err != nil {
		t.Fatalf("gcRun: %v", err)
	}
	if !strings.Contains(bufs.Out.String(), "nothing older than the cutoff") {
		t.Errorf("expected empty-plan message, got\n%s", bufs.Out.String())
	}
}

func TestGCActiveRunProtected(t *testing.T) {
	repo := fixture(t)
	// Make the lock live (this process) and the latest run "old" so age
	// alone would delete it — protection must hold it back.
	state := filepath.Join(repo, ".ralph", "state")
	if err := os.RemoveAll(filepath.Join(state, "runs", recentTS)); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(state, "pid.lock")
	if err := os.WriteFile(lockPath, []byte(itoa(int64(os.Getpid()))+"\n"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	opts, bufs := optsFor(repo, true)
	if err := gcRun(context.Background(), opts); err != nil {
		t.Fatalf("gcRun: %v", err)
	}
	// oldTS is now the latest run; with a live lock it is protected.
	if _, err := os.Stat(filepath.Join(state, "runs", oldTS)); err != nil {
		t.Errorf("active run %s was deleted: %v", oldTS, err)
	}
	if !strings.Contains(bufs.Out.String(), "skipped active run "+oldTS) {
		t.Errorf("expected skipped-active note, got\n%s", bufs.Out.String())
	}
}

func TestGCJSON(t *testing.T) {
	repo := fixture(t)
	opts, bufs := optsFor(repo, false)
	exp, err := cmdutil.NewExporter(strings.Join(gcFields, ","), "", "", gcFields)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	opts.Exporter = exp
	if err := gcRun(context.Background(), opts); err != nil {
		t.Fatalf("gcRun: %v", err)
	}
	var pl Plan
	if err := json.Unmarshal(bufs.Out.Bytes(), &pl); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, bufs.Out.String())
	}
	if len(pl.Runs) != 1 || len(pl.Iterations) != 1 || len(pl.Incidents) != 1 {
		t.Errorf("plan counts: runs=%d iters=%d incidents=%d, want 1/1/1",
			len(pl.Runs), len(pl.Iterations), len(pl.Incidents))
	}
	if pl.Applied {
		t.Errorf("dry-run plan marked Applied")
	}
	if pl.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", pl.TotalCount)
	}
}

// TestGCJQ exercises the built-in jq path: --json --jq '.total_count'.
func TestGCJQ(t *testing.T) {
	repo := fixture(t)
	opts, bufs := optsFor(repo, false)
	exp, err := cmdutil.NewExporter(strings.Join(gcFields, ","), ".total_count", "", gcFields)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	opts.Exporter = exp
	if err := gcRun(context.Background(), opts); err != nil {
		t.Fatalf("gcRun: %v", err)
	}
	if got := strings.TrimSpace(bufs.Out.String()); got != "3" {
		t.Errorf("--jq '.total_count' = %q, want %q", got, "3")
	}
}

func TestNewCmdGCFlagParsing(t *testing.T) {
	f, _ := newFactory(t.TempDir())

	var got *Options
	cmd := NewCmdGC(f, func(_ context.Context, o *Options) error { got = o; return nil })
	cmd.SetArgs([]string{"--older-than", "30d", "-f"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil || got.OlderThan != "30d" || !got.Force {
		t.Fatalf("parsed opts = %+v, want OlderThan=30d Force=true", got)
	}

	// Missing --older-than is a FlagError (exit 2).
	cmd = NewCmdGC(f, func(_ context.Context, _ *Options) error { return nil })
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Fatalf("missing --older-than: err = %v, want *cmdutil.FlagError", err)
	}

	// Bad duration is also a FlagError.
	cmd = NewCmdGC(f, func(_ context.Context, _ *Options) error { return nil })
	cmd.SetArgs([]string{"--older-than", "banana"})
	if err := cmd.Execute(); !errors.As(err, &fe) {
		t.Fatalf("bad --older-than: err = %v, want *cmdutil.FlagError", err)
	}
}
