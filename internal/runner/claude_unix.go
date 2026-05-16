//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup places the child (and any descendants it forks) in
// its own process group so context cancel can kill the whole tree.
// Without this, killing a shell wrapper leaves orphan grandchildren
// holding stdout open and cmd.Run() blocks until they exit.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid → signal the group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
