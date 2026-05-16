package version

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/ralphcmd/build"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory
}

// NewCmdVersion returns the cobra command for `ralph version`. The
// subcommand prints the richer multi-line block; the root's
// `--version` flag prints just the short banner. Both routes share
// build.Info() as the single source of truth so they cannot drift.
func NewCmdVersion(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	return &cobra.Command{
		Use:   "version",
		Short: "Print ralph version, commit, and build info",
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return versionRun(c.Context(), opts)
		},
	}
}

func versionRun(_ context.Context, opts *Options) error {
	_, _ = fmt.Fprint(opts.F.IOStreams.Out, FormatLong(build.Info()))
	return nil
}

// FormatShort renders the one-line banner used by `ralph --version`.
// Exported so the root command can feed it to cobra's Version template
// without re-deriving the format string.
func FormatShort(info build.Triple) string {
	return "ralph " + info.Version
}

// FormatLong renders the multi-line block printed by `ralph version`.
// Always prints all five rows: showing `commit: none` or `built:
// unknown` is a useful signal that the binary lacks build metadata
// rather than a defect to hide. runtime.Version / GOOS / GOARCH are
// resolved here (not in build.Info) because they belong to the running
// process, not the build artifact.
func FormatLong(info build.Triple) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ralph %s\n", info.Version)
	fmt.Fprintf(&b, "  commit: %s\n", info.Commit)
	fmt.Fprintf(&b, "  built:  %s\n", info.Date)
	fmt.Fprintf(&b, "  go:     %s\n", runtime.Version())
	fmt.Fprintf(&b, "  os:     %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return b.String()
}
