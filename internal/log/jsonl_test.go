package log

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestJSONLAppendAndReadBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.jsonl")
	j, err := OpenJSONL(path)
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}
	defer j.Close()

	records := []map[string]any{
		{"iter": 1, "state": "clean"},
		{"iter": 2, "state": "dirty"},
		{"iter": 3, "state": "revert"},
	}
	for _, r := range records {
		if err := j.Write(r); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	got := readLines(t, path)
	if len(got) != len(records) {
		t.Fatalf("got %d lines, want %d", len(got), len(records))
	}
	for i, line := range got {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line %d: not JSON: %q: %v", i, line, err)
		}
	}
}

func TestJSONLCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "log.jsonl")
	j, err := OpenJSONL(path)
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}
	defer j.Close()
	if err := j.Write(map[string]int{"x": 1}); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestJSONLConcurrentNoTearing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.jsonl")
	j, err := OpenJSONL(path)
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}
	defer j.Close()

	const writers = 16
	const perWriter = 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				// Mix a small and a large record so size variance is real.
				rec := map[string]any{
					"writer": id,
					"i":      i,
					"pad":    strings.Repeat("x", (i*id)%1024),
				}
				if err := j.Write(rec); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := readLines(t, path)
	want := writers * perWriter
	if len(lines) != want {
		t.Fatalf("got %d lines, want %d", len(lines), want)
	}

	seen := make(map[string]bool, want)
	for _, line := range lines {
		var r struct {
			Writer int `json:"writer"`
			I      int `json:"i"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("torn line: %q: %v", line, err)
		}
		key := fmt.Sprintf("%d/%d", r.Writer, r.I)
		if seen[key] {
			t.Errorf("duplicate record %s", key)
		}
		seen[key] = true
	}

	if len(seen) != want {
		var missing []string
		for w := 0; w < writers; w++ {
			for i := 0; i < perWriter; i++ {
				k := fmt.Sprintf("%d/%d", w, i)
				if !seen[k] {
					missing = append(missing, k)
				}
			}
		}
		sort.Strings(missing)
		t.Errorf("missing %d records (sample: %v)", len(missing), missing[:min(5, len(missing))])
	}
}

func TestJSONLReopenAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reopen.jsonl")

	j1, err := OpenJSONL(path)
	if err != nil {
		t.Fatalf("OpenJSONL 1: %v", err)
	}
	_ = j1.Write(map[string]int{"n": 1})
	_ = j1.Close()

	j2, err := OpenJSONL(path)
	if err != nil {
		t.Fatalf("OpenJSONL 2: %v", err)
	}
	_ = j2.Write(map[string]int{"n": 2})
	_ = j2.Close()

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
}

func TestNewSlogWritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orchestrator.log")
	logger, c, err := NewSlog(path, slog.LevelInfo)
	if err != nil {
		t.Fatalf("NewSlog: %v", err)
	}
	logger.Info("first", "k", "v")
	logger.Warn("second", "n", 42)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line not JSON: %q: %v", lines[0], err)
	}
	if first["msg"] != "first" || first["k"] != "v" {
		t.Errorf("first record = %v, want msg=first k=v", first)
	}
}

func TestNewSlogRespectsLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "level.log")
	logger, c, err := NewSlog(path, slog.LevelWarn)
	if err != nil {
		t.Fatalf("NewSlog: %v", err)
	}
	logger.Debug("skipped")
	logger.Info("also skipped")
	logger.Warn("kept")
	_ = c.Close()

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lines)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		out = append(out, s.Text())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}
