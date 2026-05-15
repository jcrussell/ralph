// Package root assembles the cobra command tree for ralph.
//
//go:generate go run ../../../tools/gendocs ../../../docs/reference
package root

import (
	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/pkg/cmd/doctor"
	fsmcmd "github.com/jcrussell/ralph/pkg/cmd/fsm"
	"github.com/jcrussell/ralph/pkg/cmd/hook"
	"github.com/jcrussell/ralph/pkg/cmd/initcmd"
	"github.com/jcrussell/ralph/pkg/cmd/logs"
	"github.com/jcrussell/ralph/pkg/cmd/prompt"
	"github.com/jcrussell/ralph/pkg/cmd/report"
	reviewcmd "github.com/jcrussell/ralph/pkg/cmd/review"
	runcmd "github.com/jcrussell/ralph/pkg/cmd/run"
	"github.com/jcrussell/ralph/pkg/cmd/status"
	"github.com/jcrussell/ralph/pkg/cmd/timeline"
	"github.com/jcrussell/ralph/pkg/cmd/trace"
	"github.com/jcrussell/ralph/pkg/cmd/version"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// NewCmdRoot returns the root cobra command for `ralph`.
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	root := &cobra.Command{
		Use:           "ralph",
		Short:         "FSM-driven autonomous-loop CLI",
		Long:          "ralph runs an AI coding agent in a loop, routed by a built-in state machine. See docs/concepts/ralph-fsm.md.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Route cobra's own help/usage/error output through IOStreams (per
	// byob-iostreams.1). Cascades to subcommands via cobra's writer lookup.
	root.SetIn(f.IOStreams.In)
	root.SetOut(f.IOStreams.Out)
	root.SetErr(f.IOStreams.ErrOut)
	// Wrap pflag's flag-parse errors as *FlagError so the top-level
	// runner maps them to exit 2. Cascades to subcommands via cobra's
	// FlagErrorFunc lookup.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &cmdutil.FlagError{Err: err}
	})

	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core commands:"},
		&cobra.Group{ID: "obs", Title: "Observability commands:"},
		&cobra.Group{ID: "info", Title: "Info commands:"},
		&cobra.Group{ID: "setup", Title: "Setup commands:"},
	)

	addTo := func(c *cobra.Command, group string) {
		c.GroupID = group
		root.AddCommand(c)
	}

	addTo(doctor.NewCmdDoctor(f, nil), "setup")
	addTo(fsmcmd.NewCmdFSM(f, nil, nil), "obs")
	addTo(hook.NewCmdHook(f, nil), "setup")
	addTo(initcmd.NewCmdInit(f, nil), "setup")
	addTo(logs.NewCmdLogs(f, nil), "obs")
	addTo(prompt.NewCmdPrompt(f, nil), "setup")
	addTo(report.NewCmdReport(f, nil), "obs")
	addTo(reviewcmd.NewCmdReview(f, nil), "core")
	addTo(runcmd.NewCmdRun(f, nil), "core")
	addTo(status.NewCmdStatus(f, nil), "obs")
	addTo(timeline.NewCmdTimeline(f, nil), "obs")
	addTo(trace.NewCmdTrace(f, nil), "obs")
	addTo(version.NewCmdVersion(f, nil), "info")

	return root
}
