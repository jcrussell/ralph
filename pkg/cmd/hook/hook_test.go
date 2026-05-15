package hook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".ralph", "hooks", "states", "clean"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return repo
}

func writeHook(t *testing.T, repo, rel, body string) string {
	t.Helper()
	path := filepath.Join(repo, ".ralph", "hooks", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	return path
}

func newFactory(repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	io, bufs := iostreams.Test()
	return &cmdutil.Factory{IOStreams: io, RepoRoot: func() (string, error) { return repo, nil }}, bufs
}

func TestRunSuccess(t *testing.T) {
	repo := newRepo(t)
	writeHook(t, repo, "states/clean/enter", `#!/bin/sh
echo "hello from hook: state=$RALPH_STATE iter=$RALPH_ITER"
`)
	f, bufs := newFactory(repo)
	opts := &Options{F: f, Path: "states/clean/enter", State: "clean"}
	err := run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(bufs.Out.String(), "hello from hook: state=clean") {
		t.Errorf("stdout missing expected line: %q", bufs.Out.String())
	}
}

func TestRunNonZeroPropagatesExitCode(t *testing.T) {
	repo := newRepo(t)
	writeHook(t, repo, "states/clean/gate", `#!/bin/sh
echo failure
exit 7
`)
	f, _ := newFactory(repo)
	opts := &Options{F: f, Path: "states/clean/gate"}
	err := run(context.Background(), opts)
	var exit *cmdutil.ExitCodeError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v (%T), want *ExitCodeError", err, err)
	}
	if exit.Code != 7 {
		t.Errorf("Code = %d, want 7", exit.Code)
	}
}

func TestRunRejectsEscape(t *testing.T) {
	repo := newRepo(t)
	f, _ := newFactory(repo)
	opts := &Options{F: f, Path: "../escape"}
	err := run(context.Background(), opts)
	if err == nil {
		t.Fatalf("expected escape error")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v (%T), want *FlagError", err, err)
	}
}

func TestRunRejectsSymlinkEscape(t *testing.T) {
	repo := newRepo(t)
	// Target hook lives OUTSIDE the .ralph/hooks/ tree.
	outside := filepath.Join(repo, "rogue.sh")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\necho pwned\n"), 0o755); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(repo, ".ralph", "hooks", "states", "clean", "enter")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	f, _ := newFactory(repo)
	opts := &Options{F: f, Path: "states/clean/enter"}
	err := run(context.Background(), opts)
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v (%T), want *FlagError", err, err)
	}
}

func TestRunMissingHook(t *testing.T) {
	repo := newRepo(t)
	f, _ := newFactory(repo)
	opts := &Options{F: f, Path: "states/clean/nope"}
	err := run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v, want 'does not exist'", err)
	}
}

func TestValidateRejectsBadEnv(t *testing.T) {
	opts := &Options{ExtraEnv: []string{"A=1", "B=2"}}
	if err := opts.Validate(); err != nil {
		t.Errorf("valid pairs rejected: %v", err)
	}
	opts = &Options{ExtraEnv: []string{"BAD"}}
	err := opts.Validate()
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("invalid pair: err = %v, want FlagError", err)
	}
}

func TestNewCmdHookRunFInjection(t *testing.T) {
	called := false
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdHook(f, func(opts *Options) error {
		called = true
		if opts.Path != "some/hook" {
			t.Errorf("opts.Path = %q, want some/hook", opts.Path)
		}
		return nil
	})
	cmd.SetArgs([]string{"run", "some/hook", "--state", "clean", "--env", "A=1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Errorf("runF not called")
	}
}

func TestNewCmdHookRejectsBadState(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdHook(f, func(*Options) error { return nil })
	cmd.SetArgs([]string{"run", "x", "--state", "bogus"})
	err := cmd.Execute()
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v, want FlagError", err)
	}
}
