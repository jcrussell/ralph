package log_test

import (
	"strings"
	"testing"

	ralphlog "github.com/jcrussell/ralph/internal/log"
)

func TestNewSummaryScannerReadsLines(t *testing.T) {
	sc := ralphlog.NewSummaryScanner(strings.NewReader("a\nb\nc\n"))
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("got %v, want a,b,c", got)
	}
}

func TestNewSummaryScannerHandlesLongLine(t *testing.T) {
	// A line far past bufio's 64KB default must not trigger
	// bufio.ErrTooLong.
	long := strings.Repeat("x", 1<<20) // 1MB
	sc := ralphlog.NewSummaryScanner(strings.NewReader(long + "\n"))
	if !sc.Scan() {
		t.Fatalf("scan returned false: %v", sc.Err())
	}
	if len(sc.Bytes()) != 1<<20 {
		t.Errorf("got %d bytes, want %d", len(sc.Bytes()), 1<<20)
	}
}
