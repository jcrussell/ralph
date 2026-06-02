package explore

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func newFactory(repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	io, bufs := iostreams.Test() // non-TTY: exploreRun takes the listing path
	rootFn := func() (string, error) { return repo, nil }
	return &cmdutil.Factory{IOStreams: io, RepoRoot: rootFn, Paths: cmdutil.LazyPaths(rootFn)}, bufs
}

func fixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()

	r, err := runs.Begin(repo)
	if err != nil {
		t.Fatalf("runs.Begin: %v", err)
	}
	_ = r.Close()

	state := filepath.Join(repo, ".ralph", "state")
	writeFile(t, filepath.Join(state, "logs", "iter-0001-20260101T000000Z.json"), "{}")
	nanos := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	writeFile(t, filepath.Join(state, "incidents", strconv.FormatInt(nanos, 10)+"-revert.md"), "x")
	return repo
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExploreNonTTYFallback(t *testing.T) {
	repo := fixture(t)
	f, bufs := newFactory(repo)
	if err := exploreRun(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("exploreRun: %v", err)
	}
	out := bufs.Out.String()
	for _, want := range []string{"Runs (1)", "Incidents (1)", "Iterations (1)", "iter-0001-20260101T000000Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing missing %q\n%s", want, out)
		}
	}
}

func TestNewCmdExploreFlagParsing(t *testing.T) {
	f, _ := newFactory(t.TempDir())
	var got *Options
	cmd := NewCmdExplore(f, func(_ context.Context, o *Options) error { got = o; return nil })
	cmd.SetArgs([]string{"--no-tui"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil || !got.NoTUI {
		t.Fatalf("parsed opts = %+v, want NoTUI=true", got)
	}
}
