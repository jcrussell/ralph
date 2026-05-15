// Package trace implements `ralph trace <iter>`: a guided dump of all
// files captured for a single iteration.
package trace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/pkg/cmdutil"
)

const logsRel = ".ralph/state/logs"

// Options is the three-part command shape's Options struct.
type Options struct {
	F *cmdutil.Factory

	Iter      int
	TailLines int
}

// Validate enforces flag-value invariants before any side effects.
// Errors are FlagErrors so the runner maps them to exit code 2.
// Iter is set from args[0] in RunE before Validate runs.
func (o *Options) Validate() error {
	if o.Iter < 0 {
		return cmdutil.FlagErrorf("<iter> must be a non-negative integer, got %d", o.Iter)
	}
	if o.TailLines < 0 {
		return cmdutil.FlagErrorf("--tail-lines must be non-negative")
	}
	return nil
}

// NewCmdTrace returns the cobra command for `ralph trace`.
func NewCmdTrace(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f, TailLines: 50}
	cmd := &cobra.Command{
		Use:   "trace <iter>",
		Short: "Show every captured artifact for a single iteration",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return cmdutil.FlagErrorf("<iter> must be a non-negative integer, got %q", args[0])
			}
			opts.Iter = n
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return run(c.Context(), opts)
		},
	}
	cmd.Flags().IntVar(&opts.TailLines, "tail-lines", 50, "tail this many lines from stdout/stderr (0 = all)")
	return cmd
}

func run(_ context.Context, opts *Options) error {
	repo, err := opts.F.RepoRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(repo, logsRel)
	rootFS := os.DirFS(dir)
	return Render(rootFS, opts.Iter, opts.TailLines, opts.F.IOStreams.Out)
}

// Render writes the trace for iter to w. It accepts fs.FS so tests
// inject fstest.MapFS without touching disk (byob-interfaces.3).
func Render(rootFS fs.FS, iter, tailLines int, w io.Writer) error {
	prefix := fmt.Sprintf("iter-%04d-", iter)

	jsonFile, err := pickOne(rootFS, prefix, ".json")
	if err != nil {
		return err
	}
	if jsonFile == "" {
		return fmt.Errorf("trace: no iter-%04d-*.json under logs/", iter)
	}
	stem := strings.TrimSuffix(jsonFile, ".json") // "iter-NNNN-<ts>"

	if err := dumpJSON(rootFS, jsonFile, w); err != nil {
		return err
	}
	if err := dumpSection(rootFS, stem+"-prompt.txt", "RENDERED PROMPT", 0, w); err != nil {
		return err
	}
	if err := dumpSection(rootFS, stem+"-stdout.txt", "RUNNER STDOUT", tailLines, w); err != nil {
		return err
	}
	if err := dumpSection(rootFS, stem+"-stderr.txt", "RUNNER STDERR", tailLines, w); err != nil {
		return err
	}
	return nil
}

// pickOne finds the first file in rootFS whose basename starts with
// prefix and ends with suffix. Deterministic order via sort.
func pickOne(rootFS fs.FS, prefix, suffix string) (string, error) {
	entries, err := fs.ReadDir(rootFS, ".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("trace: read logs dir: %w", err)
	}
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return "", nil
	}
	sort.Strings(matches)
	return matches[0], nil
}

func dumpJSON(rootFS fs.FS, name string, w io.Writer) error {
	b, err := fs.ReadFile(rootFS, name)
	if err != nil {
		return fmt.Errorf("trace: read %s: %w", name, err)
	}
	var any any
	if err := json.Unmarshal(b, &any); err == nil {
		pretty, _ := json.MarshalIndent(any, "", "  ")
		_, _ = fmt.Fprintf(w, "=== ITERATION RECORD (%s) ===\n%s\n\n", name, pretty)
		return nil
	}
	_, _ = fmt.Fprintf(w, "=== ITERATION RECORD (%s) ===\n%s\n\n", name, b)
	return nil
}

func dumpSection(rootFS fs.FS, name, header string, tailLines int, w io.Writer) error {
	b, err := fs.ReadFile(rootFS, name)
	if errors.Is(err, fs.ErrNotExist) {
		_, _ = fmt.Fprintf(w, "=== %s (%s) ===\n(missing)\n\n", header, name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("trace: read %s: %w", name, err)
	}
	body := tail(string(b), tailLines)
	_, _ = fmt.Fprintf(w, "=== %s (%s) ===\n%s", header, name, body)
	if !strings.HasSuffix(body, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintln(w)
	return nil
}

// tail returns the last n lines of s. n <= 0 returns s unchanged.
func tail(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	// strings.Split on "a\nb\n" yields ["a","b",""] — the trailing
	// empty entry is the EOL marker. Preserve it when truncating.
	hasTrailing := len(lines) > 0 && lines[len(lines)-1] == ""
	if hasTrailing {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		if hasTrailing {
			return s
		}
		return s
	}
	out := strings.Join(lines[len(lines)-n:], "\n")
	if hasTrailing {
		out += "\n"
	}
	return out
}
