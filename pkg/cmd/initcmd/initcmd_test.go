package initcmd

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
		".ralph/hooks/states/clean/enter",
		".ralph/hooks/states/clean/exit",
		".ralph/hooks/states/clean/gate",
		".ralph/hooks/states/dirty/enter",
		".ralph/hooks/states/dirty/exit",
		".ralph/hooks/states/dirty/gate",
		".ralph/hooks/states/review/enter",
		".ralph/hooks/states/review/exit",
		".ralph/hooks/states/review/gate",
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

	if !strings.Contains(bufs.Out.String(), "created") || !strings.Contains(bufs.Out.String(), "skipped") {
		// Should report at least one created and zero skipped.
		t.Logf("summary: %s", bufs.Out.String())
	}
	if !strings.Contains(bufs.Out.String(), "0 skipped") {
		t.Errorf("first run should report 0 skipped, got %q", bufs.Out.String())
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
	// Summary reports skips.
	if !strings.Contains(bufs.Out.String(), "0 created") {
		t.Errorf("second run should report 0 created, got %q", bufs.Out.String())
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

func TestNewCmdInitRunFInjection(t *testing.T) {
	called := false
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdInit(f, func(opts *Options) error {
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
