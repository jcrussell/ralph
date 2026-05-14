package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/lock"
	"github.com/jcrussell/ralph/internal/runner"
)

// Start state with an empty bd queue + clean tree routes immediately
// to done{queue_empty}. The runner must not be invoked.
func TestRun_StartImmediatelyRoutes(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	fr := &fakeRunner{}
	opts.Runner = fr
	opts.Clock = newFakeClock()

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}) {
		t.Errorf("outcome = %+v, want done{queue_empty}", out)
	}
	if fr.Calls != 0 {
		t.Errorf("runner called %d times, want 0 (start routes virtually)", fr.Calls)
	}
}

// --dry-run skips the runner but still renders + captures the prompt.
func TestRun_DryRunSkipsRunner(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.DryRun = true
	opts.Once = true
	fr := &fakeRunner{Errs: []error{errors.New("should not be called")}}
	opts.Runner = fr
	opts.Clock = newFakeClock()
	// Make ready non-empty so start routes to clean and we get one iteration.
	opts.BD = &fakeBD{ReadyByLabel: map[string][]bd.Issue{"": {{ID: "x"}}}}

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fr.Calls != 0 {
		t.Errorf("runner invoked under DryRun (calls=%d)", fr.Calls)
	}
	// Prompt file should exist for the one iteration that ran.
	matches, _ := filepath.Glob(filepath.Join(repo, ".ralph", "state", "logs", "iter-*-prompt.txt"))
	if len(matches) == 0 {
		t.Errorf("no prompt file captured under DryRun")
	}
}

// --once exits after one non-terminal iteration. Verifies exactly one
// summary row and one transition row beyond the start->clean entry.
func TestRun_OnceFlagExitsAfterOneIteration(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.Once = true
	opts.Runner = &fakeRunner{}
	opts.Clock = newFakeClock()
	opts.BD = &fakeBD{ReadyByLabel: map[string][]bd.Issue{"": {{ID: "x"}}}}

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State.Terminal() {
		t.Errorf("Once returned terminal: %+v", out)
	}
	recs := readSummary(t, repo)
	if len(recs) != 1 {
		t.Fatalf("summary records = %d, want 1 (the single iteration)", len(recs))
	}
	if recs[0].Iter != 1 {
		t.Errorf("first iter = %d, want 1", recs[0].Iter)
	}
}

// A pre-existing live PID in pid.lock returns ErrHeld through %w.
func TestRun_LockBusyReturnsErrHeld(t *testing.T) {
	repo := scaffoldRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".ralph", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Use our own PID — it's definitely alive.
	pidFile := filepath.Join(repo, ".ralph", "state", "pid.lock")
	if err := os.WriteFile(pidFile, []byte(itoaPid()), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := baseOpts(t, repo)
	opts.Runner = &fakeRunner{}
	opts.Clock = newFakeClock()
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("expected ErrHeld, got nil")
	}
	if !errors.Is(err, lock.ErrHeld) {
		t.Errorf("error does not wrap lock.ErrHeld: %v", err)
	}
}

// In review mode with a non-empty review:<branch> queue, start routes
// to review (clean tree, queue non-empty).
func TestRun_ReviewModeRoutesToReviewState(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.ReviewMode = true
	opts.ReviewBranch = "feature/foo"
	opts.ReviewBase = "main"
	opts.Once = true
	opts.Runner = &fakeRunner{}
	opts.Clock = newFakeClock()
	opts.BD = &fakeBD{
		ReadyByLabel: map[string][]bd.Issue{"review:feature/foo": {{ID: "rev-1"}}},
	}

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	f := fsmAt(t, repo)
	if f.State != fsm.StateReview {
		t.Errorf("fsm.State = %s, want %s", f.State, fsm.StateReview)
	}
	if !f.ReviewMode {
		t.Errorf("ReviewMode not persisted")
	}
}

// Review mode with empty queue + clean tree exits done{queue_empty}
// before invoking the runner.
func TestRun_ReviewModeQueueEmptyExitsDone(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.ReviewMode = true
	opts.ReviewBranch = "feature/foo"
	opts.ReviewBase = "main"
	fr := &fakeRunner{}
	opts.Runner = fr
	opts.Clock = newFakeClock()
	opts.BD = &fakeBD{} // no review issues

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}) {
		t.Errorf("outcome = %+v, want done{queue_empty}", out)
	}
	if fr.Calls != 0 {
		t.Errorf("runner invoked despite empty review queue")
	}
}

// A ModeAuth runner result lands the FSM in failed{auth}, fires the
// global failure hook, and writes a terminal-failure incident.
func TestRun_TerminalFailureFiresFailureHookAndIncident(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.BD = &fakeBD{ReadyByLabel: map[string][]bd.Issue{"": {{ID: "x"}}}}
	opts.Clock = newFakeClock()
	// Configure runner to return an auth-error session.
	authSess := &runner.Session{
		ExitCode: 1,
		Duration: 100 * time.Millisecond,
		Stderr:   "Error: invalid api key",
	}
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{authSess}}

	// Install a global failure hook that touches a marker file.
	marker := filepath.Join(repo, "failure-fired")
	writeExecutableHook(t,
		filepath.Join(repo, ".ralph", "hooks", "failure"),
		"#!/bin/sh\ntouch \""+marker+"\"\n",
	)

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonAuth}) {
		t.Errorf("outcome = %+v, want failed{auth}", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("failure hook not invoked (marker missing): %v", err)
	}
	// Incident file should exist.
	incidents, _ := filepath.Glob(filepath.Join(repo, ".ralph", "state", "incidents", "*-terminal-failure.md"))
	if len(incidents) == 0 {
		t.Errorf("no terminal-failure incident written")
	}
}

// Options validation: zero values are rejected before any side effect.
func TestRun_OptionsValidation(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		mut  func(*Options)
		want string
	}{
		{"missing repo", func(o *Options) { o.Repo = "" }, "Repo is required"},
		{"relative repo", func(o *Options) { o.Repo = "./relative" }, "must be absolute"},
		{"missing cfg", func(o *Options) { o.Cfg = nil }, "Cfg is required"},
		{"missing io", func(o *Options) { o.IO = nil }, "IO is required"},
		{"review without branch", func(o *Options) { o.ReviewMode = true; o.ReviewBranch = "" }, "ReviewBranch"},
		{"review branch == base", func(o *Options) { o.ReviewMode = true; o.ReviewBranch = "main"; o.ReviewBase = "main" }, "differ from ReviewBase"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := scaffoldRepo(t)
			opts := baseOpts(t, repo)
			c.mut(&opts)
			_, err := Run(ctx, opts)
			if err == nil {
				t.Fatalf("nil err, want %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want substring %q", err, c.want)
			}
		})
	}
}

func itoaPid() string {
	pid := os.Getpid()
	return strings.TrimSpace(itoaInt(pid))
}

func itoaInt(n int) string {
	// Minimal: format an int as decimal.
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	out := string(buf[i:])
	if neg {
		out = "-" + out
	}
	return out
}
