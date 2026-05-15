package doctor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		"claude", "bd", "git", ".ralph", ".beads",
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
