// Package config loads ralph's TOML configuration with the layered
// override semantic documented in the master plan:
//
//	defaults  <  ~/.config/ralph/config.toml  <  <repo>/.ralph/config.toml
//
// A missing file at any layer is not an error — the lower layer's value
// wins. A malformed TOML file is an error wrapped with %w.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jcrussell/ralph/internal/paths"
)

// Config holds every tunable knob ralph exposes. Field zero-values are
// the documented defaults — see Defaults().
type Config struct {
	Loop    LoopConfig    `toml:"loop"`
	Runner  RunnerConfig  `toml:"runner"`
	Gate    GateConfig    `toml:"gate"`
	Backoff BackoffConfig `toml:"backoff"`
	Budget  BudgetConfig  `toml:"budget"`
	Review  ReviewConfig  `toml:"review"`
	Beads   BeadsConfig   `toml:"beads"`
}

// LoopConfig holds the [loop] section.
type LoopConfig struct {
	MaxIterations      int    `toml:"max_iterations"`
	SessionTimeoutSecs int    `toml:"session_timeout_secs"`
	MemoryLimit        string `toml:"memory_limit_bytes"`
	SleepBetweenSecs   int    `toml:"sleep_between_secs"`
	// WaitOnQuota opts into riding out a runner quota cap (the resettable
	// 5-hour/weekly/monthly window, ModeQuota) by sleeping QuotaWaitSecs
	// and resuming the same state instead of exiting failed{runner_terminal}.
	// Default false keeps quota terminal (fail fast, don't burn iterations).
	WaitOnQuota bool `toml:"wait_on_quota"`
	// QuotaWaitSecs is the bounded sleep between quota retries when
	// WaitOnQuota is set. The upstream reset instant isn't in the error
	// envelope, so this is a fixed poll interval, not a parsed reset.
	QuotaWaitSecs int `toml:"quota_wait_secs"`
}

// RunnerConfig holds the [runner] section.
type RunnerConfig struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
	Model   string   `toml:"model"`
}

// GateConfig holds the [gate] section.
type GateConfig struct {
	TimeoutSecs int    `toml:"timeout_secs"`
	SoftFail    bool   `toml:"soft_fail"`
	RunWhen     string `toml:"run_when"`
}

// BackoffConfig holds the [backoff] section.
type BackoffConfig struct {
	UnknownSecs          int `toml:"unknown_secs"`
	OOMSecs              int `toml:"oom_secs"`
	TimeoutSecs          int `toml:"timeout_secs"`
	RateLimitDefault     int `toml:"rate_limit_default"`
	DeadSessionThreshold int `toml:"dead_session_threshold"`
	DirtyRevertThreshold int `toml:"dirty_revert_threshold"`
}

// BudgetConfig holds the [budget] section.
type BudgetConfig struct {
	MaxCostUSD       float64 `toml:"max_cost_usd"`
	MaxWallclockSecs int     `toml:"max_wallclock_secs"`
}

// ReviewConfig holds the [review] section.
type ReviewConfig struct {
	BaseBranch string `toml:"base_branch"`
}

// BeadsConfig holds the [beads] section — knobs that shape every
// orchestrator-side bd invocation. Does not affect bd calls the
// runner makes from inside an iteration.
type BeadsConfig struct {
	// ExcludeTypes lists issue types to drop from queue queries
	// (`bd ready` and the open-issue `bd list` calls in the FSM).
	// Useful when the bead DB includes reference/library records
	// that aren't actionable work — e.g. byob-go-cli's "byob"
	// decision/epic catalog. Empty (the default) = no filter.
	ExcludeTypes []string `toml:"exclude_types"`
}

// Defaults returns a Config populated with the values documented in
// the master plan's [config.toml] block.
func Defaults() *Config {
	return &Config{
		Loop: LoopConfig{
			MaxIterations:      30,
			SessionTimeoutSecs: 3600,
			MemoryLimit:        "7G",
			SleepBetweenSecs:   5,
			WaitOnQuota:        false,
			QuotaWaitSecs:      1800,
		},
		Runner: RunnerConfig{
			Command: "claude",
			Args:    []string{"--dangerously-skip-permissions", "--output-format=json"},
			Model:   "opus",
		},
		Gate: GateConfig{
			TimeoutSecs: 600,
			SoftFail:    true,
			RunWhen:     "commits-only",
		},
		Backoff: BackoffConfig{
			UnknownSecs:          30,
			OOMSecs:              120,
			TimeoutSecs:          60,
			RateLimitDefault:     900,
			DeadSessionThreshold: 3,
			DirtyRevertThreshold: 3,
		},
		Budget: BudgetConfig{
			MaxCostUSD:       0,
			MaxWallclockSecs: 0,
		},
		Review: ReviewConfig{
			BaseBranch: "main",
		},
		Beads: BeadsConfig{},
	}
}

// Load returns a Config built by layering files on top of Defaults():
// user config first (if present), then repo config (if present). Each
// merge only sets fields the file defines, so absent keys keep the
// lower layer's value.
func Load(repoRoot string) (*Config, error) {
	cfg := Defaults()
	if p, ok, err := UserConfigPath(); err != nil {
		return nil, err
	} else if ok {
		if err := mergeFile(cfg, p); err != nil {
			return nil, err
		}
	}
	if repoRoot != "" {
		if err := mergeFile(cfg, paths.New(repoRoot).Config()); err != nil {
			return nil, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// UserConfigPath returns ($XDG_CONFIG_HOME or ~/.config)/ralph/config.toml.
// The second return is false when the home directory cannot be located
// (a clear error case worth surfacing rather than silently skipping).
func UserConfigPath() (string, bool, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "ralph", "config.toml"), true, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("config: locate home: %w", err)
	}
	if home == "" {
		return "", false, nil
	}
	return filepath.Join(home, ".config", "ralph", "config.toml"), true, nil
}

// mergeFile decodes path into cfg if path exists. A missing file is
// not an error. Unknown keys in the file are rejected so typos fail
// loudly instead of being silently ignored.
func mergeFile(cfg *Config, path string) error {
	md, err := toml.DecodeFile(path, cfg)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: load %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return fmt.Errorf("config: %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	return nil
}

// Validate returns an error if any field in c is out of range or
// missing. Called by Load on the fully-merged config so layered files
// can't combine into an invalid result.
func (c *Config) Validate() error {
	if err := c.Loop.Validate(); err != nil {
		return fmt.Errorf("loop: %w", err)
	}
	if err := c.Runner.Validate(); err != nil {
		return fmt.Errorf("runner: %w", err)
	}
	if err := c.Gate.Validate(); err != nil {
		return fmt.Errorf("gate: %w", err)
	}
	if err := c.Backoff.Validate(); err != nil {
		return fmt.Errorf("backoff: %w", err)
	}
	if err := c.Budget.Validate(); err != nil {
		return fmt.Errorf("budget: %w", err)
	}
	if err := c.Review.Validate(); err != nil {
		return fmt.Errorf("review: %w", err)
	}
	if err := c.Beads.Validate(); err != nil {
		return fmt.Errorf("beads: %w", err)
	}
	return nil
}

// Validate checks LoopConfig invariants.
func (l *LoopConfig) Validate() error {
	if l.MaxIterations < 0 {
		return fmt.Errorf("max_iterations must be >= 0, got %d", l.MaxIterations)
	}
	if l.SessionTimeoutSecs <= 0 {
		return fmt.Errorf("session_timeout_secs must be > 0, got %d", l.SessionTimeoutSecs)
	}
	if l.SleepBetweenSecs < 0 {
		return fmt.Errorf("sleep_between_secs must be >= 0, got %d", l.SleepBetweenSecs)
	}
	if l.QuotaWaitSecs < 0 {
		return fmt.Errorf("quota_wait_secs must be >= 0, got %d", l.QuotaWaitSecs)
	}
	if _, err := ParseBytes(l.MemoryLimit); err != nil {
		return fmt.Errorf("memory_limit_bytes: %w", err)
	}
	return nil
}

// Validate checks RunnerConfig invariants.
func (r *RunnerConfig) Validate() error {
	if r.Command == "" {
		return errors.New("command is required")
	}
	return nil
}

// Validate checks GateConfig invariants. RunWhen must be one of the
// three values runGate() honors; anything else would silently fall
// through to "every".
func (g *GateConfig) Validate() error {
	if g.TimeoutSecs < 0 {
		return fmt.Errorf("timeout_secs must be >= 0, got %d", g.TimeoutSecs)
	}
	switch g.RunWhen {
	case "every", "commits-only", "never":
	default:
		return fmt.Errorf("run_when = %q, want one of: every, commits-only, never", g.RunWhen)
	}
	return nil
}

// Validate checks BackoffConfig invariants.
func (b *BackoffConfig) Validate() error {
	if b.UnknownSecs < 0 {
		return fmt.Errorf("unknown_secs must be >= 0, got %d", b.UnknownSecs)
	}
	if b.OOMSecs < 0 {
		return fmt.Errorf("oom_secs must be >= 0, got %d", b.OOMSecs)
	}
	if b.TimeoutSecs < 0 {
		return fmt.Errorf("timeout_secs must be >= 0, got %d", b.TimeoutSecs)
	}
	if b.RateLimitDefault < 0 {
		return fmt.Errorf("rate_limit_default must be >= 0, got %d", b.RateLimitDefault)
	}
	if b.DeadSessionThreshold < 1 {
		return fmt.Errorf("dead_session_threshold must be >= 1, got %d", b.DeadSessionThreshold)
	}
	if b.DirtyRevertThreshold < 1 {
		return fmt.Errorf("dirty_revert_threshold must be >= 1, got %d", b.DirtyRevertThreshold)
	}
	return nil
}

// Validate checks BudgetConfig invariants. Zero means "unlimited" per
// the docs; negatives are nonsense.
func (b *BudgetConfig) Validate() error {
	if b.MaxCostUSD < 0 {
		return fmt.Errorf("max_cost_usd must be >= 0, got %v", b.MaxCostUSD)
	}
	if b.MaxWallclockSecs < 0 {
		return fmt.Errorf("max_wallclock_secs must be >= 0, got %d", b.MaxWallclockSecs)
	}
	return nil
}

// Validate checks ReviewConfig invariants.
func (r *ReviewConfig) Validate() error {
	if r.BaseBranch == "" {
		return errors.New("base_branch is required")
	}
	return nil
}

// Validate checks BeadsConfig invariants. Reject entries that would
// fail at the bd CLI boundary so the misconfiguration surfaces at
// startup rather than as a confusing bd error on the first iteration.
func (b *BeadsConfig) Validate() error {
	for _, t := range b.ExcludeTypes {
		if strings.TrimSpace(t) == "" {
			return errors.New("exclude_types: entries must be non-empty")
		}
		if strings.ContainsAny(t, " ,") {
			return fmt.Errorf("exclude_types: %q must be a single type, not a comma-separated list", t)
		}
	}
	return nil
}

// MemoryLimitBytes parses Loop.MemoryLimit ("7G", "512m", "1048576")
// into a byte count. Suffixes K/M/G/T use 1024 multipliers; a trailing
// "B" is allowed. Empty or zero returns 0.
func (c *Config) MemoryLimitBytes() (int64, error) {
	return ParseBytes(c.Loop.MemoryLimit)
}
