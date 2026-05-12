package iostreams

import "bytes"

type TestBuffers struct {
	In     bytes.Buffer
	Out    bytes.Buffer
	ErrOut bytes.Buffer
}
