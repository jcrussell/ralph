package cmdutil

import (
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Factory is the central DI container. Eager, cheap fields are pointers;
// expensive dependencies are lazy closures, instantiated on first call
// (typically via sync.OnceValue inside the closure).
type Factory struct {
	IOStreams *iostreams.IOStreams

	// Lazy fields added as subsystems land:
	//   Config     func() (*config.Config, error)
	//   RepoRoot   func() (string, error)
	//   Store      func() (store.Store, error)
}

func NewFactory() *Factory {
	return &Factory{
		IOStreams: iostreams.System(),
	}
}
