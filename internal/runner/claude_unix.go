//go:build unix

package runner

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
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

// exitSignal names the signal that terminated the process ("SIGKILL"),
// or "" when it exited normally. systemd-run --scope execs the runner
// in place, so for a wrapped run this is the runner's own signal.
func exitSignal(ps *os.ProcessState) string {
	if ps == nil {
		return ""
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	if name := unix.SignalName(ws.Signal()); name != "" {
		return name
	}
	return "signal " + strconv.Itoa(int(ws.Signal()))
}
