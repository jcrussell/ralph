package loop

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// testRC builds a minimal runContext for exercising a single phase helper
// in isolation: a scaffolded repo, a fresh FSM in clean, a discard logger,
// and per-iteration paths rooted under the repo's logs dir.
func testRC(t *testing.T, repo string) (*runContext, *iostreams.TestBuffers) {
	t.Helper()
	ios, bufs := iostreams.Test()
	f := fsm.Fresh()
	f.Outcome = fsm.Outcome{State: fsm.StateClean}
	rc := &runContext{
		opts:  Options{Repo: repo},
		cfg:   config.Defaults(),
		repo:  repo,
		io:    ios,
		log:   slog.New(slog.DiscardHandler),
		fsm:   f,
		clock: newFakeClock(),
	}
	rc.paths = newIterPaths(repo, f.Iter, time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC))
	if err := os.MkdirAll(rc.paths.logsDir, 0o750); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	return rc, bufs
}

// runPreIterationHook returns no skip reason when the hook is absent or
// exits zero, a "pre-iteration exit N" reason on a non-zero exit, and a
// non-nil error only when the hook itself fails to run.
func TestRunPreIterationHook(t *testing.T) {
	hookPath := func(repo string) string {
		return filepath.Join(repo, ".ralph", "hooks", "pre-iteration")
	}

	t.Run("no hook installed", func(t *testing.T) {
		repo := scaffoldRepo(t)
		rc, _ := testRC(t, repo)
		reason, err := runPreIterationHook(context.Background(), rc)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if reason != "" {
			t.Errorf("reason = %q, want empty", reason)
		}
	})

	t.Run("hook exits zero", func(t *testing.T) {
		repo := scaffoldRepo(t)
		rc, _ := testRC(t, repo)
		writeExecutableHook(t, hookPath(repo), "#!/bin/sh\nexit 0\n")
		reason, err := runPreIterationHook(context.Background(), rc)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if reason != "" {
			t.Errorf("reason = %q, want empty", reason)
		}
	})

	t.Run("hook exits non-zero yields skip reason", func(t *testing.T) {
		repo := scaffoldRepo(t)
		rc, _ := testRC(t, repo)
		writeExecutableHook(t, hookPath(repo), "#!/bin/sh\nexit 7\n")
		reason, err := runPreIterationHook(context.Background(), rc)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if reason != "pre-iteration exit 7" {
			t.Errorf("reason = %q, want \"pre-iteration exit 7\"", reason)
		}
	})

	t.Run("hook fails to run yields error", func(t *testing.T) {
		repo := scaffoldRepo(t)
		rc, _ := testRC(t, repo)
		// Non-executable file → hooks.Run returns an error, distinct from
		// a non-zero exit code.
		if err := os.MkdirAll(filepath.Dir(hookPath(repo)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hookPath(repo), []byte("#!/bin/sh\ntrue\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := runPreIterationHook(context.Background(), rc); err == nil {
			t.Errorf("err = nil, want non-nil for an unrunnable hook")
		}
	})
}

// openGateStreams creates both artifact files and returns writers that tee
// to the operator terminal when IOStreams is present; the closer releases
// the files.
func TestOpenGateStreams(t *testing.T) {
	repo := scaffoldRepo(t)
	rc, bufs := testRC(t, repo)

	outW, errW, closeStreams, err := openGateStreams(context.Background(), rc)
	if err != nil {
		t.Fatalf("openGateStreams: %v", err)
	}

	if _, werr := io.WriteString(outW, "GATE-OUT\n"); werr != nil {
		t.Fatalf("write out: %v", werr)
	}
	if _, werr := io.WriteString(errW, "GATE-ERR\n"); werr != nil {
		t.Fatalf("write err: %v", werr)
	}
	closeStreams()

	// Both artifact files exist with the written content.
	outBytes, err := os.ReadFile(rc.paths.gateStdout)
	if err != nil {
		t.Fatalf("read gate stdout file: %v", err)
	}
	if string(outBytes) != "GATE-OUT\n" {
		t.Errorf("gate stdout file = %q, want %q", outBytes, "GATE-OUT\n")
	}
	errBytes, err := os.ReadFile(rc.paths.gateStderr)
	if err != nil {
		t.Fatalf("read gate stderr file: %v", err)
	}
	if string(errBytes) != "GATE-ERR\n" {
		t.Errorf("gate stderr file = %q, want %q", errBytes, "GATE-ERR\n")
	}

	// Output also tees to the IOStreams terminal: out → Out, err → ErrOut.
	if bufs.Out.String() != "GATE-OUT\n" {
		t.Errorf("teed Out = %q, want %q", bufs.Out.String(), "GATE-OUT\n")
	}
	if bufs.ErrOut.String() != "GATE-ERR\n" {
		t.Errorf("teed ErrOut = %q, want %q", bufs.ErrOut.String(), "GATE-ERR\n")
	}
}
