package version

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/ralphcmd/build"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory
}

// NewCmdVersion returns the cobra command for `ralph version`.
func NewCmdVersion(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	return &cobra.Command{
		Use:   "version",
		Short: "Print ralph version information",
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return versionRun(c.Context(), opts)
		},
	}
}

func versionRun(_ context.Context, opts *Options) error {
	_, _ = fmt.Fprintln(opts.F.IOStreams.Out, format(build.Info()))
	return nil
}

// format renders a single-line version banner. Sentinel-valued commit
// and date are omitted so a bare `go run` shows just "ralph dev".
// Separated from versionRun because build.Info is cached at process
// scope (sync.OnceValue) and is not injectable from tests.
func format(info build.BuildInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ralph %s", info.Version)
	if info.Commit != build.CommitNone {
		fmt.Fprintf(&b, " (%s)", info.Commit)
	}
	if info.Date != build.DateUnknown {
		fmt.Fprintf(&b, " built %s", info.Date)
	}
	return b.String()
}
