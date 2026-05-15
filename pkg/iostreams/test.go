package iostreams

import "bytes"

// TestBuffers exposes the In/Out/ErrOut buffers backing a Test()
// IOStreams so tests can read what was written to them.
type TestBuffers struct {
	In     bytes.Buffer
	Out    bytes.Buffer
	ErrOut bytes.Buffer
}
