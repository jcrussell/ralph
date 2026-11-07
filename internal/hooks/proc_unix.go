//go:build unix

package hooks

import (
	"os/exec"
	"syscall"
)

// killTreeOnCancel puts the hook in its own process group and makes a
// context cancel (stop signal, gate timeout) SIGKILL the whole group.
// Otherwise only the hook's shell dies: a test runner or compiler it
// started keeps running and holds the output pipe open, so Run waits
// for it to finish on its own.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
