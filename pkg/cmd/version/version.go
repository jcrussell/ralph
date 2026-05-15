package version

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Set at build time via -ldflags; empty in `go run` / `go build` without flags.
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory
}

// NewCmdVersion returns the cobra command for `ralph version`.
func NewCmdVersion(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f}
	return &cobra.Command{
		Use:   "version",
		Short: "Print ralph version information",
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(opts)
			}
			return versionRun(opts)
		},
	}
}

func versionRun(opts *Options) error {
	v, commit := Version, Commit
	if v == "" || commit == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			if v == "" {
				v = info.Main.Version
			}
			for _, s := range info.Settings {
				if commit == "" && s.Key == "vcs.revision" {
					commit = s.Value
				}
				if Date == "" && s.Key == "vcs.time" {
					Date = s.Value
				}
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	_, _ = fmt.Fprintf(opts.F.IOStreams.Out, "ralph %s", v)
	if commit != "" {
		_, _ = fmt.Fprintf(opts.F.IOStreams.Out, " (%s)", commit)
	}
	if Date != "" {
		_, _ = fmt.Fprintf(opts.F.IOStreams.Out, " built %s", Date)
	}
	_, _ = fmt.Fprintln(opts.F.IOStreams.Out)
	return nil
}
