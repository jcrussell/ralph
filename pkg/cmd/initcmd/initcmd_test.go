package initcmd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func newFactory(repo string) (*cmdutil.Factory, *iostreams.TestBuffers) {
	io, bufs := iostreams.Test()
	return &cmdutil.Factory{IOStreams: io, RepoRoot: func() (string, error) { return repo, nil }}, bufs
}

func TestScaffoldCreatesExpectedTree(t *testing.T) {
	repo := t.TempDir()
	f, bufs := newFactory(repo)
	if err := run(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("run: %v", err)
	}

	mustExist := []string{
		".ralph/config.toml",
		".ralph/state/.gitignore",
		".ralph/prompts/_header.md",
		".ralph/prompts/_footer.md",
		".ralph/prompts/clean.md",
		".ralph/prompts/dirty.md",
		".ralph/prompts/revert.md",
		".ralph/prompts/review.md",
		".ralph/hooks/pre-iteration",
		".ralph/hooks/post-iteration",
		".ralph/hooks/failure",
		".ralph/hooks/notify",
		".ralph/hooks/states/clean/enter",
		".ralph/hooks/states/clean/exit",
		".ralph/hooks/states/clean/gate",
		".ralph/hooks/states/dirty/enter",
		".ralph/hooks/states/dirty/exit",
		".ralph/hooks/states/dirty/gate",
		".ralph/hooks/states/review/enter",
		".ralph/hooks/states/review/exit",
		".ralph/hooks/states/review/gate",
		".ralph/hooks/states/revert/enter",
		".ralph/hooks/states/revert/exit",
		".ralph/hooks/states/revert/gate",
	}
	for _, rel := range mustExist {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	// Hook files must be executable.
	for _, rel := range []string{
		".ralph/hooks/pre-iteration",
		".ralph/hooks/states/clean/gate",
	} {
		info, err := os.Stat(filepath.Join(repo, rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s mode = %v, want executable", rel, info.Mode())
		}
	}

	// Prompt files must NOT be executable.
	info, err := os.Stat(filepath.Join(repo, ".ralph/prompts/clean.md"))
	if err != nil {
		t.Fatalf("stat clean.md: %v", err)
	}
	if info.Mode()&0o111 != 0 {
		t.Errorf("clean.md is executable; should be 0644")
	}

	// Summary and per-file chatter both go to ErrOut; stdout stays clean.
	if bufs.Out.String() != "" {
		t.Errorf("stdout should be empty, got %q", bufs.Out.String())
	}
	if !strings.Contains(bufs.ErrOut.String(), "created") || !strings.Contains(bufs.ErrOut.String(), "skipped") {
		// Should report at least one created and zero skipped.
		t.Logf("summary: %s", bufs.ErrOut.String())
	}
	if !strings.Contains(bufs.ErrOut.String(), "0 skipped") {
		t.Errorf("first run should report 0 skipped, got %q", bufs.ErrOut.String())
	}
}

func TestScaffoldSkipsExistingNoForce(t *testing.T) {
	repo := t.TempDir()
	f, _ := newFactory(repo)
	if err := run(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Mutate one file.
	custom := filepath.Join(repo, ".ralph/prompts/clean.md")
	if err := os.WriteFile(custom, []byte("USER CONTENT"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Re-run without --force.
	io, bufs := iostreams.Test()
	f2 := &cmdutil.Factory{IOStreams: io, RepoRoot: func() (string, error) { return repo, nil }}
	if err := run(context.Background(), &Options{F: f2}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	// File should be untouched.
	b, err := os.ReadFile(custom)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "USER CONTENT" {
		t.Errorf("user content was overwritten without --force: got %q", b)
	}
	// Summary reports skips on ErrOut.
	if !strings.Contains(bufs.ErrOut.String(), "0 created") {
		t.Errorf("second run should report 0 created, got %q", bufs.ErrOut.String())
	}
}

func TestScaffoldForceOverwrites(t *testing.T) {
	repo := t.TempDir()
	f, _ := newFactory(repo)
	if err := run(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	custom := filepath.Join(repo, ".ralph/prompts/clean.md")
	if err := os.WriteFile(custom, []byte("USER CONTENT"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	io, _ := iostreams.Test()
	f2 := &cmdutil.Factory{IOStreams: io, RepoRoot: func() (string, error) { return repo, nil }}
	if err := run(context.Background(), &Options{F: f2, Force: true}); err != nil {
		t.Fatalf("force run: %v", err)
	}
	b, err := os.ReadFile(custom)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) == "USER CONTENT" {
		t.Errorf("--force did not overwrite")
	}
}

func TestScaffoldFallsBackToCwdWhenNoRepoMarker(t *testing.T) {
	dir := t.TempDir()
	io, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: io,
		// Simulate "no repo root found".
		RepoRoot: func() (string, error) { return "", cmdutil.ErrNoRepoRoot },
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := run(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ralph/config.toml")); errors.Is(err, fs.ErrNotExist) {
		t.Errorf(".ralph/config.toml was not created in cwd")
	}
}

// TestShippedConfigTomlLoadsToDefaults exercises the embedded
// defaults/config.toml end-to-end through ralph init + config.Load,
// and asserts the result equals config.Defaults(). The shipped file
// is all-comments by design, so the user gets the built-in defaults
// for every field. A drift in either file should fail this test.
func TestShippedConfigTomlLoadsToDefaults(t *testing.T) {
	repo := t.TempDir()
	if err := os.Setenv("XDG_CONFIG_HOME", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("XDG_CONFIG_HOME") })

	f, _ := newFactory(repo)
	if err := run(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("init: %v", err)
	}
	got, err := config.Load(repo)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if diff := cmp.Diff(config.Defaults(), got); diff != "" {
		t.Errorf("shipped config drifted from Defaults() (-want +got):\n%s", diff)
	}
}

func TestNewCmdInitRunFInjection(t *testing.T) {
	called := false
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdInit(f, func(_ context.Context, opts *Options) error {
		called = true
		if !opts.Force {
			t.Errorf("--force not propagated")
		}
		return nil
	})
	cmd.SetArgs([]string{"--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Errorf("runF not called")
	}
}
