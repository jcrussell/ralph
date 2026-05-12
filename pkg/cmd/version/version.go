package version

import (
	"fmt"
	"runtime/debug"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Set at build time via -ldflags; empty in `go run` / `go build` without flags.
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

type Options struct {
	F *cmdutil.Factory
}

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
	fmt.Fprintf(opts.F.IOStreams.Out, "ralph %s", v)
	if commit != "" {
		fmt.Fprintf(opts.F.IOStreams.Out, " (%s)", commit)
	}
	if Date != "" {
		fmt.Fprintf(opts.F.IOStreams.Out, " built %s", Date)
	}
	fmt.Fprintln(opts.F.IOStreams.Out)
	return nil
}
