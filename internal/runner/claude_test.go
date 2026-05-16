package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunSuccessParsesEnvelope(t *testing.T) {
	bin := writeScript(t, `#!/bin/sh
cat > /dev/null  # consume stdin
echo '{"total_cost_usd": 0.42, "num_turns": 3, "subtype": "success", "usage": {"input_tokens": 1000, "output_tokens": 2500}}'
exit 0
`)
	r := New(bin, nil, "")
	ctx := context.Background()
	s, err := r.Run(ctx, "hello", t.TempDir(), nil)
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
	r := New(bin, nil, "")
	s, err := r.Run(context.Background(), "", t.TempDir(), nil)
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
	r := New(bin, nil, "")
	s, err := r.Run(context.Background(), "", t.TempDir(), nil)
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
	r := New(bin, nil, "")
	prompt := "the prompt body"
	s, err := r.Run(context.Background(), prompt, t.TempDir(), nil)
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
	r := New(bin, []string{"--flag-a", "--flag-b=value"}, "")
	_, err := r.Run(context.Background(), "", t.TempDir(), []string{"RALPH_TEST_ARGS_OUT=" + argsOut})
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

func TestRunTimeoutKills(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in -short")
	}
	bin := writeScript(t, `#!/bin/sh
sleep 30
echo '{}'
`)
	r := New(bin, nil, "")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	s, err := r.Run(ctx, "", t.TempDir(), nil)
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
	r := New(bin, nil, "")
	s, err := r.Run(context.Background(), "", t.TempDir(), nil)
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
	r := New(bin, nil, "")
	s, err := r.Run(context.Background(), "", t.TempDir(), nil)
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
	if _, ok := s.Envelope.Raw["is_error"]; !ok {
		t.Errorf("Raw missing is_error: %v", s.Envelope.Raw)
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
	r := New(bin, nil, "")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	s, err := r.Run(ctx, "", t.TempDir(), nil)
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
	r := New("/no/such/binary-12345", nil, "")
	_, err := r.Run(context.Background(), "", t.TempDir(), nil)
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

	r := New("/path/to/claude", []string{"--dangerously-skip-permissions"}, "1G")
	if _, err := r.Run(context.Background(), "", t.TempDir(), []string{"RALPH_TEST_ARGV_OUT=" + argvOut}); err != nil {
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

	r := New("/path/to/claude", nil, "512m")
	for i := 0; i < 3; i++ {
		if _, err := r.Run(context.Background(), "", t.TempDir(), []string{"RALPH_TEST_ARGV_OUT=" + argvOut}); err != nil {
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
