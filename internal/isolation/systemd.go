//go:build linux

// Package isolation runs the runner inside a systemd-run --user
// --scope cgroup with a memory cap and OOM detection. Linux-only:
// other OSes get a stub that returns a clear error from every entry
// point.
//
// The argv shape and OOM-detection logic are ported from
// ~/repos/scratch/angr/run_optimization_loop.py — preserve their
// semantics when changing this file.
package isolation

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Scope describes one cgroup scope unit ralph will create via
// systemd-run. The Unit field includes the .scope suffix.
type Scope struct {
	Unit        string // "ralph-<run-id>-iter<N>.scope"
	MemoryLimit string // "7G", "512m" — passed verbatim to systemd
}

// NewScope assembles a Scope from a base name (no .scope suffix) and
// memory limit. It enforces the .scope suffix so EventsPath and
// systemctl calls work consistently.
func NewScope(unitBase, memoryLimit string) Scope {
	unitBase = strings.TrimSuffix(unitBase, ".scope")
	return Scope{Unit: unitBase + ".scope", MemoryLimit: memoryLimit}
}

// UnitBase returns Unit without the .scope suffix — the form
// systemd-run --unit accepts.
func (s Scope) UnitBase() string {
	return strings.TrimSuffix(s.Unit, ".scope")
}

// Argv wraps command + args with the documented systemd-run
// invocation. Pass an empty MemoryLimit to skip the cap entirely
// (rare; mostly useful for the doctor smoke test).
func (s Scope) Argv(command string, args []string) []string {
	argv := []string{
		"systemd-run", "--user", "--scope",
		"--unit=" + s.UnitBase(),
	}
	if s.MemoryLimit != "" {
		argv = append(argv, "-p", "MemoryMax="+s.MemoryLimit, "-p", "MemorySwapMax=0")
	}
	argv = append(argv, "--", command)
	argv = append(argv, args...)
	return argv
}

// EventsPath returns the cgroup memory.events path for this scope
// under the v2 unified hierarchy. Matches the Python reference's
// `/sys/fs/cgroup/user.slice/user-<uid>.slice/user@<uid>.service/<unit>/memory.events`.
func (s Scope) EventsPath() string {
	uid := os.Getuid()
	return filepath.Join(
		"/sys/fs/cgroup",
		"user.slice",
		fmt.Sprintf("user-%d.slice", uid),
		fmt.Sprintf("user@%d.service", uid),
		s.Unit,
		"memory.events",
	)
}

// OOMKilled reports whether the kernel killed anything in this scope
// for OOM. Reads memory.events; absent file or unparsable line is
// "no OOM", matching the Python heuristic.
func (s Scope) OOMKilled() bool {
	killed, _ := OOMKilledFile(s.EventsPath())
	return killed
}

// OOMKilledFile parses a cgroup memory.events file and returns true
// when oom_kill is > 0. A missing file returns false, nil (not an
// error condition for this caller).
func OOMKilledFile(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("isolation: open %s: %w", path, err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if !strings.HasPrefix(line, "oom_kill") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, s.Err()
}

// Kill terminates the scope unit via `systemctl --user kill <unit>`.
// A non-zero exit from systemctl is returned to the caller; an
// already-dead unit yields a non-nil error from systemctl that the
// caller may ignore.
func (s Scope) Kill(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "kill", s.Unit)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("isolation: kill %s: %w: %s", s.Unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ResetFailed clears the failed state of the scope unit so it doesn't
// linger in systemd's status output between iterations.
func (s Scope) ResetFailed(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "reset-failed", s.Unit)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("isolation: reset-failed %s: %w: %s", s.Unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Available probes that `systemd-run --user --scope` actually works
// on this host. Returns nil on success. Used by ralph doctor.
func Available(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "systemd-run", "--user", "--scope", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("isolation: probe systemd-run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
