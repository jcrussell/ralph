package cmdutil

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/lock"
	"github.com/jcrussell/ralph/internal/paths"
)

// writePidLock plants a pid.lock under repo holding pid.
func writePidLock(t *testing.T, repo string, pid int) {
	t.Helper()
	p := paths.New(repo).PidLock()
	if err := os.MkdirAll(paths.New(repo).StateDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRepoFreeNoLockfile(t *testing.T) {
	if err := CheckRepoFree(t.TempDir()); err != nil {
		t.Errorf("CheckRepoFree with no lockfile = %v, want nil", err)
	}
}

// A live PID is the refusal case, and the message must name it so the operator
// can act (that PID is the whole point of the check).
func TestCheckRepoFreeLiveLock(t *testing.T) {
	repo := t.TempDir()
	self := os.Getpid()
	writePidLock(t, repo, self)

	err := CheckRepoFree(repo)
	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("CheckRepoFree = %v, want an error wrapping lock.ErrHeld", err)
	}
	if want := strconv.Itoa(self); !strings.Contains(err.Error(), want) {
		t.Errorf("error %q should name the holding pid %s", err, want)
	}
}

// A dead PID is not a refusal: lock.Acquire takes stale locks over, so
// preflighting one would strand the operator on a crashed run's leftovers.
func TestCheckRepoFreeStaleLockIsFree(t *testing.T) {
	repo := t.TempDir()
	// Reap a real child so its PID is definitively dead rather than guessing
	// at an unused number.
	proc, err := os.StartProcess("/bin/true", []string{"/bin/true"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn a throwaway process: %v", err)
	}
	if _, err := proc.Wait(); err != nil {
		t.Fatal(err)
	}
	writePidLock(t, repo, proc.Pid)

	if err := CheckRepoFree(repo); err != nil {
		t.Errorf("CheckRepoFree with a stale lock = %v, want nil (Acquire takes it over)", err)
	}
}

// A malformed lockfile is Acquire's problem to report, not the preflight's:
// refusing here would turn a corrupt byte into an unstartable repo.
func TestCheckRepoFreeMalformedLockIsFree(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(paths.New(repo).StateDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.New(repo).PidLock(), []byte("not-a-pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckRepoFree(repo); err != nil {
		t.Errorf("CheckRepoFree with a malformed lock = %v, want nil", err)
	}
}
