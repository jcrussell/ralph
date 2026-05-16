package ralphcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jcrussell/ralph/pkg/cmd/root"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Run is the single error→exit-code mapping point. main.go is a one-liner;
// everything else returns errors that flow up to here.
func Run(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	f := cmdutil.NewFactory()
	cmd := root.NewCmdRoot(f)
	cmd.SetArgs(args)

	return mapErr(cmd.ExecuteContext(ctx), f.IOStreams.ErrOut)
}

// mapErr classifies err and writes any user-facing diagnostic to errOut.
func mapErr(err error, errOut io.Writer) int {
	if err == nil {
		return 0
	}
	err = classifyUsageError(err)
	switch {
	case errors.Is(err, cmdutil.ErrCancel):
		return 2
	case errors.Is(err, cmdutil.ErrSilent):
		return 1
	case errors.As(err, new(*cmdutil.FlagError)):
		_, _ = fmt.Fprintln(errOut, err)
		printHint(err, errOut)
		return 2
	}
	var exit *cmdutil.ExitCodeError
	if errors.As(err, &exit) {
		return exit.Code
	}
	_, _ = fmt.Fprintln(errOut, "error:", err)
	printHint(err, errOut)
	return 1
}

// printHint emits any *ErrHint remediation on a new line.
func printHint(err error, errOut io.Writer) {
	var h *cmdutil.ErrHint
	if errors.As(err, &h) && h.Hint != "" {
		_, _ = fmt.Fprintln(errOut, "hint:", h.Hint)
	}
}

// classifyUsageError wraps cobra's untyped usage errors as *FlagError so
// the runner maps them to exit 2. Cobra exposes no sentinel for these:
//   - "unknown command ..." (typo'd subcommand)
//   - flag-group violations from MarkFlagsMutuallyExclusive /
//     MarkFlagsRequiredTogether / MarkFlagsOneRequired
func classifyUsageError(err error) error {
	var fe *cmdutil.FlagError
	if errors.As(err, &fe) {
		return err
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "unknown command ") || strings.Contains(msg, "flags in the group [") {
		return &cmdutil.FlagError{Err: err}
	}
	return err
}
