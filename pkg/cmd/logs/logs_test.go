package logs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func newRepoWithRecords(t *testing.T, records []map[string]any) (repo string) {
	t.Helper()
	repo = t.TempDir()
	dir := filepath.Join(repo, ".ralph", "state", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "summary.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	for _, r := range records {
		b, _ := json.Marshal(r)
		f.Write(b)
		f.Write([]byte("\n"))
	}
	return repo
}

func newFactory(repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	io, bufs := iostreams.Test()
	rootFn := func() (string, error) { return repo, nil }
	return &cmdutil.Factory{IOStreams: io, RepoRoot: rootFn, Paths: cmdutil.LazyPaths(rootFn)}, bufs
}

func TestDefaultPrintsNarrative(t *testing.T) {
	repo := newRepoWithRecords(t, []map[string]any{
		{"iter": 1, "state": "clean", "narrative": "first iter"},
		{"iter": 2, "state": "dirty", "narrative": "second iter"},
		{"iter": 3, "state": "clean"}, // no narrative → falls back to state
	})
	f, bufs := newFactory(repo)
	opts := &Options{F: f}
	if err := run(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Split(strings.TrimRight(bufs.Out.String(), "\n"), "\n")
	want := []string{
		"iter 0001  first iter",
		"iter 0002  second iter",
		"iter 0003  clean",
	}
	for i, w := range want {
		if i >= len(lines) || lines[i] != w {
			t.Errorf("line[%d] = %q, want %q", i, safeAt(lines, i), w)
		}
	}
}

func TestIterFiltersToOneRecord(t *testing.T) {
	repo := newRepoWithRecords(t, []map[string]any{
		{"iter": 1, "state": "clean", "narrative": "a"},
		{"iter": 2, "state": "dirty", "narrative": "b"},
		{"iter": 3, "state": "clean", "narrative": "c"},
	})
	f, bufs := newFactory(repo)
	opts := &Options{F: f, Iter: 2}
	if err := run(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := bufs.Out.String()
	if !strings.Contains(out, `"iter": 2`) || !strings.Contains(out, `"narrative": "b"`) {
		t.Errorf("--iter output missing expected fields:\n%s", out)
	}
	if strings.Contains(out, `"narrative": "a"`) || strings.Contains(out, `"narrative": "c"`) {
		t.Errorf("--iter output included unfiltered records:\n%s", out)
	}
}

func TestJSONMode(t *testing.T) {
	recs := []map[string]any{
		{"iter": 1, "state": "clean", "narrative": "a"},
		{"iter": 2, "state": "dirty", "narrative": "b"},
	}
	repo := newRepoWithRecords(t, recs)
	f, bufs := newFactory(repo)
	opts := &Options{F: f, JSON: true}
	if err := run(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Split(strings.TrimRight(bufs.Out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Errorf("line %d not valid JSON: %v", i, err)
			continue
		}
		// iter round-trips as float64 via encoding/json.
		if int(got["iter"].(float64)) != recs[i]["iter"].(int) {
			t.Errorf("line %d: iter = %v, want %v", i, got["iter"], recs[i]["iter"])
		}
	}
}

// TestJQFilter checks the built-in per-record jq: select(...) keeps only
// matching records and emits them as compact JSON, one per line.
func TestJQFilter(t *testing.T) {
	repo := newRepoWithRecords(t, []map[string]any{
		{"iter": 1, "state": "clean", "narrative": "a"},
		{"iter": 2, "state": "dirty", "narrative": "b"},
		{"iter": 3, "state": "dirty", "narrative": "c"},
	})
	f, bufs := newFactory(repo)
	opts := &Options{F: f, JQ: `select(.state=="dirty") | .iter`}
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := run(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.Split(strings.TrimRight(bufs.Out.String(), "\n"), "\n")
	want := []string{"2", "3"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestJQInvalidIsFlagError checks a malformed --jq is rejected as a FlagError
// (exit 2) at Validate time, before any streaming.
func TestJQInvalidIsFlagError(t *testing.T) {
	opts := &Options{F: nil, JQ: ".foo |"}
	err := opts.Validate()
	if err == nil {
		t.Fatal("want FlagError for bad jq, got nil")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Fatalf("got %T, want *cmdutil.FlagError", err)
	}
}

func TestMissingFileError(t *testing.T) {
	repo := t.TempDir() // no .ralph/state/logs/summary.jsonl
	f, _ := newFactory(repo)
	opts := &Options{F: f}
	err := run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "no records") {
		t.Errorf("expected 'no records' error, got %v", err)
	}
}

func TestTailCancelsCleanly(t *testing.T) {
	repo := newRepoWithRecords(t, []map[string]any{{"iter": 1, "state": "clean", "narrative": "ok"}})
	f, bufs := newFactory(repo)
	opts := &Options{F: f, Tail: true, pollInterval: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, opts) }()

	// Give the goroutine time to print the existing record, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("tail did not exit after cancel")
	}
	if !strings.Contains(bufs.Out.String(), "iter 0001") {
		t.Errorf("tail did not print initial record:\n%s", bufs.Out.String())
	}
}

func TestNewCmdLogsRejectsConflictingFlags(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdLogs(f, func(context.Context, *Options) error { return nil })
	cmd.SetArgs([]string{"--iter", "1", "--tail"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected mutually-exclusive error")
	}
	// cobra's MarkFlagsMutuallyExclusive error format includes the flag
	// names; matching on a stable substring keeps the test resilient.
	if !strings.Contains(err.Error(), "[iter tail]") {
		t.Errorf("err = %v, want substring '[iter tail]'", err)
	}
}

func TestNewCmdLogsRejectsJSONWithJQ(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdLogs(f, func(context.Context, *Options) error { return nil })
	cmd.SetArgs([]string{"--json", "--jq", ".state"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected mutually-exclusive error for --json with --jq")
	}
	if !strings.Contains(err.Error(), "[jq json]") && !strings.Contains(err.Error(), "[json jq]") {
		t.Errorf("err = %v, want a json/jq mutual-exclusion error", err)
	}
}

func safeAt(ss []string, i int) string {
	if i < 0 || i >= len(ss) {
		return "<oob>"
	}
	return ss[i]
}
