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

	"github.com/BurntSushi/toml"
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
}

type LoopConfig struct {
	MaxIterations      int    `toml:"max_iterations"`
	SessionTimeoutSecs int    `toml:"session_timeout_secs"`
	MemoryLimit        string `toml:"memory_limit_bytes"`
	SleepBetweenSecs   int    `toml:"sleep_between_secs"`
}

type RunnerConfig struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
	Model   string   `toml:"model"`
}

type GateConfig struct {
	TimeoutSecs int    `toml:"timeout_secs"`
	SoftFail    bool   `toml:"soft_fail"`
	RunWhen     string `toml:"run_when"`
}

type BackoffConfig struct {
	UnknownSecs          int `toml:"unknown_secs"`
	OOMSecs              int `toml:"oom_secs"`
	TimeoutSecs          int `toml:"timeout_secs"`
	RateLimitDefault     int `toml:"rate_limit_default"`
	DeadSessionThreshold int `toml:"dead_session_threshold"`
	DirtyRevertThreshold int `toml:"dirty_revert_threshold"`
}

type BudgetConfig struct {
	MaxCostUSD       float64 `toml:"max_cost_usd"`
	MaxWallclockSecs int     `toml:"max_wallclock_secs"`
}

type ReviewConfig struct {
	BaseBranch string `toml:"base_branch"`
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
		if err := mergeFile(cfg, filepath.Join(repoRoot, ".ralph", "config.toml")); err != nil {
			return nil, err
		}
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
// not an error.
func mergeFile(cfg *Config, path string) error {
	_, err := toml.DecodeFile(path, cfg)
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("config: load %s: %w", path, err)
}

// MemoryLimitBytes parses Loop.MemoryLimit ("7G", "512m", "1048576")
// into a byte count. Suffixes K/M/G/T use 1024 multipliers; a trailing
// "B" is allowed. Empty or zero returns 0.
func (c *Config) MemoryLimitBytes() (int64, error) {
	return ParseBytes(c.Loop.MemoryLimit)
}
