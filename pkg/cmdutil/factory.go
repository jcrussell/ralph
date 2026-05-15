package cmdutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Factory is the central DI container. Eager, cheap fields are pointers;
// expensive dependencies are lazy closures, instantiated on first call.
type Factory struct {
	IOStreams *iostreams.IOStreams

	// RepoRoot returns the absolute path to the nearest ancestor of
	// the current working directory containing a .ralph or .git
	// directory. Memoized after the first successful call.
	RepoRoot func() (string, error)

	// Lazy fields added as subsystems land:
	//   Config     func() (*config.Config, error)
	//   Store      func() (store.Store, error)
}

// NewFactory builds the default Factory: real iostreams, lazy
// memoized RepoRoot.
func NewFactory() *Factory {
	f := &Factory{
		IOStreams: iostreams.System(),
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
