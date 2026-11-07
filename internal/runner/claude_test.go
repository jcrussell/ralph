package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/isolation"
)

func TestRunSuccessParsesEnvelope(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
cat > /dev/null  # consume stdin
echo '{"total_cost_usd": 0.42, "num_turns": 3, "subtype": "success", "usage": {"input_tokens": 1000, "output_tokens": 2500}}'
exit 0
`)
	r := New(bin, nil, "", "")
	ctx := context.Background()
	s, err := runForTest(t, r, ctx, "hello", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", s.ExitCode)
	}
	if s.KilledByTimeout {
		t.Errorf("KilledByTimeout = true, want false")
	}
	if s.Envelope == nil {
		t.Fatalf("Envelope = nil, want parsed")
	}
	if s.Envelope.TotalCostUSD != 0.42 {
		t.Errorf("TotalCostUSD = %v, want 0.42", s.Envelope.TotalCostUSD)
	}
	if s.Envelope.NumTurns != 3 {
		t.Errorf("NumTurns = %d, want 3", s.Envelope.NumTurns)
	}
	if s.Envelope.Subtype != "success" {
		t.Errorf("Subtype = %q, want success", s.Envelope.Subtype)
	}
	if s.Envelope.InputTokens != 1000 || s.Envelope.OutputTokens != 2500 {
		t.Errorf("tokens = (%d, %d), want (1000, 2500)", s.Envelope.InputTokens, s.Envelope.OutputTokens)
	}
}

func TestRunLegacyCostKey(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo '{"cost_usd": 1.5, "input_tokens": 100, "output_tokens": 200}'
`)
	r := New(bin, nil, "", "")
	s, err := runForTest(t, r, context.Background(), "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Envelope == nil {
		t.Fatalf("Envelope = nil")
	}
	if s.Envelope.TotalCostUSD != 1.5 {
		t.Errorf("TotalCostUSD = %v, want 1.5", s.Envelope.TotalCostUSD)
	}
	if s.Envelope.InputTokens != 100 || s.Envelope.OutputTokens != 200 {
		t.Errorf("tokens = (%d, %d), want (100, 200)", s.Envelope.InputTokens, s.Envelope.OutputTokens)
	}
}

func TestRunNonZeroExitNoEnvelope(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo "boom" >&2
exit 3
`)
	r := New(bin, nil, "", "")
	s, err := runForTest(t, r, context.Background(), "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", s.ExitCode)
	}
	if s.Envelope != nil {
		t.Errorf("Envelope = %+v, want nil (no JSON on stdout)", s.Envelope)
	}
	if !strings.Contains(s.Stderr, "boom") {
		t.Errorf("Stderr = %q, want to contain boom", s.Stderr)
	}
}

func TestRunStdinIsPrompt(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
exec cat   # echoes stdin to stdout
`)
	r := New(bin, nil, "", "")
	prompt := "the prompt body"
	s, err := runForTest(t, r, context.Background(), prompt, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(s.Stdout, prompt) {
		t.Errorf("Stdout = %q, want to contain prompt %q", s.Stdout, prompt)
	}
}

func TestRunPassesArgs(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo "$@" > "$RALPH_TEST_ARGS_OUT"
`)
	argsOut := filepath.Join(t.TempDir(), "args")
	r := New(bin, []string{"--flag-a", "--flag-b=value"}, "", "")
	_, err := runForTest(t, r, context.Background(), "", t.TempDir(), []string{"RALPH_TEST_ARGS_OUT=" + argsOut})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := os.ReadFile(argsOut)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := strings.TrimSpace(string(b))
	want := "--flag-a --flag-b=value"
	if got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestRunModelAppendedWhenSet(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo "$@" > "$RALPH_TEST_ARGS_OUT"
`)
	argsOut := filepath.Join(t.TempDir(), "args")
	r := New(bin, []string{"--flag-a"}, "sonnet", "")
	_, err := runForTest(t, r, context.Background(), "", t.TempDir(), []string{"RALPH_TEST_ARGS_OUT=" + argsOut})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := os.ReadFile(argsOut)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := strings.TrimSpace(string(b))
	want := "--flag-a --model sonnet"
	if got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestRunModelOmittedWhenUnset(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo "$@" > "$RALPH_TEST_ARGS_OUT"
`)
	argsOut := filepath.Join(t.TempDir(), "args")
	r := New(bin, []string{"--flag-a"}, "", "")
	_, err := runForTest(t, r, context.Background(), "", t.TempDir(), []string{"RALPH_TEST_ARGS_OUT=" + argsOut})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := os.ReadFile(argsOut)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := strings.TrimSpace(string(b))
	if want := "--flag-a"; got != want {
		t.Errorf("argv = %q, want %q (no --model)", got, want)
	}
}

func TestRunTimeoutKills(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in -short")
	}
	bin := writeScript(t, `#!/bin/sh
sleep 30
echo '{}'
`)
	r := New(bin, nil, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	s, err := runForTest(t, r, ctx, "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Run took %s; expected near 250ms", elapsed)
	}
	if !s.KilledByTimeout {
		t.Errorf("KilledByTimeout = false, want true")
	}
}

func TestRunNonJSONStdout(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo "hello, not JSON"
`)
	r := New(bin, nil, "", "")
	s, err := runForTest(t, r, context.Background(), "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Envelope != nil {
		t.Errorf("Envelope = %+v, want nil for non-JSON stdout", s.Envelope)
	}
	if !strings.Contains(s.StdoutTail, "hello") {
		t.Errorf("StdoutTail = %q, want hello", s.StdoutTail)
	}
}

func TestRunRateLimitEnvelopeReaches(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo '{"subtype": "error", "api_error_status": 429, "is_error": true}'
exit 1
`)
	r := New(bin, nil, "", "")
	s, err := runForTest(t, r, context.Background(), "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Envelope == nil {
		t.Fatalf("Envelope = nil")
	}
	if s.Envelope.Subtype != "error" {
		t.Errorf("Subtype = %q, want error", s.Envelope.Subtype)
	}
	if v, ok := s.Envelope.APIErrorStatus.(float64); !ok || v != 429 {
		t.Errorf("APIErrorStatus = %v (%T), want 429 (float64)", s.Envelope.APIErrorStatus, s.Envelope.APIErrorStatus)
	}
	if !s.Envelope.IsError {
		t.Errorf("IsError = false, want true")
	}
	if _, ok := s.Envelope.Raw["is_error"]; !ok {
		t.Errorf("Raw missing is_error: %v", s.Envelope.Raw)
	}
}

// TestRunQuotaEnvelopeClassifies pins the end-to-end path for the
// captured-from-the-wild monthly-quota envelope: subprocess emits the
// real JSON shape, parseEnvelope must populate Result + IsError, and
// Classify must return ModeQuota so the loop exits failed{quota}
// instead of looping to iter_cap.
func TestRunQuotaEnvelopeClassifies(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo '{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"duration_ms":294,"num_turns":1,"result":"You'"'"'ve hit your org'"'"'s monthly usage limit","stop_reason":"stop_sequence"}'
`)
	r := New(bin, nil, "", "")
	s, err := runForTest(t, r, context.Background(), "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Envelope == nil {
		t.Fatalf("Envelope = nil")
	}
	if !s.Envelope.IsError {
		t.Errorf("IsError = false, want true")
	}
	if want := "You've hit your org's monthly usage limit"; s.Envelope.Result != want {
		t.Errorf("Result = %q, want %q", s.Envelope.Result, want)
	}
	if got := Classify(s); got != ModeQuota {
		t.Errorf("Classify = %s, want %s", got, ModeQuota)
	}
}

// TestRunPreStartDeadline pins behavior when the context's deadline
// has already passed before cmd.Run reaches Start: exec returns
// context.DeadlineExceeded before forking and ProcessState stays nil.
// The runner must report KilledByTimeout=true and ExitCode=-1
// (relying on os.ProcessState.ExitCode()'s nil-receiver fallback).
func TestRunPreStartDeadline(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
echo '{}'
`)
	r := New(bin, nil, "", "")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	s, err := runForTest(t, r, ctx, "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.KilledByTimeout {
		t.Errorf("KilledByTimeout = false, want true")
	}
	if s.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 (no process started)", s.ExitCode)
	}
}

func TestRunMissingBinary(t *testing.T) {
	r := New("/no/such/binary-12345", nil, "", "")
	_, err := runForTest(t, r, context.Background(), "", t.TempDir(), nil)
	if err == nil {
		t.Fatalf("Run: nil err, want missing-binary error")
	}
}

// TestRunWrapsArgvWhenMemLimitSet confirms that a non-empty memLimit
// causes the Runner to exec `systemd-run --user --scope --unit=... -p
// MemoryMax=... -p MemorySwapMax=0 -- <cmd> <args>...` instead of
// <cmd> directly. We plant a fake systemd-run on PATH that echoes its
// argv to a file and exits 0 — so the test does not require the real
// systemd-run binary.
func TestRunWrapsArgvWhenMemLimitSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts require POSIX shell")
	}
	binDir := t.TempDir()
	argvOut := filepath.Join(t.TempDir(), "argv")
	fake := filepath.Join(binDir, "systemd-run")
	body := `#!/bin/sh
{ for a in "$@"; do printf '%s\n' "$a"; done; } > "$RALPH_TEST_ARGV_OUT"
echo '{}'
`
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake systemd-run: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := New("/path/to/claude", []string{"--dangerously-skip-permissions"}, "", "1G")
	if _, err := runForTest(t, r, context.Background(), "", t.TempDir(), []string{"RALPH_TEST_ARGV_OUT=" + argvOut}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(argvOut)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	argv := strings.Split(strings.TrimSpace(string(got)), "\n")
	// Sanity: order matters. We expect --user --scope --unit=... -p MemoryMax=1G -p MemorySwapMax=0 -- /path/to/claude --dangerously-skip-permissions
	want := []string{"--user", "--scope"}
	for i, w := range want {
		if i >= len(argv) || argv[i] != w {
			t.Fatalf("argv[%d]=%q, want %q; full argv=%v", i, safeIdx(argv, i), w, argv)
		}
	}
	// systemd-run --scope synthesizes the .scope suffix from --unit, so
	// isolation.Scope.Argv passes the base form without it.
	if !strings.HasPrefix(argv[2], "--unit=ralph-") {
		t.Errorf("argv[2]=%q, want --unit=ralph-*", argv[2])
	}
	if strings.HasSuffix(argv[2], ".scope") {
		t.Errorf("argv[2]=%q, must not include .scope suffix (systemd-run adds it)", argv[2])
	}
	wantTail := []string{"-p", "MemoryMax=1G", "-p", "MemorySwapMax=0", "--", "/path/to/claude", "--dangerously-skip-permissions"}
	tail := argv[3:]
	if len(tail) != len(wantTail) {
		t.Fatalf("tail = %v, want %v", tail, wantTail)
	}
	for i, w := range wantTail {
		if tail[i] != w {
			t.Errorf("tail[%d]=%q, want %q", i, tail[i], w)
		}
	}
}

// TestRunUnitNamesAreUnique confirms successive Run calls on one
// Runner produce distinct unit names — systemd refuses duplicate
// transient units within a session.
func TestRunUnitNamesAreUnique(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts require POSIX shell")
	}
	binDir := t.TempDir()
	argvOut := filepath.Join(t.TempDir(), "argv")
	fake := filepath.Join(binDir, "systemd-run")
	body := `#!/bin/sh
for a in "$@"; do
  case "$a" in --unit=*) printf '%s\n' "$a" >> "$RALPH_TEST_ARGV_OUT" ;; esac
done
echo '{}'
`
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := New("/path/to/claude", nil, "", "512m")
	for i := 0; i < 3; i++ {
		if _, err := runForTest(t, r, context.Background(), "", t.TempDir(), []string{"RALPH_TEST_ARGV_OUT=" + argvOut}); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	got, err := os.ReadFile(argvOut)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 3 {
		t.Fatalf("captured %d unit flags, want 3: %v", len(lines), lines)
	}
	seen := map[string]bool{}
	for _, l := range lines {
		if seen[l] {
			t.Errorf("duplicate unit name: %s; all=%v", l, lines)
		}
		seen[l] = true
	}
}

// fakeSystemdBin plants a systemd-run that drops its own flags and execs
// the wrapped command, and a systemctl that appends its argv (one line
// per call) to the returned file. Both go first on PATH.
func fakeSystemdBin(t *testing.T) (systemctlLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts require POSIX shell")
	}
	binDir := t.TempDir()
	systemctlLog = filepath.Join(t.TempDir(), "systemctl.log")
	run := `#!/bin/sh
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
shift
exec "$@"
`
	// `show` prints $RALPH_TEST_SHOW (empty: the unit was collected).
	ctl := `#!/bin/sh
echo "$*" >> "$RALPH_TEST_SYSTEMCTL_LOG"
[ "$2" = show ] && printf '%b' "$RALPH_TEST_SHOW"
exit 0
`
	for name, body := range map[string]string{"systemd-run": run, "systemctl": ctl} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RALPH_TEST_SYSTEMCTL_LOG", systemctlLog)
	return systemctlLog
}

// TestRunStopsScope checks the systemctl calls that end a wrapped Run,
// on a normal exit and when the context deadline kills the runner: a
// collected unit (clean exit) only needs its state read; a failed one is
// stopped and reset-failed. (Leftover processes are covered by the
// real-systemd tests in claude_unix_test.go.)
func TestRunStopsScope(t *testing.T) {
	unit := fmt.Sprintf("ralph-%d-1.scope", os.Getpid())
	show := "--user show --property=LoadState,ActiveState,Result " + unit + "\n"
	for _, tc := range []struct {
		name    string
		body    string
		timeout time.Duration
		state   string
		want    string
	}{
		{"collected", "#!/bin/sh\necho '{}'\n", time.Minute, "", show},
		{"failed", "#!/bin/sh\nexit 1\n", time.Minute,
			"LoadState=loaded\\nActiveState=failed\\nResult=exit-code\\n",
			show + "--user stop " + unit + "\n--user reset-failed " + unit + "\n"},
		{"timeout", "#!/bin/sh\nsleep 30\n", 250 * time.Millisecond,
			"LoadState=loaded\\nActiveState=failed\\nResult=signal\\n",
			show + "--user stop " + unit + "\n--user reset-failed " + unit + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := fakeSystemdBin(t)
			t.Setenv("RALPH_TEST_SHOW", tc.state)
			bin := writeScript(t, tc.body)
			r := New(bin, nil, "", "1G")
			ctx, cancel := context.WithTimeout(context.Background(), tc.timeout)
			defer cancel()
			s, err := runForTest(t, r, ctx, "", t.TempDir(), nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if s.ScopeStopErr != nil {
				t.Errorf("ScopeStopErr = %v", s.ScopeStopErr)
			}
			got, err := os.ReadFile(log)
			if err != nil {
				t.Fatalf("systemctl never called: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("systemctl calls = %q, want %q", got, tc.want)
			}
		})
	}
}

func safeIdx(a []string, i int) string {
	if i < 0 || i >= len(a) {
		return "<oob>"
	}
	return a[i]
}

// writeScript writes a shell script to a temp dir, makes it
// executable, and returns its absolute path. Tests skip on Windows
// since /bin/sh isn't available.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts require POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-stub.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// TestRunStreamsOutputToTeeAndDisk verifies the streaming contract:
// every byte the subprocess writes lands in the captured tee writer
// AND in the on-disk log file before Run returns. The script sleeps
// between two writes; the producer-then-consumer test would deadlock
// today if the runner buffered output until Wait.
func TestRunStreamsOutputToTeeAndDisk(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping streaming test in -short")
	}
	bin := writeScript(t, `#!/bin/sh
printf 'first\n'
sleep 0.1
printf 'second\n'
printf 'err-one\n' >&2
sleep 0.1
printf 'err-two\n' >&2
`)
	r := New(bin, nil, "", "")
	logs := t.TempDir()
	stdoutPath := filepath.Join(logs, "stdout")
	stderrPath := filepath.Join(logs, "stderr")
	var outTee, errTee bytes.Buffer
	s, err := r.Run(context.Background(), RunOpts{
		Cwd:        t.TempDir(),
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
		StdoutTee:  &outTee,
		StderrTee:  &errTee,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(outTee.String(), want) {
			t.Errorf("stdout tee = %q, want to contain %q", outTee.String(), want)
		}
		if !strings.Contains(s.Stdout, want) {
			t.Errorf("Session.Stdout = %q, want to contain %q", s.Stdout, want)
		}
	}
	for _, want := range []string{"err-one", "err-two"} {
		if !strings.Contains(errTee.String(), want) {
			t.Errorf("stderr tee = %q, want to contain %q", errTee.String(), want)
		}
	}
	// Files on disk must match what the tee saw.
	diskOut, _ := os.ReadFile(stdoutPath)
	diskErr, _ := os.ReadFile(stderrPath)
	if string(diskOut) != outTee.String() {
		t.Errorf("disk stdout %q != tee stdout %q", diskOut, outTee.String())
	}
	if string(diskErr) != errTee.String() {
		t.Errorf("disk stderr %q != tee stderr %q", diskErr, errTee.String())
	}
}

// runForTest is a convenience that supplies tmpdir-based stdout/stderr
// paths so existing tests stay focused on inputs they actually care
// about (prompt / cwd / env). Production callers build RunOpts directly.
func runForTest(t *testing.T, r *Runner, ctx context.Context, prompt, cwd string, extraEnv []string) (*Session, error) {
	t.Helper()
	logs := t.TempDir()
	return r.Run(ctx, RunOpts{
		Prompt:     prompt,
		Cwd:        cwd,
		ExtraEnv:   extraEnv,
		StdoutPath: filepath.Join(logs, "stdout"),
		StderrPath: filepath.Join(logs, "stderr"),
	})
}

// TestRunRecordsSignal confirms a runner killed by a signal reports the
// signal's name; a normal exit reports none.
func TestRunRecordsSignal(t *testing.T) {
	bin := writeScript(t, "#!/bin/sh\nkill -9 $$\n")
	s, err := runForTest(t, New(bin, nil, "", ""), context.Background(), "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Signal != "SIGKILL" {
		t.Errorf("Signal = %q, want SIGKILL", s.Signal)
	}
	bin = writeScript(t, "#!/bin/sh\nexit 3\n")
	s, err = runForTest(t, New(bin, nil, "", ""), context.Background(), "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Signal != "" || s.ExitCode != 3 {
		t.Errorf("Signal = %q ExitCode = %d, want \"\" and 3", s.Signal, s.ExitCode)
	}
}

// TestRunDetectsScopeOOM runs under the real user manager: a runner that
// blows through a 64M MemoryMax is OOM-killed inside its scope, and Run
// reports OOMSignal (from the unit's Result=oom-kill, since the cgroup
// itself is gone by the time Run looks) and leaves no failed unit behind.
func TestRunDetectsScopeOOM(t *testing.T) {
	if testing.Short() {
		t.Skip("needs systemd-run --user --scope")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := isolation.Available(ctx); err != nil {
		t.Skipf("systemd-run --user --scope unavailable: %v", err)
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	bin := writeScript(t, "#!/bin/sh\nexec "+py+" -c 'b=[]\nwhile True: b.append(bytearray(8<<20))'\n")
	s, err := runForTest(t, New(bin, nil, "", "64M"), context.Background(), "", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.OOMSignal {
		t.Errorf("OOMSignal = false; exit %d signal %q stderr %q", s.ExitCode, s.Signal, s.StderrTail)
	}
	if got := Classify(s); got != ModeOOM {
		t.Errorf("Classify = %q, want oom", got)
	}
	unit := fmt.Sprintf("ralph-%d-1.scope", os.Getpid())
	out, _ := exec.Command("systemctl", "--user", "show", "--property=LoadState", unit).Output()
	if strings.TrimSpace(string(out)) == "LoadState=loaded" {
		t.Errorf("%s still loaded after Run (want stopped and reset-failed)", unit)
	}
}
