package root

import (
	"errors"
	"testing"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Flag-parse errors must surface as *cmdutil.FlagError so the top-level
// runner maps them to exit 2 instead of the generic exit-1 path.
func TestRootWrapsFlagParseErrorAsFlagError(t *testing.T) {
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdRoot(f)
	cmd.SetArgs([]string{"--definitely-not-a-real-flag"})
	cmd.SetOut(ios.Out)
	cmd.SetErr(ios.ErrOut)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute(): want error for unknown flag, got nil")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v (%T); want errors.As(_, **FlagError)", err, err)
	}
}
