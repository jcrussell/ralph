// Package atomicfile writes a complete file in one durable, all-or-nothing
// step. Use it for state files where a crash mid-write would leave an
// observer reading a truncated payload — fsm.json, manifest.json, iter
// records, incident postmortems.
//
// The recipe (sealed by byob-runtime-directories.3): create a temp file in
// the same directory as the target, write the full payload, fsync, close,
// chmod, rename, then fsync the parent directory for crash durability.
// Same-directory temp is required because cross-directory rename is not
// atomic on POSIX. Parent-dir fsync is skipped on Windows where the call
// has no equivalent durability barrier.
//
// Concurrent read-modify-write is *not* covered here. Ralph's single-writer
// invariant — one orchestrator per repo via internal/lock — is what
// guarantees no lost updates; if that ever changes, layer flock on top.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// WriteFile writes data to path atomically. Parent directories are not
// created; callers that may face a missing dir should MkdirAll first.
// On error the temp file is removed; the target is untouched unless the
// final rename succeeded.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".atomic-*.tmp")
	if err != nil {
		return fmt.Errorf("atomicfile: tempfile in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("atomicfile: write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("atomicfile: fsync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: close %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: chmod %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: rename %s -> %s: %w", tmpPath, path, err)
	}
	if runtime.GOOS != "windows" {
		if err := fsyncDir(dir); err != nil {
			return fmt.Errorf("atomicfile: fsync dir %s: %w", dir, err)
		}
	}
	return nil
}

// fsyncDir opens dir and calls Sync so the rename above is durable across
// kernel crash. On filesystems that don't support directory fsync the
// kernel returns success; on Windows callers skip this step entirely.
func fsyncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // dir comes from filepath.Dir of caller-supplied target
	if err != nil {
		return err
	}
	defer d.Close() //nolint:errcheck // read-only handle; Sync is the durability barrier
	return d.Sync()
}
