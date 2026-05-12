// Package lock implements ralph's PID lockfile at
// <repo>/.ralph/state/pid.lock. Only one orchestrator may hold the
// lock at a time. A lockfile whose PID is no longer alive is
// considered stale and gets taken over atomically; a lockfile whose
// PID is alive causes Acquire to return ErrHeld.
package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrHeld is returned by Acquire when another live process already
// holds the lock. The error message includes the PID it observed.
var ErrHeld = errors.New("lock: held by another process")

// Lock is the typed handle for an acquired pid.lock. Release deletes
// the file. The zero value is not usable; obtain one from Acquire.
type Lock struct {
	path string
	pid  int
}

// Acquire creates path with the current process's PID. If path
// already exists with a live PID, returns ErrHeld. If the existing
// PID is dead, the file is replaced and the new lock returned.
//
// Acquire is concurrency-safe within a single host: the final file
// appears via os.Link(tmp, path), so observers never see a partial
// write. Losers in a race either find a live PID (ErrHeld) or a
// freshly-released slot and retry.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("lock: mkdir %s: %w", filepath.Dir(path), err)
	}
	const maxAttempts = 16
	for attempt := 0; attempt < maxAttempts; attempt++ {
		l, err := tryCreate(path)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}

		// Existing file: read its PID. A racing peer may have already
		// removed it — that's a retry, not an error.
		pid, readErr := readPID(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		if processAlive(pid) {
			return nil, fmt.Errorf("%w: pid %d", ErrHeld, pid)
		}

		// Stale — remove and retry. Remove failure other than "already
		// gone" is fatal.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("lock: remove stale %s: %w", path, err)
		}
	}
	return nil, fmt.Errorf("lock: could not acquire %s after %d attempts", path, maxAttempts)
}

// Release deletes the lockfile. Safe to call once; subsequent calls
// return nil if the file is already gone.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	err := os.Remove(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lock: release %s: %w", l.path, err)
	}
	return nil
}

// Path returns the lockfile path.
func (l *Lock) Path() string { return l.path }

// PID returns the PID written to the lockfile.
func (l *Lock) PID() int { return l.pid }

// tryCreate writes the PID to a temp file in the same directory and
// then atomically links it into place. Link fails with fs.ErrExist if
// path already exists — propagate that for the caller's stale-check
// branch. Because the file only appears in the namespace after the
// link, readers never see a half-written PID file.
func tryCreate(path string) (*Lock, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pid.lock.tmp-*")
	if err != nil {
		return nil, fmt.Errorf("lock: temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	pid := os.Getpid()
	if _, err := fmt.Fprintf(tmp, "%d\n", pid); err != nil {
		tmp.Close()
		cleanup()
		return nil, fmt.Errorf("lock: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return nil, fmt.Errorf("lock: close temp: %w", err)
	}
	if err := os.Link(tmpName, path); err != nil {
		cleanup()
		return nil, err
	}
	cleanup() // unlink the temp; the link to path persists
	return &Lock{path: path, pid: pid}, nil
}

func readPID(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		return 0, fmt.Errorf("lock: read %s: %w", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("lock: malformed pid in %s: %w", path, err)
	}
	return pid, nil
}

// processAlive reports whether pid names a live process on this host.
// On Unix this uses kill(pid, 0); on other OSes it conservatively
// returns true (better to refuse than to clobber).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but we can't signal it.
	return errors.Is(err, syscall.EPERM)
}
