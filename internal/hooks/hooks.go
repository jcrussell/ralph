// Package hooks locates and runs ralph's git-style executable hooks.
//
// Layout under <repo>/.ralph/hooks/:
//
//	pre-iteration     (global)
//	post-iteration    (global)
//	failure           (global)
//	states/<state>/enter
//	states/<state>/exit
//	states/<state>/gate
//
// Filenames are single verbs (enter, exit, gate) — not on_enter/on_exit.
// A missing hook is not an error; Run returns (NoHook, nil).
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// Result captures one hook invocation.
type Result struct {
	Path     string
	NoHook   bool   // true if the hook script wasn't present
	ExitCode int    // 0 when NoHook
	Stdout   string // captured even when ExitCode != 0
	Stderr   string
}

// Env is the stable environment ralph hands every hook. Fields that
// don't apply to a phase get omitted by Run.
type Env struct {
	Repo       string // RALPH_REPO
	Iter       int    // RALPH_ITER
	State      string // RALPH_STATE
	PrevState  string // RALPH_PREV_STATE (enter/exit)
	NextState  string // RALPH_NEXT_STATE (exit only)
	PromptFile string // RALPH_PROMPT_FILE (per-iter rendered prompt path)
	IterJSON   string // RALPH_ITER_JSON   (per-iter record path)
	// Failure-only:
	FailureMode   string // RALPH_FAILURE_MODE
	FailureReason string // RALPH_FAILURE_REASON
	// Review-only:
	ReviewBranch string // RALPH_REVIEW_BRANCH
	ReviewBase   string // RALPH_REVIEW_BASE
	// Extra environment variables passed through (already KEY=VALUE).
	Extra []string
}

// Phase names a per-state phase.
type Phase string

const (
	PhaseEnter Phase = "enter"
	PhaseExit  Phase = "exit"
	PhaseGate  Phase = "gate"
)

// HooksDir returns the .ralph/hooks directory under repoRoot.
func HooksDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".ralph", "hooks")
}

// GlobalPath returns the absolute path of a global hook by name
// (pre-iteration, post-iteration, failure).
func GlobalPath(repoRoot, name string) string {
	return filepath.Join(HooksDir(repoRoot), name)
}

// StatePath returns the absolute path of a per-state hook for the
// given state + phase.
func StatePath(repoRoot, state string, phase Phase) string {
	return filepath.Join(HooksDir(repoRoot), "states", state, string(phase))
}

// Run executes the hook at path with the documented env. If path
// doesn't exist, Run returns Result{NoHook: true} and nil. If path
// exists but is not executable, Run returns a clear error. stdin is
// piped to the hook if non-nil (used by post-iteration for the
// iteration JSON).
func Run(ctx context.Context, path string, env Env, stdin io.Reader) (Result, error) {
	r := Result{Path: path}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		r.NoHook = true
		return r, nil
	}
	if err != nil {
		return r, fmt.Errorf("hooks: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return r, fmt.Errorf("hooks: %s is a directory", path)
	}
	if info.Mode()&0o111 == 0 {
		return r, fmt.Errorf("hooks: %s is not executable (chmod +x)", path)
	}

	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = env.Repo
	cmd.Env = buildEnv(env)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	r.Stdout = out.String()
	r.Stderr = errBuf.String()
	r.ExitCode = cmd.ProcessState.ExitCode()

	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return r, fmt.Errorf("hooks: run %s: %w", path, runErr)
		}
		// Non-zero exit is information, not an error. Callers decide
		// (e.g. pre-iteration uses it to skip).
	}
	return r, nil
}

func buildEnv(e Env) []string {
	// Inherit the parent env, then override.
	env := os.Environ()
	add := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	add("RALPH_REPO", e.Repo)
	if e.Iter > 0 {
		env = append(env, fmt.Sprintf("RALPH_ITER=%d", e.Iter))
	}
	add("RALPH_STATE", e.State)
	add("RALPH_PREV_STATE", e.PrevState)
	add("RALPH_NEXT_STATE", e.NextState)
	add("RALPH_PROMPT_FILE", e.PromptFile)
	add("RALPH_ITER_JSON", e.IterJSON)
	add("RALPH_FAILURE_MODE", e.FailureMode)
	add("RALPH_FAILURE_REASON", e.FailureReason)
	add("RALPH_REVIEW_BRANCH", e.ReviewBranch)
	add("RALPH_REVIEW_BASE", e.ReviewBase)
	env = append(env, e.Extra...)
	return env
}
