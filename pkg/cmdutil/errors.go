package cmdutil

import (
	"errors"
	"fmt"
)

type FlagError struct{ Err error }

func (e *FlagError) Error() string { return e.Err.Error() }
func (e *FlagError) Unwrap() error { return e.Err }

func FlagErrorf(format string, args ...any) error {
	return &FlagError{Err: fmt.Errorf(format, args...)}
}

// ErrSilent indicates the error was already reported; just exit non-zero.
var ErrSilent = errors.New("silent")

// ErrCancel indicates the user cancelled (e.g. SIGINT during a prompt).
var ErrCancel = errors.New("cancel")
