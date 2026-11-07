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
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/internal/config"
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
	// ReadFile reads /proc/meminfo and cgroup memory.max files.
	ReadFile func(path string) ([]byte, error)
	Getuid   func() int
	// MemoryLimit returns the configured [loop] memory_limit_bytes in
	// bytes (0 = no cap). NewCmdDoctor wires it to the Factory's lazy
	// config; an error (no repo, bad config) skips the headroom check.
	MemoryLimit func() (int64, error)
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
		ReadFile: os.ReadFile,
		Getuid:   os.Getuid,
	}
}

// Result is one check's outcome. Detail explains the failure or
// names the path/binary that satisfied the check.
type Result struct {
	Name   string
	OK     bool
	Detail string
	// Warn marks a passing check that still deserves attention. It is
	// printed as "warn" and does not make doctor exit non-zero.
	Warn bool
}

// Options is the three-part command shape's Options struct.
type Options struct {
	F      *cmdutil.Factory
	Probes Probes
}

// NewCmdDoctor returns the cobra command for `ralph doctor`.
func NewCmdDoctor(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{F: f, Probes: SystemProbes()}
	opts.Probes.MemoryLimit = func() (int64, error) {
		if f.Config == nil {
			return 0, errors.New("no config")
		}
		cfg, err := f.Config()
		if err != nil {
			return 0, err
		}
		return cfg.MemoryLimitBytes()
	}
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check ralph's runtime environment",
		Long: `Verify that ralph's runtime dependencies are present and usable:
Linux, systemd-run --user --scope, claude, bd, git, and the
.ralph/ and .beads/ directories in the current repo, and that
[loop] memory_limit_bytes leaves the host headroom.

Run this after ralph init on a fresh machine, or when a run fails
with an obscure spawn/exec error. Exit code is non-zero when any
check fails; the failing rows are tagged FAIL. Rows tagged warn pass
but deserve a look (exit code stays 0).`,
		Example: `  # verify the environment
  ralph doctor

  # gate a CI job on a clean environment
  ralph doctor && ralph run --once`,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(c.Context(), opts)
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
		switch {
		case !r.OK:
			mark = "FAIL"
			failed++
		case r.Warn:
			mark = "warn"
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
		checkMemoryHeadroom(p),
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

const gib = 1 << 30

// minHeadroom is how much memory the runner's cap must leave free under
// the tightest ceiling: the gate and hooks run outside the runner's
// scope, and an over-cap runner should hit its own MemoryMax (a clean
// oom) before the host or the user slice runs out (which kills the
// whole session with no output: a dead_session).
const minHeadroom = 1 * gib

// checkMemoryHeadroom compares memory_limit_bytes with the smallest of
// the host's MemTotal and the memory.max of the cgroups the runner's
// scope lives under (user-<uid>.slice, user@<uid>.service).
func checkMemoryHeadroom(p Probes) Result {
	const name = "memory cap"
	if p.MemoryLimit == nil || p.ReadFile == nil {
		return Result{Name: name, OK: true, Detail: "skipped: no probe configured"}
	}
	limit, err := p.MemoryLimit()
	if err != nil {
		return Result{Name: name, OK: true, Detail: "skipped: no repo config (" + err.Error() + ")"}
	}
	if limit == 0 {
		return Result{Name: name, OK: true, Warn: true,
			Detail: "memory_limit_bytes is unset: the runner has no memory cap, so a runaway session can run the host out of memory"}
	}
	ceiling, source, err := memoryCeiling(p)
	if err != nil {
		return Result{Name: name, OK: true, Detail: "skipped: " + err.Error()}
	}
	headroom := ceiling - limit
	if headroom < minHeadroom {
		return Result{Name: name, OK: true, Warn: true, Detail: fmt.Sprintf(
			"memory_limit_bytes %s leaves %s under %s %s, so the host can run out of memory before the runner hits its cap; set it to %s or less",
			config.FormatBytes(limit), config.FormatBytes(max(headroom, 0)), source, config.FormatBytes(ceiling), suggestLimit(ceiling))}
	}
	return Result{Name: name, OK: true, Detail: fmt.Sprintf("cap %s, %s free under %s %s",
		config.FormatBytes(limit), config.FormatBytes(headroom), source, config.FormatBytes(ceiling))}
}

// memoryCeiling returns the tightest of MemTotal and the scope's
// ancestor cgroups' memory.max, with a label for the one that won. A
// missing cgroup file or "max" means no limit at that level.
func memoryCeiling(p Probes) (int64, string, error) {
	b, err := p.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, "", fmt.Errorf("read /proc/meminfo: %w", err)
	}
	total, err := parseMemTotal(b)
	if err != nil {
		return 0, "", err
	}
	ceiling, source := total, "host RAM"
	if p.Getuid == nil {
		return ceiling, source, nil
	}
	uid := p.Getuid()
	slice := fmt.Sprintf("/sys/fs/cgroup/user.slice/user-%d.slice", uid)
	for _, c := range []struct{ path, label string }{
		{slice + "/memory.max", fmt.Sprintf("user-%d.slice MemoryMax", uid)},
		{fmt.Sprintf("%s/user@%d.service/memory.max", slice, uid), fmt.Sprintf("user@%d.service MemoryMax", uid)},
	} {
		raw, err := p.ReadFile(c.path)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(raw))
		if v == "max" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil && n < ceiling {
			ceiling, source = n, c.label
		}
	}
	return ceiling, source, nil
}

func parseMemTotal(b []byte) (int64, error) {
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse MemTotal %q: %w", f[1], err)
			}
			return kb * 1024, nil
		}
	}
	return 0, errors.New("no MemTotal in /proc/meminfo")
}

// suggestLimit is the largest whole-GiB cap (in MiB below 2G) that
// leaves minHeadroom under ceiling.
func suggestLimit(ceiling int64) string {
	room := ceiling - minHeadroom
	if room >= 2*gib {
		return fmt.Sprintf("%dG", room/gib)
	}
	if room < 256<<20 {
		return "256M"
	}
	return fmt.Sprintf("%dM", room>>20)
}
