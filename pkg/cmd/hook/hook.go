// Package hook implements `ralph hook run <path>`, a thin wrapper
// that invokes a hook with the documented env (RALPH_REPO, RALPH_STATE,
// RALPH_ITER, …) so users can debug hooks outside the loop.
package hook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/hooks"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	Path      string
	State     string
	PrevState string
	NextState string
	ExtraEnv  []string
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
func (o *Options) Validate() error {
	if o.State != "" && !fsm.State(o.State).Valid() {
		return cmdutil.FlagErrorf("unknown --state %q", o.State)
	}
	for _, kv := range o.ExtraEnv {
		if !strings.Contains(kv, "=") {
			return cmdutil.FlagErrorf("--env value %q is not KEY=VALUE", kv)
		}
	}
	return nil
}

// NewCmdHook returns the cobra command for `ralph hook`.
func NewCmdHook(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Inspect and run ralph hooks",
		Long: `Hooks are git-style executable scripts under .ralph/hooks/ that
ralph invokes at well-known points (pre-iteration, post-iteration,
failure, and per-state enter/exit/gate). The hook subcommand is for
running them manually with the documented environment so you can
debug them outside the loop.`,
	}
	cmd.AddCommand(newCmdRun(f, runF))
	return cmd
}

func newCmdRun(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "run <path>",
		Short: "Run a hook manually with the standard environment",
		Long: `<path> is relative to .ralph/hooks/ or absolute. The resolved path
must live under <repo>/.ralph/hooks/. The hook's exit code propagates
to ralph's exit code; stdout and stderr are passed through.

RALPH_REPO, RALPH_STATE, and RALPH_ITER are populated from fsm.json
by default; override them with --state, --prev-state, --next-state,
or --env KEY=VALUE to reproduce a specific iteration's environment.`,
		Example: `  # run the global pre-iteration hook with current fsm.json state
  ralph hook run pre-iteration

  # simulate the clean → dirty transition for the dirty/enter hook
  ralph hook run dirty/enter --state=dirty --prev-state=clean

  # inject extra env vars for an ad-hoc hook
  ralph hook run my-debug-hook --env=DEBUG=1 --env=DRY_RUN=1`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Path = args[0]
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return run(c.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.State, "state", "", "value for RALPH_STATE (default: from fsm.json)")
	cmd.Flags().StringVar(&opts.PrevState, "prev-state", "", "value for RALPH_PREV_STATE")
	cmd.Flags().StringVar(&opts.NextState, "next-state", "", "value for RALPH_NEXT_STATE")
	cmd.Flags().StringSliceVar(&opts.ExtraEnv, "env", nil, "extra KEY=VALUE pairs (repeatable)")
	stateCompletions := cobra.FixedCompletions(fsm.AllStateNames(), cobra.ShellCompDirectiveNoFileComp)
	cmdutil.MustRegisterFlagCompletion(cmd, "state", stateCompletions)
	cmdutil.MustRegisterFlagCompletion(cmd, "prev-state", stateCompletions)
	cmdutil.MustRegisterFlagCompletion(cmd, "next-state", stateCompletions)
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return completeHookPaths(f, toComplete)
	}
	return cmd
}

// completeHookPaths offers relative paths under .ralph/hooks/ that begin
// with toComplete. Missing or unreadable hooks dir falls through to file
// completion so the shell stays usable.
func completeHookPaths(f *cmdutil.Factory, toComplete string) ([]string, cobra.ShellCompDirective) {
	repo, err := f.RepoRoot()
	if err != nil {
		return nil, cobra.ShellCompDirectiveDefault
	}
	root := hooks.Dir(repo)
	var out []string
	werr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == root {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		if toComplete == "" || strings.HasPrefix(rel, toComplete) {
			out = append(out, rel)
		}
		return nil
	})
	if werr != nil {
		return nil, cobra.ShellCompDirectiveDefault
	}
	return out, cobra.ShellCompDirectiveNoFileComp
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

	res, err := hooks.Run(ctx, resolved, env, nil, nil, nil)
	if err != nil {
		return err
	}
	if res.NoHook {
		return fmt.Errorf("hook: %s does not exist", resolved)
	}

	io := opts.F.IOStreams
	if res.Stdout != "" {
		_, _ = fmt.Fprint(io.Out, res.Stdout)
		if !strings.HasSuffix(res.Stdout, "\n") {
			_, _ = fmt.Fprintln(io.Out)
		}
	}
	if res.Stderr != "" {
		_, _ = fmt.Fprint(io.ErrOut, res.Stderr)
		if !strings.HasSuffix(res.Stderr, "\n") {
			_, _ = fmt.Fprintln(io.ErrOut)
		}
	}
	if res.ExitCode != 0 {
		return &cmdutil.ExitCodeError{Code: res.ExitCode}
	}
	return nil
}

// resolvePath turns a user-supplied <path> (relative to .ralph/hooks/
// or absolute) into an absolute path and verifies containment under
// <repo>/.ralph/hooks/, following symlinks so a symlink inside the
// hooks dir cannot escape it (byob-input-validation.1). Missing
// targets are allowed through unresolved so callers can report
// "does not exist" cleanly.
func resolvePath(repo, in string) (string, error) {
	hooksDir, err := filepath.Abs(hooks.Dir(repo))
	if err != nil {
		return "", err
	}
	resolvedDir, err := filepath.EvalSymlinks(hooksDir)
	if err != nil {
		// hooks dir may not exist on a fresh repo; fall back to the
		// abs path so containment checks still work.
		resolvedDir = hooksDir
	}
	var candidate string
	if filepath.IsAbs(in) {
		candidate = in
	} else {
		candidate = filepath.Join(resolvedDir, in)
	}
	abs, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", err
	}
	check := abs
	if resolved, serr := filepath.EvalSymlinks(abs); serr == nil {
		check = resolved
	}
	rel, err := filepath.Rel(resolvedDir, check)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", cmdutil.FlagErrorf("hook path %q escapes %s", in, resolvedDir)
	}
	return check, nil
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
