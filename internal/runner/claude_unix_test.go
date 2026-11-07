//go:build unix

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/isolation"
)

// TestRunKillsOrphanedDescendant runs under the real user manager: the
// runner leaves a grandchild in its own session (outside the process
// group a cancel would kill), and after Run returns both the scope and
// the grandchild must be gone.
func TestRunKillsOrphanedDescendant(t *testing.T) {
	if testing.Short() {
		t.Skip("needs systemd-run --user --scope")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := isolation.Available(ctx); err != nil {
		t.Skipf("systemd-run --user --scope unavailable: %v", err)
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	bin := writeScript(t, `#!/bin/sh
setsid sleep 300 </dev/null >/dev/null 2>&1 &
echo $! > "$RALPH_TEST_PIDFILE"
echo '{}'
`)
	r := New(bin, nil, "", "512M")
	s, err := runForTest(t, r, context.Background(), "", t.TempDir(), []string{"RALPH_TEST_PIDFILE=" + pidFile})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.ScopeStopErr != nil {
		t.Fatalf("ScopeStopErr = %v", s.ScopeStopErr)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	var pid int
	if _, err := fmt.Sscan(string(raw), &pid); err != nil {
		t.Fatalf("parse pid %q: %v", raw, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	unit := fmt.Sprintf("ralph-%d-1.scope", os.Getpid())
	if out, _ := exec.Command("systemctl", "--user", "is-active", unit).Output(); strings.TrimSpace(string(out)) == "active" {
		t.Errorf("unit %s still active after Run", unit)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphaned grandchild %d still alive after Run (kill(0) = %v)", pid, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A descendant that ignores SIGTERM is still gone right after Run: the
// cleanup SIGKILLs the scope's leftovers after a short grace instead of
// waiting out the unit's 90s stop timeout.
func TestRunKillsTermIgnoringOrphan(t *testing.T) {
	if testing.Short() {
		t.Skip("needs systemd-run --user --scope")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := isolation.Available(ctx); err != nil {
		t.Skipf("systemd-run --user --scope unavailable: %v", err)
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	bin := writeScript(t, `#!/bin/sh
setsid sh -c 'trap "" TERM; echo $$ > "$RALPH_TEST_PIDFILE"; while :; do sleep 1; done' </dev/null >/dev/null 2>&1 &
while [ ! -s "$RALPH_TEST_PIDFILE" ]; do sleep 0.05; done
echo '{}'
`)
	start := time.Now()
	if _, err := runForTest(t, New(bin, nil, "", "512M"), context.Background(), "", t.TempDir(), []string{"RALPH_TEST_PIDFILE=" + pidFile}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Errorf("Run took %s; cleanup waited on the TERM-ignoring orphan", d)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(string(raw)), &pid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(2 * time.Second)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatalf("TERM-ignoring orphan %d still alive after Run", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A leftover that handles SIGTERM gets to clean up (git removing its
// index.lock, a server its pidfile) before anything is SIGKILLed.
func TestRunLetsOrphanCleanUpOnTerm(t *testing.T) {
	if testing.Short() {
		t.Skip("needs systemd-run --user --scope")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := isolation.Available(ctx); err != nil {
		t.Skipf("systemd-run --user --scope unavailable: %v", err)
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "cleaned-up")
	ready := filepath.Join(dir, "ready")
	bin := writeScript(t, `#!/bin/sh
setsid sh -c 'trap "echo bye > \"$RALPH_TEST_MARKER\"; exit 0" TERM; : > "$RALPH_TEST_READY"; while :; do sleep 0.1; done' </dev/null >/dev/null 2>&1 &
while [ ! -e "$RALPH_TEST_READY" ]; do sleep 0.05; done
echo '{}'
`)
	if _, err := runForTest(t, New(bin, nil, "", "512M"), context.Background(), "", dir,
		[]string{"RALPH_TEST_MARKER=" + marker, "RALPH_TEST_READY=" + ready}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("orphan's TERM handler never ran (killed without a grace period?): %v", err)
	}
}
