//go:build !linux

package isolation

import (
	"context"
	"errors"
	"runtime"
)

// ErrUnsupported is returned by every entry point on non-Linux hosts.
// ralph run propagates this from doctor checks at startup.
var ErrUnsupported = errors.New("isolation: only supported on Linux (systemd-run --user --scope)")

type Scope struct {
	Unit        string
	MemoryLimit string
}

func NewScope(unitBase, memoryLimit string) Scope {
	return Scope{Unit: unitBase + ".scope", MemoryLimit: memoryLimit}
}

func (s Scope) UnitBase() string { return s.Unit }
func (s Scope) Argv(command string, args []string) []string {
	return append([]string{command}, args...)
}
func (s Scope) EventsPath() string { return "" }

func OOMKilledFile(path string) (bool, error) { return false, ErrUnsupported }

func Available(ctx context.Context) error {
	return errors.New("isolation: " + runtime.GOOS + " is not Linux; ralph requires systemd-run --user --scope")
}
