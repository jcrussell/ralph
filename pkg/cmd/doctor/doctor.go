// Package doctor implements `ralph doctor`, the environment check
// subcommand. The checks are pure functions over an injectable
// Probes struct so the unit tests don't have to touch the host.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/isolation"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// Probes are the host-touching primitives every check funnels
// through. Tests inject stubs; production uses SystemProbes().
type Probes struct {
	GOOS       string
	LookPath   func(name string) (string, error)
	Stat       func(path string) (fs.FileInfo, error)
	Getwd      func() (string, error)
	SystemdRun func(ctx context.Context) error
}

// SystemProbes returns Probes wired to the real host.
func SystemProbes() Probes {
	return Probes{
		GOOS:     runtime.GOOS,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
		Getwd:    os.Getwd,
		SystemdRun: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return isolation.Available(ctx)
		},
	}
}

// Result is one check's outcome. Detail explains the failure or
// names the path/binary that satisfied the check.
type Result struct {
	Name   string
	OK     bool
	Detail string
}

// Options is the three-part command shape's Options struct.
type Options struct {
	F      *cmdutil.Factory
	Probes Probes
}

// NewCmdDoctor returns the cobra command for `ralph doctor`.
func NewCmdDoctor(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{F: f, Probes: SystemProbes()}
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check ralph's runtime environment",
		Long: `Verify that ralph's runtime dependencies are present and usable:
Linux, systemd-run --user --scope, claude, bd, git, and the
.ralph/ and .beads/ directories in the current repo.`,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(opts)
			}
			return doctorRun(c.Context(), opts)
		},
	}
}

func doctorRun(ctx context.Context, opts *Options) error {
	results := RunChecks(ctx, opts.Probes)
	io := opts.F.IOStreams
	var failed int
	for _, r := range results {
		mark := "ok"
		if !r.OK {
			mark = "FAIL"
			failed++
		}
		_, _ = fmt.Fprintf(io.Out, "[%4s] %-14s  %s\n", mark, r.Name, r.Detail)
	}
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}

// RunChecks executes every check in order and returns their results.
// Pure over Probes — call from tests with stubbed probes.
func RunChecks(ctx context.Context, p Probes) []Result {
	return []Result{
		checkOS(p),
		checkBinary(p, "systemd-run"),
		checkSystemdRun(ctx, p),
		checkBinary(p, "claude"),
		checkBinary(p, "bd"),
		checkBinary(p, "git"),
		checkRepoDir(p, ".ralph"),
		checkRepoDir(p, ".beads"),
	}
}

func checkOS(p Probes) Result {
	if p.GOOS == "linux" {
		return Result{Name: "os", OK: true, Detail: "linux"}
	}
	return Result{Name: "os", OK: false, Detail: "ralph requires linux; running on " + p.GOOS}
}

func checkBinary(p Probes, name string) Result {
	path, err := p.LookPath(name)
	if err != nil {
		return Result{Name: name, OK: false, Detail: fmt.Sprintf("not on PATH: %v", err)}
	}
	return Result{Name: name, OK: true, Detail: path}
}

func checkSystemdRun(ctx context.Context, p Probes) Result {
	if p.SystemdRun == nil {
		return Result{Name: "systemd-run probe", OK: false, Detail: "no probe configured"}
	}
	if err := p.SystemdRun(ctx); err != nil {
		return Result{Name: "systemd-run probe", OK: false, Detail: err.Error()}
	}
	return Result{Name: "systemd-run probe", OK: true, Detail: "systemd-run --user --scope works"}
}

func checkRepoDir(p Probes, name string) Result {
	wd, err := p.Getwd()
	if err != nil {
		return Result{Name: name, OK: false, Detail: "getwd: " + err.Error()}
	}
	path := filepath.Join(wd, name)
	info, err := p.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{Name: name, OK: false, Detail: "missing: " + path}
	}
	if err != nil {
		return Result{Name: name, OK: false, Detail: err.Error()}
	}
	if !info.IsDir() {
		return Result{Name: name, OK: false, Detail: path + " is not a directory"}
	}
	return Result{Name: name, OK: true, Detail: path}
}
