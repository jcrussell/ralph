package cmdutil

import (
	"errors"
	"fmt"
)

// FlagError is the error type a RunE returns to signal a flag-validation
// failure. Detected by ralphcmd.Run with errors.As to print usage and
// exit 2.
type FlagError struct{ Err error }

func (e *FlagError) Error() string { return e.Err.Error() }
func (e *FlagError) Unwrap() error { return e.Err }

// FlagErrorf wraps fmt.Errorf into a *FlagError.
func FlagErrorf(format string, args ...any) error {
	return &FlagError{Err: fmt.Errorf(format, args...)}
}

// ErrSilent indicates the error was already reported; just exit non-zero.
var ErrSilent = errors.New("silent")

// ErrCancel indicates the user cancelled (e.g. SIGINT during a prompt).
var ErrCancel = errors.New("cancel")

// ExitCodeError carries a specific process exit code up to main. Used
// when a subcommand needs to propagate a child's exit code (e.g.
// `ralph hook run` returns the hook's exit code).
type ExitCodeError struct {
	Code int
}

func (e *ExitCodeError) Error() string {
	return fmt.Sprintf("exit %d", e.Code)
}
