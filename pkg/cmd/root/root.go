package root

import (
	"github.com/jcrussell/ralph/pkg/cmd/doctor"
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
	addTo(version.NewCmdVersion(f, nil), "info")

	return root
}
