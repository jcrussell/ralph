package hooks

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunMissingHookIsNoOp(t *testing.T) {
	r, err := Run(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"), Env{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.NoHook {
		t.Errorf("NoHook = false, want true")
	}
}

func TestRunNonExecutableIsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), path, Env{}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for non-executable hook")
	}
	if !strings.Contains(err.Error(), "not executable") {
		t.Errorf("err = %v, want 'not executable'", err)
	}
}

func TestRunPropagatesEnv(t *testing.T) {
	skipNonPOSIX(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "captured")
	hook := writeHook(t, `#!/bin/sh
{
  echo "RALPH_REPO=$RALPH_REPO"
  echo "RALPH_ITER=$RALPH_ITER"
  echo "RALPH_STATE=$RALPH_STATE"
  echo "RALPH_PREV_STATE=$RALPH_PREV_STATE"
  echo "RALPH_NEXT_STATE=$RALPH_NEXT_STATE"
  echo "RALPH_PROMPT_FILE=$RALPH_PROMPT_FILE"
} > "$1"
`, dir)

	// Override Cmd to pass our capture path as $1 — easiest by
	// invoking the hook directly via a wrapper.
	wrapper := writeHook(t, `#!/bin/sh
exec "`+hook+`" "`+out+`"
`, dir)

	env := Env{
		Repo:       dir,
		Iter:       7,
		State:      "clean",
		PrevState:  "start",
		NextState:  "dirty",
		PromptFile: "/tmp/p.txt",
	}
	r, err := Run(context.Background(), wrapper, env, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d (stderr=%q)", r.ExitCode, r.Stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		"RALPH_REPO=" + dir,
		"RALPH_ITER=7",
		"RALPH_STATE=clean",
		"RALPH_PREV_STATE=start",
		"RALPH_NEXT_STATE=dirty",
		"RALPH_PROMPT_FILE=/tmp/p.txt",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("captured env missing %q\nfull capture:\n%s", want, got)
		}
	}
}

func TestRunPipesStdin(t *testing.T) {
	skipNonPOSIX(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "captured-stdin")
	hook := writeHook(t, `#!/bin/sh
cat > "`+out+`"
`, dir)

	payload := `{"iter": 3, "state": "clean"}`
	r, err := Run(context.Background(), hook, Env{Repo: dir}, strings.NewReader(payload), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.ExitCode != 0 {
		t.Fatalf("ExitCode = %d", r.ExitCode)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if got := string(b); got != payload {
		t.Errorf("stdin captured = %q, want %q", got, payload)
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	skipNonPOSIX(t)
	dir := t.TempDir()
	hook := writeHook(t, `#!/bin/sh
echo "noisy"
echo "warned" >&2
exit 7
`, dir)
	r, err := Run(context.Background(), hook, Env{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "noisy") {
		t.Errorf("Stdout = %q, want noisy", r.Stdout)
	}
	if !strings.Contains(r.Stderr, "warned") {
		t.Errorf("Stderr = %q, want warned", r.Stderr)
	}
}

// PhaseNone is the zero value loop uses when building env for a global
// hook. Lock in the value + distinctness so a rename of either phase
// doesn't silently collapse them.
func TestPhaseNoneZeroValue(t *testing.T) {
	var zero Phase
	if zero != PhaseNone {
		t.Errorf("zero Phase = %q, want PhaseNone (%q)", zero, PhaseNone)
	}
	if PhaseNone == PhaseEnter || PhaseNone == PhaseExit || PhaseNone == PhaseGate {
		t.Errorf("PhaseNone collides with a per-state phase: %q", PhaseNone)
	}
}

func TestPathHelpers(t *testing.T) {
	got := StatePath("/r", "clean", PhaseEnter)
	want := filepath.Join("/r", ".ralph", "hooks", "states", "clean", "enter")
	if got != want {
		t.Errorf("StatePath = %q, want %q", got, want)
	}
	got = GlobalPath("/r", "pre-iteration")
	want = filepath.Join("/r", ".ralph", "hooks", "pre-iteration")
	if got != want {
		t.Errorf("GlobalPath = %q, want %q", got, want)
	}
}

func writeHook(t *testing.T, body, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "hook-"+randSuffix(t))
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	return path
}

func randSuffix(t *testing.T) string {
	// Use the test's TempDir base name for uniqueness within a test.
	return filepath.Base(t.TempDir())
}

func skipNonPOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
}
