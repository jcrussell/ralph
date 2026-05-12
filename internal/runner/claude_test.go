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
	r := New(bin, nil)
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
	r := New(bin, nil)
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
	r := New(bin, nil)
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
	r := New(bin, nil)
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
	r := New(bin, []string{"--flag-a", "--flag-b=value"})
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
	r := New(bin, nil)
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
	r := New(bin, nil)
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
	r := New(bin, nil)
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

func TestRunMissingBinary(t *testing.T) {
	r := New("/no/such/binary-12345", nil)
	_, err := r.Run(context.Background(), "", t.TempDir(), nil)
	if err == nil {
		t.Fatalf("Run: nil err, want missing-binary error")
	}
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
