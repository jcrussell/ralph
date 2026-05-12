//go:build linux

package isolation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Argv =\n  %v\nwant\n  %v", got, want)
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
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Argv = %v, want %v", got, want)
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

func TestOOMKilledFile(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"no oom", "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n", false},
		{"one oom kill", "low 0\nhigh 0\nmax 5\noom 2\noom_kill 1\n", true},
		{"many oom kills", "oom_kill 7\n", true},
		{"trailing whitespace", "oom_kill   3  \n", true},
		{"no oom_kill line", "low 0\nhigh 0\n", false},
		{"malformed value", "oom_kill notanumber\n", false},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "memory.events")
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := OOMKilledFile(path)
		if err != nil {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: OOMKilledFile = %v, want %v\nbody:\n%s", c.name, got, c.want, c.body)
		}
	}
}

func TestOOMKilledFileMissing(t *testing.T) {
	got, err := OOMKilledFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Errorf("missing file err = %v, want nil", err)
	}
	if got {
		t.Errorf("missing file = true, want false")
	}
}

func TestEventsPathShape(t *testing.T) {
	s := NewScope("ralph-test", "")
	p := s.EventsPath()
	if !strings.HasPrefix(p, "/sys/fs/cgroup/user.slice/") {
		t.Errorf("EventsPath = %q, want /sys/fs/cgroup/user.slice prefix", p)
	}
	if !strings.HasSuffix(p, "/ralph-test.scope/memory.events") {
		t.Errorf("EventsPath = %q, want suffix /ralph-test.scope/memory.events", p)
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
