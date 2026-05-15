package cmdutil

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Factory is the central DI container. Eager, cheap fields are pointers;
// expensive dependencies are lazy closures, instantiated on first call.
type Factory struct {
	IOStreams *iostreams.IOStreams

	// Logger is the root slog.Logger commands inherit. The root command's
	// PersistentPreRunE attaches per-invocation attributes (cmd path) and
	// stuffs the result into cmd.Context() so leaf commands reach it via
	// log.From(ctx) instead of accepting a logger parameter.
	Logger *slog.Logger

	// RepoRoot returns the absolute path to the nearest ancestor of
	// the current working directory containing a .ralph or .git
	// directory. Memoized after the first successful call.
	RepoRoot func() (string, error)

	// Lazy fields added as subsystems land:
	//   Config     func() (*config.Config, error)
	//   Store      func() (store.Store, error)
}

// NewFactory builds the default Factory: real iostreams, an eager
// slog.Logger over ErrOut, and a lazy memoized RepoRoot.
func NewFactory() *Factory {
	ios := iostreams.System()
	f := &Factory{
		IOStreams: ios,
		Logger:    slog.New(slog.NewTextHandler(ios.ErrOut, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	f.RepoRoot = sync.OnceValues(findRepoRoot)
	return f
}

// ErrNoRepoRoot is returned when no .ralph or .git ancestor exists.
var ErrNoRepoRoot = errors.New("no .ralph or .git directory found in any ancestor")

// findRepoRoot walks up from cwd looking for .ralph or .git.
func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("repo-root: getwd: %w", err)
	}
	dir := wd
	for {
		for _, marker := range []string{".ralph", ".git"} {
			info, err := os.Stat(filepath.Join(dir, marker))
			if err == nil && info.IsDir() {
				return dir, nil
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("repo-root: stat %s: %w", filepath.Join(dir, marker), err)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", &ErrHint{
				Err:  fmt.Errorf("%w (searched from %s)", ErrNoRepoRoot, wd),
				Hint: "run `ralph init` in your project root, or cd into a git repo",
			}
		}
		dir = parent
	}
}
