package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func TestRenderHappyPath(t *testing.T) {
	repo := t.TempDir()

	// summary.jsonl with two records: one in window, one before.
	logs := filepath.Join(repo, ".ralph/state/logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	rec1, _ := json.Marshal(map[string]any{
		"iter":      1,
		"state":     "clean",
		"timestamp": time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339),
		"bd_diff":   map[string][]string{"closed": {"a-1", "a-2"}, "created": {"a-3"}},
	})
	rec2, _ := json.Marshal(map[string]any{
		"iter":      2,
		"state":     "dirty",
		"timestamp": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		"bd_diff":   map[string][]string{"closed": {"old-id"}},
	})
	body := append(rec1, '\n')
	body = append(body, rec2...)
	body = append(body, '\n')
	if err := os.WriteFile(filepath.Join(logs, "summary.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	// One run manifest.
	runDir := filepath.Join(repo, ".ralph/state/runs/r1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(-10 * time.Minute).UTC()
	manifest, _ := json.Marshal(runs.Manifest{
		StartTime:               time.Now().Add(-60 * time.Minute).UTC(),
		EndTime:                 &end,
		TotalIters:              5,
		StateCounts:             map[string]int{"clean": 3, "dirty": 2},
		CumulativeCostUSD:       0.42,
		CumulativeWallclockSecs: 3000,
	})
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	// One incident.
	incDir := filepath.Join(repo, ".ralph/state/incidents")
	if err := os.MkdirAll(incDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incDir, "100-revert.md"), []byte("# revert: dirty streak 3\n\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	since := time.Now().Add(-24 * time.Hour)
	if err := Render(context.Background(), repo, since, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"# ralph report (since ",
		"## Work done",
		"closed: a-1, a-2",
		"created: a-3",
		"## Commits",
		"## Incidents",
		"100-revert.md — revert: dirty streak 3",
		"## State distribution",
		"- clean: 3",
		"- dirty: 2",
		"## Cost",
		"- iters: 5",
		"- cost: $0.4200",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing in output: %q\n---\n%s", want, out)
		}
	}
	// Out-of-window record must not appear.
	if strings.Contains(out, "old-id") {
		t.Errorf("included out-of-window bd_diff:\n%s", out)
	}
}

func TestRenderEmpty(t *testing.T) {
	repo := t.TempDir()
	var buf bytes.Buffer
	since := time.Now().Add(-1 * time.Hour)
	if err := Render(context.Background(), repo, since, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	// Every section header is still present (no-data placeholders).
	for _, h := range []string{"## Work done", "## Commits", "## Incidents", "## State distribution", "## Cost"} {
		if !strings.Contains(out, h) {
			t.Errorf("section %q missing in empty report", h)
		}
	}
	if !strings.Contains(out, "no bead activity") {
		t.Errorf("expected 'no bead activity' placeholder")
	}
}

// newFactory wires a bare Factory over iostreams.Test() pointing at repo.
func newFactory(t *testing.T, repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	t.Helper()
	io, bufs := iostreams.Test()
	rootFn := func() (string, error) { return repo, nil }
	return &cmdutil.Factory{IOStreams: io, RepoRoot: rootFn, Paths: cmdutil.LazyPaths(rootFn)}, bufs
}

// scaffoldBeads seeds a summary.jsonl with one in-window record closing two
// beads, so the work_done section has content.
func scaffoldBeads(t *testing.T, repo string) {
	t.Helper()
	logs := filepath.Join(repo, ".ralph/state/logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, _ := json.Marshal(map[string]any{
		"iter":      1,
		"state":     "clean",
		"timestamp": time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
		"bd_diff":   map[string][]string{"closed": {"a-1", "a-2"}},
	})
	if err := os.WriteFile(filepath.Join(logs, "summary.jsonl"), append(rec, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReportSectionFilter(t *testing.T) {
	repo := t.TempDir()
	scaffoldBeads(t, repo)
	f, bufs := newFactory(t, repo)

	opts := &Options{F: f, Since: "24h", Sections: []string{"commits"}}
	if err := run(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := bufs.Out.String()
	if !strings.Contains(out, "## Commits") {
		t.Errorf("commits section missing:\n%s", out)
	}
	for _, absent := range []string{"## Work done", "## Incidents", "## State distribution", "## Cost"} {
		if strings.Contains(out, absent) {
			t.Errorf("section %q should be filtered out:\n%s", absent, out)
		}
	}
}

func TestReportJSON(t *testing.T) {
	repo := t.TempDir()
	scaffoldBeads(t, repo)
	f, bufs := newFactory(t, repo)

	exp, err := cmdutil.NewExporter(strings.Join(reportFields, ","), "", "", reportFields)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	if err := run(context.Background(), &Options{F: f, Since: "24h", Exporter: exp}); err != nil {
		t.Fatalf("run: %v", err)
	}
	var rpt Report
	if err := json.Unmarshal(bufs.Out.Bytes(), &rpt); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, bufs.Out.String())
	}
	if len(rpt.WorkDone.Closed) != 2 || rpt.WorkDone.Closed[0] != "a-1" {
		t.Errorf("work_done.closed = %v, want [a-1 a-2]", rpt.WorkDone.Closed)
	}
	// git is unavailable in this non-git tempdir, but commits must still
	// serialize as [] (non-nil), not null — so `.commits[]` never errors.
	if rpt.Commits == nil {
		t.Error("commits serialized as null; want [] even when git is unavailable")
	}
}

// TestReportJQCommitsCount is the headline use case: "how many commits has
// ralph made" via --json --jq '.commits | length'.
func TestReportJQCommitsCount(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "commit", "--allow-empty", "-q", "-m", "one")
	runGit(t, repo, "commit", "--allow-empty", "-q", "-m", "two")
	runGit(t, repo, "commit", "--allow-empty", "-q", "-m", "three")

	f, bufs := newFactory(t, repo)
	exp, err := cmdutil.NewExporter(strings.Join(reportFields, ","), ".commits | length", "", reportFields)
	if err != nil {
		t.Fatalf("NewExporter: %v", err)
	}
	if err := run(context.Background(), &Options{F: f, Since: "24h", Exporter: exp}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(bufs.Out.String()); got != "3" {
		t.Errorf("commit count = %q, want %q\n(full output: %s)", got, "3", bufs.Out.String())
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestReportSectionAndJSONExclusive verifies cobra rejects --section with --json.
func TestReportSectionAndJSONExclusive(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdReport(f, func(context.Context, *Options) error { return nil })
	cmd.SetArgs([]string{"--section", "commits", "--json", "commits"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error for --section with --json, got nil")
	}
}

func TestReportInvalidSection(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdReport(f, func(context.Context, *Options) error { return nil })
	cmd.SetArgs([]string{"--section", "bogus"})
	err := cmd.Execute()
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v, want FlagError", err)
	}
}

func TestNewCmdReportInvalidSince(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdReport(f, func(context.Context, *Options) error { return nil })
	cmd.SetArgs([]string{"--since", "bogus"})
	err := cmd.Execute()
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v, want FlagError", err)
	}
}
