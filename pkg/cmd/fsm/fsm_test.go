package fsm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corefsm "github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// scaffold sets up a fake repo with .ralph/state/fsm.json (when state
// non-nil) and an optional run with the given transitions.
func scaffold(t *testing.T, state *corefsm.FSM, withRun bool, trans []runs.Transition) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".ralph", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if state != nil {
		if err := state.Save(repo); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if withRun {
		r, err := runs.Begin(repo)
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		for _, tr := range trans {
			if err := r.AppendTransition(tr); err != nil {
				t.Fatal(err)
			}
		}
		_ = r.Close()
	}
	return repo
}

func newFactory(repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	ios, bufs := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams: ios,
		RepoRoot:  func() (string, error) { return repo, nil },
	}, bufs
}

func TestShowEmitsJSON(t *testing.T) {
	state := corefsm.Fresh()
	state.State = corefsm.StateClean
	state.Iter = 4
	repo := scaffold(t, state, false, nil)
	f, bufs := newFactory(repo)

	if err := runShow(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(bufs.Out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nout:%s", err, bufs.Out.String())
	}
	if got["state"] != "clean" {
		t.Errorf("state = %v, want clean", got["state"])
	}
	if got["iter"].(float64) != 4 {
		t.Errorf("iter = %v, want 4", got["iter"])
	}
}

func TestShowMissingFsmJSONIsFresh(t *testing.T) {
	// fsm.Load returns Fresh() when fsm.json is missing.
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".ralph"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, bufs := newFactory(repo)
	if err := runShow(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	if !strings.Contains(bufs.Out.String(), `"state": "start"`) {
		t.Errorf("expected Fresh state, got:\n%s", bufs.Out.String())
	}
}

func TestGraphMermaidContainsHeader(t *testing.T) {
	state := corefsm.Fresh()
	state.State = corefsm.StateClean
	repo := scaffold(t, state, false, nil)
	f, bufs := newFactory(repo)

	if err := runGraph(context.Background(), &Options{F: f, Run: "latest"}); err != nil {
		t.Fatalf("runGraph: %v", err)
	}
	out := bufs.Out.String()
	for _, want := range []string{
		"```mermaid",
		"stateDiagram-v2",
		"[*] --> start",
		"done --> [*]",
		"failed --> [*]",
		"class clean current",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in mermaid output:\n%s", want, out)
		}
	}
}

func TestGraphRendersEdgeCounts(t *testing.T) {
	state := corefsm.Fresh()
	state.State = corefsm.StateClean
	trans := []runs.Transition{
		{From: "start", To: "clean"},
		{From: "clean", To: "dirty"},
		{From: "clean", To: "dirty"},
		{From: "dirty", To: "clean"},
	}
	repo := scaffold(t, state, true, trans)
	f, bufs := newFactory(repo)

	if err := runGraph(context.Background(), &Options{F: f, Run: "latest"}); err != nil {
		t.Fatalf("runGraph: %v", err)
	}
	out := bufs.Out.String()
	// clean --> dirty observed twice.
	if !strings.Contains(out, "clean --> dirty: 2") {
		t.Errorf("want 'clean --> dirty: 2' in output:\n%s", out)
	}
	if !strings.Contains(out, "dirty --> clean: 1") {
		t.Errorf("want 'dirty --> clean: 1' in output:\n%s", out)
	}
}

func TestGraphNoCountsFallsBackToCanonical(t *testing.T) {
	state := corefsm.Fresh()
	repo := scaffold(t, state, false, nil)
	f, bufs := newFactory(repo)

	if err := runGraph(context.Background(), &Options{F: f, NoCounts: true}); err != nil {
		t.Fatalf("runGraph: %v", err)
	}
	out := bufs.Out.String()
	// With --no-counts we render the canonical edge set without labels.
	if !strings.Contains(out, "clean --> dirty\n") {
		t.Errorf("want bare canonical edge, got:\n%s", out)
	}
	if strings.Contains(out, ": ") {
		t.Errorf("--no-counts should suppress count labels, got:\n%s", out)
	}
}

func TestGraphAllAggregates(t *testing.T) {
	// Two runs each with the same edge; --run=all should sum.
	repo := scaffold(t, corefsm.Fresh(), true, []runs.Transition{
		{From: "clean", To: "dirty"},
	})
	// Second run needs a distinct timestamp.
	r2, err := runs.Begin(repo)
	if err != nil {
		t.Fatal(err)
	}
	// Force the second run to a new id by sleeping one second isn't
	// great in a test; instead just push two transitions into it.
	for i := 0; i < 2; i++ {
		if err := r2.AppendTransition(runs.Transition{From: "clean", To: "dirty"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = r2.Close()

	f, bufs := newFactory(repo)
	if err := runGraph(context.Background(), &Options{F: f, Run: "all"}); err != nil {
		t.Fatalf("runGraph: %v", err)
	}
	out := bufs.Out.String()
	if !strings.Contains(out, "clean --> dirty: 3") {
		t.Errorf("want 'clean --> dirty: 3' (1+2 across runs), got:\n%s", out)
	}
}

func TestGraphSpecificRun(t *testing.T) {
	repo := scaffold(t, corefsm.Fresh(), true, []runs.Transition{
		{From: "clean", To: "dirty"},
	})
	// Discover the id we just made.
	metas, err := runs.List(repo)
	if err != nil || len(metas) != 1 {
		t.Fatalf("List: %v len=%d", err, len(metas))
	}
	id := metas[0].ID

	f, bufs := newFactory(repo)
	if err := runGraph(context.Background(), &Options{F: f, Run: id}); err != nil {
		t.Fatalf("runGraph: %v", err)
	}
	if !strings.Contains(bufs.Out.String(), "clean --> dirty: 1") {
		t.Errorf("want edge from specific run, got:\n%s", bufs.Out.String())
	}
}

func TestGraphInvalidRunErrors(t *testing.T) {
	repo := scaffold(t, corefsm.Fresh(), false, nil)
	f, _ := newFactory(repo)
	err := runGraph(context.Background(), &Options{F: f, Run: "does-not-exist"})
	if err == nil {
		t.Error("nil err for nonexistent run id, want failure")
	}
}

func TestNewCmdFSMSubcommands(t *testing.T) {
	c := NewCmdFSM(&cmdutil.Factory{}, nil, nil)
	if c.Use != "fsm" {
		t.Errorf("Use = %s, want fsm", c.Use)
	}
	subs := map[string]bool{}
	for _, sc := range c.Commands() {
		subs[sc.Name()] = true
	}
	for _, want := range []string{"show", "graph"} {
		if !subs[want] {
			t.Errorf("subcommand %q missing", want)
		}
	}
}

func TestGraphRunFlagCompletesLatestAllAndIDs(t *testing.T) {
	repo := scaffold(t, corefsm.Fresh(), true, nil)
	f, _ := newFactory(repo)
	cmd := NewCmdFSM(f, nil, nil)
	graph, _, err := cmd.Find([]string{"graph"})
	if err != nil {
		t.Fatalf("find graph: %v", err)
	}
	fn, ok := graph.GetFlagCompletionFunc("run")
	if !ok {
		t.Fatal("no completion func for --run")
	}
	got, _ := fn(graph, nil, "")
	hasLatest, hasAll, hasID := false, false, false
	for _, v := range got {
		switch {
		case v == "latest":
			hasLatest = true
		case v == "all":
			hasAll = true
		case strings.Contains(v, "Z"): // run ids are 20060102T150405Z
			hasID = true
		}
	}
	if !hasLatest || !hasAll {
		t.Errorf("--run missing latest/all sentinels (got %v)", got)
	}
	if !hasID {
		t.Errorf("--run missing a run id from scaffolded run (got %v)", got)
	}
}

func TestGraphRunFlagCompletesWithoutRepo(t *testing.T) {
	f := &cmdutil.Factory{
		IOStreams: iostreams.System(),
		RepoRoot:  func() (string, error) { return "", os.ErrNotExist },
	}
	cmd := NewCmdFSM(f, nil, nil)
	graph, _, _ := cmd.Find([]string{"graph"})
	fn, _ := graph.GetFlagCompletionFunc("run")
	got, _ := fn(graph, nil, "")
	if len(got) != 2 || got[0] != "latest" || got[1] != "all" {
		t.Errorf("got %v, want [latest all] when repo lookup fails", got)
	}
}
