package root

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	ralphlog "github.com/jcrussell/ralph/internal/log"
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

// PersistentPreRunE must attach a per-command slog.Logger to
// cmd.Context() (byob-logging.2). The leaf command's RunE pulls the
// logger off the context via log.From(ctx) and emits a record; the
// captured handler output should carry the "cmd" attribute decorated by
// the root.
func TestRootAttachesLoggerWithCmdAttr(t *testing.T) {
	ios, _ := iostreams.Test()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	f := &cmdutil.Factory{IOStreams: ios, Logger: logger}

	root := NewCmdRoot(f)
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			ralphlog.From(c.Context()).InfoContext(c.Context(), "probed")
			return nil
		},
	}
	root.AddCommand(probe)
	root.SetArgs([]string{"probe"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "probed") {
		t.Errorf("log missing probe message; got %q", got)
	}
	if !strings.Contains(got, `cmd="ralph probe"`) {
		t.Errorf("log missing cmd=\"ralph probe\" attr; got %q", got)
	}
}

// When f.Logger is nil (tests that construct a bare Factory),
// PersistentPreRunE must fall back to slog.Default() rather than panic.
func TestRootSurvivesNilLogger(t *testing.T) {
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	root := NewCmdRoot(f)
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			if ralphlog.From(c.Context()) == nil {
				t.Error("From(ctx) = nil; want fallback logger")
			}
			return nil
		},
	}
	root.AddCommand(probe)
	root.SetArgs([]string{"probe"})
	root.SetContext(context.Background())

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}
