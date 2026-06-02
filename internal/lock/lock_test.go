package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAcquireReleaseHappyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("lockfile not created: %v", statErr)
	}
	if l.PID() != os.Getpid() {
		t.Errorf("PID() = %d, want %d", l.PID(), os.Getpid())
	}
	if err := l.Release(); err != nil {
		t.Errorf("Release: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lockfile still present after Release: err=%v", err)
	}
	// Double release is a no-op.
	if err := l.Release(); err != nil {
		t.Errorf("second Release: %v", err)
	}
}

func TestAcquireFailsWhenHeldByLiveProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	defer l.Release()

	_, err = Acquire(path)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire: err = %v, want ErrHeld", err)
	}
}

func TestAcquireTakesOverStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid.lock")
	// Write a stale lockfile with a PID that's almost certainly dead.
	stalePID := pickDeadPID(t)
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", stalePID)), 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire (stale takeover): %v", err)
	}
	defer l.Release()

	pid, err := readPID(path)
	if err != nil {
		t.Fatalf("readPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("after takeover, pid = %d, want %d", pid, os.Getpid())
	}
}

func TestAcquireMalformedPIDFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid.lock")
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := Acquire(path)
	if err == nil {
		t.Fatal("Acquire: nil err, want malformed")
	}
}

func TestAcquireConcurrentSingleWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid.lock")
	const N = 20
	var wins atomic.Int32
	var heldLosses atomic.Int32
	var otherErrs atomic.Int32

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			l, err := Acquire(path)
			switch {
			case err == nil:
				wins.Add(1)
				// Hold briefly so others see us live, then release.
				_ = l.Release()
			case errors.Is(err, ErrHeld):
				heldLosses.Add(1)
			default:
				otherErrs.Add(1)
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	wg.Wait()

	// Exactly one acquire wins per moment; serialized losers either
	// see ErrHeld or, if the winner already released, win themselves.
	// What we assert: no unexpected errors, total = N, at least one win.
	total := wins.Load() + heldLosses.Load() + otherErrs.Load()
	if total != N {
		t.Errorf("total = %d, want %d", total, N)
	}
	if otherErrs.Load() != 0 {
		t.Errorf("unexpected error count = %d", otherErrs.Load())
	}
	if wins.Load() < 1 {
		t.Errorf("no winners — at least one Acquire should succeed")
	}
}

// pickDeadPID returns a PID that is not currently alive. It tries a
// few high numbers; if all happen to be alive (extremely unlikely)
// the test skips.
func pickDeadPID(t *testing.T) int {
	t.Helper()
	for _, cand := range []int{999999, 998877, 887766} {
		if !processAlive(cand) {
			return cand
		}
	}
	t.Skip("could not find a dead PID to seed the stale-lock test")
	return -1
}

func TestActivePID(t *testing.T) {
	dir := t.TempDir()

	// Missing lockfile: (0, false, nil).
	missing := filepath.Join(dir, "pid.lock")
	if pid, alive, err := ActivePID(missing); err != nil || pid != 0 || alive {
		t.Errorf("ActivePID(missing) = (%d, %v, %v), want (0, false, nil)", pid, alive, err)
	}

	// Live lock held by this process.
	l, err := Acquire(missing)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = l.Release() }()
	pid, alive, err := ActivePID(missing)
	if err != nil {
		t.Fatalf("ActivePID(live): %v", err)
	}
	if pid != os.Getpid() || !alive {
		t.Errorf("ActivePID(live) = (%d, %v), want (%d, true)", pid, alive, os.Getpid())
	}

	// Dead pid: alive=false (pid 1 owned by init; use an impossibly high pid).
	deadPath := filepath.Join(dir, "dead.lock")
	if werr := os.WriteFile(deadPath, []byte("2147483646\n"), 0o600); werr != nil {
		t.Fatalf("write dead lock: %v", werr)
	}
	if _, alive, err := ActivePID(deadPath); err != nil || alive {
		t.Errorf("ActivePID(dead) alive=%v err=%v, want alive=false err=nil", alive, err)
	}

	// Malformed lockfile surfaces an error.
	badPath := filepath.Join(dir, "bad.lock")
	if werr := os.WriteFile(badPath, []byte("not-a-pid\n"), 0o600); werr != nil {
		t.Fatalf("write bad lock: %v", werr)
	}
	if _, _, err := ActivePID(badPath); err == nil {
		t.Errorf("ActivePID(malformed): nil err, want failure")
	}
}
