//go:build linux

// Package isolation runs the runner inside a systemd-run --user
// --scope cgroup with a memory cap and OOM detection. Linux-only:
// other OSes get a stub that returns a clear error from every entry
// point.
//
// The argv shape is ported from ~/repos/scratch/angr/run_optimization_loop.py.
// OOM detection reads the unit's systemd Result, not memory.events:
// the scope's cgroup is removed as soon as its last process exits.
package isolation

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Scope describes one cgroup scope unit ralph will create via
// systemd-run. The Unit field includes the .scope suffix.
type Scope struct {
	Unit        string // "ralph-<pid>-<seq>.scope"
	MemoryLimit string // "7G", "512m" — passed verbatim to systemd
}

// NewScope assembles a Scope from a base name (no .scope suffix) and
// memory limit. It enforces the .scope suffix so systemctl calls
// work consistently.
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

// stopTimeout bounds how long Stop waits for systemd to tear the
// scope down. The stop sends SIGTERM, then SIGKILL after the unit's
// TimeoutStopSec (90s by default); bound the wait here
// and let systemd finish the kill in the background if it takes longer.
const stopTimeout = 10 * time.Second

// Stop tears down the scope unit and every process left in its cgroup,
// including descendants that left the runner's process group (setsid,
// daemonized test servers, build workers), which a process-group kill
// does not reach. It runs even when ctx is already cancelled — that is
// the common case after a timeout — with its own bounded deadline. A
// unit that has already gone (the scope empties and is garbage-
// collected once its last process exits) is not an error.
func (s Scope) Stop(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	out, err := systemctl(ctx, "stop", s.Unit).CombinedOutput()
	// A failed scope (non-zero exit, OOM) stays loaded in the failed
	// state after the stop; clear it so they don't accumulate. Harmless
	// when the unit is already gone.
	_ = systemctl(ctx, "reset-failed", s.Unit).Run()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if strings.Contains(msg, "not loaded") {
		return nil
	}
	return fmt.Errorf("isolation: stop %s: %w: %s", s.Unit, err, msg)
}

// ResultOOMKill is the unit Result systemd records when the kernel OOM
// killer fires inside the unit's cgroup (e.g. on hitting MemoryMax).
const ResultOOMKill = "oom-kill"

// settleTimeout bounds how long Result waits for systemd to finish
// with a scope whose runner just exited; termGrace is how long Cleanup
// gives leftovers to exit on SIGTERM before SIGKILL; settlePoll is the
// poll interval for both.
const (
	settleTimeout = 2 * time.Second
	termGrace     = 2 * time.Second
	settlePoll    = 50 * time.Millisecond
)

// unitState is what `systemctl show` says about the scope. A unit that
// is not loaded was collected (clean exit) or never existed: settled,
// with no verdict. A loaded unit is settled once it is no longer
// active or deactivating, and only then is its Result final.
type unitState struct {
	loaded, settled bool
	result          string
}

func parseState(out string) unitState {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	if props["LoadState"] != "loaded" {
		return unitState{settled: true}
	}
	switch props["ActiveState"] {
	case "active", "activating", "deactivating", "reloading":
		return unitState{loaded: true}
	}
	return unitState{loaded: true, settled: true, result: props["Result"]}
}

// waitSettled polls the unit's state until it settles or timeout.
func (s Scope) waitSettled(ctx context.Context, timeout time.Duration) (unitState, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	deadline := time.Now().Add(timeout)
	for {
		out, err := systemctl(ctx, "show", "--property=LoadState,ActiveState,Result", s.Unit).Output()
		if err != nil {
			return unitState{}, fmt.Errorf("isolation: show %s: %w", s.Unit, err)
		}
		st := parseState(string(out))
		if st.settled || !time.Now().Before(deadline) {
			return st, nil
		}
		select {
		case <-time.After(settlePoll):
		case <-ctx.Done():
			return st, ctx.Err()
		}
	}
}

// Result returns the unit's systemd Result ("success", "oom-kill",
// "signal", ...) once it settles: the cgroup (and its memory.events) is
// gone when the runner exits, but a failed scope stays loaded with its
// Result until it is reset, and the runner's exit can reach ralph before
// systemd has processed it. "" means no verdict: collected (clean exit),
// or still active after settleTimeout.
func (s Scope) Result(ctx context.Context) (string, error) {
	st, err := s.waitSettled(ctx, settleTimeout)
	return st.result, err
}

// Cleanup ends the scope after its runner exited and reports the
// unit's settled Result:
//
//  1. SIGTERM whatever is still in the scope's cgroup — descendants that
//     left the runner's process group (setsid, daemonized test servers,
//     build workers) — and give them termGrace to exit, so git and
//     friends can drop their lock files.
//  2. SIGKILL anything still there, rather than wait out the unit's
//     TimeoutStopSec (90s) for a process that ignores SIGTERM.
//  3. Read the Result once the cgroup is empty, so an OOM verdict isn't
//     lost to a scope orphans were holding open.
//  4. If the unit is still loaded (it failed, or something lingers),
//     stop and reset-failed it so failed scopes don't pile up. A clean
//     exit was already collected: nothing more to do.
//
// It runs even when ctx is already cancelled (the common case after a
// timeout), with its own bounded deadlines. A failed state probe means
// no verdict; the error returned is the stop's, which says processes
// may have outlived the iteration.
func (s Scope) Cleanup(ctx context.Context) (string, error) {
	signal := func(sig string) {
		kctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
		defer cancel()
		_ = systemctl(kctx, "kill", "--signal="+sig, s.Unit).Run() // fails harmlessly when nothing is left
	}
	st, _ := s.waitSettled(ctx, 0)
	if !st.settled {
		signal("SIGTERM")
		st, _ = s.waitSettled(ctx, termGrace)
	}
	if !st.settled {
		signal("SIGKILL")
		st, _ = s.waitSettled(ctx, settleTimeout)
	}
	if st.settled && !st.loaded {
		return "", nil
	}
	return st.result, s.Stop(ctx)
}

func systemctl(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...) //nolint:gosec // args are fixed verbs plus the ralph-generated unit name
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
