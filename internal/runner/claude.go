// Package runner spawns the configured AI runner (v1: claude) and
// captures its output. It only invokes the subprocess and parses the
// JSON envelope. Failure-mode classification lives in
// internal/runner/classify.go (a separate bead). Isolation lives in
// internal/isolation.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jcrussell/ralph/internal/isolation"
)

// Runner invokes the AI runner subprocess. Construct via New.
type Runner struct {
	cmd      string
	args     []string
	memLimit string
	seq      atomic.Uint64
}

// New returns a Runner. command is looked up on PATH if it's not an
// absolute path. args are prepended to every invocation (e.g.
// --dangerously-skip-permissions, --output-format=json). memLimit is
// the systemd-run MemoryMax value (e.g. "7G", "512m"); empty disables
// the scope wrapper so each Run execs command directly. Production
// passes cfg.Loop.MemoryLimit; tests usually pass "".
func New(command string, args []string, memLimit string) *Runner {
	return &Runner{
		cmd:      command,
		args:     append([]string(nil), args...),
		memLimit: memLimit,
	}
}

// Session is the result of one runner invocation. JSON tags are
// indicative; the loop builds the canonical iteration-record shape
// from these fields and others.
type Session struct {
	ExitCode        int
	Duration        time.Duration
	KilledByTimeout bool
	OOMSignal       bool // cgroup memory.events reported oom_kill > 0
	Stdout          string
	Stderr          string
	StdoutTail      string
	StderrTail      string
	Envelope        *Envelope
}

// Envelope is the parsed view of claude's --output-format=json
// stdout. Fields are populated when present; missing fields stay at
// their zero value. Raw is the full decoded object for downstream
// classification that wants fields we haven't promoted.
type Envelope struct {
	TotalCostUSD   float64
	InputTokens    int64
	OutputTokens   int64
	NumTurns       int
	Subtype        string
	APIErrorStatus any
	Raw            map[string]any
}

// Run starts the runner with prompt on stdin and waits for it to
// complete, honoring ctx for cancellation/timeout. cwd is the
// working directory; extraEnv is appended to the parent env. The
// returned error is non-nil only for failures starting the process;
// non-zero exit codes are reported via Session.ExitCode.
func (r *Runner) Run(ctx context.Context, prompt string, cwd string, extraEnv []string) (*Session, error) {
	if r.cmd == "" {
		return nil, errors.New("runner: empty command")
	}
	start := time.Now()

	// Wrap argv with systemd-run --user --scope when memLimit is set so
	// the child runs under a cgroup with MemoryMax + MemorySwapMax=0 and
	// post-exec we can read cgroup memory.events for an OOM signal more
	// reliable than ExitCode==137. memLimit="" skips this and execs
	// command directly (test path, or operator opt-out via empty
	// memory_limit_bytes).
	execCmd, execArgs := r.cmd, r.args
	var scope isolation.Scope
	if r.memLimit != "" {
		scope = isolation.NewScope(r.unitBase(), r.memLimit)
		wrapped := scope.Argv(r.cmd, r.args)
		execCmd, execArgs = wrapped[0], wrapped[1:]
	}
	cmd := exec.CommandContext(ctx, execCmd, execArgs...) //nolint:gosec // execCmd/execArgs from operator-controlled config (possibly wrapped with systemd-run from internal/isolation)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Environ(), extraEnv...)
	}

	// Platform-specific: Unix puts the child in its own process group
	// so context cancel kills the whole tree (claude_unix.go); Windows
	// is a no-op since `ralph run` errors out on non-Linux before this
	// path runs (claude_windows.go).
	setupProcessGroup(cmd)
	// Bound how long Wait() blocks after Cancel fires before pipes get
	// force-closed; otherwise a slow grandchild can hold us up.
	cmd.WaitDelay = 5 * time.Second

	runErr := cmd.Run()
	dur := time.Since(start)

	timeoutKilled := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if runErr != nil && !timeoutKilled {
		// Distinguish "couldn't start" from "exited non-zero".
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return nil, fmt.Errorf("runner: start %s: %w", r.cmd, runErr)
		}
	}

	outStr := stdout.String()
	errStr := stderr.String()
	s := &Session{
		ExitCode:        cmd.ProcessState.ExitCode(),
		Duration:        dur,
		KilledByTimeout: timeoutKilled,
		Stdout:          outStr,
		Stderr:          errStr,
		StdoutTail:      tail(outStr, 2000),
		StderrTail:      tail(errStr, 1000),
	}
	s.Envelope = parseEnvelope(outStr)
	// Best-effort OOM probe: when wrapped, the cgroup memory.events file
	// often still exists for a moment after the scope's child exits.
	// A missing file (cleanup already happened) returns false — the
	// ExitCode==137 fallback in Classify picks up the rest.
	if r.memLimit != "" {
		if oom, _ := isolation.OOMKilledFile(scope.EventsPath()); oom {
			s.OOMSignal = true
		}
	}
	return s, nil
}

// unitBase returns a unique systemd unit name for this Run call. The
// shape is "ralph-<pid>-<seq>"; the .scope suffix is appended by
// isolation.NewScope. pid + per-Runner counter keeps names predictable
// for `journalctl --user --unit=...` without coupling the runner to
// the run-id / iter the orchestrator owns.
func (r *Runner) unitBase() string {
	n := r.seq.Add(1)
	return fmt.Sprintf("ralph-%d-%d", os.Getpid(), n)
}

// parseEnvelope returns nil when stdout is not a valid JSON object,
// matching the Python reference behavior.
func parseEnvelope(stdout string) *Envelope {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil
	}
	e := &Envelope{Raw: raw}

	e.TotalCostUSD = readFloat(raw, "total_cost_usd", "cost_usd")
	e.NumTurns = int(readFloat(raw, "num_turns"))
	if v, ok := raw["subtype"].(string); ok {
		e.Subtype = v
	}
	e.APIErrorStatus = raw["api_error_status"]

	// Tokens may be at top-level or under usage{}.
	e.InputTokens = readInt(raw, "input_tokens")
	e.OutputTokens = readInt(raw, "output_tokens")
	if usage, ok := raw["usage"].(map[string]any); ok {
		if v := readInt(usage, "input_tokens"); v != 0 {
			e.InputTokens = v
		}
		if v := readInt(usage, "output_tokens"); v != 0 {
			e.OutputTokens = v
		}
	}
	return e
}

func readFloat(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return v
		case json.Number:
			f, _ := v.Float64()
			return f
		}
	}
	return 0
}

func readInt(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int64(v)
		case json.Number:
			i, _ := v.Int64()
			return i
		}
	}
	return 0
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
