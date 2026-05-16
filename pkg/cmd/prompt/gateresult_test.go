package prompt

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func TestGateResultFlagSet(t *testing.T) {
	cases := []struct {
		in   string
		want GateResultFlag
		ok   bool
	}{
		{"passed", GateResultPassed, true},
		{"failed", GateResultFailed, true},
		{"not-run", GateResultNotRun, true},
		{"skipped", "", false},
		{"", "", false},
		{"PASSED", "", false},
	}
	for _, c := range cases {
		var g GateResultFlag
		err := g.Set(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("Set(%q) = %v, want nil", c.in, err)
			}
			if g != c.want {
				t.Errorf("Set(%q): g = %q, want %q", c.in, g, c.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("Set(%q) = nil, want error", c.in)
		}
	}
}

func TestGateResultFlagTypeAndString(t *testing.T) {
	g := GateResultPassed
	if got := g.String(); got != "passed" {
		t.Errorf("String() = %q, want %q", got, "passed")
	}
	if got := g.Type(); got != "gate-result" {
		t.Errorf("Type() = %q, want %q", got, "gate-result")
	}
}

// Cobra must reject an invalid --gate-result at parse time and surface
// a *cmdutil.FlagError so the runner maps it to exit 2. RunE never
// runs. FlagErrorFunc is wired on the root command in production, so
// this test mounts prompt under a bare root with the same wrapper.
func TestNewCmdPromptRejectsBadGateResult(t *testing.T) {
	called := false
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	root := &cobra.Command{Use: "ralph", SilenceUsage: true, SilenceErrors: true}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &cmdutil.FlagError{Err: err}
	})
	root.AddCommand(NewCmdPrompt(f, func(context.Context, *Options) error {
		called = true
		return nil
	}))
	root.SetArgs([]string{"prompt", "show", "clean", "--gate-result=garbage"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute = nil, want FlagError")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %T %v, want *FlagError", err, err)
	}
	if called {
		t.Error("runF was called; flag parse should fail before RunE")
	}
}

// The default --gate-result value must be "not-run" so existing
// callers that omit the flag still feed the template a recognized
// value.
func TestNewCmdPromptGateResultDefault(t *testing.T) {
	var got GateResultFlag
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdPrompt(f, func(_ context.Context, opts *Options) error {
		got = opts.GateResult
		return nil
	})
	cmd.SetArgs([]string{"show", "clean"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != GateResultNotRun {
		t.Errorf("opts.GateResult = %q, want %q", got, GateResultNotRun)
	}
}
