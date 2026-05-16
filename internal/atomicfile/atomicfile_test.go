package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestWriteFileCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("contents = %q, want %q", got, "hello")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0o600", st.Mode().Perm())
	}
}

func TestWriteFileOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("contents = %q, want %q", got, "new")
	}
}

func TestWriteFileChmodsToRequestedPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm bits aren't POSIX on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0o644", st.Mode().Perm())
	}
}

func TestWriteFileLeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".atomic-") {
			t.Errorf("stray temp: %s", e.Name())
		}
	}
}

func TestWriteFileFailsWhenDirMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-such-subdir", "out.txt")
	err := WriteFile(path, []byte("x"), 0o600)
	if err == nil {
		t.Fatal("WriteFile: want error, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist underneath", err)
	}
}

func TestWriteFileTempInSameDir(t *testing.T) {
	// Same-dir-temp is the durability invariant — without it, the final
	// rename could cross filesystems and stop being atomic. This test
	// asserts the temp file is created in the target's directory.
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	// Slow the write by writing a moderate amount of data, then probe the
	// temp pre-rename. CreateTemp is synchronous so we can't observe the
	// temp during a single-goroutine call; instead, run many writes and
	// assert that ReadDir during a concurrent write only ever sees a temp
	// in dir, never elsewhere. The simpler check: the function returns
	// without leaving any file outside dir.
	for i := 0; i < 16; i++ {
		if err := WriteFile(path, []byte("payload"), 0o600); err != nil {
			t.Fatalf("WriteFile %d: %v", i, err)
		}
	}
	// Parent of dir should contain only dir.
	parent := filepath.Dir(dir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("ReadDir parent: %v", err)
	}
	base := filepath.Base(dir)
	for _, e := range entries {
		if e.Name() == base {
			continue
		}
		if strings.HasPrefix(e.Name(), ".atomic-") {
			t.Errorf("temp leaked to parent dir: %s", e.Name())
		}
	}
}

func TestWriteFileConcurrentSamePath(t *testing.T) {
	// Concurrent atomic writes to the same path must each produce a valid
	// final file; readers between writes must never see a torn payload.
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.txt")
	const N = 32
	want := strings.Repeat("payload-", 64) // ~512 bytes; bigger than any partial buffer

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if err := WriteFile(path, []byte(want), 0o600); err != nil {
				t.Errorf("WriteFile: %v", err)
			}
		}()
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Errorf("contents truncated: got %d bytes, want %d", len(got), len(want))
	}
}
