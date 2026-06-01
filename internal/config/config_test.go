package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsMatchPlan(t *testing.T) {
	d := Defaults()
	if d.Loop.MaxIterations != 30 {
		t.Errorf("Loop.MaxIterations = %d, want 30", d.Loop.MaxIterations)
	}
	if d.Loop.MemoryLimit != "7G" {
		t.Errorf("Loop.MemoryLimit = %q, want 7G", d.Loop.MemoryLimit)
	}
	if d.Runner.Command != "claude" {
		t.Errorf("Runner.Command = %q, want claude", d.Runner.Command)
	}
	if got := len(d.Runner.Args); got != 2 {
		t.Errorf("len(Runner.Args) = %d, want 2", got)
	}
	if !d.Gate.SoftFail {
		t.Error("Gate.SoftFail = false, want true")
	}
	if d.Backoff.DirtyRevertThreshold != 3 {
		t.Errorf("Backoff.DirtyRevertThreshold = %d, want 3", d.Backoff.DirtyRevertThreshold)
	}
	if d.Review.BaseBranch != "main" {
		t.Errorf("Review.BaseBranch = %q, want main", d.Review.BaseBranch)
	}
}

func TestLoadMissingFilesUsesDefaults(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	repo := t.TempDir()

	cfg, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Loop.MaxIterations != 30 {
		t.Errorf("got %d, want defaults", cfg.Loop.MaxIterations)
	}
}

func TestLoadRepoOverridesUserOverridesDefaults(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	repo := t.TempDir()

	userDir := filepath.Join(xdg, "ralph")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userTOML := `[loop]
max_iterations = 100
sleep_between_secs = 1

[runner]
model = "sonnet"
`
	if err := os.WriteFile(filepath.Join(userDir, "config.toml"), []byte(userTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	ralphDir := filepath.Join(repo, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoTOML := `[loop]
max_iterations = 7

[gate]
soft_fail = false
`
	if err := os.WriteFile(filepath.Join(ralphDir, "config.toml"), []byte(repoTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Loop.MaxIterations != 7 {
		t.Errorf("repo override: MaxIterations = %d, want 7", cfg.Loop.MaxIterations)
	}
	if cfg.Loop.SleepBetweenSecs != 1 {
		t.Errorf("user override survives: SleepBetweenSecs = %d, want 1", cfg.Loop.SleepBetweenSecs)
	}
	if cfg.Runner.Model != "sonnet" {
		t.Errorf("user override: Runner.Model = %q, want sonnet", cfg.Runner.Model)
	}
	if cfg.Runner.Command != "claude" {
		t.Errorf("default survives: Runner.Command = %q, want claude", cfg.Runner.Command)
	}
	if cfg.Gate.SoftFail {
		t.Error("repo override: Gate.SoftFail = true, want false")
	}
	if cfg.Review.BaseBranch != "main" {
		t.Errorf("default survives: Review.BaseBranch = %q, want main", cfg.Review.BaseBranch)
	}
}

func TestLoadBadTOMLWraps(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	repo := t.TempDir()

	ralphDir := filepath.Join(repo, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ralphDir, "config.toml"), []byte("this is not = toml ["), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(repo)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "config: load") {
		t.Errorf("error %q lacks context prefix", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q is not wrapped (errors.Unwrap returned nil)", err)
	}
}

func TestUserConfigPathPrefersXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/example")
	p, ok, err := UserConfigPath()
	if err != nil || !ok {
		t.Fatalf("UserConfigPath: ok=%v err=%v", ok, err)
	}
	if p != "/tmp/example/ralph/config.toml" {
		t.Errorf("UserConfigPath = %q", p)
	}
}

func TestParseBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"1024", 1024, false},
		{"7G", 7 << 30, false},
		{"512m", 512 << 20, false},
		{"100KB", 100 << 10, false},
		{"  2GiB ", 2 << 30, false},
		{"4T", 4 << 40, false},
		{"3K", 3 << 10, false},
		{"abc", 0, true},
		{"-5M", 0, true},
		{"5X", 0, true},
	}
	for _, c := range cases {
		got, err := ParseBytes(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseBytes(%q) = %d, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseBytes(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseBytes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestValidateDefaults(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("Defaults().Validate(): %v", err)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"loop max_iterations negative", func(c *Config) { c.Loop.MaxIterations = -1 }, "max_iterations"},
		{"loop session_timeout_secs zero", func(c *Config) { c.Loop.SessionTimeoutSecs = 0 }, "session_timeout_secs"},
		{"loop sleep_between_secs negative", func(c *Config) { c.Loop.SleepBetweenSecs = -1 }, "sleep_between_secs"},
		{"loop memory_limit_bytes garbage", func(c *Config) { c.Loop.MemoryLimit = "abc" }, "memory_limit_bytes"},
		{"loop quota_wait_secs negative", func(c *Config) { c.Loop.QuotaWaitSecs = -1 }, "quota_wait_secs"},
		{"runner command empty", func(c *Config) { c.Runner.Command = "" }, "command is required"},
		{"gate timeout_secs negative", func(c *Config) { c.Gate.TimeoutSecs = -1 }, "timeout_secs"},
		{"gate run_when typo", func(c *Config) { c.Gate.RunWhen = "commits_only" }, "run_when"},
		{"backoff unknown_secs negative", func(c *Config) { c.Backoff.UnknownSecs = -1 }, "unknown_secs"},
		{"backoff dead_session_threshold zero", func(c *Config) { c.Backoff.DeadSessionThreshold = 0 }, "dead_session_threshold"},
		{"backoff dirty_revert_threshold zero", func(c *Config) { c.Backoff.DirtyRevertThreshold = 0 }, "dirty_revert_threshold"},
		{"budget max_cost_usd negative", func(c *Config) { c.Budget.MaxCostUSD = -0.01 }, "max_cost_usd"},
		{"budget max_wallclock_secs negative", func(c *Config) { c.Budget.MaxWallclockSecs = -1 }, "max_wallclock_secs"},
		{"review base_branch empty", func(c *Config) { c.Review.BaseBranch = "" }, "base_branch is required"},
		{"beads exclude_types blank entry", func(c *Config) { c.Beads.ExcludeTypes = []string{""} }, "exclude_types"},
		{"beads exclude_types whitespace entry", func(c *Config) { c.Beads.ExcludeTypes = []string{"  "} }, "exclude_types"},
		{"beads exclude_types comma-separated", func(c *Config) { c.Beads.ExcludeTypes = []string{"byob,foo"} }, "comma-separated"},
		{"beads exclude_types embedded space", func(c *Config) { c.Beads.ExcludeTypes = []string{"by ob"} }, "single type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Defaults()
			c.mut(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate() = %q, want substring %q", err, c.want)
			}
		})
	}
}

func TestBeadsDefaultsEmpty(t *testing.T) {
	d := Defaults()
	if len(d.Beads.ExcludeTypes) != 0 {
		t.Errorf("Beads.ExcludeTypes default = %v, want empty", d.Beads.ExcludeTypes)
	}
}

func TestLoadBeadsExcludeTypesRoundTrip(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	repo := t.TempDir()

	repoDir := filepath.Join(repo, ".ralph")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoTOML := `[beads]
exclude_types = ["byob", "convoy"]
`
	if err := os.WriteFile(filepath.Join(repoDir, "config.toml"), []byte(repoTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Beads.ExcludeTypes, []string{"byob", "convoy"}; !equalStrings(got, want) {
		t.Errorf("Beads.ExcludeTypes = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	repo := t.TempDir()

	ralphDir := filepath.Join(repo, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// max_iteratiunz is a typo — must fail loudly.
	bad := `[loop]
max_iteratiunz = 10
`
	if err := os.WriteFile(filepath.Join(ralphDir, "config.toml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(repo)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("error %q lacks 'unknown keys' marker", err)
	}
	if !strings.Contains(err.Error(), "loop.max_iteratiunz") {
		t.Errorf("error %q does not name the bad key", err)
	}
}

func TestLoadFailsOnInvalidMergedValue(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	repo := t.TempDir()

	ralphDir := filepath.Join(repo, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := `[gate]
run_when = "commits_only"
`
	if err := os.WriteFile(filepath.Join(ralphDir, "config.toml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(repo)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "run_when") {
		t.Errorf("error %q does not mention run_when", err)
	}
}

func TestConfigMemoryLimitBytes(t *testing.T) {
	c := Defaults()
	got, err := c.MemoryLimitBytes()
	if err != nil {
		t.Fatalf("MemoryLimitBytes: %v", err)
	}
	if want := int64(7) << 30; got != want {
		t.Errorf("MemoryLimitBytes() = %d, want %d", got, want)
	}
}
