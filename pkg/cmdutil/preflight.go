package cmdutil

import (
	"fmt"

	"github.com/jcrussell/ralph/internal/lock"
	"github.com/jcrussell/ralph/internal/paths"
)

// CheckRepoFree reports an error when another live orchestrator already holds
// repo's .ralph/state/pid.lock. Commands that are about to call loop.Run use it
// as a preflight so contention is refused with a clear, actionable message
// before any expensive or visible setup happens — notably before `ralph run`
// paints its TUI, which would otherwise park on a badge with the real error
// stuck behind it until the operator quits.
//
// This is advisory only: loop.Run still takes the real lock, so the check
// racing a peer that exits (or starts) in the gap is harmless — Acquire remains
// the authority. For the same reason an unreadable or malformed lockfile is not
// a refusal here; Acquire will surface it with better context. A lockfile whose
// PID is dead is likewise no refusal: Acquire takes over stale locks.
//
// The returned error wraps lock.ErrHeld so callers can errors.Is it.
func CheckRepoFree(repo string) error {
	pid, alive, err := lock.ActivePID(paths.New(repo).PidLock())
	if err != nil || !alive {
		return nil
	}
	return fmt.Errorf("%w: pid %d is still running in this repo; wait for it to finish or stop it before starting another run", lock.ErrHeld, pid)
}
