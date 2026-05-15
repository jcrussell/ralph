package ralphcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
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

	err := cmd.ExecuteContext(ctx)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, cmdutil.ErrCancel):
		return 2
	case errors.Is(err, cmdutil.ErrSilent):
		return 1
	case errors.As(err, new(*cmdutil.FlagError)):
		_, _ = fmt.Fprintln(f.IOStreams.ErrOut, err)
		return 2
	default:
		var exit *cmdutil.ExitCodeError
		if errors.As(err, &exit) {
			return exit.Code
		}
		_, _ = fmt.Fprintln(f.IOStreams.ErrOut, "error:", err)
		return 1
	}
}
