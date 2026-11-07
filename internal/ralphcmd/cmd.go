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
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	// SIGINT/SIGTERM cancel the context with the signal as the cause, so
	// a run unwinds its iteration and then exits 128+signo. After the
	// first signal the default disposition is restored: a second one
	// kills the process outright.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		select {
		case s := <-sigs:
			signal.Stop(sigs)
			cancel(signalInterrupt(s))
		case <-ctx.Done():
		}
	}()

	f := cmdutil.NewFactory()
	cmd := root.NewCmdRoot(f)
	cmd.SetArgs(args)

	return exitCode(ctx, cmd.ExecuteContext(ctx), f.IOStreams.ErrOut)
}

// signalInterrupt is the cancel cause for a received signal: exit
// 128+signo (130 SIGINT, 143 SIGTERM).
func signalInterrupt(sig os.Signal) *cmdutil.InterruptedError {
	ie := &cmdutil.InterruptedError{By: sig.String(), Code: 1}
	switch sig {
	case os.Interrupt:
		ie.By = "SIGINT"
	case syscall.SIGTERM:
		ie.By = "SIGTERM"
	}
	if n, ok := sig.(syscall.Signal); ok {
		ie.Code = 128 + int(n)
	}
	return ie
}

// exitCode maps err to the process exit code. A run that was stopped on
// purpose — the error carries a *cmdutil.InterruptedError, or a signal
// cancelled the context — exits with the interrupt's code (130 SIGINT
// or UI quit, 143 SIGTERM) and a one-line notice instead of "error:
// ...". The error need not wrap context.Canceled: a subprocess the
// cancel killed (bd, git, a hook) surfaces as "signal: killed", and the
// notice names it. An outcome the command still reached
// (*ExitCodeError) stands. Everything else goes through mapErr.
func exitCode(ctx context.Context, err error, errOut io.Writer) int {
	if err == nil || errors.As(err, new(*cmdutil.ExitCodeError)) {
		return mapErr(err, errOut)
	}
	var ie *cmdutil.InterruptedError
	if !errors.As(err, &ie) && !errors.As(context.Cause(ctx), &ie) {
		return mapErr(err, errOut)
	}
	msg := ie.Error()
	if !errors.Is(err, context.Canceled) && err.Error() != msg {
		msg += " (" + strings.TrimPrefix(err.Error(), msg+": ") + ")"
	}
	_, _ = fmt.Fprintf(errOut, "ralph: %s\n", msg)
	return ie.Code
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
