// Package hook implements `ralph hook run <path>`, a thin wrapper
// that invokes a hook with the documented env (RALPH_REPO, RALPH_STATE,
// RALPH_ITER, …) so users can debug hooks outside the loop.
package hook

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/hooks"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type Options struct {
	F *cmdutil.Factory

	Path      string
	State     string
	PrevState string
	NextState string
	ExtraEnv  []string
}

func NewCmdHook(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Inspect and run ralph hooks",
	}
	cmd.AddCommand(newCmdRun(f, runF))
	return cmd
}

func newCmdRun(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "run <path>",
		Short: "Run a hook manually with the standard environment",
		Long: `<path> is relative to .ralph/hooks/ or absolute. The resolved path
must live under <repo>/.ralph/hooks/. The hook's exit code propagates
to ralph's exit code; stdout and stderr are passed through.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Path = args[0]
			if opts.State != "" && !fsm.State(opts.State).Valid() {
				return cmdutil.FlagErrorf("unknown --state %q", opts.State)
			}
			if err := validateEnvPairs(opts.ExtraEnv); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return run(c.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.State, "state", "", "value for RALPH_STATE (default: from fsm.json)")
	cmd.Flags().StringVar(&opts.PrevState, "prev-state", "", "value for RALPH_PREV_STATE")
	cmd.Flags().StringVar(&opts.NextState, "next-state", "", "value for RALPH_NEXT_STATE")
	cmd.Flags().StringSliceVar(&opts.ExtraEnv, "env", nil, "extra KEY=VALUE pairs (repeatable)")
	return cmd
}

func validateEnvPairs(env []string) error {
	for _, kv := range env {
		if !strings.Contains(kv, "=") {
			return cmdutil.FlagErrorf("--env value %q is not KEY=VALUE", kv)
		}
	}
	return nil
}

func run(ctx context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	resolved, err := resolvePath(repo, opts.Path)
	if err != nil {
		return err
	}
	env, err := buildEnv(repo, resolved, opts)
	if err != nil {
		return err
	}

	res, err := hooks.Run(ctx, resolved, env, nil)
	if err != nil {
		return err
	}
	if res.NoHook {
		return fmt.Errorf("hook: %s does not exist", resolved)
	}

	io := opts.F.IOStreams
	if res.Stdout != "" {
		fmt.Fprint(io.Out, res.Stdout)
		if !strings.HasSuffix(res.Stdout, "\n") {
			fmt.Fprintln(io.Out)
		}
	}
	if res.Stderr != "" {
		fmt.Fprint(io.ErrOut, res.Stderr)
		if !strings.HasSuffix(res.Stderr, "\n") {
			fmt.Fprintln(io.ErrOut)
		}
	}
	if res.ExitCode != 0 {
		return &cmdutil.ExitCodeError{Code: res.ExitCode}
	}
	return nil
}

// resolvePath turns a user-supplied <path> (relative to .ralph/hooks/
// or absolute) into an absolute path and verifies containment under
// <repo>/.ralph/hooks/ (byob-input-validation.1).
func resolvePath(repo, in string) (string, error) {
	hooksDir, err := filepath.Abs(hooks.HooksDir(repo))
	if err != nil {
		return "", err
	}
	var candidate string
	if filepath.IsAbs(in) {
		candidate = in
	} else {
		candidate = filepath.Join(hooksDir, in)
	}
	abs, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(hooksDir, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", cmdutil.FlagErrorf("hook path %q escapes %s", in, hooksDir)
	}
	return abs, nil
}

// buildEnv composes the hooks.Env, loading fsm.json for defaults and
// applying flag overrides.
func buildEnv(repo, hookPath string, opts *Options) (hooks.Env, error) {
	env := hooks.Env{
		Repo:      repo,
		State:     opts.State,
		PrevState: opts.PrevState,
		NextState: opts.NextState,
		Extra:     opts.ExtraEnv,
	}
	f, err := fsm.Load(repo)
	if err != nil && !errors.Is(err, fsm.ErrSchemaTooNew) {
		// A schema-too-new fsm.json shouldn't block manual hook
		// testing; everything else is.
		return env, err
	}
	if f != nil {
		if env.State == "" {
			env.State = string(f.State)
		}
		env.Iter = f.Iter
	}
	_ = hookPath // currently unused beyond logging; reserved
	return env, nil
}
