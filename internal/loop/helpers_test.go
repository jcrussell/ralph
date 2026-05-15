package loop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/runner"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// fakeRunner records every Run call and returns the next queued
// session/mode. Tests append to Sessions in the order they expect
// invocations; once exhausted, Run returns the last appended session
// or a default ModeOK envelope.
type fakeRunner struct {
	mu       sync.Mutex
	Calls    int
	Prompts  []string
	Cwds     []string
	Sessions []*runner.Session
	Errs     []error
}

func (f *fakeRunner) Run(_ context.Context, prompt, cwd string, _ []string) (*runner.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Prompts = append(f.Prompts, prompt)
	f.Cwds = append(f.Cwds, cwd)
	idx := f.Calls
	f.Calls++
	if idx < len(f.Errs) && f.Errs[idx] != nil {
		return nil, f.Errs[idx]
	}
	if idx < len(f.Sessions) {
		return f.Sessions[idx], nil
	}
	// Default: a healthy session with $0.01 cost.
	return &runner.Session{
		ExitCode: 0,
		Duration: time.Second,
		Stdout:   `{"total_cost_usd":0.01,"num_turns":1,"subtype":"success"}`,
		Envelope: &runner.Envelope{TotalCostUSD: 0.01, NumTurns: 1, Subtype: "success", Raw: map[string]any{"subtype": "success"}},
	}, nil
}

// fakeBD lets tests pin Ready/List output by (label) and List(status,label),
// and Snapshot output. The zero value is empty everywhere.
type fakeBD struct {
	ReadyByLabel    map[string][]bd.Issue
	ListByStatusLab map[string][]bd.Issue
	Snap            *bd.Snapshot
	Err             error
}

func (b *fakeBD) Ready(_ context.Context, label string) ([]bd.Issue, error) {
	if b.Err != nil {
		return nil, b.Err
	}
	return b.ReadyByLabel[label], nil
}

func (b *fakeBD) List(_ context.Context, status, label string) ([]bd.Issue, error) {
	if b.Err != nil {
		return nil, b.Err
	}
	return b.ListByStatusLab[status+"/"+label], nil
}

func (b *fakeBD) Snapshot(_ context.Context) (*bd.Snapshot, error) {
	if b.Snap != nil {
		return b.Snap, nil
	}
	return &bd.Snapshot{Status: map[string]string{}}, nil
}

// fakeClock records Sleep durations and pins Now to a deterministic
// instant that advances 1s per Sleep call (so iter timestamps differ).
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	Sleeps []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.now
	c.now = c.now.Add(time.Second)
	return t
}

func (c *fakeClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Sleeps = append(c.Sleeps, d)
}

// scaffoldRepo initializes a throwaway repo with a .ralph/ tree
// containing the minimal prompts needed to satisfy promptlib.Render
// for every loop state. Returns the absolute repo path.
func scaffoldRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	promptsDir := filepath.Join(dir, ".ralph", "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	for _, s := range []string{"clean", "dirty", "revert", "review"} {
		body := "PROMPT " + s + " iter={{.Iter}}\n"
		if err := os.WriteFile(filepath.Join(promptsDir, s+".md"), []byte(body), 0o644); err != nil {
			t.Fatalf("write prompt %s: %v", s, err)
		}
	}
	return dir
}

// markRepoDirty writes a tracked-modified file so git.Clean returns false.
func markRepoDirty(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, "file.txt")
	for _, args := range [][]string{
		{"-C", repo, "rev-parse", "--verify", "HEAD"},
	} {
		_ = exec.Command("git", args...).Run()
	}
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	for _, args := range [][]string{
		{"add", "file.txt"},
		{"commit", "-q", "-m", "track"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(path, []byte("v2-dirty"), 0o644); err != nil {
		t.Fatalf("dirty: %v", err)
	}
}

// writeExecutableHook drops a script at the given path with chmod +x,
// creating parent dirs. Body should start with a shebang.
func writeExecutableHook(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir hook %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write hook %s: %v", path, err)
	}
}

// baseOpts assembles a valid Options for testing. Tests override
// fields as needed.
func baseOpts(t *testing.T, repo string) Options {
	t.Helper()
	ios, _ := iostreams.Test()
	cfg := config.Defaults()
	// Tests do not exercise gate by default — every per-state-gate hook
	// missing should be reported as NotRun, which is the natural state
	// when no hook is installed.
	cfg.Gate.RunWhen = "always"
	return Options{
		Repo: repo,
		Cfg:  cfg,
		IO:   ios,
		BD:   &fakeBD{},
	}
}

// readSummary parses every line of .ralph/state/logs/summary.jsonl.
func readSummary(t *testing.T, repo string) []IterRecord {
	t.Helper()
	path := filepath.Join(repo, ".ralph", "state", "logs", "summary.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open summary: %v", err)
	}
	defer f.Close()
	var out []IterRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec IterRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("decode summary line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan summary: %v", err)
	}
	return out
}

// fsmAt loads the persisted FSM under repo.
func fsmAt(t *testing.T, repo string) *fsm.FSM {
	t.Helper()
	f, err := fsm.Load(repo)
	if err != nil {
		t.Fatalf("fsm.Load: %v", err)
	}
	return f
}
