//go:build windows

package runner

import "os/exec"

// setupProcessGroup is a no-op on Windows. `ralph run` errors at
// startup on non-Linux (isolation is Linux-only), so this cancel path
// is unreachable in production. exec.CommandContext installs its own
// Cancel (cmd.Process.Kill) when none is set, which is sufficient for
// cross-compile completeness.
func setupProcessGroup(_ *exec.Cmd) {}
