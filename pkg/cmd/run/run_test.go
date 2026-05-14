package run

import (
	"testing"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

func TestNewCmdRunMetadata(t *testing.T) {
	c := NewCmdRun(&cmdutil.Factory{}, func(*Options) error { return nil })
	if c.Use != "run" {
		t.Errorf("Use = %q, want %q", c.Use, "run")
	}
	for _, name := range []string{
		"once", "skip-gate", "dry-run", "label",
		"max-iterations", "timeout", "memory",
	} {
		if c.Flags().Lookup(name) == nil {
			t.Errorf("--%s flag missing", name)
		}
	}
}

// TestNewCmdRunFlagsCaptured exercises the cobra flag-parser end-to-end
// via the runF seam (byob-testing.1): no real loop.Run runs, but every
// flag round-trips into Options.
func TestNewCmdRunFlagsCaptured(t *testing.T) {
	var got *Options
	c := NewCmdRun(&cmdutil.Factory{}, func(o *Options) error {
		got = o
		return nil
	})
	c.SetArgs([]string{
		"--once",
		"--skip-gate",
		"--dry-run",
		"--label=foo",
		"--max-iterations=7",
		"--timeout=30",
		"--memory=512m",
	})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil {
		t.Fatal("runF was not invoked")
	}
	if !got.Once || !got.SkipGate || !got.DryRun {
		t.Errorf("bool flags wrong: %+v", got)
	}
	if got.Label != "foo" {
		t.Errorf("Label = %q, want foo", got.Label)
	}
	if got.MaxIterations != 7 || got.SessionTimeout != 30 || got.MemoryLimit != "512m" {
		t.Errorf("override flags wrong: max=%d timeout=%d mem=%q",
			got.MaxIterations, got.SessionTimeout, got.MemoryLimit)
	}
}

func TestApplyOverrides(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want config.LoopConfig
	}{
		{
			name: "all zero — preserves config defaults",
			opts: Options{},
			want: config.LoopConfig{
				MaxIterations:      30,
				SessionTimeoutSecs: 3600,
				MemoryLimit:        "7G",
				SleepBetweenSecs:   5,
			},
		},
		{
			name: "max-iterations only",
			opts: Options{MaxIterations: 7},
			want: config.LoopConfig{
				MaxIterations:      7,
				SessionTimeoutSecs: 3600,
				MemoryLimit:        "7G",
				SleepBetweenSecs:   5,
			},
		},
		{
			name: "timeout only",
			opts: Options{SessionTimeout: 30},
			want: config.LoopConfig{
				MaxIterations:      30,
				SessionTimeoutSecs: 30,
				MemoryLimit:        "7G",
				SleepBetweenSecs:   5,
			},
		},
		{
			name: "memory only",
			opts: Options{MemoryLimit: "1G"},
			want: config.LoopConfig{
				MaxIterations:      30,
				SessionTimeoutSecs: 3600,
				MemoryLimit:        "1G",
				SleepBetweenSecs:   5,
			},
		},
		{
			name: "all three",
			opts: Options{MaxIterations: 5, SessionTimeout: 60, MemoryLimit: "2G"},
			want: config.LoopConfig{
				MaxIterations:      5,
				SessionTimeoutSecs: 60,
				MemoryLimit:        "2G",
				SleepBetweenSecs:   5,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			applyOverrides(cfg, &tc.opts)
			if cfg.Loop != tc.want {
				t.Errorf("Loop = %+v, want %+v", cfg.Loop, tc.want)
			}
		})
	}
}
