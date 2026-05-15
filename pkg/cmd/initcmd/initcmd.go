// Package initcmd implements `ralph init`: scaffolds .ralph/ in the
// current repo with empty placeholder prompts and executable hook
// stubs. The P2 beads that ship real default content edit the embedded
// files referenced here.
package initcmd

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/pkg/cmdutil"
)

//go:embed all:defaults
var defaultsFS embed.FS

// Options is the three-part command shape's Options struct.
type Options struct {
	F     *cmdutil.Factory
	Force bool
}

// NewCmdInit returns the cobra command for `ralph init`.
func NewCmdInit(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold .ralph/ in the current repo",
		Long: `Creates .ralph/{config.toml, prompts/*.md, hooks/...} and writes a
.gitignore inside .ralph/state/ so runtime state is ignored. Existing
files are preserved unless --force is given.`,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(opts)
			}
			return run(c.Context(), opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "overwrite existing files")
	return cmd
}

func run(_ context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		// Fall back to cwd when no repo marker exists yet — `ralph init`
		// is one of the few commands that can run in a bare directory.
		if !errors.Is(err, cmdutil.ErrNoRepoRoot) {
			return err
		}
		repo, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	return scaffold(repo, opts)
}

// stat counts changes for the summary line.
type stat struct {
	created int
	skipped int
}

func scaffold(repo string, opts *Options) error {
	files, err := collect()
	if err != nil {
		return err
	}
	var s stat
	io := opts.F.IOStreams
	for _, fe := range files {
		dst := filepath.Join(repo, fe.dst)
		written, err := writeOne(dst, fe.content, fe.exec, opts.Force)
		if err != nil {
			return err
		}
		if written {
			_, _ = fmt.Fprintf(io.ErrOut, "created %s\n", fe.dst)
			s.created++
		} else {
			_, _ = fmt.Fprintf(io.ErrOut, "skipped %s\n", fe.dst)
			s.skipped++
		}
	}
	_, _ = fmt.Fprintf(io.Out, "%d created, %d skipped\n", s.created, s.skipped)
	return nil
}

type fileEntry struct {
	dst     string // path under repo, e.g. ".ralph/prompts/clean.md"
	content []byte
	exec    bool
}

// collect walks the embedded defaults/ tree and produces the planned
// file list. Sorting is deterministic so output is stable.
func collect() ([]fileEntry, error) {
	var out []fileEntry
	err := fs.WalkDir(defaultsFS, "defaults", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel("defaults", path)
		if err != nil {
			return err
		}
		body, err := fs.ReadFile(defaultsFS, path)
		if err != nil {
			return err
		}
		out = append(out, fileEntry{
			dst:     filepath.Join(".ralph", filepath.ToSlash(rel)),
			content: body,
			exec:    isHookFile(rel),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dst < out[j].dst })
	return out, nil
}

// isHookFile reports whether rel (relative to defaults/) lives under
// hooks/ — those files need the executable bit.
func isHookFile(rel string) bool {
	parts := splitPath(rel)
	return len(parts) > 0 && parts[0] == "hooks"
}

func splitPath(p string) []string {
	out := []string{}
	for {
		dir, base := filepath.Split(p)
		if base == "" {
			break
		}
		out = append([]string{base}, out...)
		if dir == "" {
			break
		}
		p = filepath.Clean(dir)
		if p == "." || p == "/" {
			break
		}
	}
	return out
}

// writeOne creates dst with content. Returns (true, nil) when it
// actually wrote, (false, nil) when the file existed and force is
// false. Parent dirs are created. Hook files get 0o755; everything
// else 0o644.
func writeOne(dst string, content []byte, exec, force bool) (bool, error) {
	if _, err := os.Stat(dst); err == nil && !force {
		return false, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return false, fmt.Errorf("init: mkdir %s: %w", filepath.Dir(dst), err)
	}
	mode := fs.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	if err := os.WriteFile(dst, content, mode); err != nil {
		return false, fmt.Errorf("init: write %s: %w", dst, err)
	}
	return true, nil
}
