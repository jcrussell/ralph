//go:build linux

package isolation

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestScopeArgvShape(t *testing.T) {
	s := NewScope("ralph-abc-iter5", "512M")
	got := s.Argv("claude", []string{"-p", "hello", "--output-format=json"})
	want := []string{
		"systemd-run", "--user", "--scope",
		"--unit=ralph-abc-iter5",
		"-p", "MemoryMax=512M",
		"-p", "MemorySwapMax=0",
		"--",
		"claude", "-p", "hello", "--output-format=json",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Argv mismatch (-want +got):\n%s", diff)
	}
}

func TestScopeArgvNoMemoryLimit(t *testing.T) {
	s := NewScope("ralph-x", "")
	got := s.Argv("/bin/true", nil)
	want := []string{
		"systemd-run", "--user", "--scope",
		"--unit=ralph-x",
		"--",
		"/bin/true",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Argv mismatch (-want +got):\n%s", diff)
	}
}

func TestNewScopeNormalizesSuffix(t *testing.T) {
	s := NewScope("foo.scope", "1G")
	if s.Unit != "foo.scope" {
		t.Errorf("Unit = %q, want foo.scope", s.Unit)
	}
	if s.UnitBase() != "foo" {
		t.Errorf("UnitBase = %q, want foo", s.UnitBase())
	}
}

func TestParseState(t *testing.T) {
	for _, c := range []struct {
		out  string
		want unitState
	}{
		{"LoadState=loaded\nActiveState=failed\nResult=oom-kill\n", unitState{loaded: true, settled: true, result: "oom-kill"}},
		{"Result=exit-code\nActiveState=failed\nLoadState=loaded\n", unitState{loaded: true, settled: true, result: "exit-code"}},
		// Still running down (or held open by orphans): not final yet.
		{"LoadState=loaded\nActiveState=active\nResult=success\n", unitState{loaded: true}},
		{"LoadState=loaded\nActiveState=deactivating\nResult=success\n", unitState{loaded: true}},
		// Collected (clean exit) or never existed: settled, no verdict.
		{"LoadState=not-found\nActiveState=inactive\nResult=success\n", unitState{settled: true}},
		{"", unitState{settled: true}},
	} {
		if got := parseState(c.out); got != c.want {
			t.Errorf("parseState(%q) = %+v, want %+v", c.out, got, c.want)
		}
	}
}

// TestAvailableRoundsTrip is a real integration test against the host
// systemd-run. It auto-skips if systemd-run isn't present or if the
// user-mode systemd isn't reachable (common in containers / CI).
func TestAvailableRoundsTrip(t *testing.T) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skipf("systemd-run not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Available(ctx); err != nil {
		t.Skipf("systemd-run --user not usable here: %v", err)
	}
}
