// Package log writes ralph's two on-disk log streams: JSONL iteration
// records and a structured slog stream to orchestrator.log. Concurrent
// callers from one orchestrator are serialized in-process; concurrent
// orchestrators are prevented by internal/lock.
package log

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// JSONL appends one JSON object per line. Open it once per stream and
// share across goroutines.
type JSONL struct {
	f  *os.File
	mu sync.Mutex
}

// OpenJSONL opens path for append, creating parent directories as
// needed. The file is created with 0o644 if absent.
func OpenJSONL(path string) (*JSONL, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("log: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("log: open %s: %w", path, err)
	}
	return &JSONL{f: f}, nil
}

// Write encodes v as JSON and appends it as one line. The mutex
// guarantees no torn writes when multiple goroutines call concurrently.
func (j *JSONL) Write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("log: marshal: %w", err)
	}
	b = append(b, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, err := j.f.Write(b); err != nil {
		return fmt.Errorf("log: write %s: %w", j.f.Name(), err)
	}
	return nil
}

// Close flushes and closes the underlying file.
func (j *JSONL) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.f.Close()
}

// Path returns the file path JSONL was opened with. Useful for tests.
func (j *JSONL) Path() string { return j.f.Name() }

// NewSlog returns a slog.Logger writing JSON records to path at the
// given level. Closer flushes and closes the underlying file. Parent
// directories are created if absent.
func NewSlog(path string, level slog.Level) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("log: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("log: open %s: %w", path, err)
	}
	h := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: level})
	return slog.New(h), f, nil
}
