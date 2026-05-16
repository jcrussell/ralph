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

	// LogLevel is the LevelVar wired into Logger's handler. The root
	// command's PersistentPreRunE flips it based on -v/-vv/--log-level/
	// $RALPH_LOG (byob-logging.3). Tests that construct a bare Factory
	// may leave this nil; the verbosity wiring no-ops when so.
	LogLevel *slog.LevelVar

	// RepoRoot returns the absolute path to the nearest ancestor of
	// the current working directory containing a .ralph or .git
	// directory. Memoized after the first successful call.
	RepoRoot func() (string, error)

	// Lazy fields added as subsystems land:
	//   Config     func() (*config.Config, error)
	//   Store      func() (store.Store, error)
}

// NewFactory builds the default Factory: real iostreams, an eager
// slog.Logger over ErrOut at Warn, and a lazy memoized RepoRoot. The
// Warn default keeps the binary quiet unless the user opts in via
// -v/--log-level/$RALPH_LOG (byob-logging.3).
func NewFactory() *Factory {
	ios := iostreams.System()
	lvl := new(slog.LevelVar)
	lvl.Set(slog.LevelWarn)
	f := &Factory{
		IOStreams: ios,
		LogLevel:  lvl,
		Logger:    slog.New(slog.NewTextHandler(ios.ErrOut, &slog.HandlerOptions{Level: lvl})),
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
				Err: fmt.Errorf("%w (searched from %s)", ErrNoRepoRoot, wd),
				Hint: "run `ralph init` in your project root, or cd into a git repo\n" +
					"      (see: https://github.com/jcrussell/ralph/blob/main/docs/troubleshooting.md#no-repo-root)",
			}
		}
		dir = parent
	}
}
