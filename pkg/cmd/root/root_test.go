package root

import (
	"errors"
	"strings"
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

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute(): want error for unknown flag, got nil")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v (%T); want errors.As(_, **FlagError)", err, err)
	}
}

// Cobra's auto-generated help text must flow through IOStreams.Out, not
// straight to os.Stdout (byob-iostreams.1).
func TestRootHelpWritesToIOStreamsOut(t *testing.T) {
	ios, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdRoot(f)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(--help): %v", err)
	}
	if got := bufs.Out.String(); !strings.Contains(got, "ralph") {
		t.Errorf("help output missing 'ralph' on IOStreams.Out; got %q", got)
	}
}
