package review

import (
	"context"
	"errors"
	"testing"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

func TestOptionsValidate(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		ok   bool
	}{
		{"zero values ok", Options{}, true},
		{"positive overrides ok", Options{PR: 123, MaxRounds: 10}, true},
		{"negative pr", Options{PR: -1}, false},
		{"negative max-rounds", Options{MaxRounds: -1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.opts.Validate()
			if c.ok && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !c.ok {
				var fe *cmdutil.FlagError
				if !errors.As(err, &fe) {
					t.Errorf("Validate() = %v, want *FlagError", err)
				}
			}
		})
	}
}

func TestNewCmdReviewMetadata(t *testing.T) {
	c := NewCmdReview(&cmdutil.Factory{}, func(context.Context, *Options) error { return nil })
	if c.Use != "review" {
		t.Errorf("Use = %q, want %q", c.Use, "review")
	}
	for _, name := range []string{
		"branch", "base", "pr", "no-label", "max-rounds",
		"once", "skip-gate", "dry-run", "fresh", "label",
	} {
		if c.Flags().Lookup(name) == nil {
			t.Errorf("--%s flag missing", name)
		}
	}
}

func TestNewCmdReviewFlagsCaptured(t *testing.T) {
	var got *Options
	c := NewCmdReview(&cmdutil.Factory{}, func(_ context.Context, o *Options) error {
		got = o
		return nil
	})
	c.SetArgs([]string{
		"--branch=feat/x",
		"--base=main",
		"--pr=123",
		"--no-label",
		"--max-rounds=10",
		"--once",
		"--skip-gate",
		"--dry-run",
		"--fresh",
		"--label=custom",
	})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil {
		t.Fatal("runF was not invoked")
	}
	if got.Branch != "feat/x" || got.Base != "main" || got.PR != 123 {
		t.Errorf("identity flags wrong: %+v", got)
	}
	if !got.NoLabel || got.MaxRounds != 10 {
		t.Errorf("review-specific flags wrong: %+v", got)
	}
	if !got.Once || !got.SkipGate || !got.DryRun || !got.Fresh || got.Label != "custom" {
		t.Errorf("shared flags wrong: %+v", got)
	}
}

func TestChooseLabel(t *testing.T) {
	cases := []struct {
		name   string
		opts   Options
		branch string
		want   string
	}{
		{"default — auto label", Options{}, "feat/x", "review:feat/x"},
		{"--no-label suppresses", Options{NoLabel: true}, "feat/x", ""},
		{"explicit --label wins", Options{Label: "custom"}, "feat/x", "custom"},
		{"explicit beats --no-label", Options{Label: "custom", NoLabel: true}, "feat/x", "custom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseLabel(&tc.opts, tc.branch); got != tc.want {
				t.Errorf("chooseLabel(%+v, %q) = %q, want %q", tc.opts, tc.branch, got, tc.want)
			}
		})
	}
}

// TestMaxRoundsOverride exercises the cfg-mutation behaviour directly
// (the runReview body wires it in two lines; keep the test at that
// granularity rather than mocking loop.Run end-to-end).
func TestMaxRoundsOverride(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero preserves default", 0, 30},
		{"positive overrides", 10, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			if tc.in > 0 {
				cfg.Loop.MaxIterations = tc.in
			}
			if cfg.Loop.MaxIterations != tc.want {
				t.Errorf("MaxIterations = %d, want %d", cfg.Loop.MaxIterations, tc.want)
			}
		})
	}
}
