//go:build windows

package hooks

import "os/exec"

// killTreeOnCancel is a no-op on Windows; ralph run refuses to start
// on non-Linux hosts, and exec.CommandContext's default Kill suffices
// for `ralph hook run`.
func killTreeOnCancel(_ *exec.Cmd) {}
