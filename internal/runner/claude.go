// Package runner spawns the configured AI runner (v1: claude) and
// captures its output. It only invokes the subprocess and parses the
// JSON envelope. Failure-mode classification lives in
// internal/runner/classify.go (a separate bead). Isolation lives in
// internal/isolation.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// --dangerously-skip-permissions, --output-format=json). model, when
// non-empty, is appended as `--model <model>` after args so operator
// args still take precedence; empty omits the flag entirely (no
// `--model ""`). memLimit is the systemd-run MemoryMax value (e.g.
// "7G", "512m"); empty disables the scope wrapper so each Run execs
// command directly. Production passes cfg.Loop.MemoryLimit; tests
// usually pass "".
func New(command string, args []string, model, memLimit string) *Runner {
	a := append([]string(nil), args...)
	if model != "" {
		a = append(a, "--model", model)
	}
	return &Runner{
		cmd:      command,
		args:     a,
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
	Result         string
	IsError        bool
	APIErrorStatus any
	Raw            map[string]any
}

// RunOpts bundles parameters for one Run call. StdoutPath/StderrPath
// are required: the runner writes subprocess output directly to these
// files (no in-memory buffer), so output appears on disk as the
// subprocess produces it instead of after Wait. StdoutTee/StderrTee,
// when non-nil, receive a copy of the bytes — production passes the
// operator's terminal so output streams live; tests usually leave them
// nil.
type RunOpts struct {
	Prompt     string
	Cwd        string
	ExtraEnv   []string
	StdoutPath string
	StderrPath string
	StdoutTee  io.Writer
	StderrTee  io.Writer
}

// Run starts the runner with opts.Prompt on stdin and waits for it to
// complete, honoring ctx for cancellation/timeout. Subprocess stdout
// and stderr stream to opts.StdoutPath / opts.StderrPath (and to
// opts.StdoutTee / opts.StderrTee when set) as they're produced — the
// runner does not buffer them in memory. The returned error is non-nil
// only for failures starting the process or opening the log files;
// non-zero exit codes are reported via Session.ExitCode.
func (r *Runner) Run(ctx context.Context, opts RunOpts) (*Session, error) {
	if r.cmd == "" {
		return nil, errors.New("runner: empty command")
	}
	if opts.StdoutPath == "" || opts.StderrPath == "" {
		return nil, errors.New("runner: stdout/stderr paths required")
	}
	start := time.Now()

	stdoutFile, err := os.Create(opts.StdoutPath)
	if err != nil {
		return nil, fmt.Errorf("runner: create stdout file: %w", err)
	}
	stderrFile, err := os.Create(opts.StderrPath)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, fmt.Errorf("runner: create stderr file: %w", err)
	}

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
	cmd.Dir = opts.Cwd
	cmd.Stdin = strings.NewReader(opts.Prompt)
	var stdoutW io.Writer = stdoutFile
	if opts.StdoutTee != nil {
		stdoutW = io.MultiWriter(stdoutFile, opts.StdoutTee)
	}
	var stderrW io.Writer = stderrFile
	if opts.StderrTee != nil {
		stderrW = io.MultiWriter(stderrFile, opts.StderrTee)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	if len(opts.ExtraEnv) > 0 {
		cmd.Env = append(cmd.Environ(), opts.ExtraEnv...)
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

	// Close before reading back so any buffered writes flush to disk.
	_ = stdoutFile.Close()
	_ = stderrFile.Close()

	timeoutKilled := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if runErr != nil && !timeoutKilled {
		// Distinguish "couldn't start" from "exited non-zero".
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return nil, fmt.Errorf("runner: start %s: %w", r.cmd, runErr)
		}
	}

	// Re-read from disk for Session.Stdout/Stderr + envelope parsing.
	// Streaming-to-disk made the runtime memory profile flat; the read-
	// back is a transient end-of-run cost.
	outBytes, _ := os.ReadFile(opts.StdoutPath)
	errBytes, _ := os.ReadFile(opts.StderrPath)
	outStr := string(outBytes)
	errStr := string(errBytes)
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

// Envelope JSON keys read from more than one map (top-level and the
// nested usage{} object); named so the two reads stay in lockstep.
const (
	keyInputTokens  = "input_tokens"
	keyOutputTokens = "output_tokens"
)

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
	if v, ok := raw["result"].(string); ok {
		e.Result = v
	}
	if v, ok := raw["is_error"].(bool); ok {
		e.IsError = v
	}
	e.APIErrorStatus = raw["api_error_status"]

	// Tokens may be at top-level or under usage{}. The keys are named
	// consts because each is read from two maps (top-level + usage).
	e.InputTokens = readInt(raw, keyInputTokens)
	e.OutputTokens = readInt(raw, keyOutputTokens)
	if usage, ok := raw["usage"].(map[string]any); ok {
		if v := readInt(usage, keyInputTokens); v != 0 {
			e.InputTokens = v
		}
		if v := readInt(usage, keyOutputTokens); v != 0 {
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
