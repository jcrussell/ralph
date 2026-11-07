package doctor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// stubProbes returns Probes wired with happy-path stubs that the
// tests then selectively override.
func stubProbes(t *testing.T) Probes {
	t.Helper()
	repo := t.TempDir()
	for _, dir := range []string{".ralph", ".beads"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Probes{
		GOOS: "linux",
		LookPath: func(name string) (string, error) {
			return "/usr/bin/" + name, nil
		},
		Stat:  os.Stat,
		Getwd: func() (string, error) { return repo, nil },
		SystemdRun: func(ctx context.Context) error {
			return nil
		},
		ReadFile:    fakeFiles(map[string]string{"/proc/meminfo": "MemTotal:       16777216 kB\n"}),
		Getuid:      func() int { return 1000 },
		MemoryLimit: func() (int64, error) { return 7 << 30, nil },
	}
}

// fakeFiles serves ReadFile from a map; a missing path is ErrNotExist.
func fakeFiles(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		if c, ok := files[path]; ok {
			return []byte(c), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestRunChecksAllGreen(t *testing.T) {
	p := stubProbes(t)
	results := RunChecks(context.Background(), p)

	for _, r := range results {
		if !r.OK {
			t.Errorf("check %q failed: %s", r.Name, r.Detail)
		}
	}
	wantNames := []string{
		"os", "systemd-run", "systemd-run probe",
		"claude", "bd", "git", ".ralph", ".beads", "memory cap",
	}
	if len(results) != len(wantNames) {
		t.Fatalf("got %d checks, want %d", len(results), len(wantNames))
	}
	for i, want := range wantNames {
		if results[i].Name != want {
			t.Errorf("check[%d].Name = %q, want %q", i, results[i].Name, want)
		}
	}
}

func TestCheckOSNonLinux(t *testing.T) {
	p := stubProbes(t)
	p.GOOS = "darwin"
	r := checkOS(p)
	if r.OK {
		t.Errorf("expected fail on darwin, got %+v", r)
	}
	if !strings.Contains(r.Detail, "darwin") {
		t.Errorf("Detail = %q, want to mention darwin", r.Detail)
	}
}

func TestCheckBinaryMissing(t *testing.T) {
	p := stubProbes(t)
	p.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return "", errors.New("exec: \"claude\": not found in $PATH")
		}
		return "/usr/bin/" + name, nil
	}
	results := RunChecks(context.Background(), p)
	var claudeR Result
	for _, r := range results {
		if r.Name == "claude" {
			claudeR = r
		}
	}
	if claudeR.OK {
		t.Errorf("claude check passed unexpectedly")
	}
	if !strings.Contains(claudeR.Detail, "not on PATH") {
		t.Errorf("Detail = %q", claudeR.Detail)
	}
}

func TestCheckRepoDirMissing(t *testing.T) {
	p := stubProbes(t)
	// Force .beads missing by pointing Getwd at an empty dir.
	empty := t.TempDir()
	p.Getwd = func() (string, error) { return empty, nil }
	r := checkRepoDir(p, ".beads")
	if r.OK {
		t.Errorf("expected fail when .beads missing, got %+v", r)
	}
	if !strings.Contains(r.Detail, "missing") {
		t.Errorf("Detail = %q, want 'missing'", r.Detail)
	}
}

func TestCheckRepoDirIsFile(t *testing.T) {
	p := stubProbes(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ralph"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Getwd = func() (string, error) { return dir, nil }
	r := checkRepoDir(p, ".ralph")
	if r.OK {
		t.Errorf("expected fail when .ralph is a file, got %+v", r)
	}
}

func TestCheckSystemdRunFailure(t *testing.T) {
	p := stubProbes(t)
	p.SystemdRun = func(ctx context.Context) error {
		return errors.New("systemd-run probe: command not found")
	}
	r := checkSystemdRun(context.Background(), p)
	if r.OK {
		t.Errorf("expected fail, got %+v", r)
	}
	if !strings.Contains(r.Detail, "command not found") {
		t.Errorf("Detail = %q", r.Detail)
	}
}

func TestDoctorRunWritesResultsAndReportsFailure(t *testing.T) {
	io, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: io}
	p := stubProbes(t)
	p.GOOS = "darwin" // force one failure

	opts := &Options{F: f, Probes: p}
	err := doctorRun(context.Background(), opts)
	if err == nil {
		t.Fatal("doctorRun: nil err, want failure count")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("err = %v, want 'failed'", err)
	}
	out := bufs.Out.String()
	if !strings.Contains(out, "[FAIL] os") {
		t.Errorf("Out = %q, want FAIL line for os", out)
	}
	if !strings.Contains(out, "[  ok] claude") {
		t.Errorf("Out = %q, want ok line for claude", out)
	}
}

func TestDoctorRunAllGreen(t *testing.T) {
	io, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: io}
	opts := &Options{F: f, Probes: stubProbes(t)}
	if err := doctorRun(context.Background(), opts); err != nil {
		t.Fatalf("doctorRun: %v\n%s", err, bufs.Out.String())
	}
	if strings.Contains(bufs.Out.String(), "FAIL") {
		t.Errorf("Out contains FAIL:\n%s", bufs.Out.String())
	}
}

func TestNewCmdDoctorRunFInjection(t *testing.T) {
	called := false
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdDoctor(f, func(_ context.Context, opts *Options) error {
		called = true
		if opts.F != f {
			t.Errorf("opts.F not propagated")
		}
		return nil
	})
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Errorf("runF was not called")
	}
}

func TestStatNotExist(t *testing.T) {
	// Sanity: confirm errors.Is(_, fs.ErrNotExist) catches os.Stat's
	// not-found error — this is what checkRepoDir relies on.
	_, err := os.Stat(filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat err = %v, want fs.ErrNotExist", err)
	}
}

func TestCheckMemoryHeadroom(t *testing.T) {
	const meminfo8G = "MemTotal:        8388608 kB\nMemFree: 1 kB\n"
	const slice = "/sys/fs/cgroup/user.slice/user-1000.slice/memory.max"
	const svc = "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/memory.max"
	for _, tc := range []struct {
		name     string
		limit    string
		limitErr error
		files    map[string]string
		wantOK   bool
		wantWarn bool
		want     string
	}{
		{name: "headroom ok", limit: "6G", files: map[string]string{"/proc/meminfo": meminfo8G},
			wantOK: true, want: "cap 6.0G, 2.0G free under host RAM 8.0G"},
		{name: "512M free", limit: "7680M", files: map[string]string{"/proc/meminfo": meminfo8G},
			wantOK: true, wantWarn: true, want: "leaves 512.0M under host RAM 8.0G, so the host can run out of memory before the runner hits its cap; set it to 7G or less"},
		{name: "cap equals RAM", limit: "8G", files: map[string]string{"/proc/meminfo": meminfo8G},
			wantOK: true, wantWarn: true, want: "leaves 0B under host RAM 8.0G"},
		{name: "exactly 1G free is enough", limit: "7G", files: map[string]string{"/proc/meminfo": meminfo8G},
			wantOK: true, want: "cap 7.0G, 1.0G free"},
		{name: "user slice tighter than RAM", limit: "4G", files: map[string]string{
			"/proc/meminfo": meminfo8G, slice: "4294967296\n", svc: "max\n"},
			wantOK: true, wantWarn: true, want: "under user-1000.slice MemoryMax 4.0G"},
		{name: "user service tighter still", limit: "2G", files: map[string]string{
			"/proc/meminfo": meminfo8G, slice: "4294967296\n", svc: "3221225472\n"},
			wantOK: true, wantWarn: false, want: "free under user@1000.service MemoryMax 3.0G"},
		{name: "cap disabled", limit: "", files: map[string]string{"/proc/meminfo": meminfo8G},
			wantOK: true, wantWarn: true, want: "no memory cap"},
		{name: "cap zero", limit: "0", files: map[string]string{"/proc/meminfo": meminfo8G},
			wantOK: true, wantWarn: true, want: "no memory cap"},
		{name: "no repo config", limitErr: errors.New("not in a ralph repo"),
			wantOK: true, want: "skipped: no repo config"},
		{name: "no meminfo", limit: "6G", files: map[string]string{},
			wantOK: true, want: "skipped: read /proc/meminfo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := stubProbes(t)
			p.ReadFile = fakeFiles(tc.files)
			p.MemoryLimit = func() (int64, error) {
				n, err := config.ParseBytes(tc.limit)
				if err != nil {
					t.Fatal(err)
				}
				return n, tc.limitErr
			}
			r := checkMemoryHeadroom(p)
			if r.OK != tc.wantOK || r.Warn != tc.wantWarn {
				t.Errorf("OK=%v Warn=%v, want OK=%v Warn=%v (%s)", r.OK, r.Warn, tc.wantOK, tc.wantWarn, r.Detail)
			}
			if !strings.Contains(r.Detail, tc.want) {
				t.Errorf("Detail = %q, want it to contain %q", r.Detail, tc.want)
			}
		})
	}
}

// A warning is printed as "warn" and leaves doctor's exit at 0.
func TestDoctorRunWarnIsNotFailure(t *testing.T) {
	io, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: io}
	p := stubProbes(t)
	p.MemoryLimit = func() (int64, error) { return 0, nil }
	if err := doctorRun(context.Background(), &Options{F: f, Probes: p}); err != nil {
		t.Fatalf("doctorRun: %v", err)
	}
	if !strings.Contains(bufs.Out.String(), "[warn] memory cap") {
		t.Errorf("Out = %q, want a warn row for memory cap", bufs.Out.String())
	}
}
