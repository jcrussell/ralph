package cmdutil

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestMustRegisterFlagCompletionAttaches(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("fmt", "", "")
	MustRegisterFlagCompletion(cmd, "fmt",
		cobra.FixedCompletions([]string{"a", "b"}, cobra.ShellCompDirectiveNoFileComp))
	fn, ok := cmd.GetFlagCompletionFunc("fmt")
	if !ok {
		t.Fatal("completion func not registered")
	}
	got, dir := fn(cmd, nil, "")
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", dir)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got = %v, want [a b]", got)
	}
}

func TestMustRegisterFlagCompletionPanicsOnUnknownFlag(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for unknown flag")
		}
	}()
	cmd := &cobra.Command{Use: "x"}
	MustRegisterFlagCompletion(cmd, "nope",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveDefault
		})
	_ = errors.New("unreachable")
}
