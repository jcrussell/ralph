package cmdutil

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewFactoryHasLogger(t *testing.T) {
	f := NewFactory()
	if f.Logger == nil {
		t.Fatal("NewFactory().Logger = nil; want a default *slog.Logger")
	}
}

// byob-logging.3: the binary must be quiet by default. The LevelVar is
// what the root command flips when -v/--log-level/$RALPH_LOG fires; its
// starting value is the user-visible default.
func TestNewFactoryDefaultLevelIsWarn(t *testing.T) {
	f := NewFactory()
	if f.LogLevel == nil {
		t.Fatal("NewFactory().LogLevel = nil; want a *slog.LevelVar")
	}
	if got := f.LogLevel.Level(); got != slog.LevelWarn {
		t.Errorf("LogLevel = %v; want Warn", got)
	}
}

// byob-config.3: f.Config must be a memoized closure so commands that
// never dereference it (--help, --version, completions) pay no
// filesystem cost. NewFactory wires it; tests can swap via LazyConfig.
func TestNewFactoryConfigIsLazy(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".ralph"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, root)

	f := NewFactory()
	if f.Config == nil {
		t.Fatal("NewFactory().Config = nil; want closure")
	}
	cfg, err := f.Config()
	if err != nil {
		t.Fatalf("f.Config(): %v", err)
	}
	if cfg.Loop.MaxIterations == 0 {
		t.Errorf("Config returned zero defaults: %+v", cfg.Loop)
	}
}

// LazyConfig must propagate a RepoRoot error rather than calling the
// loader with an empty path (which would mis-classify the failure as
// a config-load error).
func TestLazyConfigPropagatesRepoRootError(t *testing.T) {
	rootErr := errors.New("no repo")
	fn := LazyConfig(func() (string, error) { return "", rootErr })
	_, err := fn()
	if !errors.Is(err, rootErr) {
		t.Errorf("err = %v, want %v", err, rootErr)
	}
}

// sync.OnceValues guarantees one load per closure; verify by counting
// the underlying RepoRoot invocations.
func TestLazyConfigMemoizes(t *testing.T) {
	root := t.TempDir()
	calls := 0
	fn := LazyConfig(func() (string, error) {
		calls++
		return root, nil
	})
	if _, err := fn(); err != nil {
		t.Fatalf("fn(): %v", err)
	}
	if _, err := fn(); err != nil {
		t.Fatalf("fn() second call: %v", err)
	}
	if calls != 1 {
		t.Errorf("RepoRoot called %d times; want 1 (sync.OnceValues)", calls)
	}
}

func TestRepoRootFindsRalph(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".ralph"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	chdir(t, sub)
	got, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	// macOS /tmp symlinks to /private/tmp; resolve both sides.
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("findRepoRoot = %s, want %s", gotResolved, wantResolved)
	}
}

func TestRepoRootFindsGit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, root)
	got, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("findRepoRoot = %s, want %s", gotResolved, wantResolved)
	}
}

func TestRepoRootMissing(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, err := findRepoRoot()
	if !errors.Is(err, ErrNoRepoRoot) {
		t.Errorf("err = %v, want errors.Is(_, ErrNoRepoRoot)", err)
	}
	var h *ErrHint
	if !errors.As(err, &h) || h.Hint == "" {
		t.Errorf("err = %v, want *ErrHint with a non-empty hint", err)
	}
}

// byob-user-docs.5: anchor stability is a compatibility contract.
// Each anchor referenced from an ErrHint must exist as a matching
// "## <anchor>" heading in docs/troubleshooting.md. Renaming either
// side without the other breaks the docs link in already-shipped
// binaries.
var troubleshootingAnchors = map[string]string{
	"no-repo-root": "ErrNoRepoRoot in findRepoRoot",
}

func TestRepoRootMissingHintLinksToTroubleshooting(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, err := findRepoRoot()
	var h *ErrHint
	if !errors.As(err, &h) {
		t.Fatalf("err = %v, want *ErrHint", err)
	}
	if !strings.Contains(h.Hint, "#no-repo-root") {
		t.Errorf("hint = %q, want anchor reference to #no-repo-root", h.Hint)
	}
}

func TestTroubleshootingAnchorsExistInDocs(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "troubleshooting.md")
	docs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for anchor, origin := range troubleshootingAnchors {
		if !strings.Contains(string(docs), "## "+anchor+"\n") {
			t.Errorf("docs/troubleshooting.md missing `## %s` heading (referenced from %s)", anchor, origin)
		}
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}
