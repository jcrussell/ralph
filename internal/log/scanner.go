package log

import (
	"bufio"
	"io"
)

// summaryBufferMax raises the scanner's per-line ceiling from bufio's
// 64KB default. Iteration records in summary.jsonl embed narratives and
// bd diffs and can exceed the default, which would otherwise surface as
// bufio.ErrTooLong.
const summaryBufferMax = 4 * 1024 * 1024

// NewSummaryScanner returns a bufio.Scanner configured to read
// summary.jsonl lines, which can be large enough to exceed bufio's
// default token cap. The read-side commands (logs/timeline/report)
// share this setup.
func NewSummaryScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), summaryBufferMax)
	return sc
}
