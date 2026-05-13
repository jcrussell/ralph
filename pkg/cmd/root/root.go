package root

import (
	"github.com/jcrussell/ralph/pkg/cmd/doctor"
	"github.com/jcrussell/ralph/pkg/cmd/hook"
	"github.com/jcrussell/ralph/pkg/cmd/initcmd"
	"github.com/jcrussell/ralph/pkg/cmd/logs"
	"github.com/jcrussell/ralph/pkg/cmd/prompt"
	"github.com/jcrussell/ralph/pkg/cmd/report"
	"github.com/jcrussell/ralph/pkg/cmd/status"
	"github.com/jcrussell/ralph/pkg/cmd/trace"
	"github.com/jcrussell/ralph/pkg/cmd/version"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/spf13/cobra"
)

func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	root := &cobra.Command{
		Use:           "ralph",
		Short:         "FSM-driven autonomous-loop CLI",
		Long:          "ralph runs an AI coding agent in a loop, routed by a built-in state machine. See docs/concepts/ralph-fsm.md.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

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
	addTo(hook.NewCmdHook(f, nil), "setup")
	addTo(initcmd.NewCmdInit(f, nil), "setup")
	addTo(logs.NewCmdLogs(f, nil), "obs")
	addTo(prompt.NewCmdPrompt(f, nil), "setup")
	addTo(report.NewCmdReport(f, nil), "obs")
	addTo(status.NewCmdStatus(f, nil), "obs")
	addTo(trace.NewCmdTrace(f, nil), "obs")
	addTo(version.NewCmdVersion(f, nil), "info")

	return root
}
