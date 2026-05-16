package cmdutil

import (
	"fmt"

	"github.com/spf13/cobra"
)

// MustRegisterFlagCompletion wires a CompletionFunc to a flag and panics
// on error. Completion is a build-time wiring concern: a typoed flag
// name should fail the test suite, not silently disable completion in a
// user's shell. Use this for every RegisterFlagCompletionFunc call.
func MustRegisterFlagCompletion(cmd *cobra.Command, flag string, fn cobra.CompletionFunc) {
	if err := cmd.RegisterFlagCompletionFunc(flag, fn); err != nil {
		panic(fmt.Sprintf("register completion for --%s: %v", flag, err))
	}
}
